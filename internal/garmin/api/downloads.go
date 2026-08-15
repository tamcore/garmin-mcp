package api

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// ActivityFiles downloads the recorded file of one activity.
//
// Source: download_activity, which maps a format onto one of Garmin's five
// download paths. Two upstream behaviors are deliberately not reproduced: the
// bytes are not buffered whole — they are streamed into a sink the caller owns —
// and no path is accepted from a caller. Upstream's MCP layer takes an
// output_dir argument, creates the directory and writes "{activity_id}.{ext}"
// into it, which lets a tool argument choose a filesystem location. Choosing
// where bytes land is the caller's business, not this package's.
type ActivityFiles struct {
	req requester
}

// NewActivityFiles returns a download client over the request layer.
func NewActivityFiles(rc *client.Client) (*ActivityFiles, error) {
	req, err := newRequester(rc)
	if err != nil {
		return nil, err
	}
	return &ActivityFiles{req: req}, nil
}

// FileFormat is a validated activity download format.
type FileFormat string

// The formats Garmin exports an activity in. Original is the device FIT file,
// which Garmin serves as a zip archive.
const (
	FormatOriginal FileFormat = "fit"
	FormatTCX      FileFormat = "tcx"
	FormatGPX      FileFormat = "gpx"
	FormatKML      FileFormat = "kml"
	FormatCSV      FileFormat = "csv"
)

// formatPath pairs a format with its path prefix. Source: the urls dict of
// download_activity.
type formatPath struct {
	format FileFormat
	prefix string
}

// downloadPaths is the closed format set. It is an array rather than a map so
// there is no package-level mutable state to insert a format into at runtime.
var downloadPaths = [...]formatPath{
	{FormatOriginal, client.PathActivityOriginalDownload},
	{FormatTCX, client.PathActivityTCXDownload},
	{FormatGPX, client.PathActivityGPXDownload},
	{FormatKML, client.PathActivityKMLDownload},
	{FormatCSV, client.PathActivityCSVDownload},
}

// lookupDownloadPath reports the path prefix of a format.
func lookupDownloadPath(format FileFormat) (string, bool) {
	for _, entry := range downloadPaths {
		if entry.format == format {
			return entry.prefix, true
		}
	}
	return "", false
}

// ParseFileFormat validates a download format against the closed set, so an
// unknown format can never reach a URL path.
func ParseFileFormat(value string) (FileFormat, error) {
	format := FileFormat(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := lookupDownloadPath(format); !ok {
		return "", fmt.Errorf("%w: download format must be fit, tcx, gpx, kml or csv",
			client.ErrValidation)
	}
	return format, nil
}

// FileFormats returns the recognized download formats.
func FileFormats() []FileFormat {
	formats := make([]FileFormat, 0, len(downloadPaths))
	for _, entry := range downloadPaths {
		formats = append(formats, entry.format)
	}
	return formats
}

// String is the validated format key.
func (f FileFormat) String() string { return string(f) }

// Download streams one activity file into dst.
//
// The transfer is bounded twice — the wire bytes against Limits.MaxResponseBytes
// and the bytes handed to dst against Limits.MaxDecompressedBytes — and it is
// attempted once, because a retry would append a second attempt's bytes to a
// sink that already holds the first one's. On a failure the sink may hold a
// prefix of the file, so a caller must discard what it collected.
func (a *ActivityFiles) Download(
	ctx context.Context, session client.Session, id client.ID,
	format FileFormat, dst io.Writer,
) (client.Download, error) {
	prefix, known := lookupDownloadPath(format)
	req := client.Request{
		Op:           client.OpDownloadActivityFile,
		Endpoint:     client.EndpointActivityDownload,
		Path:         prefix + "/" + id.String(),
		Effect:       client.EffectRead,
		FileTransfer: true,
	}
	if !known {
		return client.Download{}, invalid(req, fmt.Errorf(
			"%w: download format is not one Garmin exports", client.ErrValidation))
	}
	if err := requireID(req, id); err != nil {
		return client.Download{}, err
	}
	return a.req.download(ctx, session, req, dst)
}
