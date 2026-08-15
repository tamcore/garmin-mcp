package tools

import (
	"bytes"
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolDownloadActivityFile is the upstream compatibility name of the activity
// download.
const ToolDownloadActivityFile = "download_activity_file"

// activityResourceStart is the URI prefix of a returned activity file.
const activityResourceStart = "garmin://activity/"

// downloadMediaTypes maps a validated format onto the media type the embedded
// resource is labelled with. The label is this server's, never the response's: a
// Content-Type from Garmin is not something a client should be asked to trust.
func downloadMediaTypes() map[api.FileFormat]string {
	return map[api.FileFormat]string{
		api.FormatOriginal: "application/zip",
		api.FormatTCX:      "application/vnd.garmin.tcx+xml",
		api.FormatGPX:      "application/gpx+xml",
		api.FormatKML:      "application/vnd.google-earth.kml+xml",
		api.FormatCSV:      "text/csv",
	}
}

// A boundedSink collects a streamed download in memory, up to a bound.
//
// It is the whole download policy of this package: the bytes go into memory and then
// into an MCP resource, never to a filesystem path, and a transfer that outgrows the
// bound stops being collected and is reported as too large rather than truncated.
type boundedSink struct {
	buffer   bytes.Buffer
	limit    int64
	overflow bool
}

func newBoundedSink(limit int64) *boundedSink { return &boundedSink{limit: limit} }

// Write collects the chunk unless the bound has been passed.
func (s *boundedSink) Write(chunk []byte) (int, error) {
	if s.overflow {
		return len(chunk), nil
	}
	if int64(s.buffer.Len()+len(chunk)) > s.limit {
		s.overflow = true
		s.buffer.Reset()
		return len(chunk), nil
	}
	return s.buffer.Write(chunk)
}

// err reports the refusal when the transfer outgrew the bound.
func (s *boundedSink) err() error {
	if !s.overflow {
		return nil
	}
	return tooLarge("the file is larger than this server will inline into a result")
}

func (s *boundedSink) len() int { return s.buffer.Len() }

func (s *boundedSink) bytes() []byte { return s.buffer.Bytes() }

// A DownloadedFile reports what a download produced.
//
// It reports sizes and this server's own labels, never a filesystem path: no path is
// accepted from a caller and none is created, so there is none to report.
type DownloadedFile struct {
	ID        int64  `json:"id" jsonschema:"the record the file belongs to"`
	Format    string `json:"format" jsonschema:"the requested export format"`
	MediaType string `json:"media_type" jsonschema:"the media type of the embedded resource"`
	Bytes     int    `json:"bytes" jsonschema:"how many bytes the embedded resource carries"`
	URI       string `json:"uri" jsonschema:"the URI of the embedded resource in this result"`
}

// LogValue reports the transfer size, never the bytes.
func (f DownloadedFile) LogValue() slog.Value {
	return shape("downloadedFile",
		slog.String(argNameFormat, f.Format),
		slog.Int("bytes", f.Bytes),
	)
}

// newDownloadedFile describes one collected transfer.
func newDownloadedFile(id client.ID, format, mediaType, uri string, size int) DownloadedFile {
	return DownloadedFile{
		ID:        id.Int64(),
		Format:    format,
		MediaType: mediaType,
		Bytes:     size,
		URI:       uri,
	}
}

// blobResult wraps collected bytes in an embedded MCP resource. The bytes are copied,
// so the sink the transfer used cannot be reached through the returned result.
func blobResult(uri, mediaType string, payload []byte) *mcp.CallToolResult {
	blob := make([]byte, len(payload))
	copy(blob, payload)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.EmbeddedResource{
			Resource: &mcp.ResourceContents{URI: uri, MIMEType: mediaType, Blob: blob},
		}},
	}
}

// downloadActivityFileInput is the download argument set.
//
// It declares no output directory. The manifest states an output_dir argument, a
// GARMIN_FIT_DOWNLOAD_DIR environment variable and a persisted directory, all of
// which let a tool argument choose where this server writes. None is ported: the
// bytes are returned to the caller instead.
type downloadActivityFileInput struct {
	ActivityID any    `json:"activity_id" jsonschema:"the Garmin activity identifier"`
	Format     string `json:"format" jsonschema:"the export format: fit, tcx, gpx, kml or csv"`
}

func downloadActivityFileContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolDownloadActivityFile,
			Title: "Download an activity file",
			Description: "download one activity's recorded file and return it as an embedded " +
				"MCP resource. Nothing is written to this server's filesystem, no directory " +
				"is accepted or remembered, and a file over this server's bound is refused " +
				"rather than truncated. The original format is the device FIT file, which " +
				"Garmin serves as a zip archive",
			Tier:        policy.TierWrite,
			Category:    categoryLocation,
			Annotations: writeAnnotations(true),
		},
		Schema: NewSchema(activityIDProperty(), Property{
			Name:        argNameFormat,
			Types:       []string{typeString},
			Description: "the export format",
			Enum:        downloadFormatEnum(),
			MaxLength:   new(maxActivityTypeArgumentLen),
			Default:     defaultDownloadFormat,
		}),
	}
}

// downloadFormatEnum renders the closed format set the API layer validates against.
func downloadFormatEnum() []any {
	formats := api.FileFormats()
	out := make([]any, 0, len(formats))
	for _, format := range formats {
		out = append(out, format.String())
	}
	return out
}

func registerDownloadActivityFile(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in downloadActivityFileInput) (
		*mcp.CallToolResult, DownloadedFile, error,
	) {
		format, err := parseDownloadFormat(in.Format)
		if err != nil {
			return nil, DownloadedFile{}, err
		}
		id, session, err := svc.resolveActivityRead(ctx, in.ActivityID)
		if err != nil {
			return nil, DownloadedFile{}, err
		}

		sink := newBoundedSink(svc.bounds.MaxDownloadBytes)
		if _, err := svc.files.Download(ctx, session, id, format, sink); err != nil {
			return nil, DownloadedFile{}, fail(err)
		}
		if err := sink.err(); err != nil {
			return nil, DownloadedFile{}, err
		}

		mediaType := downloadMediaTypes()[format]
		uri := activityResourceStart + id.String() + "." + format.String()
		file := newDownloadedFile(id, format.String(), mediaType, uri, sink.len())
		return blobResult(uri, mediaType, sink.bytes()), file, nil
	}
	return mcpserver.AddTool(registry, downloadActivityFileContract().Registration(), handler)
}

// parseDownloadFormat applies the manifest default and validates the format against
// the closed set, so an unknown format can never reach a URL path.
func parseDownloadFormat(value string) (api.FileFormat, error) {
	format, err := api.ParseFileFormat(optionalTextArg(value, defaultDownloadFormat))
	if err != nil {
		return "", invalidArgument("format must be one of fit, tcx, gpx, kml or csv")
	}
	return format, nil
}
