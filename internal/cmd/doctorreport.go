package cmd

import "strings"

// tierNote states the rule an operator cannot see from the enablement flags alone:
// enabling a tier is necessary and never sufficient, because a call in that tier
// also needs a granted OAuth scope, and this repository issues none yet.
const tierNote = "a write or destructive call also needs a granted OAuth scope, " +
	"which no deployment issues yet, so both tiers refuse today"

// render formats the report for an operator.
//
// The layout is one fact per line, so a reader and a grep both find what they came
// for. Nothing here formats a secret: the effective configuration arrives already
// redacted, and every other field is a path, a label, or a bool.
func (d diagnosis) render() string {
	var b strings.Builder

	b.WriteString("garmin-mcp doctor\n\n")
	writeLine(&b, "transport", d.Transport)
	writeLine(&b, "region", d.Region)
	if !d.Remote {
		// A remote deployment binds no account in configuration: every principal
		// arrives with a request, so reporting one here would name a setting
		// nothing reads.
		writeLine(&b, "principal", d.PrincipalID+" ("+boundLabel(d.PrincipalBound)+")")
	}
	writeLine(&b, "state directory", d.StateDir)
	writeCheck(&b, "encryption key", d.KeyFile, d.KeyState, d.KeyDetail)
	if d.Remote {
		d.writeRemoteSection(&b)
	} else {
		writeCheck(&b, "token store", d.TokenDir, d.StoreState, d.StoreDetail)
		writeCheck(&b, "garmin tokens", "", d.TokensState, d.TokensDetail)
	}

	b.WriteString("\ntool tiers:\n")
	writeLine(&b, "  read-only", "always registered")
	writeLine(&b, "  write", enabledLabel(d.WriteEnabled))
	writeLine(&b, "  destructive", enabledLabel(d.DestructiveEnabled))
	b.WriteString("  note: " + tierNote + "\n")

	b.WriteString("\neffective configuration:\n")
	b.WriteString(d.ConfigLine + "\n")
	return b.String()
}

// writeLine writes one "label: value" line.
func writeLine(b *strings.Builder, label, value string) {
	b.WriteString(label + ": " + value + "\n")
}

// writeCheck writes one check, with its location when it has one.
func writeCheck(b *strings.Builder, label, location string, checked state, detail string) {
	value := detail
	if location != "" {
		value = location + " — " + detail
	}
	writeLine(b, label, string(checked)+": "+value)
}

// boundLabel renders whether the principal is usable as one.
func boundLabel(bound bool) string {
	if bound {
		return "bound"
	}
	return "not a usable principal identifier"
}

// enabledLabel renders an operator enablement flag.
func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
