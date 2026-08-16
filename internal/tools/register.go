package tools

import (
	"fmt"
	"slices"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// Log categories. Each is the manifest's sensitivity label for the tools that carry
// it, so a log line names the domain a call touched without naming the call.
const (
	categoryProfile  = "profile"
	categoryHealth   = "health"
	categoryLocation = "location"
	categoryDevice   = "device"
	categoryOrdinary = "ordinary"
)

// noArguments is the input type of every tool that takes no argument.
//
// It is also where the principal rule is visible: there is no field through which a
// caller could name a user, an email address or a token path, and adding one would
// not help, because the principal comes from the request context.
type noArguments struct{}

// readOnlyAnnotations declares all four MCP hints for the read-only tier.
//
// All four are declared explicitly, in one place, because a wrong hint is not
// cosmetic: a client decides whether to prompt its user based on it. OpenWorld is
// true for every tool here — Garmin is an unofficial, undocumented external API whose
// responses this server does not control.
func readOnlyAnnotations() mcpserver.Annotations {
	return mcpserver.Annotations{
		ReadOnly:    true,
		Destructive: false,
		Idempotent:  true,
		OpenWorld:   true,
	}
}

// writeAnnotations declares all four MCP hints for the write tier.
//
// idempotent is passed explicitly rather than defaulted, because it is the one hint
// that differs between two writes of the same tier: an absolute-value PUT converges
// and a create does not. It is the manifest's idempotency classification, which for
// the scheduling tools is "non-idempotent" even though upstream's own description
// claims otherwise — that pre-check fails open, so it is duplicate avoidance and not
// a guarantee, and this server does not repeat the claim.
func writeAnnotations(idempotent bool) mcpserver.Annotations {
	return mcpserver.Annotations{
		ReadOnly:    false,
		Destructive: false,
		Idempotent:  idempotent,
		OpenWorld:   true,
	}
}

// destructiveAnnotations declares all four MCP hints for the destructive tier.
//
// Destructive is true, which is what puts the call through the server's confirmation
// middleware: a tool that failed to declare it would be executed without ever asking.
// Idempotent is true for every destructive tool on this surface, because every one of
// them is a removal and a second removal of the same record converges on the same end
// state; the hint is still written out rather than left to a default.
func destructiveAnnotations() mcpserver.Annotations {
	return mcpserver.Annotations{
		ReadOnly:    false,
		Destructive: true,
		Idempotent:  true,
		OpenWorld:   true,
	}
}

// A registration is one tool's registration function, paired with the contract that
// describes it. The pair is what keeps the wire and the declaration honest: both come
// from the same tool file.
type registration struct {
	contract func() Contract
	register func(*mcpserver.Registry, *service) error
}

// readOnlyRegistrations lists the read-only tools in registration order, grouped by
// domain. Read-only tools always register: no operator switch and no scope gates them.
func readOnlyRegistrations() []registration {
	return []registration{
		{getUserProfileContract, registerGetUserProfile},
		{getFullNameContract, registerGetFullName},
		{getUnitSystemContract, registerGetUnitSystem},
		{getActivitiesContract, registerGetActivities},
		{getActivitiesByDateContract, registerGetActivitiesByDate},
		{getActivitiesForDateContract, registerGetActivitiesForDate},
		{countActivitiesContract, registerCountActivities},
		{getActivityContract, registerGetActivity},
		{getActivityGearContract, registerGetActivityGear},
		{getActivityTypesContract, registerGetActivityTypes},
		{getSleepDataContract, registerGetSleepData},
		{getUserSummaryContract, registerGetUserSummary},

		// Health and wellness: the daily summaries.
		{getStatsContract, registerGetStats},
		{getStatsAndBodyContract, registerGetStatsAndBody},
		{getBodyCompositionContract, registerGetBodyComposition},
		{getStepsDataContract, registerGetStepsData},
		{getDailyStepsContract, registerGetDailySteps},
		{getWeeklyStepsContract, registerGetWeeklySteps},
		{getFloorsContract, registerGetFloors},
		{getWeeklyIntensityMinutesContract, registerGetWeeklyIntensityMinutes},

		// Health and wellness: stress, body battery and readiness.
		{getStressDataContract, registerGetStressData},
		{getStressSummaryContract, registerGetStressSummary},
		{getAllDayStressContract, registerGetAllDayStress},
		{getWeeklyStressContract, registerGetWeeklyStress},
		{getBodyBatteryContract, registerGetBodyBattery},
		{getBodyBatteryEventsContract, registerGetBodyBatteryEvents},
		{getTrainingReadinessContract, registerGetTrainingReadiness},
		{getMorningTrainingReadinessContract, registerGetMorningTrainingReadiness},
		{getAllDayEventsContract, registerGetAllDayEvents},

		// Health and wellness: cardio, respiration and the logged reads.
		{getHeartRatesContract, registerGetHeartRates},
		{getHeartRatesSummaryContract, registerGetHeartRatesSummary},
		{getRestingHeartRateDayContract, registerGetRestingHeartRateDay},
		{getRespirationDataContract, registerGetRespirationData},
		{getRespirationSummaryContract, registerGetRespirationSummary},
		{getSpO2DataContract, registerGetSpO2Data},
		{getSleepSummaryContract, registerGetSleepSummary},
		{getBloodPressureContract, registerGetBloodPressure},
		{getHydrationDataContract, registerGetHydrationData},
		{getLifestyleLoggingDataContract, registerGetLifestyleLoggingData},
		{getDevicesContract, registerGetDevices},
		{getActivityTypedSplitsContract, registerGetActivityTypedSplits},
		{getActivityExerciseSetsContract, registerGetActivityExerciseSets},
		{getUserProfileSettingsContract, registerGetUserProfileSettings},
		{getPersonalRecordContract, registerGetPersonalRecord},
		{getActivitySplitsContract, registerGetActivitySplits},
		{getActivitySplitSummariesContract, registerGetActivitySplitSummaries},
		{getActivityHRInZonesContract, registerGetActivityHRInZones},
		{getActivityPowerInZonesContract, registerGetActivityPowerInZones},
		{getActivityWeatherContract, registerGetActivityWeather},
		{getActivityFITDataContract, registerGetActivityFITData},
		{getPowerDurationCurveContract, registerGetPowerDurationCurve},
		{getExerciseTypesContract, registerGetExerciseTypes},
		{getWorkoutsContract, registerGetWorkouts},
		{getWorkoutByIDContract, registerGetWorkoutByID},
		{downloadWorkoutContract, registerDownloadWorkout},
		{getScheduledWorkoutsContract, registerGetScheduledWorkouts},
		{getTrainingPlanWorkoutsContract, registerGetTrainingPlanWorkouts},
		{getGarminCoachWorkoutsContract, registerGetGarminCoachWorkouts},

		// Training: scores and thresholds.
		{getHillScoreContract, registerGetHillScore},
		{getEnduranceScoreContract, registerGetEnduranceScore},
		{getTrainingEffectContract, registerGetTrainingEffect},
		{getFitnessAgeDataContract, registerGetFitnessAgeData},
		{getTrainingStatusContract, registerGetTrainingStatus},
		{getCyclingFTPContract, registerGetCyclingFTP},
		{getLactateThresholdContract, registerGetLactateThreshold},

		// Training: the day-by-day walks and the aggregated reads.
		{getProgressSummaryBetweenDatesContract, registerGetProgressSummaryBetweenDates},
		{getHRVDataContract, registerGetHRVData},
		{getHRVTrendContract, registerGetHRVTrend},
		{getVO2MaxTrendContract, registerGetVO2MaxTrend},
		{getRespirationTrendContract, registerGetRespirationTrend},
		{getTrainingLoadTrendContract, registerGetTrainingLoadTrend},
		{getTrainingLoadBalanceContract, registerGetTrainingLoadBalance},
	}
}

// writeRegistrations lists the write tools in registration order.
//
// Every one of them needs both operator enablement and a granted write scope, and the
// two come from different places: enablement from configuration, the scope from the
// caller's verified access token, which only the remote transport supplies. A stdio
// deployment therefore refuses all of them, and a default deployment of either shape
// refuses them too, because the tier starts disabled. They are registered regardless,
// so the policy has a tool to refuse and the start-up tier validation covers them.
func writeRegistrations() []registration {
	return []registration{
		// Training: the domain's only write.
		{requestReloadContract, registerRequestReload},

		{setActivityNameContract, registerSetActivityName},
		{setActivityTypeContract, registerSetActivityType},
		{setActivityEventTypeContract, registerSetActivityEventType},
		{setActivityDescriptionContract, registerSetActivityDescription},
		{setActivityFeelContract, registerSetActivityFeel},
		{setPerceivedEffortContract, registerSetPerceivedEffort},
		{addGearToActivityContract, registerAddGearToActivity},
		{removeGearFromActivityContract, registerRemoveGearFromActivity},
		{createManualActivityContract, registerCreateManualActivity},
		{setActivityStrengthExerciseSetsContract, registerSetActivityStrengthExerciseSets},
		{createStrengthTrainingActivityContract, registerCreateStrengthTrainingActivity},
		{uploadWorkoutContract, registerUploadWorkout},
		{uploadWorkoutsContract, registerUploadWorkouts},
		{updateWorkoutContract, registerUpdateWorkout},
		{scheduleWorkoutContract, registerScheduleWorkout},
		{scheduleWorkoutsContract, registerScheduleWorkouts},
		{scheduleWeekContract, registerScheduleWeek},
		{createWalkRunWorkoutContract, registerCreateWalkRunWorkout},
		{createRunWorkoutContract, registerCreateRunWorkout},
		{createZ2WalkWorkoutContract, registerCreateZ2WalkWorkout},
		{createStrengthWorkoutContract, registerCreateStrengthWorkout},
		{downloadActivityFileContract, registerDownloadActivityFile},
	}
}

// destructiveRegistrations lists the destructive tools in registration order.
//
// Each one declares itself destructive, which is what routes it through the server's
// confirmation middleware. That middleware fails closed: a client that cannot be
// asked, a user who declines and a wait that elapses all refuse the call.
func destructiveRegistrations() []registration {
	return []registration{
		{deleteActivityContract, registerDeleteActivity},
		{deleteWorkoutContract, registerDeleteWorkout},
		{deleteWorkoutsContract, registerDeleteWorkouts},
		{unscheduleWorkoutContract, registerUnscheduleWorkout},
		{unscheduleWorkoutsContract, registerUnscheduleWorkouts},
	}
}

// allRegistrations lists every tool this package registers, in tier order.
func allRegistrations() []registration {
	return slices.Concat(readOnlyRegistrations(), writeRegistrations(), destructiveRegistrations())
}

// namesOf renders the wire names of a registration list.
func namesOf(registrations []registration) []string {
	names := make([]string, 0, len(registrations))
	for _, entry := range registrations {
		names = append(names, entry.contract().Spec.Name)
	}
	return names
}

// Contracts returns every registered tool's declared contract, keyed by wire name.
//
// It is the input to the contract test, which compares these schemas with
// compat/tools.json. The returned map is a fresh copy.
func Contracts() map[string]Contract {
	registrations := allRegistrations()
	contracts := make(map[string]Contract, len(registrations))
	for _, entry := range registrations {
		contract := entry.contract()
		contracts[contract.Spec.Name] = contract
	}
	return cloneContracts(contracts)
}

// ReadOnlyTools names every tool in the read-only tier, including the server's own
// built-in server_info tool.
//
// It includes server_info because this package validates the whole registered set
// against these lists, and the server registers that tool itself. The composition
// root adds the same name, so the two lists overlap by exactly one entry and the
// root removes the duplicate.
func ReadOnlyTools() []string {
	return append([]string{mcpserver.ServerInfoToolName}, namesOf(readOnlyRegistrations())...)
}

// WriteTools names every tool in the write tier.
//
// The list is validated against the registered set at start-up in both directions, so
// a name here that is not registered, and a registered write tool missing from here,
// both fail the server before it serves a call.
func WriteTools() []string { return namesOf(writeRegistrations()) }

// DestructiveTools names every tool in the destructive tier.
func DestructiveTools() []string { return namesOf(destructiveRegistrations()) }

// tierLists are the three explicit name lists, injected so the start-up validation
// can be tested with a deliberate typo.
type tierLists struct {
	readOnly    []string
	write       []string
	destructive []string
}

// defaultTierLists returns the real lists.
func defaultTierLists() tierLists {
	return tierLists{
		readOnly:    ReadOnlyTools(),
		write:       WriteTools(),
		destructive: DestructiveTools(),
	}
}

// A Registrar contributes this package's tools to a server. It satisfies
// mcpserver.ToolRegistrar.
type Registrar struct {
	svc   *service
	lists tierLists
}

// New validates deps and returns the registrar for the whole tool surface.
func New(deps Deps) (*Registrar, error) {
	return newWithLists(deps, defaultTierLists())
}

// newWithLists is New with injected tier lists.
func newWithLists(deps Deps, lists tierLists) (*Registrar, error) {
	svc, err := newService(deps)
	if err != nil {
		return nil, err
	}
	return &Registrar{svc: svc, lists: lists}, nil
}

// RegisterTools registers every tool and then validates the tier lists against what
// was actually registered.
func (r *Registrar) RegisterTools(registry *mcpserver.Registry) error {
	if r == nil || r.svc == nil {
		return fmt.Errorf("registrar was not built by New: %w", ErrMissingDependency)
	}
	return registerAll(registry, r.svc, r.lists)
}

// RegisterAll registers the whole tool surface on registry, in explicit tier order.
//
// The order is read-only, then write, then destructive, so a reader of the wiring
// sees the surface grouped by the effect it has on the account.
func RegisterAll(registry *mcpserver.Registry, deps Deps) error {
	registrar, err := New(deps)
	if err != nil {
		return err
	}
	return registrar.RegisterTools(registry)
}

// registerAll does the work, against an injected tier list set.
func registerAll(registry *mcpserver.Registry, svc *service, lists tierLists) error {
	if registry == nil {
		return fmt.Errorf("no registry: %w", ErrMissingDependency)
	}
	tiers := []struct {
		label         string
		registrations []registration
	}{
		{"read-only", readOnlyRegistrations()},
		{"write", writeRegistrations()},
		{"destructive", destructiveRegistrations()},
	}
	for _, tier := range tiers {
		for _, entry := range tier.registrations {
			if err := entry.register(registry, svc); err != nil {
				return fmt.Errorf("registering a %s tool: %w", tier.label, err)
			}
		}
	}
	return validateTierLists(registry.Names(), lists)
}

// validateTierLists checks both directions against the registered set.
//
// A name in a tier list that is not registered is a typo, and it fails at start-up
// rather than at call time. A registered tool that appears in no tier list has no
// policy tier, so the policy would refuse it on every call: that is a wiring mistake
// too, and it fails just as loudly.
func validateTierLists(registered []string, lists tierLists) error {
	declared := slices.Concat(lists.readOnly, lists.write, lists.destructive)

	for _, name := range declared {
		if !slices.Contains(registered, name) {
			return fmt.Errorf("tier list entry %q: %w", name, ErrUnknownTierTool)
		}
	}
	for _, name := range registered {
		if !slices.Contains(declared, name) {
			return fmt.Errorf("registered tool %q: %w", name, ErrUntieredTool)
		}
	}
	return nil
}
