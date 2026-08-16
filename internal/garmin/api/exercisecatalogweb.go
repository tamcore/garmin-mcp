package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The one web-tier read this project makes: Garmin's published strength catalog.
// Why it is preferred to the vendored FIT profile, and why the exception is
// exactly this URL, is in docs/parity.md.

// ExerciseCatalogURL is the published strength catalog. It is the only URL under
// connect.garmin.com/web-data this project reads, and it is compiled in: no
// configuration and no caller contributes a host, a path or a query.
const ExerciseCatalogURL = "https://connect.garmin.com/web-data/exercises/Exercises.json"

// The two headers this request carries, and nothing else. Both are compiled in.
const (
	catalogAcceptHeader = "application/json"
	catalogUserAgent    = "garmin-mcp"
)

const (
	// MaxExerciseCatalogBytes bounds the document read from the network. The
	// published document measured 198 KB on 2026-08-16, so this is 21 times the
	// observed size.
	MaxExerciseCatalogBytes = 4 << 20

	// exerciseCatalogTimeout bounds the whole read, connection included. It is
	// short because this runs at start-up: a slow CDN costs a fallback, never a
	// server that will not start.
	exerciseCatalogTimeout = 5 * time.Second

	// catalogDialTimeout bounds the connection alone, so a black-holed address
	// cannot spend the whole budget on a handshake that will not complete.
	catalogDialTimeout = 2 * time.Second
)

// LoadExerciseCatalog reads the published catalog and falls back to the
// compiled-in subset.
//
// It never returns nil and never returns an error: no failure here may stop a
// server from starting. [ExerciseCatalog.Source] reports which one answered.
func LoadExerciseCatalog(ctx context.Context) *ExerciseCatalog {
	hc := newCatalogHTTPClient()
	defer hc.CloseIdleConnections()

	catalog, _ := loadExerciseCatalog(ctx, hc, ExerciseCatalogURL)
	return catalog
}

// loadExerciseCatalog is [LoadExerciseCatalog] with the client and the URL
// injected, and with the failure reported so a test can name it. The catalog it
// returns is usable in either case.
func loadExerciseCatalog(
	ctx context.Context, hc *http.Client, url string,
) (*ExerciseCatalog, error) {
	fetched, err := fetchExerciseCatalog(ctx, hc, url)
	if err != nil {
		return BuiltinExerciseCatalog(), err
	}
	return fetched, nil
}

// newCatalogHTTPClient builds the client this read uses: its own transport and
// pool, never the process-wide [http.DefaultTransport], and no cookie jar.
//
// Compression is off so the transport adds no header of its own and the request
// carries exactly the two set below. A redirect is not followed, so the read
// cannot be moved to another host. The proxy policy is the environment's, which an
// operator routing egress needs and which is what lets the end-to-end suite prove
// no test reaches the public service.
func newCatalogHTTPClient() *http.Client {
	return &http.Client{
		Timeout: exerciseCatalogTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: catalogDialTimeout}).DialContext,
			TLSHandshakeTimeout:   catalogDialTimeout,
			ResponseHeaderTimeout: exerciseCatalogTimeout,
			DisableCompression:    true,
			ForceAttemptHTTP2:     true,
		},
	}
}

// fetchExerciseCatalog performs the one permitted web-tier read. Nothing that
// could identify or authenticate an account is in scope here, so none can travel
// with it.
func fetchExerciseCatalog(
	ctx context.Context, hc *http.Client, url string,
) (*ExerciseCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, exerciseCatalogTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building the exercise catalog request: %w", err)
	}
	req.Header.Set("Accept", catalogAcceptHeader)
	req.Header.Set("User-Agent", catalogUserAgent)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading the exercise catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: the exercise catalog answered with status %d",
			client.ErrMalformedPayload, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxExerciseCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the exercise catalog body: %w", err)
	}
	if len(raw) > MaxExerciseCatalogBytes {
		return nil, fmt.Errorf("%w: the exercise catalog is larger than %d bytes",
			client.ErrMalformedPayload, MaxExerciseCatalogBytes)
	}
	return ParseExerciseCatalog(raw)
}
