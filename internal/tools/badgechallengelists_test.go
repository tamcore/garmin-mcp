package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// challengeWindowEnd is a synthetic end-of-window date shared by several
// challenge/badge and blood-pressure fixtures in this package.
const challengeWindowEnd = "2026-02-01"

// keyProgress and keyTarget are the badge/challenge progress keys several
// assertions in this package check for presence or absence of.
const (
	keyProgress = "progress"
	keyTarget   = "target"
)

// challengeNameSooner and challengeNameLater name the two synthetic challenges the
// sort-order tests in this file share, one starting or ending sooner than the
// other.
const (
	challengeNameSooner = "Sooner"
	challengeNameLater  = "Later"
)

// challengesHarness drives the challenges/badges/goals tools ahead of their wiring
// into register.go's readOnlyRegistrations(): a minimal in-package registrar calls
// only the register<Name> functions a test names, over the same fake-Garmin harness
// plumbing harness_internal_test.go already provides (harnessCaller, harnessPrincipal,
// connectHarness). This lets the whole suite in this file exercise the real
// middleware chain — policy, the MCP session, structured content — without editing
// register.go, which is out of scope for this slice.

// challengesRegistrar registers exactly the register funcs it is given, against one
// shared service.
type challengesRegistrar struct {
	svc       *service
	registers []func(*mcpserver.Registry, *service) error
}

func (r challengesRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	for _, register := range r.registers {
		if err := register(registry, r.svc); err != nil {
			return err
		}
	}
	return nil
}

// newChallengesHarness builds a harness carrying only the named tools, each
// permitted read-only by the policy this harness builds.
func newChallengesHarness(
	t *testing.T, script testkit.Script, toolNames []string,
	registers ...func(*mcpserver.Registry, *service) error,
) toolHarness {
	t.Helper()
	return newChallengesHarnessWith(t, script, client.Limits{}, toolNames, registers...)
}

func newChallengesHarnessWith(
	t *testing.T, script testkit.Script, limits client.Limits, toolNames []string,
	registers ...func(*mcpserver.Registry, *service) error,
) toolHarness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	rc, err := client.New(client.Config{
		Hosts:   fake.Hosts(protocol.DomainGlobal),
		Limits:  limits,
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	svc, err := newService(Deps{Client: rc, Caller: harnessCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{harnessPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: append([]string{mcpserver.ServerInfoToolName}, toolNames...),
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-challenges-test", Version: harnessVersion},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{
			challengesRegistrar{svc: svc, registers: registers},
		},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return toolHarness{fake: fake, session: connectHarness(t, server)}
}

// badgeChallengeDocument is one badge challenge with every curated field present.
// Every value is invented.
const badgeChallengeDocument = `[{"uuid":"chal-0001","badgeChallengeName":"January 5K",` +
	`"badgePoints":150,"challengeCategoryId":1,"badgeChallengeStatusId":2,` +
	`"badgeUnitId":1,"badgeProgressValue":3200,"badgeTargetValue":5000,` +
	`"startDate":"2026-01-01T00:00:00.0","endDate":"2026-01-31T23:59:59.0",` +
	`"userJoined":true,"joinable":false}]`

// badgeChallengeEarnedDocument is one completed badge challenge, carrying an earned
// date and no positive target.
const badgeChallengeEarnedDocument = `[{"uuid":"chal-0002","badgeChallengeName":"Winter Steps",` +
	`"challengeCategoryId":4,"badgeChallengeStatusId":3,"badgeEarnedDate":"2026-02-01T09:00:00.0",` +
	`"userJoined":true}]`

func TestGetAvailableBadgeChallengesCuratesTheChallenge(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAvailableBadgeChallenges,
		testkit.JSON(http.StatusOK, badgeChallengeDocument))
	h := newChallengesHarness(t, script, []string{ToolGetAvailableBadgeChallenges},
		registerGetAvailableBadgeChallenges)

	result := h.call(t, ToolGetAvailableBadgeChallenges, nil)
	challenges := list(t, result, "challenges")
	if len(challenges) != 1 {
		t.Fatalf("challenges holds %d entries, want 1", len(challenges))
	}
	challenge := entry(t, challenges, 0)

	if got, _ := challenge["name"].(string); got != "January 5K" {
		t.Errorf("name = %q, want %q", got, "January 5K")
	}
	if got, _ := challenge["category"].(string); got != challengeLabelRunning {
		t.Errorf("category = %q, want Running for category id 1", got)
	}
	if got, _ := challenge["status"].(string); got != "In Progress" {
		t.Errorf("status = %q, want In Progress for status id 2", got)
	}
	if got, _ := challenge["progress"].(string); got != "3.20 km" {
		t.Errorf("progress = %q, want 3.20 km", got)
	}
	if got, _ := challenge["target"].(string); got != "5.00 km" {
		t.Errorf("target = %q, want 5.00 km", got)
	}
	if got, _ := challenge["progress_percent"].(string); got != "64.0%" {
		t.Errorf("progress_percent = %q, want 64.0%%", got)
	}
	if got, ok := challenge["joinable"].(bool); !ok || got {
		t.Errorf("joinable = %v, want false", challenge["joinable"])
	}
}

// TestGetAvailableBadgeChallengesReportsAnAbsentProgressAsAbsent proves a target
// present with no matching progress value renders progress and progress_percent as
// absent fields rather than a fabricated zero, matching challenges.py:182
// (`challenge.get("badgeProgressValue")` can be None) and _format_badge_value's own
// `if value is None: return None` (challenges.py:146-149).
func TestGetAvailableBadgeChallengesReportsAnAbsentProgressAsAbsent(t *testing.T) {
	t.Parallel()

	body := `[{"uuid":"chal-0004","badgeChallengeName":"No Progress Yet",` +
		`"badgeTargetValue":5000,"badgeUnitId":1}]`
	script := testkit.NewScript().With(client.PathAvailableBadgeChallenges,
		testkit.JSON(http.StatusOK, body))
	h := newChallengesHarness(t, script, []string{ToolGetAvailableBadgeChallenges},
		registerGetAvailableBadgeChallenges)

	challenge := entry(t, list(t, h.call(t, ToolGetAvailableBadgeChallenges, nil),
		"challenges"), 0)
	if got, _ := challenge["target"].(string); got != "5.00 km" {
		t.Errorf("target = %q, want 5.00 km", got)
	}
	for _, key := range []string{keyProgress, "progress_percent"} {
		if _, present := challenge[key]; present {
			t.Errorf("%s = %v with no progress value, want the key absent, not a fabricated zero",
				key, challenge[key])
		}
	}
}

// TestGetAvailableBadgeChallengesSortsByStartDateAscending proves the soonest
// challenge to start is returned first, matching challenges.py:435.
func TestGetAvailableBadgeChallengesSortsByStartDateAscending(t *testing.T) {
	t.Parallel()

	body := `[` +
		`{"uuid":"chal-later","badgeChallengeName":"` + challengeNameLater + `","startDate":"2026-03-01T00:00:00.0"},` +
		`{"uuid":"chal-sooner","badgeChallengeName":"` + challengeNameSooner + `","startDate":"2026-01-01T00:00:00.0"}` +
		`]`
	script := testkit.NewScript().With(client.PathAvailableBadgeChallenges,
		testkit.JSON(http.StatusOK, body))
	h := newChallengesHarness(t, script, []string{ToolGetAvailableBadgeChallenges},
		registerGetAvailableBadgeChallenges)

	challenges := list(t, h.call(t, ToolGetAvailableBadgeChallenges, nil), "challenges")
	if got, _ := entry(t, challenges, 0)["name"].(string); got != challengeNameSooner {
		t.Errorf("the first entry is %q, want the soonest-starting challenge first", got)
	}
	if got, _ := entry(t, challenges, 1)["name"].(string); got != challengeNameLater {
		t.Errorf("the second entry is %q, want the later-starting challenge second", got)
	}
}

// TestGetAvailableBadgeChallengesCapsAtTheRequestedLimit proves a Garmin response
// carrying more challenges than the requested limit is cut to it and reported as
// truncated, since Garmin does not reliably honor the limit it is asked for.
func TestGetAvailableBadgeChallengesCapsAtTheRequestedLimit(t *testing.T) {
	t.Parallel()

	body := `[` +
		`{"uuid":"chal-a","badgeChallengeName":"A","startDate":"2026-01-01T00:00:00.0"},` +
		`{"uuid":"chal-b","badgeChallengeName":"B","startDate":"2026-02-01T00:00:00.0"}` +
		`]`
	script := testkit.NewScript().With(client.PathAvailableBadgeChallenges,
		testkit.JSON(http.StatusOK, body))
	h := newChallengesHarness(t, script, []string{ToolGetAvailableBadgeChallenges},
		registerGetAvailableBadgeChallenges)

	result := h.call(t, ToolGetAvailableBadgeChallenges, map[string]any{argLimit: 1})
	challenges := list(t, result, "challenges")
	if len(challenges) != 1 {
		t.Fatalf("challenges holds %d entries, want 1: the response should be cut to the limit", len(challenges))
	}
	if truncated, _ := result["truncated"].(bool); !truncated {
		t.Error("truncated = false, want true: Garmin returned more than the requested limit")
	}
}

func TestGetBadgeChallengesUsesTheManifestDefaults(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBadgeChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetBadgeChallenges}, registerGetBadgeChallenges)

	h.call(t, ToolGetBadgeChallenges, nil)

	requests := h.fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("the fake received %d requests, want one", len(requests))
	}
	if got := requests[0].Query.Get(client.QueryStart); got != "1" {
		t.Errorf("start = %q, want the manifest default 1", got)
	}
	if got := requests[0].Query.Get(client.QueryLimit); got != "20" {
		t.Errorf("limit = %q, want the manifest default 20", got)
	}
}

// TestGetBadgeChallengesReportsAnEarnedChallengeWithNoProgress proves an earned
// challenge with no positive target carries an earned_date and no progress fields,
// matching challenges.py:197-207: progress is only added when target is set and
// positive, independent of earned_date.
func TestGetBadgeChallengesReportsAnEarnedChallengeWithNoProgress(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathBadgeChallenges,
		testkit.JSON(http.StatusOK, badgeChallengeEarnedDocument))
	h := newChallengesHarness(t, script, []string{ToolGetBadgeChallenges}, registerGetBadgeChallenges)

	challenge := entry(t, list(t, h.call(t, ToolGetBadgeChallenges, nil), "challenges"), 0)
	if got, _ := challenge["status"].(string); got != "Completed" {
		t.Errorf("status = %q, want Completed for status id 3", got)
	}
	if got, _ := challenge["earned_date"].(string); got != challengeWindowEnd {
		t.Errorf("earned_date = %q, want 2026-02-01", got)
	}
	if got, _ := challenge["category"].(string); got != challengeLabelSteps {
		t.Errorf("category = %q, want Steps for category id 4", got)
	}
	for _, key := range []string{keyProgress, keyTarget, "progress_percent"} {
		if _, present := challenge[key]; present {
			t.Errorf("%s = %v for a challenge with no positive target, want the key absent",
				key, challenge[key])
		}
	}
}

// TestGetBadgeChallengesSortsByStartDateDescending proves the most recently started
// joined challenge is returned first, matching challenges.py:466-468.
func TestGetBadgeChallengesSortsByStartDateDescending(t *testing.T) {
	t.Parallel()

	body := `[` +
		`{"uuid":"chal-earlier","badgeChallengeName":"Earlier","startDate":"2026-01-01T00:00:00.0"},` +
		`{"uuid":"chal-later","badgeChallengeName":"` + challengeNameLater + `","startDate":"2026-03-01T00:00:00.0"}` +
		`]`
	script := testkit.NewScript().With(client.PathBadgeChallenges,
		testkit.JSON(http.StatusOK, body))
	h := newChallengesHarness(t, script, []string{ToolGetBadgeChallenges}, registerGetBadgeChallenges)

	challenges := list(t, h.call(t, ToolGetBadgeChallenges, nil), "challenges")
	if got, _ := entry(t, challenges, 0)["name"].(string); got != challengeNameLater {
		t.Errorf("the first entry is %q, want the most-recently-started challenge first", got)
	}
	if got, _ := entry(t, challenges, 1)["name"].(string); got != "Earlier" {
		t.Errorf("the second entry is %q, want the earlier-started challenge second", got)
	}
}

// TestGetNonCompletedBadgeChallengesSortsByEndDateAscending proves the challenge
// ending soonest is returned first, matching challenges.py:503.
func TestGetNonCompletedBadgeChallengesSortsByEndDateAscending(t *testing.T) {
	t.Parallel()

	body := `[` +
		`{"uuid":"chal-later","badgeChallengeName":"` + challengeNameLater + `","endDate":"2026-06-01T00:00:00.0"},` +
		`{"uuid":"chal-sooner","badgeChallengeName":"` + challengeNameSooner + `","endDate":"2026-02-01T00:00:00.0"}` +
		`]`
	script := testkit.NewScript().With(client.PathNonCompletedBadgeChallenges,
		testkit.JSON(http.StatusOK, body))
	h := newChallengesHarness(t, script, []string{ToolGetNonCompletedBadgeChallenges},
		registerGetNonCompletedBadgeChallenges)

	challenges := list(t, h.call(t, ToolGetNonCompletedBadgeChallenges, nil), "challenges")
	if got, _ := entry(t, challenges, 0)["name"].(string); got != challengeNameSooner {
		t.Errorf("the first entry is %q, want the soonest-ending challenge first", got)
	}
	if got, _ := entry(t, challenges, 1)["name"].(string); got != challengeNameLater {
		t.Errorf("the second entry is %q, want the later-ending challenge second", got)
	}
}

func TestGetNonCompletedBadgeChallengesReportsAnUnrecognizedCategoryAsACode(t *testing.T) {
	t.Parallel()

	body := `[{"uuid":"chal-0003","badgeChallengeName":"Mystery Challenge",` +
		`"challengeCategoryId":42,"badgeChallengeStatusId":99}]`
	script := testkit.NewScript().With(client.PathNonCompletedBadgeChallenges,
		testkit.JSON(http.StatusOK, body))
	h := newChallengesHarness(t, script, []string{ToolGetNonCompletedBadgeChallenges},
		registerGetNonCompletedBadgeChallenges)

	challenge := entry(t, list(t, h.call(t, ToolGetNonCompletedBadgeChallenges, nil),
		"challenges"), 0)
	if got, _ := challenge["category"].(string); got != "category_42" {
		t.Errorf("category = %q, want the code fallback category_42", got)
	}
	if got, _ := challenge["status"].(string); got != "status_99" {
		t.Errorf("status = %q, want the code fallback status_99", got)
	}
}

func TestChallengePageRejectsALimitAboveTheBound(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathAvailableBadgeChallenges,
		testkit.JSON(http.StatusOK, `[]`))
	h := newChallengesHarness(t, script, []string{ToolGetAvailableBadgeChallenges},
		registerGetAvailableBadgeChallenges)

	advice := h.callError(t, ToolGetAvailableBadgeChallenges,
		map[string]any{argLimit: 101})
	assertNoRawPayload(t, advice)

	if len(h.fake.Requests()) != 0 {
		t.Error("an out-of-range limit reached the fake Garmin service")
	}
}

func TestBadgeChallengeListLogValueReportsShapeOnly(t *testing.T) {
	t.Parallel()

	name := "January 5K"
	progress := "64.0%"
	value := BadgeChallengeList{Challenges: []BadgeChallenge{
		{Name: &name, ProgressPercent: &progress},
	}}.LogValue().String()

	if strings.Contains(value, "January 5K") {
		t.Errorf("the log value %q carries the name", value)
	}
	if !strings.Contains(value, "badgeChallengeList") {
		t.Errorf("the log value %q does not name the model", value)
	}
}
