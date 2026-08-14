package testkit

import (
	"errors"
	"net/http"
	"time"
)

// Doer performs HTTP requests against the fake Garmin server. It is the whole
// request surface testkit offers, deliberately: an interface with one method has
// no field a caller could reassign, so the origin guard cannot be detached.
//
// The concrete implementation is unexported. Do not type-assert it; use
// Server.Doer with a DoerOption to configure the request path instead.
type Doer interface {
	// Do performs req. It returns an *OffOriginError, and performs no DNS
	// lookup and no dial, when req does not target Server.BaseURL.
	Do(req *http.Request) (*http.Response, error)
}

// errNilRequest reports Do called with no request.
var errNilRequest = errors.New("testkit: Do called with a nil request")

// doerConfig holds the knobs a DoerOption may set.
type doerConfig struct {
	timeout time.Duration
}

// DoerOption configures a Doer. Options are the only way to adjust the request
// path, because the Doer itself exposes nothing to mutate.
type DoerOption func(doerConfig) doerConfig

// WithTimeout bounds the total time of every request the Doer performs,
// including redirects and body reads, exactly like http.Client.Timeout. The
// zero duration, which is also the default, means no timeout.
func WithTimeout(timeout time.Duration) DoerOption {
	return func(cfg doerConfig) doerConfig {
		cfg.timeout = timeout
		return cfg
	}
}

// guardedDoer performs requests through an http.Client that no caller holds a
// reference to. It re-checks the origin immediately before dispatch, so the
// check runs even if the embedded client's own guards were somehow absent.
type guardedDoer struct {
	origin string
	client *http.Client
}

// Do implements Doer.
func (d guardedDoer) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errNilRequest
	}
	if got := originOf(req.URL); got != d.origin {
		return nil, &OffOriginError{Origin: d.origin, Attempt: got}
	}
	return d.client.Do(req)
}
