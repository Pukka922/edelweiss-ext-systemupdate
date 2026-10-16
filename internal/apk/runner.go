package apk

import (
	"context"
	"errors"
	"os/exec"
)

// Path is apk's location on Alpine.
const Path = "/sbin/apk"

// Env is the whole environment apk runs with.
var Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}

// Runner runs apk with arguments and returns its combined output and exit status. err is set only when apk could
// not be run at all.
type Runner interface {
	Run(ctx context.Context, args ...string) (out string, exit int, err error)
}

// Exec runs the real apk (Path) with Env, unless Path or Env are set.
type Exec struct {
	Path string
	Env  []string
}

func (e Exec) Run(ctx context.Context, args ...string) (string, int, error) {
	path, env := e.Path, e.Env
	if path == "" {
		path = Path
	}
	if env == nil {
		env = Env
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Dir = "/"
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	if err != nil {
		return string(out), -1, err
	}
	return string(out), 0, nil
}
