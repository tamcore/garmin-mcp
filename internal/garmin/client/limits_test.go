package client_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestDefaultLimitsValidate(t *testing.T) {
	t.Parallel()

	limits := client.DefaultLimits()
	if err := limits.Validate(); err != nil {
		t.Fatalf("DefaultLimits().Validate() = %v, want nil", err)
	}

	checks := map[string]struct{ got, minimum, maximum int64 }{
		"max response bytes":     {limits.MaxResponseBytes, 1, client.MaxResponseBytesCap},
		"max decompressed bytes": {limits.MaxDecompressedBytes, 1, client.MaxDecompressedBytesCap},
		"max page size":          {int64(limits.MaxPageSize), 1, int64(client.MaxPageSizeCap)},
		"max pages":              {int64(limits.MaxPages), 1, int64(client.MaxPagesCap)},
		"max date range days":    {int64(limits.MaxDateRangeDays), 1, int64(client.MaxDateRangeDaysCap)},
		"max concurrency":        {int64(limits.MaxConcurrency), 1, int64(client.MaxConcurrencyCap)},
		"max attempts":           {int64(limits.MaxAttempts), 1, int64(client.MaxAttemptsCap)},
	}
	for name, check := range checks {
		if check.got < check.minimum || check.got > check.maximum {
			t.Errorf("%s = %d, want within [%d, %d]", name, check.got, check.minimum, check.maximum)
		}
	}

	if limits.MaxDecompressedBytes < limits.MaxResponseBytes {
		t.Errorf("MaxDecompressedBytes = %d, want at least MaxResponseBytes = %d",
			limits.MaxDecompressedBytes, limits.MaxResponseBytes)
	}
	for name, timeout := range map[string]time.Duration{
		"request":         limits.RequestTimeout,
		"connect":         limits.ConnectTimeout,
		"response header": limits.ResponseHeaderTimeout,
		"tls handshake":   limits.TLSHandshakeTimeout,
		"base backoff":    limits.BaseBackoff,
		"max backoff":     limits.MaxBackoff,
	} {
		if timeout <= 0 {
			t.Errorf("%s timeout = %v, want a positive safe default", name, timeout)
		}
	}
}

func TestZeroLimitsFallBackToDefaults(t *testing.T) {
	t.Parallel()

	if err := (client.Limits{}).Validate(); err != nil {
		t.Fatalf("the zero Limits must be usable and mean \"defaults\", got %v", err)
	}
	if got := (client.Limits{}).Resolved(); got != client.DefaultLimits() {
		t.Errorf("Limits{}.Resolved() = %+v, want DefaultLimits() = %+v", got, client.DefaultLimits())
	}
}

func TestLimitsRejectHostileValues(t *testing.T) {
	t.Parallel()

	cases := map[string]client.Limits{
		"negative response bytes":     {MaxResponseBytes: -1},
		"response bytes over the cap": {MaxResponseBytes: client.MaxResponseBytesCap + 1},
		"decompressed over the cap":   {MaxDecompressedBytes: client.MaxDecompressedBytesCap + 1},
		"decompressed below body":     {MaxResponseBytes: 4096, MaxDecompressedBytes: 2048},
		"page size over the cap":      {MaxPageSize: client.MaxPageSizeCap + 1},
		"pages over the cap":          {MaxPages: client.MaxPagesCap + 1},
		"date range over the cap":     {MaxDateRangeDays: client.MaxDateRangeDaysCap + 1},
		"concurrency over the cap":    {MaxConcurrency: client.MaxConcurrencyCap + 1},
		"attempts over the cap":       {MaxAttempts: client.MaxAttemptsCap + 1},
		"negative attempts":           {MaxAttempts: -3},
		"negative request timeout":    {RequestTimeout: -time.Second},
		"backoff over the cap":        {MaxBackoff: client.MaxBackoffCap + time.Second},
		"base backoff above max":      {BaseBackoff: time.Minute, MaxBackoff: time.Second},
	}

	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := limits.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want a rejection for %s", name)
			}
			if !errors.Is(err, client.ErrInvalidLimits) {
				t.Errorf("Validate() = %v, want it to match ErrInvalidLimits", err)
			}
		})
	}
}
