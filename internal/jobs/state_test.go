package jobs

import (
	"os"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

func TestStoreCreateGetList(t *testing.T) {
	s := Store{Base: t.TempDir(), Proc: host.New()}
	now := time.Now()
	id, dir, err := s.Create(Spec{Kind: "upgrade", User: "admin"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(id) {
		t.Fatalf("Create made the invalid id %q", id)
	}
	st, err := s.Get(id)
	if err != nil || st.Status != Queued || st.Kind != "upgrade" || st.User != "admin" || st.Schema != 1 {
		t.Fatalf("Get = %+v, %v", st, err)
	}
	for name, want := range map[string]os.FileMode{"": 0o755, "/spec.json": 0o600, "/state.json": 0o644} {
		if fi, err := os.Stat(dir + name); err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s: mode %v, %v", dir+name, fi.Mode(), err)
		}
	}
	if cur, _ := s.Current(); cur == nil || cur.ID != id {
		t.Fatalf("Current = %+v", cur)
	}
	id2, _, _ := s.Create(Spec{Kind: "refresh"}, now.Add(time.Minute))
	if list, _ := s.List(10); len(list) != 2 || list[0].ID != id2 {
		t.Fatalf("List, newest first: %+v", list)
	}
	if list, _ := s.List(1); len(list) != 1 {
		t.Fatalf("List(1): %+v", list)
	}
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"20261013T120000.000-0a1b2c3d":      true,
		"../etc":                            false,
		"20261013T120000.000-0a1b2c3d/../x": false,
		"":                                  false,
	} {
		if ValidID(id) != want {
			t.Errorf("ValidID(%q) = %v", id, !want)
		}
	}
	if _, err := (Store{Base: t.TempDir(), Proc: host.New()}).Get("../x"); err == nil {
		t.Fatal("Get accepted an invalid id")
	}
}

func TestDeadRunnerIsInterrupted(t *testing.T) {
	s := Store{Base: t.TempDir(), Proc: host.New()}
	id, dir, _ := s.Create(Spec{Kind: "upgrade"}, time.Now())
	me, _ := s.Proc.StartTime(os.Getpid())
	write := func(f func(*State)) {
		st, _ := ReadState(dir)
		f(st)
		if err := WriteJSON(dir+"/state.json", st, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(func(st *State) {
		st.Status, st.RunnerPID, st.RunnerStart, st.BootID = Running, 999999, 1, s.Proc.BootID()
	})
	if got, _ := s.Get(id); got.Status != Interrupted || got.APKAlive {
		t.Fatalf("a dead runner: %+v", got)
	}
	write(func(st *State) { st.APKPID, st.APKStart = os.Getpid(), me })
	if got, _ := s.Get(id); got.Status != Interrupted || !got.APKAlive {
		t.Fatalf("a dead runner whose apk still runs: %+v", got)
	}
	write(func(st *State) { st.RunnerPID, st.RunnerStart, st.BootID = os.Getpid(), me, "another-boot" })
	if got, _ := s.Get(id); got.Status != Interrupted || got.APKAlive {
		t.Fatalf("another boot: %+v", got)
	}
	write(func(st *State) { st.BootID = s.Proc.BootID() })
	if got, _ := s.Get(id); got.Status != Running {
		t.Fatalf("alive: %s", got.Status)
	}
	if cur, _ := s.Current(); cur == nil || cur.ID != id {
		t.Fatalf("Current = %+v", cur)
	}
	old, _, _ := s.Create(Spec{Kind: "refresh"}, time.Now().Add(-2*time.Minute))
	if got, _ := s.Get(old); got.Status != Interrupted {
		t.Fatalf("a job queued two minutes ago whose runner never started: %s", got.Status)
	}
}

// finishedJobs creates n done jobs a minute apart, oldest first, each with a log of logBytes.
func finishedJobs(t *testing.T, s Store, n, logBytes int) []string {
	t.Helper()
	start := time.Now().Add(-time.Hour)
	var ids []string
	for i := 0; i < n; i++ {
		id, dir, err := s.Create(Spec{Kind: "refresh"}, start.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		st, _ := ReadState(dir)
		st.Status = Done
		_ = WriteJSON(dir+"/state.json", st, 0o644)
		_ = os.WriteFile(dir+"/log", make([]byte, logBytes), 0o640)
		ids = append(ids, id)
	}
	return ids
}

func TestPruneKeepsTheNewest(t *testing.T) {
	s := Store{Base: t.TempDir(), Proc: host.New()}
	ids := finishedJobs(t, s, 5, 10)
	if err := s.Prune(3, 1<<30); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(10)
	if len(list) != 3 || list[0].ID != ids[4] || list[2].ID != ids[2] {
		t.Fatalf("after Prune(3): %+v", list)
	}
}

func TestPruneBySize(t *testing.T) {
	s := Store{Base: t.TempDir(), Proc: host.New()}
	ids := finishedJobs(t, s, 3, 1024)
	total := dirSize(t, s.Base+"/jobs")
	if err := s.Prune(20, total-1); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List(10)
	if len(list) != 2 || list[1].ID != ids[1] {
		t.Fatalf("one byte too many removes the oldest job only: %+v", list)
	}
	running, _, _ := s.Create(Spec{Kind: "upgrade"}, time.Now()) // queued just now: not finished
	if err := s.Prune(20, 0); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(10); len(list) != 1 || list[0].ID != running {
		t.Fatalf("Prune never removes an unfinished job: %+v", list)
	}
}

func TestLock(t *testing.T) {
	path := t.TempDir() + "/lock"
	if Locked(path) {
		t.Fatal("a missing lock file is free")
	}
	f, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if !Locked(path) {
		t.Fatal("the held lock is not seen")
	}
	if _, err := Lock(path); err != ErrBusy {
		t.Fatalf("second Lock: %v", err)
	}
	f.Close()
	if Locked(path) {
		t.Fatal("the released lock is still seen")
	}
}
