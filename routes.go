package main

import (
	"os"
	"slices"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/api"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
)

// proxyVars are the proxy variables the runner, apk and the previews inherit from this process.
var proxyVars = []string{"http_proxy", "https_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

// runnerEnv is the whole environment of the runner, its apk and the preview apk calls of this process: apk's fixed
// environment, then those of the proxy variables that are set here (in the order of proxyVars). Nothing else of this
// process's environment gets through.
func runnerEnv() []string {
	env := slices.Clone(apk.Env)
	for _, k := range proxyVars {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// register adds the API. The launcher starts the runner from /proc/self/exe: the binary this process runs from, also
// after an extension update renamed a new one into its place.
func register(r fiber.Router) {
	env := jobs.DefaultEnv()
	env.Runner = apk.Exec{Path: apk.Path, Env: runnerEnv()}
	api.Register(r, &api.API{
		Env:      env,
		Store:    jobs.Store{Base: env.Base, Proc: host.New()},
		Launcher: jobs.ExecLauncher{Binary: "/proc/self/exe", Env: runnerEnv()},
		LockPath: jobs.DefaultLock,
	})
}

// runnerConfig is the configuration of the detached runner (--run-job): the real host, root only.
func runnerConfig() jobs.RunnerConfig {
	env := jobs.DefaultEnv()
	env.Runner = apk.Exec{Path: apk.Path, Env: runnerEnv()}
	return jobs.RunnerConfig{
		Base: env.Base, LockPath: jobs.DefaultLock, APK: env.APK, Env: runnerEnv(), RequireRoot: true,
		Now: time.Now, Proc: host.New(), Kinds: jobs.Kinds(env),
	}
}
