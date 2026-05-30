// Package httpx is a tiny HTTP helper shared by providers: it sets common
// behavior (timeout, single 429 back-off honoring Retry-After) and surfaces
// non-200 responses as a typed StatusError so providers can detect auth loss.
package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client wraps an *http.Client with a small retry/error policy.
type Client struct {
	HTTP *http.Client
}

// New returns a Client with a 30s timeout.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Get issues a GET and returns the response body for HTTP 200. A single 429 is
// retried after Retry-After (default 2s). Any other non-200 returns the body
// alongside a *StatusError.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			return body, nil
		case resp.StatusCode == http.StatusTooManyRequests && attempt == 0:
			wait := parseRetryAfter(resp.Header.Get("Retry-After"))
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		default:
			return body, &StatusError{Code: resp.StatusCode, Body: string(body)}
		}
	}
}

// StatusError is returned for non-200 responses.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Code, truncate(e.Body, 200))
}

// Unauthorized reports whether the status indicates lost/invalid auth.
func (e *StatusError) Unauthorized() bool {
	return e.Code == http.StatusUnauthorized || e.Code == http.StatusForbidden
}

func parseRetryAfter(s string) time.Duration {
	if s == "" {
		return 2 * time.Second
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return 2 * time.Second
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
