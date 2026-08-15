package api_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func newActivityFiles(t *testing.T, h harness) *api.ActivityFiles {
	t.Helper()

	files, err := api.NewActivityFiles(h.rc)
	if err != nil {
		t.Fatalf("NewActivityFiles() = %v", err)
	}
	return files
}

// gzipped renders body as a gzip stream, which is how the compression bound is
// exercised without a fixture file.
func gzipped(t *testing.T, body string) string {
	t.Helper()

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("gzip Write() = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip Close() = %v", err)
	}
	return buf.String()
}

// gzipBehavior scripts a gzip-encoded response body.
func gzipBehavior(t *testing.T, body string) testkit.Behavior {
	t.Helper()

	header := http.Header{}
	header.Set("Content-Encoding", "gzip")
	return testkit.Behavior{
		Status:      http.StatusOK,
		ContentType: "application/gpx+xml",
		Header:      header,
		Body:        gzipped(t, body),
	}
}

// TestEveryDownloadFormatHasItsOwnPath pins the five upstream download paths.
func TestEveryDownloadFormatHasItsOwnPath(t *testing.T) {
	t.Parallel()

	wanted := map[api.FileFormat]string{
		api.FormatOriginal: client.PathActivityOriginalDownload,
		api.FormatTCX:      client.PathActivityTCXDownload,
		api.FormatGPX:      client.PathActivityGPXDownload,
		api.FormatKML:      client.PathActivityKMLDownload,
		api.FormatCSV:      client.PathActivityCSVDownload,
	}
	for format, prefix := range wanted {
		t.Run(format.String(), func(t *testing.T) {
			t.Parallel()

			path := prefix + "/18446744"
			script := testkit.NewScript().With(path, testkit.Behavior{
				Status: http.StatusOK, ContentType: "application/octet-stream", Body: "BYTES",
			})
			h := newHarness(t, script, client.Limits{})

			var sink bytes.Buffer
			if _, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
				format, &sink); err != nil {
				t.Fatalf("Download() = %v", err)
			}
			if got := h.server.Requests()[0].Path; got != path {
				t.Errorf("path = %q, want %q", got, path)
			}
			if sink.String() != "BYTES" {
				t.Errorf("sink = %q, want the streamed body", sink.String())
			}
		})
	}
}

// TestDownloadFormatParsingIsAClosedSet keeps an unvalidated format out of a URL
// path.
func TestDownloadFormatParsingIsAClosedSet(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"FIT", " gpx ", "csv"} {
		if _, err := api.ParseFileFormat(value); err != nil {
			t.Errorf("ParseFileFormat(%q) = %v, want it accepted", value, err)
		}
	}
	if len(api.FileFormats()) != 5 {
		t.Errorf("%d formats, want the five Garmin exports", len(api.FileFormats()))
	}
	for _, value := range []string{"", "zip", "../fit", "fit/../../etc"} {
		if _, err := api.ParseFileFormat(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseFileFormat(%q) = %v, want ErrValidation", value, err)
		}
	}

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	var sink bytes.Buffer
	if _, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
		api.FileFormat("zip"), &sink); !errors.Is(err, client.ErrValidation) {
		t.Errorf("Download() with an unknown format = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestDownloadDecompressesUnderItsOwnBound proves the gzip path streams and that
// both sizes are reported.
func TestDownloadDecompressesUnderItsOwnBound(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("GPX", 400)
	script := testkit.NewScript().With(client.PathActivityGPXDownload+"/18446744",
		gzipBehavior(t, body))
	h := newHarness(t, script, client.Limits{})

	var sink bytes.Buffer
	result, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
		api.FormatGPX, &sink)
	if err != nil {
		t.Fatalf("Download() = %v", err)
	}
	if sink.String() != body {
		t.Errorf("sink holds %d bytes, want the %d decompressed ones", sink.Len(), len(body))
	}
	if result.Bytes != int64(len(body)) || result.WireBytes >= result.Bytes {
		t.Errorf("Bytes = %d and WireBytes = %d, want the decompressed size above the wire size",
			result.Bytes, result.WireBytes)
	}
}

// TestDownloadRefusesABodyOverTheWireBound reports an oversized file rather than
// truncating it: a truncated activity file is a corrupt one.
func TestDownloadRefusesABodyOverTheWireBound(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityCSVDownload+"/18446744",
		testkit.Behavior{
			Status: http.StatusOK, ContentType: "text/csv", Body: strings.Repeat("x", 4096),
		})
	h := newHarness(t, script, client.Limits{MaxResponseBytes: 64, MaxDecompressedBytes: 1 << 20})

	var sink bytes.Buffer
	if _, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
		api.FormatCSV, &sink); !errors.Is(err, client.ErrResponseTooLarge) {
		t.Fatalf("Download() = %v, want ErrResponseTooLarge", err)
	}
}

// TestDownloadRefusesABodyOverTheDecompressedBound is the compression-bomb test:
// a small archive that expands past the decompressed bound is refused.
func TestDownloadRefusesABodyOverTheDecompressedBound(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityGPXDownload+"/18446744",
		gzipBehavior(t, strings.Repeat("a", 1<<16)))
	h := newHarness(t, script, client.Limits{
		MaxResponseBytes: 4096, MaxDecompressedBytes: 4096,
	})

	var sink bytes.Buffer
	if _, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
		api.FormatGPX, &sink); !errors.Is(err, client.ErrResponseTooLarge) {
		t.Fatalf("Download() = %v, want ErrResponseTooLarge", err)
	}
	if int64(sink.Len()) > 4097 {
		t.Errorf("sink holds %d bytes, want the bound to have stopped the stream", sink.Len())
	}
}

// TestDownloadRefusesAMissingSink keeps a caller from asking for bytes with
// nowhere to put them, which would otherwise buffer them here.
func TestDownloadRefusesAMissingSink(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
		api.FormatTCX, nil); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("Download() without a sink = %v, want ErrValidation", err)
	}
	if _, err := newActivityFiles(t, h).Download(t.Context(), h.session, client.ID{},
		api.FormatTCX, &bytes.Buffer{}); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("Download() without an activity = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestDownloadReportsAMissingActivity keeps the failure classes distinct through
// the streaming path, and proves nothing reaches the sink for a failure.
func TestDownloadReportsAMissingActivity(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathActivityTCXDownload+"/18446744",
		testkit.JSON(http.StatusNotFound, `{"error":"no such activity"}`))
	h := newHarness(t, script, client.Limits{})

	var sink bytes.Buffer
	if _, err := newActivityFiles(t, h).Download(t.Context(), h.session, mustID(t),
		api.FormatTCX, &sink); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("Download() = %v, want ErrNotFound", err)
	}
	if sink.Len() != 0 {
		t.Errorf("sink holds %d bytes, want none for a failed download", sink.Len())
	}
}
