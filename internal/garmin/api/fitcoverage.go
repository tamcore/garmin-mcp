package api

import "time"

// A fitCoverage reports whether a span's window survived the record bound. See
// docs/parity.md.
type fitCoverage struct {
	truncated bool
	last      time.Time
	measured  bool
}

func newFITCoverage(records []FITRecord, truncated bool) fitCoverage {
	out := fitCoverage{truncated: truncated}
	if len(records) > 0 {
		out.last, out.measured = records[len(records)-1].Time, true
	}
	return out
}

// covers reports whether the derived figures of one span describe its whole window.
func (c fitCoverage) covers(span FITSpan) bool {
	if !c.truncated {
		return true
	}
	if !c.measured || span.End.IsZero() {
		return false
	}
	return !span.End.After(c.last)
}

// withoutDerived drops every figure the record stream implied. The window is taken
// from the span, never from the retained records, whose end is only the prefix's.
func (s FITSegment) withoutDerived(span FITSpan) FITSegment {
	kept := FITSegment{
		Start:   s.Start,
		End:     span.End,
		Sport:   s.Sport,
		Samples: s.Samples,
	}
	if !span.Start.IsZero() && !span.End.IsZero() {
		kept.Seconds = fitNumber(span.End.Sub(span.Start).Seconds())
	}
	return kept
}
