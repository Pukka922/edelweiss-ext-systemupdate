// Package jobs runs apk changes as jobs that outlive the extension's process: the extension starts its own binary
// with --run-job <dir> in a new session, and that runner – not the extension – runs apk, writing the job's state,
// log and progress to files below the job directory that every extension instance reads.
package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/repos"
)

const (
	DefaultBase = "/var/lib/edelweiss-ext-systemupdate"
	DefaultLock = "/run/edelweiss-systemupdate.lock"
	schema      = 1
)

// Status of a job.
type Status string

const (
	Queued           Status = "queued"
	Running          Status = "running"
	Done             Status = "done"
	Partial          Status = "partial"
	Failed           Status = "failed"
	FailedClean      Status = "failed_clean"       // a release upgrade failed before any package changed; restored
	DoneNotPersisted Status = "done_not_persisted" // diskless/data: the packages changed but lbu commit failed
	Interrupted      Status = "interrupted"        // the runner died (crash, reboot): computed, never written
)

// Spec is what a job is to do; the API writes it, the runner reads it.
type Spec struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	PlanID string `json:"planId,omitempty"`
	User   string `json:"user"`
}

// Step is one program run of a job.
type Step struct {
	Name    string    `json:"name"`
	Exit    int       `json:"exit"`
	Errors  int       `json:"errors"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`
}

// State is state.json of a job.
type State struct {
	Schema        int             `json:"schema"`
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Target        string          `json:"target,omitempty"`
	User          string          `json:"user"`
	Status        Status          `json:"status"`
	Phase         string          `json:"phase,omitempty"`
	Steps         []Step          `json:"steps"`
	Created       time.Time       `json:"created"`
	Started       time.Time       `json:"started,omitzero"`
	Ended         time.Time       `json:"ended,omitzero"`
	RunnerPID     int             `json:"runnerPid,omitempty"`
	RunnerStart   uint64          `json:"runnerStart,omitempty"`
	BootID        string          `json:"bootId,omitempty"`
	APKPID        int             `json:"apkPid,omitempty"`
	APKStart      uint64          `json:"apkStart,omitempty"`
	APKAlive      bool            `json:"apkAlive,omitempty"`
	Waiting       bool            `json:"waiting,omitempty"`
	LockHolder    *host.Process   `json:"lockHolder,omitempty"`
	ReposSwitched bool            `json:"reposSwitched,omitempty"`
	Progress      *apk.Progress   `json:"progress,omitempty"`
	Current       string          `json:"current,omitempty"`
	Post          json.RawMessage `json:"post,omitempty"`
	Error         string          `json:"error,omitempty"`
}

// Finished reports whether the job ended, well or not.
func (s *State) Finished() bool { return s.Status != Queued && s.Status != Running }

// WriteFile writes data to path atomically with perm.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return repos.WriteAtomic(path, data, perm)
}

// WriteJSON writes v as JSON to path atomically with perm.
func WriteJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return WriteFile(path, append(data, '\n'), perm)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// ReadState reads state.json of the job in dir.
func ReadState(dir string) (*State, error) {
	var s State
	if err := readJSON(filepath.Join(dir, "state.json"), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ReadSpec reads spec.json of the job in dir.
func ReadSpec(dir string) (*Spec, error) {
	var s Spec
	if err := readJSON(filepath.Join(dir, "spec.json"), &s); err != nil {
		return nil, err
	}
	return &s, nil
}
