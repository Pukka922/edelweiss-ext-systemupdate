package api

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss/sdk"
)

const (
	tailBytes  = 256 << 10 // how much of the log a new stream starts with
	chunkBytes = 64 << 10  // the most log one event carries
)

// LogEvent is the data of a "log" event; its event id is Offset.
type LogEvent struct {
	Lines  []string `json:"lines"`
	Offset int64    `json:"offset"`
}

// events streams a job: "state" whenever its state changes, "log" with new complete lines (admins only; the event id
// is the log offset after them, so that Last-Event-ID resumes exactly), and "end" with the final state.
func (a *API) events(c fiber.Ctx, s *sse.Stream) error {
	// everything from c first: the stream may outlive the request
	id := strings.Clone(c.Params("id"))
	admin := sdk.UserFrom(c).Admin
	logPath := filepath.Join(a.Store.Dir(id), "log")
	offset := startOffset(logPath, s.LastEventID())
	poll := a.Poll
	if poll == 0 {
		poll = 500 * time.Millisecond
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	var last []byte
	for {
		st, err := a.Store.Get(id) // the state first: when it says ended, the log is complete
		if err != nil {
			return err
		}
		ended := st.Finished()
		for admin {
			ev, ok := readLog(logPath, offset, ended)
			if !ok {
				break
			}
			if err := s.Event(sse.Event{Name: "log", ID: strconv.FormatInt(ev.Offset, 10), Data: ev}); err != nil {
				return err
			}
			offset = ev.Offset
		}
		if ended {
			return s.Event(sse.Event{Name: "end", Data: st})
		}
		if data, _ := json.Marshal(st); !bytes.Equal(data, last) {
			if err := s.Event(sse.Event{Name: "state", Data: st}); err != nil {
				return err
			}
			last = data
		}
		select {
		case <-s.Done():
			return nil
		case <-t.C:
		}
	}
}

// startOffset is where a stream starts in the log: after the last event the client got (Last-Event-ID), else at the
// first complete line of the last 256 KiB.
func startOffset(path, lastID string) int64 {
	if n, err := strconv.ParseInt(lastID, 10, 64); err == nil && n >= 0 {
		return n
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= tailBytes {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	start := fi.Size() - tailBytes
	buf := make([]byte, chunkBytes)
	n, _ := f.ReadAt(buf, start-1) // the byte before: start may already be the first byte of a line
	if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
		return start + int64(i)
	}
	return start
}

// readLog reads the complete lines from offset on, at most chunkBytes; with all (the job has ended) also a last line
// without a newline. A line longer than a chunk is split. ok is false when there is nothing to send.
func readLog(path string, offset int64, all bool) (LogEvent, bool) {
	f, err := os.Open(path)
	if err != nil {
		return LogEvent{}, false
	}
	defer f.Close()
	buf := make([]byte, chunkBytes)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return LogEvent{}, false
	}
	end := bytes.LastIndexByte(buf[:n], '\n') + 1
	switch {
	case n == 0:
		return LogEvent{}, false
	case all && n < chunkBytes:
		end = n
	case end == 0 && n == chunkBytes:
		end = n
	case end == 0:
		return LogEvent{}, false
	}
	text := strings.ToValidUTF8(strings.TrimSuffix(string(buf[:end]), "\n"), "�")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = jobs.Redact(lines[i])
	}
	return LogEvent{Lines: lines, Offset: offset + int64(end)}, true
}
