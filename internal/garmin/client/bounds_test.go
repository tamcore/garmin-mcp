package client_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestDoBoundsTheResponseBody(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: jsonHeader(),
		body:   bytes.Repeat([]byte("a"), 4096),
	}}}

	limits := client.Limits{MaxResponseBytes: 1024, MaxDecompressedBytes: 1024}
	_, err := newTestClient(t, limits).Do(t.Context(), mustSession(t, caller), profileRequest())
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Fatalf("Do() = %v, want ErrResponseTooLarge", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.Endpoint != client.EndpointSocialProfile {
		t.Errorf("Endpoint = %q, want the profile label", apiErr.Endpoint)
	}
}

func TestDoBoundsTheDecompressedBody(t *testing.T) {
	t.Parallel()

	// A compression bomb: a tiny gzip body that expands far past the bound.
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(bytes.Repeat([]byte("a"), 1<<20)); err != nil {
		t.Fatalf("gzip write = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close = %v", err)
	}
	if compressed.Len() > 8192 {
		t.Fatalf("the probe compressed to %d bytes, want a small body", compressed.Len())
	}

	header := jsonHeader()
	header.Set("Content-Encoding", "gzip")
	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: header,
		body:   compressed.Bytes(),
	}}}

	limits := client.Limits{MaxResponseBytes: 64 << 10, MaxDecompressedBytes: 64 << 10}
	_, err := newTestClient(t, limits).Do(t.Context(), mustSession(t, caller), profileRequest())
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("Do() = %v, want ErrResponseTooLarge for a decompression bomb", err)
	}
}

func TestDoDecompressesAGzippedBodyWithinItsBound(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(zw, profileBody); err != nil {
		t.Fatalf("gzip write = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close = %v", err)
	}

	header := jsonHeader()
	header.Set("Content-Encoding", "gzip")
	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusOK, header: header, body: compressed.Bytes()}}}

	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	if got := string(payload.Bytes()); got != profileBody {
		t.Errorf("payload = %q, want the decompressed body", got)
	}
}
