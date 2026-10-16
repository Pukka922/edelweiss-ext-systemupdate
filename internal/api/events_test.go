package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

type event struct{ name, id, data string }

// stream reads the events of a job until the stream ends.
func (f *fixture) stream(t *testing.T, id, lastID string, u sdk.User) []event {
	t.Helper()
	req := sdktest.Request("GET", "/jobs/"+id+"/events", nil, u)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp := sdktest.Do(t, f.app, req)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var out []event
	var cur event
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.name != "" {
				out = append(out, cur)
			}
			cur = event{}
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			cur.data += strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

// lines joins the lines of the log events.
func lines(t *testing.T, events []event) []string {
	t.Helper()
	var out []string
	for _, e := range events {
		if e.name != "log" {
			continue
		}
		var l struct {
			Lines  []string `json:"lines"`
			Offset int64    `json:"offset"`
		}
		if err := json.Unmarshal([]byte(e.data), &l); err != nil || e.id != fmt.Sprint(l.Offset) {
			t.Fatalf("log event %+v: %v", e, err)
		}
		out = append(out, l.Lines...)
	}
	return out
}

func names(events []event) string {
	var out []string
	for _, e := range events {
		out = append(out, e.name)
	}
	return strings.Join(out, ",")
}

func TestEventsResumeFromOffset(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, dir := f.job(t, "upgrade", func(st *jobs.State) { st.Status = jobs.Done })
	_ = os.WriteFile(filepath.Join(dir, "log"), []byte("a\nb\nc\n"), 0o640)
	all := f.stream(t, st.ID, "", admin)
	if got := lines(t, all); strings.Join(got, "|") != "a|b|c" || names(all) != "log,end" || all[0].id != "6" {
		t.Fatalf("events = %+v", all)
	}
	if got := lines(t, f.stream(t, st.ID, "2", admin)); strings.Join(got, "|") != "b|c" {
		t.Fatalf("from offset 2: %v", got)
	}
	if ev := f.stream(t, st.ID, "6", admin); names(ev) != "end" {
		t.Fatalf("from the end: %+v", ev)
	}
}

func TestEventsHideTheLogFromUsers(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, dir := f.job(t, "upgrade", func(st *jobs.State) { st.Status, st.Current = jobs.Done, "(1/1) Upgrading curl" })
	_ = os.WriteFile(filepath.Join(dir, "log"), []byte("secret line\n"), 0o640)
	ev := f.stream(t, st.ID, "", alice)
	if names(ev) != "end" || strings.Contains(ev[0].data, "secret line") || !strings.Contains(ev[0].data, "Upgrading curl") {
		t.Fatalf("events = %+v", ev)
	}
}

func TestEventsFollowARunningJob(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, dir := f.job(t, "upgrade", markRunning)
	log := filepath.Join(dir, "log")
	_ = os.WriteFile(log, nil, 0o640)
	go func() {
		appendLog := func(s string) {
			fh, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
			_, _ = fh.WriteString(s)
			_ = fh.Close()
		}
		time.Sleep(50 * time.Millisecond)
		appendLog("x\n")
		time.Sleep(50 * time.Millisecond)
		appendLog("y") // no newline yet: sent only when the job has ended
		time.Sleep(50 * time.Millisecond)
		setState(dir, func(st *jobs.State) { st.Status = jobs.Done })
	}()
	ev := f.stream(t, st.ID, "", admin)
	if got := lines(t, ev); strings.Join(got, "|") != "x|y" {
		t.Fatalf("lines = %v (events %+v)", got, ev)
	}
	if ev[0].name != "state" || ev[len(ev)-1].name != "end" {
		t.Fatalf("events = %s", names(ev))
	}
}

func TestEventsStartAtTheTail(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, dir := f.job(t, "upgrade", func(st *jobs.State) { st.Status = jobs.Done })
	var b strings.Builder
	for i := 0; i < 30000; i++ { // 12 bytes a line: 360000 bytes, more than 256 KiB
		fmt.Fprintf(&b, "line-%06d\n", i)
	}
	_ = os.WriteFile(filepath.Join(dir, "log"), []byte(b.String()), 0o640)
	got := lines(t, f.stream(t, st.ID, "", admin))
	whole := regexp.MustCompile(`^line-\d{6}$`)
	if !whole.MatchString(got[0]) || got[len(got)-1] != "line-029999" || len(got) > 256<<10/12+1 || len(got) < 256<<10/12-1 {
		t.Fatalf("%d lines from %q to %q", len(got), got[0], got[len(got)-1])
	}
}

func TestEventsRedactTheLog(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, dir := f.job(t, "upgrade", func(st *jobs.State) { st.Status = jobs.Done })
	_ = os.WriteFile(filepath.Join(dir, "log"), []byte("fetch https://myUser:s3cret@repo.example.org/alpine/v3.23/main\n"), 0o640)
	got := lines(t, f.stream(t, st.ID, "", admin))
	if len(got) != 1 || got[0] != "fetch https://***@repo.example.org/alpine/v3.23/main" {
		t.Fatalf("lines = %q", got)
	}
}

func TestEventsCarryAtMost64KiB(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, dir := f.job(t, "upgrade", func(st *jobs.State) { st.Status = jobs.Done })
	var b strings.Builder
	for i := 0; i < 1500; i++ { // 100 bytes a line: 150000 bytes, more than two chunks
		fmt.Fprintf(&b, "%099d\n", i)
	}
	_ = os.WriteFile(filepath.Join(dir, "log"), []byte(b.String()), 0o640)
	events, sent := 0, 0
	for _, e := range f.stream(t, st.ID, "", admin) {
		if e.name != "log" {
			continue
		}
		var l LogEvent
		if err := json.Unmarshal([]byte(e.data), &l); err != nil {
			t.Fatal(err)
		}
		size := 0
		for _, line := range l.Lines {
			size += len(line) + 1
		}
		if size > 64<<10 {
			t.Fatalf("event %d carries %d bytes", events, size)
		}
		events, sent = events+1, sent+len(l.Lines)
		if l.Offset != int64(sent)*100 {
			t.Fatalf("event %d ends at offset %d after %d lines", events, l.Offset, sent)
		}
	}
	if events < 3 || sent != 1500 {
		t.Fatalf("%d lines in %d events", sent, events)
	}
}

func TestEventsUnknownJob(t *testing.T) {
	f := newFixture(t, nil, nil)
	expectError(t, f.do(t, "GET", "/jobs/20261013T120000.000-00000000/events", "", admin), 404, "not_found")
	expectError(t, f.do(t, "GET", "/jobs/nope/events", "", admin), 404, "not_found")
}
