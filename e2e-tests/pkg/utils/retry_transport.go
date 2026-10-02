package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// IdempotencyKeyHeader is the de facto standard header used by APIs that
// de-duplicate repeated mutations server side. A request carrying it is safe
// to replay even though its method is not idempotent.
const IdempotencyKeyHeader = "Idempotency-Key"

// mutationRetryKey marks a request context as safe to replay despite using a
// non-idempotent method, see AllowMutationRetries.
type mutationRetryKey struct{}

// AllowMutationRetries returns a context that opts requests made with it into
// retries even when their HTTP method is not idempotent.
//
// Only use it for calls that are reconciled by the caller, i.e. where a replay
// that partially succeeded on the server cannot corrupt the test state:
// the operation either detects the existing resource itself (create-if-absent)
// or the caller verifies/cleans up the result afterwards. Plain "create a PR",
// "add a comment" or "push a commit" calls must NOT use it: the first attempt
// may well have succeeded and only its response got lost, so a replay creates
// a duplicate.
func AllowMutationRetries(ctx context.Context) context.Context {
	return context.WithValue(ctx, mutationRetryKey{}, true)
}

// mutationRetriesAllowed reports whether the context was marked by AllowMutationRetries.
func mutationRetriesAllowed(ctx context.Context) bool {
	allowed, ok := ctx.Value(mutationRetryKey{}).(bool)
	return ok && allowed
}

// idempotentMethods are the HTTP methods that RFC 9110 defines as idempotent,
// i.e. replaying them has the same effect as a single call.
// POST and PATCH are deliberately absent.
var idempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
	http.MethodPut:     true,
	http.MethodDelete:  true,
}

// RetryTransport wraps an http.RoundTripper and retries requests that
// receive transient server errors (HTTP 500, 502, 503, 504).
// It uses exponential backoff between retries.
//
// Only requests that are safe to replay are retried: idempotent methods,
// requests carrying an IdempotencyKeyHeader, and requests explicitly opted in
// with AllowMutationRetries. Everything else (POST/PATCH creating repositories,
// pull requests, comments, ...) is passed through untouched, because a replay
// of a mutation whose response was lost duplicates the resource.
type RetryTransport struct {
	Base       http.RoundTripper
	MaxRetries int
	BaseDelay  time.Duration
}

// isRetryable reports whether req may be sent more than once.
func (t *RetryTransport) isRetryable(req *http.Request) bool {
	// A body that cannot be rewound can only be sent once, no matter the method.
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return false
	}
	if idempotentMethods[req.Method] {
		return true
	}
	if req.Header.Get(IdempotencyKeyHeader) != "" {
		return true
	}
	return mutationRetriesAllowed(req.Context())
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.isRetryable(req) {
		return t.Base.RoundTrip(req)
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.MaxRetries; attempt++ {
		// A RoundTripper must not modify the request it is given, and the body
		// is consumed on each attempt, so send a clone with a fresh body.
		attemptReq := req.Clone(req.Context())
		if req.GetBody != nil {
			bodyClone, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			attemptReq.Body = bodyClone
		}

		resp, err = t.Base.RoundTrip(attemptReq)
		if err != nil {
			return resp, err
		}

		if resp.StatusCode < 500 || attempt == t.MaxRetries {
			return resp, nil
		}

		// Transient 5xx -- drain body and retry with exponential backoff
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		delay := t.BaseDelay * (1 << attempt) // 2s, 4s, 8s
		fmt.Printf("[http-retry] %s %s: attempt %d/%d got HTTP %d, retrying in %s\n",
			req.Method, req.URL.Path, attempt+1, t.MaxRetries, resp.StatusCode, delay)

		select {
		case <-time.After(delay):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}

	return resp, err
}

// NewRetryTransport creates a RetryTransport that wraps the given base transport
// with 5 retries and 2s base delay (exponential: 2s, 4s, 8s, 16s, 32s).
func NewRetryTransport(base http.RoundTripper) *RetryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RetryTransport{
		Base:       base,
		MaxRetries: 5,
		BaseDelay:  2 * time.Second,
	}
}
