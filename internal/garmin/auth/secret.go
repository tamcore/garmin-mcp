package auth

// secretString is one sealed secret value.
//
// The secret-bearing types keep their material as *secretString rather than as a
// string, and that indirection is the last line of defense against a
// method-stripping alias:
//
//	type Raw auth.TokenSet
//	fmt.Sprintf("%s", Raw(set))
//
// The alias has none of the redacting methods, so fmt reflects the value instead.
// Under %s and %q it takes its bad-verb path, which re-prints the value at depth
// zero and dereferences the pointer to the sealed struct, printing that struct's
// fields. A string field is printed verbatim there; a pointer field is printed as an
// address, because fmt prints a pointer at a depth greater than zero as a number.
// Giving the inner struct a String method does not help: fmt cannot call a method on
// a value reached through an unexported field, so it reflects the raw value anyway.
//
// secretString deliberately has no String, GoString or MarshalText method, so
// nothing is tempted to render it.
type secretString string

// sealSecret seals value, or returns nil for the empty string so absence stays
// distinguishable from an empty secret.
func sealSecret(value string) *secretString {
	if value == "" {
		return nil
	}
	sealed := secretString(value)
	return &sealed
}

// revealSecret returns the sealed value, or "" when none is held. It is the only way
// back to the material, and every caller of it hands that value straight to a request
// body or to an accessor a caller asked for deliberately.
func revealSecret(sealed *secretString) string {
	if sealed == nil {
		return ""
	}
	return string(*sealed)
}
