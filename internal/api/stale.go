package api

import (
	"encoding/json"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
)

// live reduces post.stale of each state to the processes that still use replaced files. The list is a snapshot the
// job took when it ended and the store keeps it; the page must not show it once the processes have restarted (a
// service restart, a reboot). A rescan is not an option: /proc/<pid>/maps of other users' processes needs root,
// /proc/<pid>/stat does not. The states are the caller's copies; the store is never written.
//
// An entry with a start is kept while that process runs. An entry without one (a job from before the start was
// recorded) cannot be checked: it is dropped when its job ended before the host booted, kept otherwise, and kept
// whenever the boot time is unknown.
func (a *API) live(states ...*jobs.State) {
	var boot time.Time
	var bootRead bool
	bootTime := func() (time.Time, bool) {
		if !bootRead {
			bootRead = true
			boot, _ = a.Env.Host.BootTime()
		}
		return boot, !boot.IsZero()
	}
	for _, st := range states {
		if st == nil || len(st.Post) == 0 {
			continue
		}
		var fields map[string]json.RawMessage // the other fields of the post stay as they are
		if json.Unmarshal(st.Post, &fields) != nil || len(fields["stale"]) == 0 {
			continue
		}
		var stale []host.Process
		if json.Unmarshal(fields["stale"], &stale) != nil {
			continue
		}
		kept := make([]host.Process, 0, len(stale))
		for _, p := range stale {
			if p.Start != 0 {
				if a.Env.Host.Running(p) {
					kept = append(kept, p)
				}
				continue
			}
			ended := st.Ended
			if ended.IsZero() {
				ended = st.Created
			}
			if b, ok := bootTime(); !ok || !ended.Before(b) {
				kept = append(kept, p)
			}
		}
		if len(kept) == len(stale) {
			continue
		}
		data, err := json.Marshal(kept)
		if err != nil {
			continue
		}
		fields["stale"] = data
		if post, err := json.Marshal(fields); err == nil {
			st.Post = post
		}
	}
}
