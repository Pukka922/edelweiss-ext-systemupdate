package api

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss/sdk"
)

func busy(cur *jobs.State) error {
	e := sdk.NewError(fiber.StatusConflict, "busy", "another job runs")
	if cur != nil {
		e = e.WithDetails(map[string]any{"job": cur})
	}
	return e
}

func (a *API) refresh(c fiber.Ctx) error {
	return a.launch(c, jobs.Spec{Kind: "refresh"})
}

type previewRequest struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

func (a *API) preview(c fiber.Ctx) error {
	var req previewRequest
	if err := body(c, &req); err != nil {
		return err
	}
	if req.Kind != "upgrade" && req.Kind != "release" {
		return fieldError("invalid_kind", "kind", "kind must be upgrade or release")
	}
	if req.Kind == "release" && req.Target == "" {
		return fieldError("invalid_target", "target", "a release upgrade needs a target")
	}
	if cur, _ := a.Store.Current(); cur != nil {
		return busy(cur)
	}
	done := sdk.Busy() // a preview runs apk for up to some seconds
	defer done()
	var p *jobs.Plan
	var err error
	if req.Kind == "upgrade" {
		p, err = a.Env.PreviewUpgrade(c.Context())
	} else {
		p, err = a.Env.PreviewRelease(c.Context(), req.Target)
	}
	if err != nil {
		return err
	}
	return c.JSON(p)
}

// startRequest starts a job. Confirm is the word the administrator typed for a release upgrade to edge ("edge").
type startRequest struct {
	Kind    string `json:"kind"`
	PlanID  string `json:"planId"`
	Confirm string `json:"confirm"`
}

func (a *API) start(c fiber.Ctx) error {
	var req startRequest
	if err := body(c, &req); err != nil {
		return err
	}
	spec := jobs.Spec{Kind: req.Kind}
	switch req.Kind {
	case "upgrade", "release":
		p, err := a.Env.LoadPlan(req.PlanID)
		if err != nil {
			return sdk.NewError(fiber.StatusConflict, "plan_expired", "the preview is outdated: preview again")
		}
		if p.Kind != req.Kind {
			return fieldError("invalid_plan", "planId", "the plan is for another kind of job")
		}
		if p.Kind == "release" && p.Target == "edge" && req.Confirm != "edge" {
			return fieldError("invalid_confirm", "confirm", `a release upgrade to edge needs the confirmation "edge"`)
		}
		if len(p.Blockers) > 0 {
			return sdk.NewError(fiber.StatusConflict, "plan_blocked", "the preview found problems that block this job")
		}
		spec.PlanID, spec.Target = p.ID, p.Target
	case "resume":
		r := a.Env.Resumable(a.Store)
		if r == nil {
			return sdk.NewError(fiber.StatusConflict, "nothing_to_resume", "there is no release upgrade to resume")
		}
		spec.Target = r.Target
	default:
		return fieldError("invalid_kind", "kind", "kind must be upgrade, release or resume")
	}
	return a.launch(c, spec)
}

// launch creates the job, starts its runner and waits until it runs (at most StartWait); 202 with its state. It
// answers 409 busy while a job runs, while the lock is held, and while the newest job lost its runner but its apk
// still runs: the lock is free then, but apk's database is not.
func (a *API) launch(c fiber.Ctx, spec jobs.Spec) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, _ := a.Store.Current()
	if cur != nil || jobs.Locked(a.LockPath) {
		return busy(cur)
	}
	if newest, _ := a.Store.List(1); len(newest) == 1 && newest[0].Status == jobs.Interrupted && newest[0].APKAlive {
		return busy(&newest[0])
	}
	spec.User = sdk.UserFrom(c).Name
	id, dir, err := a.Store.Create(spec, time.Now())
	if err != nil {
		return err
	}
	if err := a.Launcher.Launch(dir); err != nil {
		if st, rerr := jobs.ReadState(dir); rerr == nil {
			st.Status, st.Error, st.Ended = jobs.Failed, "could not start the runner: "+err.Error(), time.Now()
			_ = jobs.WriteJSON(filepath.Join(dir, "state.json"), st, 0o644)
		}
		return sdk.NewError(fiber.StatusInternalServerError, "launch_failed", "the job could not be started")
	}
	wait := a.StartWait
	if wait == 0 {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	st, err := a.Store.Get(id)
	for err == nil && st.Status == jobs.Queued && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		st, err = a.Store.Get(id)
	}
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(st)
}

func (a *API) current(c fiber.Ctx) error {
	cur, err := a.Store.Current()
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"job": cur})
}

func (a *API) list(c fiber.Ctx) error {
	list, err := a.Store.List(20)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"jobs": list})
}

// checkJob answers 404 for an unknown job id, before a log or a stream opens.
func (a *API) checkJob(c fiber.Ctx) error {
	if _, err := a.Store.Get(c.Params("id")); err != nil {
		return sdk.NewError(fiber.StatusNotFound, "not_found", "no such job")
	}
	return c.Next()
}

func (a *API) log(c fiber.Ctx) error {
	data, err := os.ReadFile(filepath.Join(a.Store.Dir(c.Params("id")), "log"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	return c.SendString(jobs.Redact(strings.ToValidUTF8(string(data), "�")))
}
