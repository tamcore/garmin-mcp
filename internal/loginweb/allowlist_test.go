package loginweb_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

const testEmailA = "a@example.com"

func TestAnEmptyEmailAllowlistPermitsEveryAddress(t *testing.T) {
	allowlist, err := loginweb.NewEmailAllowlist(nil)
	if err != nil {
		t.Fatalf("NewEmailAllowlist(nil): %v", err)
	}
	if !allowlist.IsOpen() {
		t.Fatal("an empty allowlist does not report itself as open")
	}
	if !allowlist.Permits("anyone@example.com") {
		t.Fatal("an empty allowlist refused an address")
	}
}

func TestEmailAllowlistPermitsOnlyListedAddressesAndFoldsCase(t *testing.T) {
	allowlist, err := loginweb.NewEmailAllowlist([]string{"A@Example.com", "b@example.com"})
	if err != nil {
		t.Fatalf("NewEmailAllowlist: %v", err)
	}
	if allowlist.IsOpen() || allowlist.Len() != 2 {
		t.Fatalf("IsOpen()=%v Len()=%d, want false and 2", allowlist.IsOpen(), allowlist.Len())
	}
	for _, permitted := range []string{testEmailA, "A@EXAMPLE.COM", " b@example.com "} {
		if !allowlist.Permits(permitted) {
			t.Fatalf("Permits(%q) = false, want true", permitted)
		}
	}
	for _, refused := range []string{"c@example.com", "", "a@example.com.evil", "a@example"} {
		if allowlist.Permits(refused) {
			t.Fatalf("Permits(%q) = true, want false", refused)
		}
	}
}

// TestNewEmailAllowlistRefusesAMalformedEntry pins each case to the exact
// checkAllowedEmail branch (or NewEmailAllowlist's own cap/duplicate check) it
// exercises, so two cases never pass for the same reason by accident:
//
//   - "empty entry": trimmed to "", hits address == "" (the empty branch).
//   - "no at sign": Cut finds no "@", hits the malformed-address branch via
//     !found.
//   - "two at signs": Cut splits on the first "@", leaving a second "@" in the
//     host, hits the malformed-address branch via strings.Contains(host, "@").
//     Distinct from "no at sign": found is true here.
//   - "empty local": Cut yields local == "", hits the malformed-address branch
//     via local == "". Distinct from "empty host": host is non-empty here.
//   - "empty host": Cut yields host == "", hits the malformed-address branch via
//     host == "". Distinct from "empty local": local is non-empty here.
//   - "host has no dot": passes the malformed-address branch, then fails
//     strings.Contains(host, ".") — the only case that reaches that branch.
//   - "embedded space": an interior 0x20 byte, hits the space/control-byte loop
//     via the <= 0x20 half of the condition.
//   - "interior tab": an interior 0x09 byte, hits the same loop as "embedded
//     space" but through a byte TrimSpace would also strip at the ends —
//     placed mid-string so it survives trimming and still reaches the loop.
//     Kept distinct from "embedded space" (whitespace) and "control byte" (a
//     non-whitespace control character) by using a third, different byte value.
//   - "control byte": an interior 0x0A byte, hits the same loop as the two
//     above through the same <= 0x20 half, with a byte that is not printable
//     whitespace at all.
//   - "over the bound": length exceeds MaxEmailLen, hits the length branch —
//     the only case that reaches it.
//   - "case duplicate" and "exact duplicate": both entries individually pass
//     checkAllowedEmail; the second collides with the first's folded value in
//     NewEmailAllowlist's own duplicate check. Deliberate double coverage of
//     that one branch under two input shapes (differing only in the first
//     entry's case).
//
// "blank entry" ({"   "}) is deliberately absent: TrimSpace runs before
// checkAllowedEmail is ever called, so a whitespace-only entry collapses to ""
// and lands on the identical "empty entry" branch for the identical reason —
// it added a second name for one branch rather than a new one, which is not
// real coverage.
func TestNewEmailAllowlistRefusesAMalformedEntry(t *testing.T) {
	cases := map[string][]string{
		"empty entry":     {""},
		"no at sign":      {"nobody"},
		"two at signs":    {"a@b@example.com"},
		"empty local":     {"@example.com"},
		"empty host":      {"a@"},
		"host has no dot": {"a@example"},
		"embedded space":  {"a b@example.com"},
		"interior tab":    {"a\tb@example.com"},
		"control byte":    {"a\n@example.com"},
		"over the bound":  {strings.Repeat("a", loginweb.MaxEmailLen) + "@example.com"},
		"case duplicate":  {testEmailA, "A@Example.com"},
		"exact duplicate": {testEmailA, testEmailA},
	}
	for name, addresses := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loginweb.NewEmailAllowlist(addresses); !errors.Is(err, loginweb.ErrInvalidConfig) {
				t.Fatalf("NewEmailAllowlist(%q) error = %v, want ErrInvalidConfig", name, err)
			}
		})
	}
}

func TestNewEmailAllowlistRefusesTooManyEntries(t *testing.T) {
	addresses := make([]string, 0, loginweb.MaxAllowedEmails+1)
	for i := range loginweb.MaxAllowedEmails + 1 {
		addresses = append(addresses, fmt.Sprintf("user-%d@example.com", i))
	}
	if _, err := loginweb.NewEmailAllowlist(addresses); !errors.Is(err, loginweb.ErrInvalidConfig) {
		t.Fatalf("NewEmailAllowlist over the cap error = %v, want ErrInvalidConfig", err)
	}
}

func TestEmailAllowlistNeverRendersAnAddress(t *testing.T) {
	allowlist, err := loginweb.NewEmailAllowlist([]string{"secret@example.com"})
	if err != nil {
		t.Fatalf("NewEmailAllowlist: %v", err)
	}

	verbs := []string{"%v", "%s", "%+v", "%#v", "%d", "%q", "%x", "%X"}
	rendered := make([]string, 0, len(verbs)+1)
	rendered = append(rendered, allowlist.String())
	// The verbs are runtime values rather than literals so go vet's printf check,
	// which would otherwise flag %d and %s against a struct, cannot short-circuit
	// the very verbs this test exists to exercise.
	for _, verb := range verbs {
		rendered = append(rendered, fmt.Sprintf(verb, allowlist))
	}

	for _, r := range rendered {
		if strings.Contains(r, "secret@example.com") {
			t.Fatalf("a rendering leaked an allowlisted address: %s", r)
		}
	}
}

// strippedEmailAllowlist is the method-stripping alias attack: a caller that
// declares its own type from EmailAllowlist loses String, so fmt falls back to
// reflection over the fields. It must still be unable to reach the address,
// because the material sits behind a pointer one level deeper than the type.
type strippedEmailAllowlist loginweb.EmailAllowlist

func TestEmailAllowlistNeverRendersAnAddressEvenWithMethodsStripped(t *testing.T) {
	allowlist, err := loginweb.NewEmailAllowlist([]string{"stripped-secret@example.com"})
	if err != nil {
		t.Fatalf("NewEmailAllowlist: %v", err)
	}
	stripped := strippedEmailAllowlist(allowlist)

	// The verbs are runtime values, not literals, for the same reason as in
	// TestEmailAllowlistNeverRendersAnAddress: go vet's printf check must not be
	// able to short-circuit the verbs under test.
	for _, verb := range []string{"%v", "%+v", "%#v", "%d", "%s", "%q", "%x", "%X"} {
		rendered := fmt.Sprintf(verb, stripped)
		if strings.Contains(rendered, "stripped-secret@example.com") {
			t.Fatalf("rendering %s of the stripped alias leaked an allowlisted address: %s", verb, rendered)
		}
	}
}
