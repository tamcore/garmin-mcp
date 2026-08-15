package tools

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// ToolGetPowerDurationCurve is the upstream compatibility name of the season-best
// power duration curve.
const ToolGetPowerDurationCurve = "get_power_duration_curve"

// Bounds of the season-best curve. It is the one read on this surface that fetches
// many files in one call, so every dimension of that fan-out is bounded.
const (
	// defaultCurveActivities and maxCurveActivities are the manifest's default and
	// maximum activity counts.
	defaultCurveActivities = 20
	maxCurveActivities     = 50

	// defaultCurveActivityType is the manifest's activity filter default.
	defaultCurveActivityType = "cycling"

	// maxCurveBytes bounds the bytes one call may pull from Garmin in total. It is
	// the fan-out bound: a single file is already bounded by MaxDownloadBytes, and
	// this one keeps fifty of them from adding up to an unbounded transfer.
	maxCurveBytes = 64 << 20

	// ftpFactor is the share of a twenty-minute best that estimates threshold power.
	ftpFactor = 0.95

	// ftpDurationSeconds is the window that estimate comes from.
	ftpDurationSeconds = 1200
)

// powerCurveInput is the season-best argument set.
type powerCurveInput struct {
	NumActivities *int   `json:"num_activities" jsonschema:"how many recent activities to analyse"`
	ActivityType  string `json:"activity_type" jsonschema:"the Garmin activity type to filter by"`
}

// A SeasonBest is the best mean maximal power over one duration, and where it came
// from.
type SeasonBest struct {
	DurationSecs int     `json:"duration_seconds" jsonschema:"the window length in seconds"`
	Label        string  `json:"duration" jsonschema:"the window length as a label, for example 20min"`
	Watts        float64 `json:"watts" jsonschema:"the best mean power over that window"`
	ActivityID   int64   `json:"activity_id" jsonschema:"the activity the best came from"`
	ActivityName *string `json:"activity_name,omitempty" jsonschema:"the activity's name, when it has one"`
	StartTime    *string `json:"start_time,omitempty" jsonschema:"when that activity started"`
}

// A PowerCurve is the season-best curve across the analysed activities.
type PowerCurve struct {
	ActivityType    string       `json:"activity_type" jsonschema:"the activity type that was analysed"`
	Analyzed        int          `json:"activities_analyzed" jsonschema:"how many files were decoded"`
	Skipped         int          `json:"activities_skipped" jsonschema:"how many were skipped, for any reason"`
	BytesRead       int          `json:"bytes_downloaded" jsonschema:"how many bytes the call pulled from Garmin"`
	BudgetExhausted bool         `json:"budget_exhausted" jsonschema:"whether the transfer bound stopped the scan"`
	FTPEstimate     *float64     `json:"ftp_estimate_w,omitempty" jsonschema:"the threshold power estimate in watts"`
	FTPNote         string       `json:"ftp_note" jsonschema:"how the estimate was derived"`
	SeasonBests     []SeasonBest `json:"season_bests" jsonschema:"the best power at each standard duration"`
}

// LogValue reports the scan's shape, never a reading.
func (c PowerCurve) LogValue() slog.Value {
	return shape("powerCurve",
		slog.Int("analyzed", c.Analyzed),
		slog.Int("skipped", c.Skipped),
		slog.Int("bests", len(c.SeasonBests)),
		slog.Bool("truncated", c.BudgetExhausted),
	)
}

func getPowerDurationCurveContract() Contract {
	return Contract{
		Spec: mcpserver.ToolSpec{
			Name:  ToolGetPowerDurationCurve,
			Title: "Get season-best power duration curve",
			Description: "download the device files of recent activities and report the best " +
				"mean maximal power at 5s, 30s, 1min, 5min, 10min, 20min and 60min, naming " +
				"the activity each best came from. The twenty-minute best times 0.95 comes " +
				"back as a threshold estimate. This call downloads one file per activity, so " +
				"it is slow and it stops at this server's transfer bound; an activity whose " +
				"file cannot be read is skipped and counted",
			Tier:        policy.TierReadOnly,
			Category:    categoryLocation,
			Annotations: readOnlyAnnotations(),
		},
		Schema: NewSchema(
			Property{
				Name:        "num_activities",
				Types:       []string{typeInteger},
				Description: "how many recent activities to analyse",
				Minimum:     bound(1),
				Maximum:     bound(maxCurveActivities),
				Default:     defaultCurveActivities,
			},
			Property{
				Name:        "activity_type",
				Types:       []string{typeString},
				Description: "the Garmin activity type to filter by",
				MaxLength:   new(maxActivityTypeArgumentLen),
				Default:     defaultCurveActivityType,
			},
		),
	}
}

// registerGetPowerDurationCurve registers the tool.
func registerGetPowerDurationCurve(registry *mcpserver.Registry, svc *service) error {
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in powerCurveInput) (
		*mcp.CallToolResult, PowerCurve, error,
	) {
		curve, err := svc.powerDurationCurve(ctx, in)
		if err != nil {
			return nil, PowerCurve{}, err
		}
		return nil, curve, nil
	}
	return mcpserver.AddTool(registry, getPowerDurationCurveContract().Registration(), handler)
}

// powerDurationCurve scans recent activities of one type for their best efforts.
func (s *service) powerDurationCurve(ctx context.Context, in powerCurveInput) (PowerCurve, error) {
	count, err := resolveCurveCount(in.NumActivities)
	if err != nil {
		return PowerCurve{}, err
	}
	filter, err := parseActivityTypeFilter(optionalTextArg(in.ActivityType, defaultCurveActivityType))
	if err != nil {
		return PowerCurve{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return PowerCurve{}, err
	}

	page, err := client.NewPage(0, min(count, s.limits.MaxPageSize))
	if err != nil {
		return PowerCurve{}, fail(err)
	}
	listed, err := s.activities.List(ctx, session, api.ListQuery{Page: page, Type: filter})
	if err != nil {
		return PowerCurve{}, fail(err)
	}
	return s.scanCurve(ctx, session, listed.Activities, filter.String())
}

// resolveCurveCount applies the manifest default and refuses an out-of-range count.
func resolveCurveCount(value *int) (int, error) {
	count := defaultCurveActivities
	if value != nil {
		count = *value
	}
	switch {
	case count < 1:
		return 0, invalidArgument("num_activities must be at least 1")
	case count > maxCurveActivities:
		return 0, invalidArgument(
			"num_activities must not exceed " + strconv.Itoa(maxCurveActivities))
	}
	return count, nil
}

// scanCurve decodes each listed activity and folds its curve into the season bests.
func (s *service) scanCurve(
	ctx context.Context, session client.Session, activities []api.Activity, filter string,
) (PowerCurve, error) {
	scan := &curveScan{bests: map[int]SeasonBest{}}
	for _, activity := range activities {
		if scan.bytes >= maxCurveBytes {
			scan.exhausted = true
			break
		}
		if err := s.foldActivity(ctx, session, activity, scan); err != nil {
			return PowerCurve{}, err
		}
	}
	return scan.result(filter), nil
}

// foldActivity folds one activity's curve into the scan, skipping what cannot be
// read. A failure Garmin decided about the whole session — a rejected token, a rate
// limit, a cancelled call — stops the scan instead of being counted as a skip.
func (s *service) foldActivity(
	ctx context.Context, session client.Session, activity api.Activity, scan *curveScan,
) error {
	id, ok := curveActivityID(activity)
	if !ok {
		scan.skipped++
		return nil
	}

	decoded, size, err := s.downloadFITActivity(ctx, session, id)
	if err != nil {
		if fatalCurveError(err) {
			return err
		}
		scan.skipped++
		return nil
	}

	scan.bytes += size
	scan.analyzed++
	scan.fold(activity, id.Int64(), api.PowerDurationCurve(decoded.Records))
	return nil
}

// curveActivityID validates the identifier the listing carried.
func curveActivityID(activity api.Activity) (client.ID, bool) {
	if activity.ActivityID == nil {
		return client.ID{}, false
	}
	id, err := client.NewID(*activity.ActivityID)
	if err != nil {
		return client.ID{}, false
	}
	return id, true
}

// fatalCurveError reports the failures that must stop the whole scan.
func fatalCurveError(err error) bool {
	return errors.Is(err, client.ErrAuthentication) ||
		errors.Is(err, client.ErrRateLimited) ||
		errors.Is(err, client.ErrMissingPrincipal) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// A curveScan is the running state of one season-best scan.
type curveScan struct {
	bests     map[int]SeasonBest
	analyzed  int
	skipped   int
	bytes     int
	exhausted bool
}

// fold keeps the better of the stored best and this activity's.
func (c *curveScan) fold(activity api.Activity, id int64, curve []api.FITPowerBest) {
	for _, best := range curve {
		stored, seen := c.bests[best.Seconds]
		if seen && stored.Watts >= best.Watts {
			continue
		}
		c.bests[best.Seconds] = SeasonBest{
			DurationSecs: best.Seconds,
			Label:        durationLabel(best.Seconds),
			Watts:        fitRound(best.Watts, placesOne),
			ActivityID:   id,
			ActivityName: activity.ActivityName,
			StartTime:    activity.StartTimeGMT,
		}
	}
}

// result renders the scan in the curve's own duration order.
func (c *curveScan) result(filter string) PowerCurve {
	durations := api.CurveDurations()
	bests := make([]SeasonBest, 0, len(durations))
	for _, duration := range durations {
		if best, ok := c.bests[duration]; ok {
			bests = append(bests, best)
		}
	}

	curve := PowerCurve{
		ActivityType:    filter,
		Analyzed:        c.analyzed,
		Skipped:         c.skipped,
		BytesRead:       c.bytes,
		BudgetExhausted: c.exhausted,
		SeasonBests:     bests,
		FTPNote: "the twenty-minute best multiplied by 0.95. It is an estimate from " +
			"recorded efforts, not a measured threshold test",
	}
	if best, ok := c.bests[ftpDurationSeconds]; ok {
		estimate := fitRound(best.Watts*ftpFactor, placesOne)
		curve.FTPEstimate = &estimate
	}
	return curve
}

// durationLabel renders a window length the way the upstream description names it.
func durationLabel(seconds int) string {
	if seconds < secondsPerMinute {
		return strconv.Itoa(seconds) + "s"
	}
	return strconv.Itoa(seconds/secondsPerMinute) + "min"
}
