package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    Domain
		wantErr bool
	}{
		{name: "empty defaults to global", in: "", want: DomainGlobal},
		{name: "blank defaults to global", in: "   ", want: DomainGlobal},
		{name: "global passthrough", in: "garmin.com", want: DomainGlobal},
		{name: "china passthrough", in: "garmin.cn", want: DomainChina},
		{name: "uppercase folded", in: "GARMIN.CN", want: DomainChina},
		{name: "surrounding space trimmed", in: "  garmin.com  ", want: DomainGlobal},
		{name: "trailing dot rejected", in: "garmin.com.", wantErr: true},
		{name: "attacker host rejected", in: testHostileDomain, wantErr: true},
		{name: "suffix confusion rejected", in: "garmin.com." + testHostileDomain, wantErr: true},
		{name: "prefix confusion rejected", in: "notgarmin.com", wantErr: true},
		{name: "subdomain rejected", in: "sso.garmin.com", wantErr: true},
		{name: "scheme rejected", in: "https://garmin.com", wantErr: true},
		{name: "test domain rejected", in: "example.test", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDomain(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDomain(%q) = %q, want error", tc.in, got)
				}
				if !errors.Is(err, ErrUnsupportedDomain) {
					t.Fatalf("ParseDomain(%q) error = %v, want ErrUnsupportedDomain", tc.in, err)
				}
				if got.IsValid() || got.Domain() != "" {
					t.Fatalf("ParseDomain(%q) = %q, want the zero ValidatedDomain on error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDomain(%q) unexpected error: %v", tc.in, err)
			}
			if got.Domain() != tc.want {
				t.Fatalf("ParseDomain(%q) = %q, want %q", tc.in, got.Domain(), tc.want)
			}
			if !got.IsValid() {
				t.Fatalf("ParseDomain(%q) returned a ValidatedDomain that reports itself invalid", tc.in)
			}
		})
	}
}

// A rejection message must name the allowlist, not echo caller-controlled text
// that could smuggle markup or a credential into a log line.
func TestParseDomainErrorDoesNotEchoInput(t *testing.T) {
	t.Parallel()

	const hostile = testHostileDomain + "/?password=S3cr3t"

	_, err := ParseDomain(hostile)
	if err == nil {
		t.Fatal("ParseDomain accepted a hostile domain")
	}

	msg := err.Error()
	for _, forbidden := range []string{testHostileDomain, "S3cr3t", "password", "?"} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("error %q echoed %q", msg, forbidden)
		}
	}
	for _, want := range []string{string(DomainGlobal), string(DomainChina)} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not name allowed domain %q", msg, want)
		}
	}
}

func TestDomainIsAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   Domain
		want bool
	}{
		{in: DomainGlobal, want: true},
		{in: DomainChina, want: true},
		{in: "", want: false},
		{in: testCaseVariantDomain, want: false},
		{in: testHostileDomain, want: false},
	}

	for _, tc := range tests {
		t.Run(string(tc.in), func(t *testing.T) {
			t.Parallel()
			if got := tc.in.IsAllowed(); got != tc.want {
				t.Fatalf("Domain(%q).IsAllowed() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestAllowedDomainsReturnsFreshCopy(t *testing.T) {
	t.Parallel()

	first := AllowedDomains()
	if len(first) != 2 || first[0] != DomainGlobal || first[1] != DomainChina {
		t.Fatalf("AllowedDomains() = %v, want [garmin.com garmin.cn]", first)
	}

	first[0] = testHostileDomain
	if second := AllowedDomains(); second[0] != DomainGlobal {
		t.Fatalf("AllowedDomains() leaked shared backing array: %v", second)
	}
}

// Domain.Validate is stricter than ParseDomain: it neither trims nor folds case,
// and it refuses the zero Domain rather than reading it as "use the default".
func TestDomainValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      Domain
		wantErr bool
	}{
		{name: "global", in: DomainGlobal},
		{name: "china", in: DomainChina},
		{name: "zero domain rejected", in: "", wantErr: true},
		{name: "case variant rejected", in: testCaseVariantDomain, wantErr: true},
		{name: "untrimmed rejected", in: " garmin.com", wantErr: true},
		{name: "attacker host rejected", in: testHostileDomain, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.in.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Domain(%q).Validate() = %q, want error", tc.in, got)
				}
				if !errors.Is(err, ErrUnsupportedDomain) {
					t.Fatalf("Domain(%q).Validate() error = %v, want ErrUnsupportedDomain", tc.in, err)
				}
				if got.IsValid() {
					t.Fatalf("Domain(%q).Validate() returned a valid ValidatedDomain on error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Domain(%q).Validate() unexpected error: %v", tc.in, err)
			}
			if got.Domain() != tc.in || got.String() != string(tc.in) {
				t.Fatalf("Domain(%q).Validate() = %q", tc.in, got)
			}
		})
	}
}
