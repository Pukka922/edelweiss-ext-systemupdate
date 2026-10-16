package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

const simUpgrade = "(1/1) Upgrading curl (8.16.0-r0 -> 8.16.0-r1)\nNeed to download 1 MiB of packages.\n" +
	"After this operation, 0 B of additional disk space will be used.\nOK: 1 GiB in 1 packages\n"

func (f *fixture) preview(t *testing.T, body string) jobs.Plan {
	t.Helper()
	resp := f.do(t, "POST", "/preview", body, admin)
	if resp.StatusCode != 200 {
		t.Fatalf("preview: %d", resp.StatusCode)
	}
	return sdktest.DecodeJSON[jobs.Plan](t, resp)
}

func TestPreview(t *testing.T) {
	f := newFixture(t, nil, fakeRunner{"upgrade -s -vv": simUpgrade})
	expectError(t, f.do(t, "POST", "/preview", `{"kind":"nope"}`, admin), 400, "invalid_kind")
	if e := expectError(t, f.do(t, "POST", "/preview", `{"kind":"release"}`, admin), 400, "invalid_target"); e.Error.Field != "target" {
		t.Fatalf("field = %q", e.Error.Field)
	}
	expectError(t, f.do(t, "POST", "/preview", `{`, admin), 400, "invalid_json")
	p := f.preview(t, `{"kind":"upgrade"}`)
	if p.ID == "" || p.Kind != "upgrade" || len(p.Simulation.Changes) != 1 {
		t.Fatalf("plan = %+v", p)
	}
	if _, err := f.api.Env.LoadPlan(p.ID); err != nil {
		t.Fatalf("the plan is not stored: %v", err)
	}
	f.job(t, "upgrade", markRunning)
	expectError(t, f.do(t, "POST", "/preview", `{"kind":"upgrade"}`, admin), 409, "busy")
}

func TestStartJob(t *testing.T) {
	f := newFixture(t, nil, fakeRunner{"upgrade -s -vv": simUpgrade})
	p := f.preview(t, `{"kind":"upgrade"}`)
	resp := f.do(t, "POST", "/jobs", `{"kind":"upgrade","planId":"`+p.ID+`"}`, admin)
	if resp.StatusCode != 202 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	st := sdktest.DecodeJSON[jobs.State](t, resp)
	if st.Status != jobs.Running || st.Kind != "upgrade" || st.User != "alice" || len(f.launcher.dirs) != 1 {
		t.Fatalf("state = %+v", st)
	}
	spec, _ := jobs.ReadSpec(f.launcher.dirs[0])
	if spec.PlanID != p.ID || spec.Kind != "upgrade" {
		t.Fatalf("spec = %+v", spec)
	}
	cur := sdktest.DecodeJSON[struct{ Job *jobs.State }](t, f.do(t, "GET", "/jobs/current", "", alice))
	if cur.Job == nil || cur.Job.ID != st.ID {
		t.Fatalf("current = %+v", cur.Job)
	}
	list := sdktest.DecodeJSON[struct{ Jobs []jobs.State }](t, f.do(t, "GET", "/jobs", "", alice))
	if len(list.Jobs) != 1 || list.Jobs[0].ID != st.ID {
		t.Fatalf("jobs = %+v", list.Jobs)
	}
}

func TestStartRefresh(t *testing.T) {
	f := newFixture(t, nil, nil)
	resp := f.do(t, "POST", "/refresh", "", admin)
	if st := sdktest.DecodeJSON[jobs.State](t, resp); resp.StatusCode != 202 || st.Kind != "refresh" {
		t.Fatalf("status %d, state %+v", resp.StatusCode, st)
	}
}

func TestStartJobNeedsAFreshPlan(t *testing.T) {
	f := newFixture(t, nil, fakeRunner{"upgrade -s -vv": simUpgrade})
	expectError(t, f.do(t, "POST", "/jobs", `{"kind":"nope"}`, admin), 400, "invalid_kind")
	expectError(t, f.do(t, "POST", "/jobs", `{"kind":"upgrade","planId":"nope"}`, admin), 409, "plan_expired")
	up := f.preview(t, `{"kind":"upgrade"}`)
	if e := expectError(t, f.do(t, "POST", "/jobs", `{"kind":"release","planId":"`+up.ID+`"}`, admin), 400, "invalid_plan"); e.Error.Field != "planId" {
		t.Fatalf("field = %q", e.Error.Field)
	}
	blocked := f.preview(t, `{"kind":"release","target":"v3.25"}`)
	expectError(t, f.do(t, "POST", "/jobs", `{"kind":"release","planId":"`+blocked.ID+`"}`, admin), 409, "plan_blocked")
	if len(f.launcher.dirs) != 0 {
		t.Fatalf("launched %v", f.launcher.dirs)
	}
}

// The typed confirmation of edge is the server's to require: the dialog of the page is a convenience, and a request
// that cites a plan for edge says it too.
func TestStartEdgeNeedsTheTypedConfirmation(t *testing.T) {
	f := newFixture(t, nil, fakeRunner{})
	p := f.preview(t, `{"kind":"release","target":"edge"}`)
	if p.Target != "edge" || len(p.Blockers) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	start := func(extra string) *http.Response {
		return f.do(t, "POST", "/jobs", `{"kind":"release","planId":"`+p.ID+`"`+extra+`}`, admin)
	}
	for _, extra := range []string{"", `,"confirm":""`, `,"confirm":"Edge"`, `,"confirm":" edge"`, `,"confirm":"edge "`, `,"confirm":"yes"`, `,"confirm":"v3.24"`} {
		if e := expectError(t, start(extra), 400, "invalid_confirm"); e.Error.Field != "confirm" {
			t.Fatalf("%q: field = %q", extra, e.Error.Field)
		}
	}
	if len(f.launcher.dirs) != 0 {
		t.Fatalf("launched %v", f.launcher.dirs)
	}
	resp := start(`,"confirm":"edge"`)
	if st := sdktest.DecodeJSON[jobs.State](t, resp); resp.StatusCode != 202 || st.Kind != "release" || st.Target != "edge" {
		t.Fatalf("status %d, state %+v", resp.StatusCode, st)
	}
}

func TestStartStableReleaseNeedsNoConfirmation(t *testing.T) {
	f := newFixture(t, nil, fakeRunner{})
	p := f.preview(t, `{"kind":"release","target":"v3.24"}`)
	if len(p.Blockers) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	resp := f.do(t, "POST", "/jobs", `{"kind":"release","planId":"`+p.ID+`","confirm":"whatever"}`, admin) // ignored
	if st := sdktest.DecodeJSON[jobs.State](t, resp); resp.StatusCode != 202 || st.Target != "v3.24" {
		t.Fatalf("status %d, state %+v", resp.StatusCode, st)
	}
}

func TestStartJobBusy(t *testing.T) {
	f := newFixture(t, nil, fakeRunner{"upgrade -s -vv": simUpgrade})
	p := f.preview(t, `{"kind":"upgrade"}`)
	held, err := jobs.Lock(f.api.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, f.do(t, "POST", "/jobs", `{"kind":"upgrade","planId":"`+p.ID+`"}`, admin), 409, "busy")
	running, _ := f.job(t, "release", markRunning)
	e := expectError(t, f.do(t, "POST", "/jobs", `{"kind":"upgrade","planId":"`+p.ID+`"}`, admin), 409, "busy")
	if e.Error.Job == nil || e.Error.Job.ID != running.ID || e.Error.Job.Kind != "release" {
		t.Fatalf("the busy answer names the running job: %+v", e.Error.Job)
	}
	held.Close()
	if len(f.launcher.dirs) != 0 {
		t.Fatalf("launched %v", f.launcher.dirs)
	}
}

// diedRunning is a job whose runner is gone: status running, a dead runner pid of this boot. With apkAlive its apk is
// the test process, which runs.
func diedRunning(apkAlive bool) func(*jobs.State) {
	return func(st *jobs.State) {
		st.Status = jobs.Running
		st.RunnerPID, st.RunnerStart, st.BootID = 999999, 1, host.New().BootID()
		if apkAlive {
			me, _ := host.New().StartTime(os.Getpid())
			st.APKPID, st.APKStart = os.Getpid(), me
		}
	}
}

func TestStartJobBusyWhileApkOutlivesTheRunner(t *testing.T) {
	f := newFixture(t, nil, nil)
	orphan, _ := f.job(t, "upgrade", diedRunning(true))
	if orphan.Status != jobs.Interrupted || !orphan.APKAlive {
		t.Fatalf("the test job = %+v", orphan)
	}
	e := expectError(t, f.do(t, "POST", "/refresh", "", admin), 409, "busy")
	if e.Error.Job == nil || e.Error.Job.ID != orphan.ID || !e.Error.Job.APKAlive {
		t.Fatalf("the busy answer names the job whose apk runs: %+v", e.Error.Job)
	}
	if len(f.launcher.dirs) != 0 {
		t.Fatalf("launched %v", f.launcher.dirs)
	}
}

func TestStartJobAfterAnInterruptedJobWithoutApk(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.job(t, "upgrade", diedRunning(false))
	resp := f.do(t, "POST", "/refresh", "", admin)
	if resp.StatusCode != 202 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, want 202: %s", resp.StatusCode, b)
	}
	if st := sdktest.DecodeJSON[jobs.State](t, resp); st.Kind != "refresh" || len(f.launcher.dirs) != 1 {
		t.Fatalf("state %+v, launched %v", st, f.launcher.dirs)
	}
}

func TestStartResume(t *testing.T) {
	f := newFixture(t, nil, nil)
	expectError(t, f.do(t, "POST", "/jobs", `{"kind":"resume"}`, admin), 409, "nothing_to_resume")
	write(t, f.root, map[string]string{"etc/apk/repositories": strings.ReplaceAll(repos323, "v3.23", "v3.24")})
	resp := f.do(t, "POST", "/jobs", `{"kind":"resume"}`, admin)
	if st := sdktest.DecodeJSON[jobs.State](t, resp); resp.StatusCode != 202 || st.Kind != "resume" || st.Target != "v3.24" {
		t.Fatalf("status %d, state %+v", resp.StatusCode, st)
	}
}

func TestStartJobLaunchFails(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.launcher.fail = errors.New("no such file")
	expectError(t, f.do(t, "POST", "/refresh", "", admin), 500, "launch_failed")
	list, _ := f.api.Store.List(1)
	if len(list) != 1 || list[0].Status != jobs.Failed || list[0].Error == "" {
		t.Fatalf("the job that could not start = %+v", list)
	}
}

func TestStartJobAnswersWhileTheRunnerIsSlow(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.launcher.start = false
	f.api.StartWait = 50 * time.Millisecond
	resp := f.do(t, "POST", "/refresh", "", admin)
	if st := sdktest.DecodeJSON[jobs.State](t, resp); resp.StatusCode != 202 || st.Status != jobs.Queued {
		t.Fatalf("status %d, state %+v", resp.StatusCode, st)
	}
}

func TestLogRedacted(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, dir := f.job(t, "refresh", func(st *jobs.State) { st.Status = jobs.Done })
	if err := os.WriteFile(filepath.Join(dir, "log"), []byte("fetch https://myUser:s3cret@repo.example.org/alpine/v3.23/main\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	resp := f.do(t, "GET", "/jobs/"+st.ID+"/log", "", admin)
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	if resp.StatusCode != 200 || strings.Contains(b.String(), "s3cret") || !strings.Contains(b.String(), "https://***@repo.example.org") {
		t.Fatalf("log %d: %s", resp.StatusCode, b)
	}
	expectError(t, f.do(t, "GET", "/jobs/20261013T120000.000-00000000/log", "", admin), 404, "not_found")
}
