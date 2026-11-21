package utils

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// countingTransport always answers with the given status code and counts the
// attempts, recording the body of every attempt.
type countingTransport struct {
	status int
	calls  int
	bodies []string
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls++
	body := ""
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(raw)
	}
	c.bodies = append(c.bodies, body)
	return &http.Response{
		StatusCode: c.status,
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func newTestTransport(base http.RoundTripper) *RetryTransport {
	return &RetryTransport{Base: base, MaxRetries: 2, BaseDelay: time.Millisecond}
}

func newRequest(t *testing.T, ctx context.Context, method, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://example.com/api", reader)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	return req
}

func TestRetryTransportRetriesIdempotentMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		base := &countingTransport{status: http.StatusBadGateway}
		resp, err := newTestTransport(base).RoundTrip(newRequest(t, context.Background(), method, ""))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", method, err)
		}
		resp.Body.Close()
		if base.calls != 3 {
			t.Errorf("%s: expected 3 attempts, got %d", method, base.calls)
		}
	}
}

func TestRetryTransportDoesNotRetryMutations(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		base := &countingTransport{status: http.StatusInternalServerError}
		resp, err := newTestTransport(base).RoundTrip(newRequest(t, context.Background(), method, `{"name":"repo"}`))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", method, err)
		}
		resp.Body.Close()
		if base.calls != 1 {
			t.Errorf("%s: mutation must be sent exactly once, got %d attempts", method, base.calls)
		}
	}
}

func TestRetryTransportRetriesMutationWithIdempotencyKey(t *testing.T) {
	base := &countingTransport{status: http.StatusServiceUnavailable}
	req := newRequest(t, context.Background(), http.MethodPost, `{"name":"repo"}`)
	req.Header.Set(IdempotencyKeyHeader, "key-1")

	resp, err := newTestTransport(base).RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if base.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", base.calls)
	}
	for i, body := range base.bodies {
		if body != `{"name":"repo"}` {
			t.Errorf("attempt %d sent body %q, want the original body", i+1, body)
		}
	}
}

func TestRetryTransportRetriesMutationWhenOptedIn(t *testing.T) {
	base := &countingTransport{status: http.StatusGatewayTimeout}
	req := newRequest(t, AllowMutationRetries(context.Background()), http.MethodPost, `{"name":"repo"}`)

	resp, err := newTestTransport(base).RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if base.calls != 3 {
		t.Errorf("expected 3 attempts, got %d", base.calls)
	}
}

func TestRetryTransportDoesNotRetrySuccess(t *testing.T) {
	base := &countingTransport{status: http.StatusOK}
	resp, err := newTestTransport(base).RoundTrip(newRequest(t, context.Background(), http.MethodGet, ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if base.calls != 1 {
		t.Errorf("expected 1 attempt, got %d", base.calls)
	}
}

func TestRetryTransportDoesNotMutateRequest(t *testing.T) {
	base := &countingTransport{status: http.StatusInternalServerError}
	req := newRequest(t, context.Background(), http.MethodPut, `{"name":"repo"}`)
	originalBody := req.Body

	resp, err := newTestTransport(base).RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if req.Body != originalBody {
		t.Error("RoundTrip replaced the body of the caller's request")
	}
}

func TestRetryTransportAbortsOnContextCancellation(t *testing.T) {
	base := &countingTransport{status: http.StatusInternalServerError}
	ctx, cancel := context.WithCancel(context.Background())
	transport := &RetryTransport{Base: base, MaxRetries: 5, BaseDelay: time.Hour}

	cancel()
	if _, err := transport.RoundTrip(newRequest(t, ctx, http.MethodGet, "")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if base.calls != 1 {
		t.Errorf("expected 1 attempt before giving up, got %d", base.calls)
	}
}
