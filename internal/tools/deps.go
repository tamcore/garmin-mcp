// Package tools registers the Garmin MCP tools.
//
// One file per tool, or per closely-related tool group where a batch tool is nothing
// but the loop of its single-item neighbour, each exposing a register<Name> function,
// and one register.go that wires them in explicit tier order. A tool file owns four
// things and nothing else: the tool's wire name, its strict input schema, its bounded
// result model, and the handler that maps arguments onto a domain client in
// internal/garmin/api.
//
// What a tool file deliberately does not own:
//
//   - Policy, rate limiting, logging and destructive confirmation. Those are applied
//     centrally by the middleware chain in internal/mcpserver, so a handler that
//     forgets to check something cannot exist.
//   - The principal. It is resolved from the request context through
//     identity.FromContext, never from an argument. No tool accepts a user id, an
//     email address, an account selector, a display name or a filesystem path.
//   - Caching. No Garmin result is cached here. Health, location, nutrition and
//     women's-health payloads must not be persisted or shared, so every call reads
//     through to Garmin.
//   - The filesystem. A download streams into memory under a bound and comes back as
//     an embedded MCP resource. No tool accepts a directory, creates one or writes a
//     file, which is why upstream's set_fit_download_dir is not implemented here and
//     why download_activity_file declares no output_dir.
//
// Every result model implements slog.LogValuer and reports its shape rather than its
// content, so a result that reaches a log sink by accident leaks nothing. Every
// failure is returned as a *ToolError, whose message is an authored remediation and
// never a raw payload, token, cookie, coordinate or stack trace.
package tools

import (
	"context"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
)

// Result bounds. Each one is the point at which a result stops being useful to a
// model and starts being a denial of service.
const (
	// DefaultMaxWindowActivities bounds how many activities one date window may
	// yield before the tool refuses and asks for a narrower window.
	DefaultMaxWindowActivities = 2000

	// DefaultMaxActivityPageSize bounds one requested page of activities. It
	// matches the maximum upstream documents for get_activities.
	DefaultMaxActivityPageSize = 100

	// DefaultMaxWindowPageSize bounds one requested page of a date window. It
	// matches the maximum upstream documents for get_activities_by_date.
	DefaultMaxWindowPageSize = 200

	// DefaultMaxDevices bounds the returned device list.
	DefaultMaxDevices = 64

	// DefaultMaxSplits bounds the returned split list.
	DefaultMaxSplits = 500

	// DefaultMaxExerciseSets bounds the returned strength-set list.
	DefaultMaxExerciseSets = 500

	// DefaultMaxWindowPage bounds the page index of a date window, so a caller
	// cannot ask for page one billion.
	DefaultMaxWindowPage = 10_000

	// DefaultMaxWorkouts bounds the returned workout library listing.
	DefaultMaxWorkouts = 500

	// DefaultMaxPersonalRecords bounds the returned personal-record list.
	DefaultMaxPersonalRecords = 200

	// DefaultMaxZones bounds a returned time-in-zones list.
	DefaultMaxZones = 32

	// DefaultMaxSplitSummaries bounds the returned split-summary list.
	DefaultMaxSplitSummaries = 200

	// DefaultMaxBatchItems bounds how many items one batch tool may act on. A
	// batch loops the single-item call, so this is also a bound on how many
	// Garmin writes one MCP call can cause.
	DefaultMaxBatchItems = 50

	// DefaultMaxDownloadBytes bounds a file this server will inline into an MCP
	// result. A download is base64-encoded into the response, so the ceiling is
	// what a client can be asked to receive in one message, not what Garmin can
	// serve.
	DefaultMaxDownloadBytes = 4 << 20
)

// Bounds are the per-tool result bounds. It is plain immutable data: a zero field
// means "use the default", so Bounds{} is the safe configuration rather than an
// unbounded one.
type Bounds struct {
	// MaxWindowActivities bounds the activities one date window may yield.
	MaxWindowActivities int
	// MaxDevices bounds the returned device list.
	MaxDevices int
	// MaxSplits bounds the returned split list.
	MaxSplits int
	// MaxExerciseSets bounds the returned strength-set list.
	MaxExerciseSets int
	// MaxWorkouts bounds the returned workout library listing.
	MaxWorkouts int
	// MaxPersonalRecords bounds the returned personal-record list.
	MaxPersonalRecords int
	// MaxBatchItems bounds how many items one batch tool may act on.
	MaxBatchItems int
	// MaxDownloadBytes bounds a file inlined into an MCP result.
	MaxDownloadBytes int64
}

// resolved returns a copy of b with every zero field replaced by its default. The
// receiver is not modified.
func (b Bounds) resolved() Bounds {
	out := Bounds{
		MaxWindowActivities: DefaultMaxWindowActivities,
		MaxDevices:          DefaultMaxDevices,
		MaxSplits:           DefaultMaxSplits,
		MaxExerciseSets:     DefaultMaxExerciseSets,
		MaxWorkouts:         DefaultMaxWorkouts,
		MaxPersonalRecords:  DefaultMaxPersonalRecords,
		MaxBatchItems:       DefaultMaxBatchItems,
		MaxDownloadBytes:    DefaultMaxDownloadBytes,
	}
	pick(&out.MaxWindowActivities, b.MaxWindowActivities)
	pick(&out.MaxDevices, b.MaxDevices)
	pick(&out.MaxSplits, b.MaxSplits)
	pick(&out.MaxExerciseSets, b.MaxExerciseSets)
	pick(&out.MaxWorkouts, b.MaxWorkouts)
	pick(&out.MaxPersonalRecords, b.MaxPersonalRecords)
	pick(&out.MaxBatchItems, b.MaxBatchItems)
	pick(&out.MaxDownloadBytes, b.MaxDownloadBytes)
	return out
}

// pick replaces target with candidate when candidate is a positive override.
func pick[T int | int64](target *T, candidate T) {
	if candidate > 0 {
		*target = candidate
	}
}

// Deps is the injected dependency set. Both clients are required: a tool that could
// not name whose tokens it uses would be reaching Garmin as somebody.
type Deps struct {
	// Client is the authenticated request layer every domain client is built on.
	Client *client.Client

	// Caller performs one authenticated request for one principal. In production
	// this is *auth.Refresher, which owns the token lifecycle.
	Caller client.Caller

	// Bounds are the per-tool result bounds. The zero value means the defaults.
	Bounds Bounds

	// ExerciseCatalog is the strength catalog this process serves and validates
	// against. It is loaded once, before the server starts, and is immutable
	// afterwards, so every concurrent tool call reads the same snapshot. A nil
	// catalog selects the compiled-in subset, which is what every test that does
	// not exercise the fetch uses.
	ExerciseCatalog *api.ExerciseCatalog
}

func (d Deps) validate() error {
	if d.Client == nil {
		return fmt.Errorf("no request layer: %w", ErrMissingDependency)
	}
	if d.Caller == nil {
		return fmt.Errorf("no authenticated caller: %w", ErrMissingDependency)
	}
	return nil
}

// service is the resolved dependency set every handler closes over. It is immutable
// after construction and safe for concurrent use: it holds no per-request state, and
// the per-principal session is built fresh from the request context on every call.
type service struct {
	caller     client.Caller
	bounds     Bounds
	limits     client.Limits
	catalog    *api.ExerciseCatalog
	profile    *api.Profile
	activities *api.Activities
	wellness   *api.Wellness
	devices    *api.Devices
	details    *api.ActivityDetails
	writes     *api.ActivityWrites
	gear       *api.Gear
	workouts   *api.Workouts
	calendar   *api.Calendar
	strength   *api.StrengthWrites
	files      *api.ActivityFiles
}

func newService(deps Deps) (*service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}

	built := &service{
		caller:  deps.Caller,
		bounds:  deps.Bounds.resolved(),
		limits:  deps.Client.Limits(),
		catalog: deps.ExerciseCatalog,
	}
	if err := built.buildClients(deps.Client); err != nil {
		return nil, err
	}
	return built, nil
}

// buildClients constructs the domain clients this slice reads and writes through.
func (s *service) buildClients(rc *client.Client) error {
	if err := s.buildReadClients(rc); err != nil {
		return err
	}
	return s.buildWriteClients(rc)
}

func (s *service) buildReadClients(rc *client.Client) error {
	var err error
	if s.profile, err = api.NewProfile(rc); err != nil {
		return fmt.Errorf("building the profile client: %w", err)
	}
	if s.activities, err = api.NewActivities(rc); err != nil {
		return fmt.Errorf("building the activity client: %w", err)
	}
	if s.wellness, err = api.NewWellness(rc); err != nil {
		return fmt.Errorf("building the wellness client: %w", err)
	}
	if s.devices, err = api.NewDevices(rc); err != nil {
		return fmt.Errorf("building the device client: %w", err)
	}
	if s.details, err = api.NewActivityDetails(rc); err != nil {
		return fmt.Errorf("building the activity-detail client: %w", err)
	}
	if s.files, err = api.NewActivityFiles(rc); err != nil {
		return fmt.Errorf("building the download client: %w", err)
	}
	if s.calendar, err = api.NewCalendar(rc); err != nil {
		return fmt.Errorf("building the calendar client: %w", err)
	}
	return nil
}

func (s *service) buildWriteClients(rc *client.Client) error {
	var err error
	if s.writes, err = api.NewActivityWrites(rc); err != nil {
		return fmt.Errorf("building the activity write client: %w", err)
	}
	if s.gear, err = api.NewGear(rc); err != nil {
		return fmt.Errorf("building the gear client: %w", err)
	}
	if s.workouts, err = api.NewWorkouts(rc); err != nil {
		return fmt.Errorf("building the workout client: %w", err)
	}
	if s.strength, err = api.NewStrengthWrites(rc, s.catalog); err != nil {
		return fmt.Errorf("building the strength write client: %w", err)
	}
	return nil
}

// session binds the request's principal to the caller that may act for it.
//
// The principal comes from the context and from nowhere else. There is no argument,
// header or default that could name a different account.
func (s *service) session(ctx context.Context) (client.Session, error) {
	principal, err := identity.FromContext(ctx)
	if err != nil {
		return client.Session{}, fail(err)
	}
	session, err := client.NewSession(s.caller, principal.ID())
	if err != nil {
		return client.Session{}, fail(err)
	}
	return session, nil
}

// dailyRead is everything a date-keyed wellness read needs: the validated day, the
// session, and the display name Garmin wants in the path.
type dailyRead struct {
	date    client.Date
	session client.Session
	name    client.DisplayName
}

// resolveDailyRead validates the date argument first, so a malformed date costs no
// Garmin call at all, and only then resolves the session and the display name.
func (s *service) resolveDailyRead(ctx context.Context, date string) (dailyRead, error) {
	day, err := parseCalendarDate("date", date)
	if err != nil {
		return dailyRead{}, err
	}
	session, err := s.session(ctx)
	if err != nil {
		return dailyRead{}, err
	}
	name, err := s.displayName(ctx, session)
	if err != nil {
		return dailyRead{}, err
	}
	return dailyRead{date: day, session: session, name: name}, nil
}

// displayName resolves the account's own display name, which the date-keyed wellness
// paths take as a path segment. It is read from the profile on every call: caching it
// would mean caching profile data.
func (s *service) displayName(
	ctx context.Context, session client.Session,
) (client.DisplayName, error) {
	name, err := s.profile.DisplayName(ctx, session)
	if err != nil {
		return client.DisplayName{}, fail(err)
	}
	return name, nil
}
