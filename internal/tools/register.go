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
		{getSleepDataContract, registerGetSleepData},
		{getUserSummaryContract, registerGetUserSummary},
		{getDevicesContract, registerGetDevices},
		{getActivityTypedSplitsContract, registerGetActivityTypedSplits},
		{getActivityExerciseSetsContract, registerGetActivityExerciseSets},
	}
}

// Contracts returns every registered tool's declared contract, keyed by wire name.
//
// It is the input to the contract test, which compares these schemas with
// compat/tools.json. The returned map is a fresh copy.
func Contracts() map[string]Contract {
	registrations := readOnlyRegistrations()
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
	registrations := readOnlyRegistrations()
	names := make([]string, 0, len(registrations)+1)
	names = append(names, mcpserver.ServerInfoToolName)
	for _, entry := range registrations {
		names = append(names, entry.contract().Spec.Name)
	}
	return names
}

// WriteTools names every tool in the write tier.
//
// It is empty today because this slice registers no write tool. The list exists
// anyway, and is validated against the registered set at start-up, so the first write
// tool cannot be added with a typo that silently never matches.
func WriteTools() []string { return nil }

// DestructiveTools names every tool in the destructive tier. It is empty today, for
// the same reason WriteTools is.
func DestructiveTools() []string { return nil }

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
// The order is read-only, then write, then destructive. Only the first tier has
// members today; the other two are still named in the wiring, so the shape does not
// change when the first write tool arrives.
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
	for _, entry := range readOnlyRegistrations() {
		if err := entry.register(registry, svc); err != nil {
			return fmt.Errorf("registering a read-only tool: %w", err)
		}
	}
	// The write and destructive tiers register nothing yet.
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
