package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetLimitAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Write([]byte(strings.Repeat("x", 2048)))
		case "/exact":
			w.Write([]byte(strings.Repeat("x", 1024)))
		case "/ok":
			w.Write([]byte(`{"a":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := srv.Client()
	if b, err := Get(context.Background(), c, srv.URL+"/ok", 1024); err != nil || string(b) != `{"a":1}` {
		t.Fatalf("ok: %q %v", b, err)
	}
	if b, err := Get(context.Background(), c, srv.URL+"/exact", 1024); err != nil || len(b) != 1024 {
		t.Fatalf("a body of exactly the limit is fine: %d %v", len(b), err)
	}
	if _, err := Get(context.Background(), c, srv.URL+"/big", 1024); err == nil {
		t.Fatal("a body over the limit must fail")
	}
	if _, err := Get(context.Background(), c, srv.URL+"/missing", 1024); err == nil {
		t.Fatal("404 must fail")
	}
}

func TestClientHTTPSOnly(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	if _, err := Get(context.Background(), NewClient(), srv.URL+"/x", 1024); err == nil {
		t.Fatal("a plain http URL must fail")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the server was contacted %d times", n)
	}
}

func TestClientRedirects(t *testing.T) {
	c := NewClient()
	if c.Timeout != 15*time.Second {
		t.Fatalf("timeout %v", c.Timeout)
	}
	first, _ := http.NewRequest("GET", "https://alpinelinux.org/x", nil)
	for _, bad := range []string{"https://evil.example/x", "http://alpinelinux.org/x"} {
		req, _ := http.NewRequest("GET", bad, nil)
		if c.CheckRedirect(req, []*http.Request{first}) == nil {
			t.Errorf("redirect to %s accepted", bad)
		}
	}
	same, _ := http.NewRequest("GET", "https://alpinelinux.org/y", nil)
	if c.CheckRedirect(same, []*http.Request{first}) != nil {
		t.Fatal("a same-host https redirect is fine")
	}
}
