package client_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// downloadRequest is a valid file transfer: a read, marked as a transfer.
func downloadRequest() client.Request {
	return client.Request{
		Op:           client.OpDownloadActivityFile,
		Endpoint:     client.EndpointActivityDownload,
		Path:         client.PathActivityTCXDownload + "/900001",
		Effect:       client.EffectRead,
		FileTransfer: true,
	}
}

// gzipBody renders body as a gzip stream.
func gzipBody(t *testing.T, body string) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("gzip Write() = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip Close() = %v", err)
	}
	return buf.Bytes()
}

// gzipHeader declares a gzip-encoded response body.
func gzipHeader() http.Header {
	header := http.Header{}
	header.Set("Content-Encoding", "gzip")
	return header
}

func TestDownloadStreamsIntoTheCallersSink(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK, body: []byte("FILE-BYTES"),
	}}}

	var sink bytes.Buffer
	result, err := newTestClient(t, client.Limits{}).Download(
		t.Context(), mustSession(t, caller), downloadRequest(), &sink)
	if err != nil {
		t.Fatalf("Download() = %v", err)
	}
	if sink.String() != "FILE-BYTES" {
		t.Errorf("sink = %q, want the streamed body", sink.String())
	}
	if result.Bytes != int64(len("FILE-BYTES")) || result.WireBytes != result.Bytes {
		t.Errorf("Bytes = %d, WireBytes = %d, want both to be the body size",
			result.Bytes, result.WireBytes)
	}
	if result.Status != http.StatusOK {
		t.Errorf("Status = %d, want 200", result.Status)
	}
}

func TestDownloadDecompressesAGzipResponse(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("TCX", 200)
	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK, header: gzipHeader(), body: gzipBody(t, body),
	}}}

	var sink bytes.Buffer
	result, err := newTestClient(t, client.Limits{}).Download(
		t.Context(), mustSession(t, caller), downloadRequest(), &sink)
	if err != nil {
		t.Fatalf("Download() = %v", err)
	}
	if sink.String() != body {
		t.Errorf("sink holds %d bytes, want the %d decompressed ones", sink.Len(), len(body))
	}
	if result.WireBytes >= result.Bytes {
		t.Errorf("WireBytes = %d and Bytes = %d, want the compressed size to be smaller",
			result.WireBytes, result.Bytes)
	}
}

func TestDownloadRefusesABodyOverTheWireBound(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK, body: []byte(strings.Repeat("x", 4096)),
	}}}
	c := newTestClient(t, client.Limits{MaxResponseBytes: 64, MaxDecompressedBytes: 1 << 20})

	var sink bytes.Buffer
	if _, err := c.Download(t.Context(), mustSession(t, caller), downloadRequest(),
		&sink); !errors.Is(err, client.ErrResponseTooLarge) {
		t.Fatalf("Download() = %v, want ErrResponseTooLarge", err)
	}
}

func TestDownloadRefusesABodyOverTheDecompressedBound(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK, header: gzipHeader(),
		body: gzipBody(t, strings.Repeat("a", 1<<16)),
	}}}
	c := newTestClient(t, client.Limits{MaxResponseBytes: 4096, MaxDecompressedBytes: 4096})

	var sink bytes.Buffer
	if _, err := c.Download(t.Context(), mustSession(t, caller), downloadRequest(),
		&sink); !errors.Is(err, client.ErrResponseTooLarge) {
		t.Fatalf("Download() = %v, want ErrResponseTooLarge", err)
	}
}

func TestDownloadReportsAnUndecodableGzipStream(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK, header: gzipHeader(), body: []byte("not gzip at all"),
	}}}

	var sink bytes.Buffer
	if _, err := newTestClient(t, client.Limits{}).Download(t.Context(),
		mustSession(t, caller), downloadRequest(), &sink); !errors.Is(
		err, client.ErrMalformedPayload) {
		t.Fatalf("Download() = %v, want ErrMalformedPayload", err)
	}
}

func TestDownloadRefusesWhatItMustNotDispatch(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusOK, body: []byte("x")}}}
	c := newTestClient(t, client.Limits{})
	session := mustSession(t, caller)

	write := downloadRequest()
	write.Effect = client.EffectUnsafeWrite
	write.Method = http.MethodPost

	notATransfer := downloadRequest()
	notATransfer.FileTransfer = false

	unusablePath := downloadRequest()
	unusablePath.Path = "relative/path"

	cases := map[string]struct {
		req  client.Request
		sink io.Writer
	}{
		"no sink":             {req: downloadRequest(), sink: nil},
		"a write":             {req: write, sink: &bytes.Buffer{}},
		"not a file transfer": {req: notATransfer, sink: &bytes.Buffer{}},
		"an unusable path":    {req: unusablePath, sink: &bytes.Buffer{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Download(t.Context(), session, tc.req, tc.sink); !errors.Is(
				err, client.ErrValidation) {
				t.Errorf("Download() = %v, want ErrValidation", err)
			}
		})
	}

	if _, err := c.Download(t.Context(), client.Session{}, downloadRequest(),
		&bytes.Buffer{}); !errors.Is(err, client.ErrMissingPrincipal) {
		t.Errorf("Download() with a zero session = %v, want ErrMissingPrincipal", err)
	}
	if got := len(caller.requests); got != 0 {
		t.Errorf("the caller saw %d requests, want 0: validation runs before dispatch", got)
	}
}

func TestDownloadIsNeverRetried(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusServiceUnavailable}}}
	c := newTestClient(t, client.Limits{MaxAttempts: 3})

	var sink bytes.Buffer
	if _, err := c.Download(t.Context(), mustSession(t, caller), downloadRequest(),
		&sink); !errors.Is(err, client.ErrServer) {
		t.Fatalf("Download() = %v, want ErrServer", err)
	}
	if got := len(caller.requests); got != 1 {
		t.Errorf("the caller saw %d requests, want 1: a retry would append to a sink that "+
			"already holds the failed attempt", got)
	}
}

func TestDownloadNormalizesANoContentResponse(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusNoContent}}}

	var sink bytes.Buffer
	result, err := newTestClient(t, client.Limits{}).Download(
		t.Context(), mustSession(t, caller), downloadRequest(), &sink)
	if err != nil {
		t.Fatalf("Download() = %v", err)
	}
	if result.Bytes != 0 || sink.Len() != 0 {
		t.Errorf("Bytes = %d and sink = %d, want an empty file", result.Bytes, sink.Len())
	}
}
