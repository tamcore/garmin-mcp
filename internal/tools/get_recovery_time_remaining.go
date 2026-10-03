package tools

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetRecoveryTimeRemaining is the upstream compatibility name.
const ToolGetRecoveryTimeRemaining = "get_recovery_time_remaining"

// The sources a recovery time is read from, in fallback order.
const (
	recoverySourceReadiness = "training_readiness"
	recoverySourceMorning   = "morning_training_readiness"
	recoverySourceActivity  = "recent_activity"
)

// The coarse recovery states and their bands. Source: health_wellness.py:27-37.
const (
	recoveryStateRecovered       = "recovered"
	recoveryStateNearlyRecovered = "nearly_recovered"
	recoveryStateRecovering      = "recovering"
	recoveryStateNotRecovered    = "not_recovered"
	recoveryStateUnavailable     = "unavailable"

	nearlyRecoveredHours = 6
	recoveringHours      = 24
)

// recoveryActivityCount is how many recent activities the fallback reads.
// Source: health_wellness.py:103.
const recoveryActivityCount = 20

// noteRecoveryUnavailable explains an answer with no recovery time.
const noteRecoveryUnavailable = "no recovery time found: the day has no training readiness " +
	"snapshot with a recovery time, and the recent-activity fallback applies only to today. " +
	"Some devices show recovery on the watch without publishing it to Garmin Connect"

// RecoveryTimeRemaining is the recovery clock for one day. It is health data: never
// log it.
type RecoveryTimeRemaining struct {
	Date           string   `json:"date" jsonschema:"the day that was asked for, YYYY-MM-DD"`
	State          string   `json:"state" jsonschema:"recovered, nearly_recovered, recovering, not_recovered, unavailable"`
	Source         string   `json:"source,omitempty" jsonschema:"where the recovery time came from"`
	RemainingHours *float64 `json:"remaining_hours,omitempty" jsonschema:"recovery hours still to run, to 0.1 h"`
	ReadinessScore *float64 `json:"readiness_score,omitempty" jsonschema:"the readiness score of the snapshot"`
	ReadinessLevel *string  `json:"readiness_level,omitempty" jsonschema:"the readiness level of the snapshot"`
	ActivityID     *int64   `json:"activity_id,omitempty" jsonschema:"the activity whose recovery time was decayed"`
	Note           string   `json:"note,omitempty" jsonschema:"why no recovery time is reported"`
}

// LogValue reports the source and state, never a reading.
func (r RecoveryTimeRemaining) LogValue() slog.Value {
	return shape("recoveryTimeRemaining", slog.String("source", r.Source), slog.String("state", r.State))
}

// recoveryTimeRemainingInput is the strict argument set: one optional day.
type recoveryTimeRemainingInput struct {
	Date string `json:"date,omitempty" jsonschema:"the day to read, YYYY-MM-DD; today when omitted"`
}

func getRecoveryTimeRemainingContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetRecoveryTimeRemaining,
			Title: "Get remaining recovery time",
			Description: "read the recovery hours still to run for one day, today (the server's " +
				"local date) when no date is given. It reads the newest training readiness snapshot, then the " +
				"morning snapshot, and for today only falls back to the recovery time of " +
				"the most recent activity, decayed by the time since it ended. The answer " +
				"names its source",
			Tier:        policy.TierReadOnly,
			Category:    categoryHealth,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(optionalDateProperty("date", "the day to read; today when omitted")),
	}
}

// registerGetRecoveryTimeRemaining registers the tool.
func registerGetRecoveryTimeRemaining(registry *mcpserver.Registry, svc *service) error {
	stress, err := stressClient(svc)
	if err != nil {
		return err
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in recoveryTimeRemainingInput) (
		*mcp.CallToolResult, RecoveryTimeRemaining, error,
	) {
		out, err := svc.readRecoveryTimeRemaining(ctx, stress, in.Date)
		return nil, out, err
	}
	return mcpserver.AddTool(registry, getRecoveryTimeRemainingContract().Registration(), handler)
}

// readRecoveryTimeRemaining walks the fallback chain. Unlike upstream, a failed read
// fails the call: only a missing or empty document moves on to the next source.
func (s *service) readRecoveryTimeRemaining(
	ctx context.Context, stress *api.WellnessStress, date string,
) (RecoveryTimeRemaining, error) {
	now := s.now()
	today := now.Format(time.DateOnly)
	day, session, err := s.resolveStressDay(ctx, cmp.Or(date, today))
	if err != nil {
		return RecoveryTimeRemaining{}, err
	}
	entries, err := stress.TrainingReadiness(ctx, session, day, api.ReadinessViewAll)
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		return RecoveryTimeRemaining{}, fail(err)
	}
	latest, _ := api.LatestReadiness(entries)
	if out, ok := recoveryFromReadiness(recoverySourceReadiness, latest); ok {
		out.Date = day.String()
		return out, nil
	}
	morning, _, _ := api.MorningReadiness(entries)
	if out, ok := recoveryFromReadiness(recoverySourceMorning, morning); ok {
		out.Date = day.String()
		return out, nil
	}

	if day.String() == today {
		out, ok, err := s.recoveryFromActivities(ctx, session, now)
		if err != nil || ok {
			out.Date = day.String()
			return out, err
		}
	}
	return RecoveryTimeRemaining{Date: day.String(), State: recoveryStateUnavailable,
		Note: noteRecoveryUnavailable}, nil
}

// recoveryFromReadiness maps a snapshot that carries a usable recovery time.
func recoveryFromReadiness(source string, snapshot api.Readiness) (RecoveryTimeRemaining, bool) {
	minutes, ok := snapshot.RemainingRecoveryMinutes()
	if !ok || minutes < 0 {
		return RecoveryTimeRemaining{}, false
	}
	hours := roundedHours(minutes)
	return RecoveryTimeRemaining{
		State:          recoveryState(hours),
		Source:         source,
		RemainingHours: &hours,
		ReadinessScore: optionalFloat(snapshot.Score),
		ReadinessLevel: optionalText(snapshot.Level),
	}, true
}

// recoveryFromActivities decays the recovery time of the most recently ended activity
// that carries one. Source: health_wellness.py:82-133.
func (s *service) recoveryFromActivities(
	ctx context.Context, session client.Session, now time.Time,
) (RecoveryTimeRemaining, bool, error) {
	page, err := client.NewPage(0, min(recoveryActivityCount, s.limits.MaxPageSize))
	if err != nil {
		return RecoveryTimeRemaining{}, false, fail(err)
	}
	result, err := s.activities.List(ctx, session, api.ListQuery{Page: page})
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		return RecoveryTimeRemaining{}, false, fail(err)
	}

	var best *api.Activity
	var bestEnd time.Time
	for index, activity := range result.Activities {
		end, ok := activityEnd(activity)
		if _, set := activity.RecoveryTime.Float64(); !set || !ok {
			continue
		}
		if best == nil || end.After(bestEnd) {
			best, bestEnd = &result.Activities[index], end
		}
	}
	if best == nil {
		return RecoveryTimeRemaining{}, false, nil
	}
	assigned, _ := best.RecoveryTime.Float64()
	hours := roundedHours(max(0, assigned-max(0, now.Sub(bestEnd).Minutes())))
	return RecoveryTimeRemaining{
		State: recoveryState(hours), Source: recoverySourceActivity,
		RemainingHours: &hours, ActivityID: best.ActivityID,
	}, true, nil
}

// activityEnd is the activity's start plus its duration, or elapsed duration.
func activityEnd(activity api.Activity) (time.Time, bool) {
	begin, ok := activity.BeginTimestamp.Float64()
	if !ok || begin <= 0 {
		return time.Time{}, false
	}
	seconds, _ := cmp.Or(activity.Duration, activity.ElapsedTime).Float64()
	return time.UnixMilli(int64(begin)).Add(time.Duration(seconds * float64(time.Second))), true
}

// recoveryState maps remaining hours to a coarse state.
func recoveryState(hours float64) string {
	switch {
	case hours == 0:
		return recoveryStateRecovered
	case hours <= nearlyRecoveredHours:
		return recoveryStateNearlyRecovered
	case hours <= recoveringHours:
		return recoveryStateRecovering
	default:
		return recoveryStateNotRecovered
	}
}
