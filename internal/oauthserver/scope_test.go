package oauthserver

import (
	"errors"
	"strings"
	"testing"
)

func mustScopeSet(t *testing.T, raw string) ScopeSet {
	t.Helper()
	set, err := ParseScopeSet(raw)
	if err != nil {
		t.Fatalf("ParseScopeSet(%q): %v", raw, err)
	}
	return set
}

func TestParseScopeSetNormalizesOrderAndDuplicates(t *testing.T) {
	set := mustScopeSet(t, "garmin.health.read  garmin.profile.read garmin.health.read")

	if got, want := set.String(), "garmin.health.read garmin.profile.read"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if set.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", set.Len())
	}
}

func TestParseScopeSetEmptyIsEmptySet(t *testing.T) {
	set := mustScopeSet(t, "   ")

	if !set.IsEmpty() || set.String() != "" {
		t.Fatalf("empty input produced %q", set.String())
	}
}

func TestParseScopeSetRejectsInvalidInput(t *testing.T) {
	long := strings.Repeat("a", MaxScopeLen+1)
	many := make([]string, 0, MaxScopeCount+1)
	for i := range MaxScopeCount + 1 {
		many = append(many, "s"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}

	for name, raw := range map[string]string{
		"quote":                  `read "write"`,
		"backslash":              `read\write`,
		"control":                "read\x01write",
		"tab":                    "read\twrite",
		"newline inside a token": "read\nwrite",
		"over-long token":        long,
		"too many":               strings.Join(many, " "),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseScopeSet(raw); !errors.Is(err, ErrInvalidScope) {
				t.Fatalf("ParseScopeSet(%q) error = %v, want ErrInvalidScope", raw, err)
			}
		})
	}
}

func TestScopeSetSubsetAndEqual(t *testing.T) {
	read := mustScopeSet(t, "a.read b.read")
	wider := mustScopeSet(t, "a.read b.read c.write")
	empty := ScopeSet{}

	if !read.IsSubsetOf(wider) {
		t.Fatal("read should be a subset of wider")
	}
	if wider.IsSubsetOf(read) {
		t.Fatal("wider must not be a subset of read")
	}
	if !empty.IsSubsetOf(read) {
		t.Fatal("the empty set is a subset of every set")
	}
	if !read.Equal(mustScopeSet(t, "b.read a.read")) {
		t.Fatal("Equal must ignore ordering")
	}
	if read.Equal(wider) {
		t.Fatal("Equal must compare exact membership")
	}
	if !read.Contains("a.read") || read.Contains("c.write") {
		t.Fatal("Contains is wrong")
	}
}

func TestScopeSetSliceIsACopy(t *testing.T) {
	set := mustScopeSet(t, "a.read b.read")

	got := set.Slice()
	got[0] = "mutated"

	if set.Slice()[0] == "mutated" {
		t.Fatal("Slice exposed the internal backing array")
	}
}

func TestNewScopeSetValidates(t *testing.T) {
	if _, err := NewScopeSet("ok.read", "bad scope"); !errors.Is(err, ErrInvalidScope) {
		t.Fatal("NewScopeSet accepted a scope containing a space")
	}
	set, err := NewScopeSet("b.read", "a.read")
	if err != nil {
		t.Fatalf("NewScopeSet: %v", err)
	}
	if got, want := set.String(), "a.read b.read"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
