package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/releases"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/repos"
)

// g are the options of every apk step of a job ("G" in the spec).
func g(args ...string) []string {
	return append([]string{"--no-interactive", "--wait", "300"}, args...)
}

// Kinds are the job kinds the runner knows.
func Kinds(e Env) map[string]Kind {
	return map[string]Kind{"refresh": e.refresh, "upgrade": e.upgrade, "release": e.release, "resume": e.resume}
}

const (
	busyError          = "apk is busy: another apk holds the database lock"
	currentFailedError = "the current branch could not be fully upgraded; nothing was switched"
)

// finish maps the exit status of the last apk step to the job's status.
func finish(r *Run, exit int, out string) {
	switch o, _ := apk.Classify(exit, out); o {
	case apk.OK:
	case apk.Partial:
		r.Set(func(s *State) { s.Status = Partial })
	case apk.LockTimeout:
		r.Set(func(s *State) { s.Status, s.Error = Failed, busyError })
	default:
		r.Set(func(s *State) { s.Status = Failed })
	}
}

// noteBusy gives the job finish's error when exit says that apk gave up waiting for its database lock. It leaves the
// status to the caller, which may restore the repositories or go on.
func noteBusy(r *Run, exit int, out string) {
	if o, _ := apk.Classify(exit, out); o == apk.LockTimeout {
		r.Set(func(s *State) { s.Error = busyError })
	}
}

func (e Env) refresh(r *Run) error {
	exit, out, err := r.APK("update", g("update")...)
	if err != nil {
		return err
	}
	ctx := context.Background()
	release, _ := e.Host.Release()
	for _, ferr := range e.fetchMetadata(ctx, releases.BranchOf(release)) {
		r.Note("metadata: %s", Redact(ferr.Error())) // no network: no badges, no release offer – no failure
	}
	if err := e.refreshList(ctx); err != nil {
		r.Note("list: %v", err)
	}
	finish(r, exit, out)
	return nil
}

func (e Env) upgrade(r *Run) error {
	start := e.Now()
	before, known := e.dbSum()
	exit, out, err := r.APK("self-upgrade", g("upgrade", "--self-upgrade-only")...)
	if err != nil {
		return err
	}
	if exit == 0 {
		var out2 string
		exit, out2, err = r.APK("upgrade", g("upgrade", "--no-self-upgrade")...)
		out += out2
		if err != nil {
			return err
		}
	}
	finish(r, exit, out)
	// lbu commit is for a host known to run from RAM: a mode that cannot be read is no reason to run it (on a system
	// installed to disk it would fail, or worse, save a configuration nobody asked to save)
	mode, merr := e.Host.Mode()
	fromRAM := merr == nil && (mode == host.Diskless || mode == host.Data)
	if fromRAM && (apk.HasChanges(out) || !e.dbUnchanged(before, known)) {
		e.persist(r)
	}
	post := e.post(start, out, false)
	post.UpdateKernel = fromRAM && bootPackageChanged(out)
	r.setPost(post)
	if err := e.refreshList(context.Background()); err != nil {
		r.Note("list: %v", err)
	}
	return nil
}

// persist saves the changes of a diskless or data mode host to its boot medium: the apk cache, then lbu commit as
// the last step, also after a partial upgrade. A failing commit leaves the job done_not_persisted: the changes are
// gone after a reboot.
func (e Env) persist(r *Run) {
	if _, err := os.Stat(filepath.Join(e.Root, "etc/apk/cache")); err == nil {
		_, _, _ = r.APK("cache-clean", g("cache", "clean")...)
	}
	if exit, _, err := r.Exec("lbu-commit", e.LBU, "commit"); err != nil || exit != 0 {
		r.Set(func(s *State) {
			if s.Status == Running {
				s.Status = DoneNotPersisted
			} else {
				s.Error = "lbu commit failed: the changes are lost on reboot"
			}
		})
	}
}

// fileSum is the sha256 of the file at path; ok is false when it cannot be read.
func fileSum(path string) (sum string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), true
}

// dbSum is the fingerprint of apk's database of installed packages. apk writes it only after its change loop, so it
// says nothing about a transaction that stopped in the middle of that loop: the change lines of apk's output do.
func (e Env) dbSum() (string, bool) { return fileSum(filepath.Join(e.Root, "lib/apk/db/installed")) }

// dbUnchanged reports whether the database is still the one dbSum fingerprinted; a database that cannot be read is
// no proof of that.
func (e Env) dbUnchanged(before string, known bool) bool {
	after, ok := e.dbSum()
	return known && ok && after == before
}

// writeRepos writes /etc/apk/repositories (a variable for the tests, which make the write fail).
var writeRepos = repos.WriteAtomic

// requireSys fails unless the host is known to be installed to disk: the preview checks it, and every job that
// switches or resumes a release upgrade checks it again (the host may have changed since, and a plan is only a file).
func (e Env) requireSys() error {
	if mode, err := e.Host.Mode(); err != nil || mode != host.Sys {
		return errors.New("a release upgrade needs a system installed to disk")
	}
	return nil
}

func (e Env) release(r *Run) error {
	if err := e.requireSys(); err != nil {
		return err
	}
	plan, err := e.LoadPlan(r.Spec.PlanID)
	if err != nil {
		return err
	}
	if plan.Kind != "release" || plan.Target != r.Spec.Target || len(plan.Blockers) > 0 {
		return errors.New("the plan does not allow this job")
	}
	if err := e.allowed(r.Spec.Target); err != nil {
		return err
	}
	fs, err := repos.Read(e.Root) // R1
	if err != nil {
		return err
	}
	if fs.Sum() != plan.ReposSum {
		return errors.New("the repositories changed since the preview")
	}
	rp, err := repos.Rewrite(fs.Main.Content, r.Spec.Target)
	if err != nil {
		return err
	}
	if rp.Changed == 0 { // latest-stable lines only: no line changes, R4–R8 only
		return e.switchedUpgrade(r, false)
	}
	// R2: the current branch first, fully (it brings the newest alpine-keys); nothing to restore if it fails
	exit, out, err := r.APK("current-self-upgrade", g("upgrade", "--self-upgrade-only")...)
	if err == nil && exit == 0 {
		exit, out, err = r.APK("current-upgrade", g("upgrade", "--no-self-upgrade")...)
	}
	if err != nil {
		return err
	}
	if exit != 0 {
		// Whatever apk ended with (a few errors are "partial" for any other step), nothing was switched and there is no
		// release upgrade to resume: the job failed. Partial would make the page hold the restart back for an upgrade
		// that never began.
		reason := currentFailedError
		if o, _ := apk.Classify(exit, out); o == apk.LockTimeout {
			reason = busyError
		}
		r.Set(func(s *State) { s.Status, s.Error = Failed, reason })
		return nil
	}
	// R3: back up, mark, switch
	if err := repos.Backup(e.Root, r.ID()); err != nil {
		return err
	}
	r.Set(func(s *State) { s.ReposSwitched = true })
	if err := writeRepos(filepath.Join(e.Root, "etc/apk/repositories"), []byte(rp.Content), 0o644); err != nil {
		// The write can fail after its rename (the fsync of the directory), so the new file may be in place. Nothing
		// else has happened yet, so putting the backup back is right in both cases.
		if rerr := repos.Restore(e.Root, r.ID()); rerr != nil {
			// nobody knows which file is in place: the job stays marked as switched
			return fmt.Errorf("switching the repositories failed (%v) and the old ones could not be put back: %w", err, rerr)
		}
		r.Set(func(s *State) { s.ReposSwitched = false })
		return err
	}
	return e.switchedUpgrade(r, true)
}

// switchedUpgrade is R4–R8 on switched repositories. A release job may restore the old repositories while no
// package has changed (canRestore); a resume never does, and neither does a switch without changed lines.
func (e Env) switchedUpgrade(r *Run, canRestore bool) error {
	restore := func() {
		if !canRestore {
			r.Set(func(s *State) { s.Status = Failed })
			return
		}
		if err := repos.Restore(e.Root, r.ID()); err != nil {
			r.Note("restoring the repositories failed: %v", err)
			r.Set(func(s *State) { s.Status, s.Error = Failed, "the old repositories could not be restored" })
			return
		}
		r.Set(func(s *State) { s.ReposSwitched = false })
		_, _, _ = r.APK("restore-update", g("update")...) // best effort: the indexes of the old branch
		r.Set(func(s *State) { s.Status = FailedClean })
	}
	exit, out, err := r.APK("update", g("update")...) // R4
	if err != nil || exit != 0 || len(updateFailures(out)) > 0 {
		noteBusy(r, exit, out)
		restore()
		return nil
	}
	f0, known := e.dbSum() // R5
	start := e.Now()
	exit, out, err = r.APK("self-upgrade", g("upgrade", "--self-upgrade-only")...) // R6
	last := out
	if err == nil && exit == 0 {
		var out7 string
		exit, out7, err = r.APK("upgrade", g("upgrade", "-a", "--no-self-upgrade")...) // R7
		out, last = out+out7, out7
	}
	if err != nil || exit != 0 {
		noteBusy(r, exit, last)
		// Back only while nothing is known to have changed: no change line in what apk printed (any counter, padded
		// or not) and a database that is still the same.
		if !apk.HasChanges(out) && e.dbUnchanged(f0, known) {
			restore()
			return nil
		}
		r.Set(func(s *State) { s.Status = Partial }) // never back after a commit: Resume goes forward
	}
	unix.Sync() // R8; also after a partial run: what changed should be on disk
	r.setPost(e.post(start, out, true))
	if err := e.refreshList(context.Background()); err != nil {
		r.Note("list: %v", err)
	}
	return nil
}

// resume runs R4–R8 again after a partial or interrupted release upgrade, on the repositories as they are: they must
// already point to the target, or follow latest-stable. It never rewrites or restores them.
func (e Env) resume(r *Run) error {
	if err := e.requireSys(); err != nil {
		return err
	}
	fs, err := repos.Read(e.Root)
	if err != nil {
		return err
	}
	target := r.Spec.Target
	switched := false
	switch br := repos.Branches(fs.Main.Content); {
	case !repos.ValidTarget(target):
		return errors.New("the repositories do not point to the target")
	case len(br) == 1 && br[0] == target:
		switched = true
	case len(br) == 0 && hasLatestStable(fs.Main.Content) && target != "edge":
		// lines that follow latest-stable name no branch; the job switched nothing
	default:
		return errors.New("the repositories do not point to the target")
	}
	// the repositories may point to the target without this page ever having upgraded to it: not a branch to run
	// apk upgrade -a on without a preview
	list, _ := Store{Base: r.Cfg.Base, Proc: r.Cfg.Proc}.List(20)
	if !e.resumeTargetOffered(list, target) {
		return errors.New("the repositories point to a branch this page did not upgrade to")
	}
	if switched {
		r.Set(func(s *State) { s.ReposSwitched = true })
	}
	return e.switchedUpgrade(r, false)
}

// Resume is a release upgrade the page offers to resume.
type Resume struct {
	Target string `json:"target"`
	Reason string `json:"reason"` // mismatch, partial, interrupted, failed
}

// resumeTargetOffered reports whether a resume to target may be offered or run: only to a branch this page upgrades
// to, never to one the repositories merely name (they may have been edited to a branch that is gone, to one beyond
// the next, or to edge, and a resume runs apk upgrade -a there with one plain confirmation, past the preview and
// every pre-flight check). That is the target of the newest release job in list (newest first), whatever became of
// it, as that job went through a preview, or the next allowed stable branch.
func (e Env) resumeTargetOffered(list []State, target string) bool {
	for _, j := range list {
		if j.Kind == "release" {
			if j.Target == target {
				return true
			}
			break
		}
	}
	targets, _ := e.Targets()
	for _, t := range targets {
		if !t.Edge && t.Branch == target {
			return true
		}
	}
	return false
}

// Resumable reports the release upgrade to resume: the repositories point to a newer branch than the installed
// release (an upgrade stopped before alpine-release changed), or the newest release or resume job on the branch they
// point to did not finish well. Repositories that follow latest-stable name no branch: there the newest release or
// resume job says where the upgrade was going (and there is no "mismatch"). Nothing while a job runs, on a host that
// is not installed to disk, when the installed release is unknown, or when the branch to resume to is not one this
// page upgrades to (resumeTargetOffered).
func (e Env) Resumable(s Store) *Resume {
	res := e.resumable(s)
	if res == nil {
		return nil
	}
	list, _ := s.List(20)
	if !e.resumeTargetOffered(list, res.Target) {
		return nil
	}
	return res
}

func (e Env) resumable(s Store) *Resume {
	if cur, _ := s.Current(); cur != nil {
		return nil
	}
	if mode, err := e.Host.Mode(); err != nil || mode != host.Sys {
		return nil
	}
	release, _ := e.Host.Release()
	current := releases.BranchOf(release)
	if current == "" {
		return nil
	}
	fs, err := repos.Read(e.Root)
	if err != nil {
		return nil
	}
	var target string // "" for repositories that follow latest-stable
	switch br := repos.Branches(fs.Main.Content); {
	case len(br) == 1 && repos.ValidTarget(br[0]):
		target = br[0]
		if BranchLess(current, target) {
			return &Resume{Target: target, Reason: "mismatch"}
		}
	case len(br) == 0 && hasLatestStable(fs.Main.Content):
	default:
		return nil
	}
	list, _ := s.List(20)
	for _, j := range list {
		if j.Kind != "release" && j.Kind != "resume" {
			continue
		}
		if target != "" && j.Target != target {
			return nil
		}
		if target == "" && (!repos.ValidTarget(j.Target) || j.Target == "edge") {
			return nil // latest-stable lines never lead to edge
		}
		switch {
		case j.Status == Partial:
			return &Resume{Target: j.Target, Reason: "partial"}
		case j.Status == Interrupted && (j.ReposSwitched || target == ""): // a latest-stable job switches nothing
			return &Resume{Target: j.Target, Reason: "interrupted"}
		case j.Status == Failed && j.Kind == "resume":
			return &Resume{Target: j.Target, Reason: "failed"}
		}
		return nil
	}
	return nil
}
