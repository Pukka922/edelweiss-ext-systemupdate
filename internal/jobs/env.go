package jobs

import (
	"net/http"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/fetch"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/releases"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/secdb"
)

// Env are the dependencies of previews and job kinds. Root is the host root of the files they read and change.
type Env struct {
	Root, Base, RunDir, APK, LBU string
	Host                         host.FS
	Runner                       apk.Runner
	Statfs                       host.Statfs
	Uname                        func() string
	Now                          func() time.Time
	HTTP                         *http.Client
	ReleasesURL                  string
	SecDBURL                     func(branch, repo string) string
}

// DefaultEnv is the real host.
func DefaultEnv() Env {
	return Env{
		Root: "/", Base: DefaultBase, RunDir: "/run/edelweiss-systemupdate", APK: apk.Path, LBU: "/sbin/lbu",
		Host: host.New(), Runner: apk.Exec{Path: apk.Path, Env: apk.Env}, Statfs: host.DiskSpace, Uname: uname,
		Now: time.Now, HTTP: fetch.NewClient(), ReleasesURL: releases.URL, SecDBURL: secdb.URL,
	}
}

func uname() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	return unix.ByteSliceToString(u.Release[:])
}
