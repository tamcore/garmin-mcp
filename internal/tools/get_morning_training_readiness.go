package tools

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetMorningTrainingReadiness is the upstream compatibility name of the morning
// readiness tool.
const ToolGetMorningTrainingReadiness = "get_morning_training_readiness"

// MorningReadinessResult is the readiness snapshot taken after waking.
//
// It is health data — never log it, never cache it. It is the second view of the
// readiness read: the same URL as get_training_readiness, reduced to one snapshot.
type MorningReadinessResult struct {
	Date string `json:"date" jsonschema:"the calendar day, YYYY-MM-DD"`

	// HasData reports whether Garmin held any snapshot for the day.
	HasData bool `json:"has_data" jsonschema:"whether Garmin held a readiness snapshot for the day"`

	// FromWakeupReset reports whether the returned snapshot is the after-waking one.
	// Not every device records the trigger, so when none of the day's snapshots
	// carries it the first snapshot is returned instead and this is false. That is
	// upstream's documented fallback, and saying so is the honest half of it.
	FromWakeupReset bool `json:"from_wakeup_reset" jsonschema:"whether the snapshot is the after-waking one"`

	Readiness *ReadinessEntry `json:"readiness,omitempty" jsonschema:"the selected readiness snapshot"`
}

// LogValue reports the shape of the answer, never a score.
//
// FromWakeupReset is deliberately absent, and the call is arguable both ways. It reads
// like shape, because it names which branch the selection took, and a branch taken is
// ordinarily server behaviour worth logging. It is not, because the branch is chosen by
// a predicate over the payload: the flag answers whether any of the day's snapshots
// carried the wake-up trigger. Logged daily, that is a record of when the account wore
// a device overnight — the same coverage disclosure as a count of readings that passed
// a value test, arriving as one bit at a time. The result still carries the flag,
// because a caller must know which snapshot it got; the log does not need to.
func (m MorningReadinessResult) LogValue() slog.Value {
	return shape("morningReadiness",
		slog.Bool("hasData", m.HasData),
		slog.String("readiness", presence(m.Readiness != nil)),
	)
}

// getMorningTrainingReadinessInput is the strict argument set: one calendar day.
type getMorningTrainingReadinessInput struct {
	Date string `json:"date" jsonschema:"the calendar day to read, YYYY-MM-DD"`
}

func getMorningTrainingReadinessContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetMorningTrainingReadiness,
			Title: "Get morning training readiness",
			Description: "read the account's readiness snapshot taken after waking, which " +
				"is what Garmin's morning report shows. When no snapshot names that " +
				"trigger — some devices do not record it — the day's first snapshot is " +
				"returned and from_wakeup_reset is false",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(dateProperty("date", "the calendar day to read")),
	}
}

// registerGetMorningTrainingReadiness registers the tool.
func registerGetMorningTrainingReadiness(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(
		ctx context.Context, _ *mcp.CallToolRequest, in getMorningTrainingReadinessInput,
	) (*mcp.CallToolResult, MorningReadinessResult, error) {
		day, session, err := svc.resolveStressDay(ctx, in.Date)
		if err != nil {
			return nil, MorningReadinessResult{}, err
		}
		read, err := stress.TrainingReadiness(ctx, session, day, api.ReadinessViewMorning)
		if err != nil {
			return nil, MorningReadinessResult{}, fail(err)
		}
		return nil, newMorningReadiness(day.String(), read), nil
	}
	return mcpserver.AddTool(registry,
		getMorningTrainingReadinessContract().Registration(), handler)
}

// newMorningReadiness selects the snapshot and maps it. A day with no snapshot is a
// normal state — a device that was not worn, or an account with no history — so it is
// reported as an empty answer rather than as a failure.
func newMorningReadiness(date string, entries []api.Readiness) MorningReadinessResult {
	selected, matched, ok := api.MorningReadiness(entries)
	if !ok {
		return MorningReadinessResult{Date: date}
	}
	mapped := newReadinessEntry(selected)
	return MorningReadinessResult{
		Date:            date,
		HasData:         true,
		FromWakeupReset: matched,
		Readiness:       &mapped,
	}
}
