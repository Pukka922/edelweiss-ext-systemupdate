package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var bootedAt = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

func statFile(pid int, comm string, start uint64) string {
	return fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 %d 1000000 500 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n",
		pid, comm, pid, pid, start)
}

// staleFixture is a host with three recorded processes: pid 100 still runs (same start), pid 200 restarted (other
// start), pid 300 is gone.
func staleFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixture(t, map[string]string{
		"proc/stat":     fmt.Sprintf("cpu 1 2 3\nbtime %d\n", bootedAt.Unix()),
		"proc/100/stat": statFile(100, "sshd", 4242),
		"proc/200/stat": statFile(200, "ntpd", 9999),
	}, nil)
}

func postWith(t *testing.T, stale ...host.Process) json.RawMessage {
	t.Helper()
	p := jobs.Post{APKNew: []string{}, Stale: stale, Removed: []string{"gtk+2.0"}, GRUB: true}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (f *fixture) staleJob(t *testing.T, ended time.Time, stale ...host.Process) (*jobs.State, string) {
	t.Helper()
	return f.job(t, "upgrade", func(st *jobs.State) {
		st.Status, st.Ended, st.Post = jobs.Done, ended, postWith(t, stale...)
	})
}

func staleOf(t *testing.T, st *jobs.State) []host.Process {
	t.Helper()
	if st == nil {
		t.Fatal("no job")
	}
	var p jobs.Post
	if err := json.Unmarshal(st.Post, &p); err != nil {
		t.Fatalf("post %s: %v", st.Post, err)
	}
	return p.Stale
}

var (
	runs      = host.Process{PID: 100, Name: "sshd", Service: "sshd", Start: 4242}
	restarted = host.Process{PID: 200, Name: "ntpd", Start: 1234}
	gone      = host.Process{PID: 300, Name: "cron", Start: 55}
)

func TestStatusDropsStaleProcessesThatAreGone(t *testing.T) {
	f := staleFixture(t)
	f.staleJob(t, bootedAt.Add(time.Hour), runs, restarted, gone)
	st := sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice))
	got := staleOf(t, st.Last)
	if len(got) != 1 || got[0] != runs {
		t.Fatalf("stale = %+v, want only %+v", got, runs)
	}
	var p jobs.Post
	_ = json.Unmarshal(st.Last.Post, &p)
	if len(p.Removed) != 1 || !p.GRUB {
		t.Fatalf("the rest of the post changed: %s", st.Last.Post)
	}
}

func TestStatusStaleListStaysAnEmptyArray(t *testing.T) {
	f := staleFixture(t)
	f.staleJob(t, bootedAt.Add(time.Hour), gone)
	resp := f.do(t, "GET", "/status", "", alice)
	raw := sdktest.DecodeJSON[struct {
		Last struct {
			Post struct {
				Stale *[]host.Process `json:"stale"`
			} `json:"post"`
		} `json:"last"`
	}](t, resp)
	if raw.Last.Post.Stale == nil || len(*raw.Last.Post.Stale) != 0 {
		t.Fatalf("stale = %v, want []", raw.Last.Post.Stale)
	}
}

func TestStatusKeepsOldRecordsOnlyWhenTheJobEndedAfterTheBoot(t *testing.T) {
	// a record without a start cannot be checked: before the restart it may still be true, after it, it is not
	old := host.Process{PID: 100, Name: "sshd"}
	f := staleFixture(t)
	f.staleJob(t, bootedAt.Add(-time.Hour), old)
	st := sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice))
	if got := staleOf(t, st.Last); len(got) != 0 {
		t.Fatalf("job before the boot: stale = %+v", got)
	}

	f = staleFixture(t)
	f.staleJob(t, bootedAt.Add(time.Hour), old)
	st = sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice))
	if got := staleOf(t, st.Last); len(got) != 1 || got[0] != old {
		t.Fatalf("job after the boot: stale = %+v", got)
	}
}

func TestStatusKeepsOldRecordsWithoutTheBootTime(t *testing.T) {
	old := host.Process{PID: 100, Name: "sshd"}
	f := newFixture(t, nil, nil) // no /proc/stat
	f.staleJob(t, bootedAt.Add(-time.Hour), old, restarted)
	st := sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice))
	if got := staleOf(t, st.Last); len(got) != 1 || got[0] != old {
		t.Fatalf("stale = %+v, want only the old record", got)
	}
}

func TestJobsListFiltersTheSame(t *testing.T) {
	f := staleFixture(t)
	mixed, _ := f.staleJob(t, bootedAt.Add(time.Hour), runs, restarted, gone)
	beforeBoot, _ := f.staleJob(t, bootedAt.Add(-time.Hour), host.Process{PID: 100, Name: "sshd"})
	body := sdktest.DecodeJSON[struct {
		Jobs []jobs.State `json:"jobs"`
	}](t, f.do(t, "GET", "/jobs", "", alice))
	if len(body.Jobs) != 2 {
		t.Fatalf("jobs = %d", len(body.Jobs))
	}
	for i := range body.Jobs {
		got := staleOf(t, &body.Jobs[i])
		switch body.Jobs[i].ID {
		case mixed.ID:
			if len(got) != 1 || got[0] != runs {
				t.Errorf("stale = %+v, want only %+v", got, runs)
			}
		case beforeBoot.ID:
			if len(got) != 0 {
				t.Errorf("job before the boot: stale = %+v", got)
			}
		default:
			t.Errorf("unknown job %s", body.Jobs[i].ID)
		}
	}
}

func TestCurrentJobFiltersTheSame(t *testing.T) {
	f := staleFixture(t)
	f.job(t, "upgrade", func(st *jobs.State) {
		markRunning(st)
		st.Post = postWith(t, runs, restarted)
	})
	body := sdktest.DecodeJSON[struct {
		Job *jobs.State `json:"job"`
	}](t, f.do(t, "GET", "/jobs/current", "", alice))
	if got := staleOf(t, body.Job); len(got) != 1 || got[0] != runs {
		t.Fatalf("current: stale = %+v", got)
	}
	st := sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice))
	if got := staleOf(t, st.Current); len(got) != 1 || got[0] != runs {
		t.Fatalf("status current: stale = %+v", got)
	}
}

func TestEventsEndCarriesTheFilteredPost(t *testing.T) {
	f := staleFixture(t)
	st, _ := f.staleJob(t, bootedAt.Add(time.Hour), runs, restarted, gone)
	events := f.stream(t, st.ID, "", alice)
	end := events[len(events)-1]
	if end.name != "end" {
		t.Fatalf("events = %s", names(events))
	}
	var got jobs.State
	if err := json.Unmarshal([]byte(end.data), &got); err != nil {
		t.Fatal(err)
	}
	if s := staleOf(t, &got); len(s) != 1 || s[0] != runs {
		t.Fatalf("end: stale = %+v", s)
	}
}

func TestFilteringNeverWritesTheStore(t *testing.T) {
	f := staleFixture(t)
	st, dir := f.staleJob(t, bootedAt.Add(time.Hour), runs, restarted, gone)
	file := filepath.Join(dir, "state.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	f.do(t, "GET", "/status", "", alice).Body.Close()
	f.do(t, "GET", "/jobs", "", alice).Body.Close()
	f.do(t, "GET", "/jobs/current", "", alice).Body.Close()
	f.stream(t, st.ID, "", alice)
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatalf("state.json changed:\n%s\n--- to ---\n%s", before, after)
	}
	if stored, _ := jobs.ReadState(dir); len(staleOf(t, stored)) != 3 {
		t.Fatalf("the store lost entries: %s", stored.Post)
	}
}
