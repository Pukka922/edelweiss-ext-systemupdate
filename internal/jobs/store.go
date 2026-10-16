package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

// Store is the job directories below Base/jobs. Proc is the real host's /proc, for the liveness check.
type Store struct {
	Base string
	Proc host.FS
}

var idRe = regexp.MustCompile(`^\d{8}T\d{6}\.\d{3}-[0-9a-f]{8}$`)

// ValidID reports whether id has the form Create gives ids (the API passes ids from URLs).
func ValidID(id string) bool { return idRe.MatchString(id) }

// Dir is the directory of job id.
func (s Store) Dir(id string) string { return filepath.Join(s.Base, "jobs", id) }

// Create makes a job directory (0755) with spec.json (0600) and a queued state.json (0644). Ids sort by creation
// time.
func (s Store) Create(spec Spec, now time.Time) (string, string, error) {
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", "", err
	}
	id := now.UTC().Format("20060102T150405.000") + "-" + hex.EncodeToString(rnd[:])
	dir := s.Dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	for _, d := range []string{s.Base, filepath.Dir(dir), dir} { // MkdirAll applies the umask
		if err := os.Chmod(d, 0o755); err != nil {
			return "", "", err
		}
	}
	if err := WriteJSON(filepath.Join(dir, "spec.json"), spec, 0o600); err != nil {
		return "", "", err
	}
	st := State{Schema: schema, ID: id, Kind: spec.Kind, Target: spec.Target, User: spec.User, Status: Queued,
		Steps: []Step{}, Created: now}
	if err := WriteJSON(filepath.Join(dir, "state.json"), &st, 0o644); err != nil {
		return "", "", err
	}
	return id, dir, nil
}

// effective reports a running job whose runner is gone (another boot, a dead or reused pid) as interrupted, and
// whether its apk still runs. A queued job whose runner has not started after a minute never will.
func (s Store) effective(st *State) *State {
	switch st.Status {
	case Running:
		sameBoot := st.BootID == s.Proc.BootID()
		if !sameBoot || !s.Proc.Alive(st.RunnerPID, st.RunnerStart) {
			st.Status = Interrupted
			st.APKAlive = sameBoot && st.APKPID != 0 && s.Proc.Alive(st.APKPID, st.APKStart)
		}
	case Queued:
		if st.RunnerPID == 0 && time.Since(st.Created) > time.Minute {
			st.Status = Interrupted
		}
	}
	return st
}

// Get reads job id with its effective status.
func (s Store) Get(id string) (*State, error) {
	if !ValidID(id) {
		return nil, errors.New("jobs: invalid id")
	}
	st, err := ReadState(s.Dir(id))
	if err != nil {
		return nil, err
	}
	return s.effective(st), nil
}

// ids lists the job ids, newest first.
func (s Store) ids() []string {
	entries, _ := os.ReadDir(filepath.Join(s.Base, "jobs"))
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// Current is the newest job that is queued or running (nil if none).
func (s Store) Current() (*State, error) {
	for _, id := range s.ids() {
		if st, err := s.Get(id); err == nil && !st.Finished() {
			return st, nil
		}
	}
	return nil, nil
}

// List returns up to n jobs, newest first.
func (s Store) List(n int) ([]State, error) {
	out := []State{}
	for _, id := range s.ids() {
		if len(out) == n {
			break
		}
		if st, err := s.Get(id); err == nil {
			out = append(out, *st)
		}
	}
	return out, nil
}

// Prune removes finished jobs, oldest first: every one beyond the newest keep jobs, then more while all jobs together
// take more than maxBytes. A job that is not finished, or whose state cannot be read, is never removed.
func (s Store) Prune(keep int, maxBytes int64) error {
	ids := s.ids() // newest first
	sizes := make(map[string]int64, len(ids))
	var total int64
	for _, id := range ids {
		sizes[id] = size(s.Dir(id))
		total += sizes[id]
	}
	for i := len(ids) - 1; i >= 0; i-- {
		if i < keep && total <= maxBytes {
			break
		}
		st, err := s.Get(ids[i])
		if err != nil || !st.Finished() {
			continue
		}
		if err := os.RemoveAll(s.Dir(ids[i])); err != nil {
			return err
		}
		total -= sizes[ids[i]]
	}
	return nil
}

func size(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
