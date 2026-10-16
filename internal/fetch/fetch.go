// Package fetch gets the small metadata files of alpinelinux.org: https only, redirects to the same host only, a
// 15 s timeout, the proxy of the environment, and a size cap per file.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// httpsOnly refuses every request that is not https, before any connection is made.
type httpsOnly struct{ next http.RoundTripper }

func (t httpsOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		if req.Body != nil {
			req.Body.Close() // a RoundTripper closes the body also when it fails
		}
		return nil, fmt.Errorf("fetch: %s: only https is allowed", req.URL.Redacted())
	}
	return t.next.RoundTrip(req)
}

// NewClient returns the client for metadata.
func NewClient() *http.Client {
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: httpsOnly{&http.Transport{Proxy: http.ProxyFromEnvironment}},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("fetch: too many redirects")
			}
			if req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
				return errors.New("fetch: redirect to another host")
			}
			return nil
		},
	}
}

// Get fetches url and fails for a status other than 200, or for a body over limit bytes.
func Get(ctx context.Context, c *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("fetch: %s: larger than %d bytes", url, limit)
	}
	return data, nil
}
