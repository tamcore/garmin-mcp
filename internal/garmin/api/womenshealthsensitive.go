package api

import "log/slog"

// The women's-health domain's LogValue implementations. Every model here is
// menstrual-cycle or pregnancy data — the most sensitive category this project
// handles — so each reports only that a document was retained, through its own
// redacted client.Payload, never a byte of Document itself.

// LogValue reports only that a document was retained for one calendar day, never
// its content.
func (d MenstrualDay) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "menstrualDay"),
		slog.Any("payload", d.raw),
	)
}

// LogValue reports only that a document was retained for the requested window,
// never its content.
func (c MenstrualCalendar) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "menstrualCalendar"),
		slog.Any("payload", c.raw),
	)
}

// LogValue reports only that a document was retained, never its content.
func (p PregnancySummary) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("model", "pregnancySummary"),
		slog.Any("payload", p.raw),
	)
}
