package client

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Download reports what a streamed file transfer produced. It holds sizes and
// sanitized labels only, never the bytes: the bytes went to the caller's sink.
type Download struct {
	// Status is the HTTP status of the response the bytes came from.
	Status int
	// ContentType is the sanitized media type, without parameters.
	ContentType string
	// WireBytes is how many bytes were read off the wire.
	WireBytes int64
	// Bytes is how many bytes were written to the sink, which differs from
	// WireBytes when the response was compressed.
	Bytes int64
}

// Download streams the response for req into dst under both size bounds.
//
// It exists because an activity or workout file is the one Garmin response that
// is not a small JSON document: buffering it whole would put an operator-sized
// bound directly onto the process heap. The bytes therefore never accumulate
// here, and this package never chooses where they land — dst is supplied by the
// caller, and no method in this package opens, creates or names a file.
//
// Both bounds of Limits apply: MaxResponseBytes to the wire bytes and
// MaxDecompressedBytes to the bytes handed to dst, so a compression bomb cannot
// exhaust the sink either. An overrun is reported as ErrResponseTooLarge.
//
// The transfer is attempted exactly once. A retry would have to start the stream
// over, and dst has already received the bytes of the failed attempt, so a
// retried download can only corrupt the sink. A caller that wants another
// attempt supplies a fresh sink and calls again.
//
// On any failure dst may already hold a prefix of the file, so a caller must
// discard what it collected rather than treat it as a short file.
func (c *Client) Download(
	ctx context.Context, session Session, req Request, dst io.Writer,
) (Download, error) {
	if dst == nil {
		return Download{}, c.fail(req, 0, KindValidation, 0,
			validationError("a download needs a sink to write to"))
	}
	if session.IsZero() {
		return Download{}, c.fail(req, 0, KindValidation, 0,
			fmt.Errorf("garmin api: unusable session: %w", ErrMissingPrincipal))
	}
	if err := req.Validate(); err != nil {
		return Download{}, c.fail(req, 0, KindValidation, 0, err)
	}
	if !req.FileTransfer || req.Effect != EffectRead {
		return Download{}, c.fail(req, 0, KindValidation, 0,
			validationError("a download must be a read marked as a file transfer"))
	}
	if err := ctx.Err(); err != nil {
		return Download{}, c.fail(req, 0, KindTemporaryConnection, 0, err)
	}
	return c.downloadAttempt(ctx, session, req, dst)
}

// downloadAttempt performs the one request a download is allowed.
func (c *Client) downloadAttempt(
	ctx context.Context, session Session, req Request, dst io.Writer,
) (Download, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, c.limits.RequestTimeout)
	defer cancel()

	httpReq, err := c.newHTTPRequest(attemptCtx, req)
	if err != nil {
		return Download{}, c.fail(req, 0, KindValidation, 0, err)
	}

	resp, err := session.caller.Do(attemptCtx, session.principal, httpReq)
	if err != nil {
		return Download{}, c.fail(req, 0, transportKind(err), 0, err)
	}
	defer closeBody(resp)

	result := Download{
		Status:      resp.StatusCode,
		ContentType: sanitizeMediaType(resp.Header.Get(headerContentType)),
	}
	if kind, failed := classifyStatus(resp.StatusCode, true); failed {
		return result, c.fail(req, resp.StatusCode, kind, c.retryAfter(resp), nil)
	}
	if resp.Body == nil || resp.StatusCode == http.StatusNoContent {
		return result, nil
	}
	return c.streamBody(req, resp, result, dst)
}

// streamBody copies the response body into dst under both bounds.
func (c *Client) streamBody(
	req Request, resp *http.Response, result Download, dst io.Writer,
) (Download, error) {
	wire := &countingReader{inner: io.LimitReader(resp.Body, c.limits.MaxResponseBytes+1)}

	source, release, err := decompress(wire, resp)
	if err != nil {
		return result, c.fail(req, resp.StatusCode, KindMalformedPayload, 0, err)
	}
	defer release()

	written, copyErr := io.Copy(dst, io.LimitReader(source, c.limits.MaxDecompressedBytes+1))
	result.WireBytes = wire.count
	result.Bytes = written

	switch {
	case copyErr != nil:
		return result, c.fail(req, resp.StatusCode, transportKind(copyErr), 0, copyErr)
	case wire.count > c.limits.MaxResponseBytes, written > c.limits.MaxDecompressedBytes:
		return result, c.fail(req, resp.StatusCode, KindUnknown, 0,
			fmt.Errorf("garmin api: download over its bound: %w", ErrResponseTooLarge))
	}
	return result, nil
}

// decompress wraps reader in a gzip decoder when the response declares one. The
// second result always releases whatever the first one holds.
func decompress(reader io.Reader, resp *http.Response) (io.Reader, func(), error) {
	encoding := strings.TrimSpace(resp.Header.Get(headerContentEncoding))
	if !strings.EqualFold(encoding, encodingGzip) {
		return reader, func() {}, nil
	}

	zipped, err := gzip.NewReader(reader)
	if err != nil {
		return nil, func() {}, fmt.Errorf("garmin api: gzip response could not be read: %w",
			ErrMalformedPayload)
	}
	return zipped, func() { _ = zipped.Close() }, nil
}

// countingReader records how many bytes were read through it, which is how the
// wire bound is checked without buffering the body.
type countingReader struct {
	inner io.Reader
	count int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	read, err := r.inner.Read(p)
	r.count += int64(read)
	return read, err
}
