package auth

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The synthetic secret-bearing content a Result can carry.
const (
	leakGarminAccount = "900001"
	leakGarminName    = "Fake User"
	leakCapability    = "transaction-capability-0001"
)

// strippedResult is the method-stripping alias. A defined type with the same
// underlying type has none of the redacting methods, so fmt falls back to
// reflection and dereferences the sealed content. Nothing readable may come out.
//
// It lives in the internal test package because a Result carrying a confirmed
// Garmin account can only be produced inside this package; the path fmt takes is
// identical either way.
type strippedResult Result

func resultWithAccount() Result {
	return authenticatedResult(StrategyMobileIOS, garminAccount{
		accountID:   leakGarminAccount,
		displayName: leakGarminName,
	})
}

// strippedForms renders a value under every verb a stripped alias can reach. The
// value is passed as any, exactly as the external alias test does: %s and %q on a
// concrete non-stringer would be a vet finding, and the point is the bad-verb path
// fmt takes at run time, which is identical either way.
func strippedForms(value any) map[string]string {
	return map[string]string{
		"%v":  fmt.Sprintf("%v", value),
		"%+v": fmt.Sprintf("%+v", value),
		"%#v": fmt.Sprintf("%#v", value),
		"%s":  fmt.Sprintf("%s", value),
		"%q":  fmt.Sprintf("%q", value),
	}
}

func TestStrippedResultLeaksNothingUnderAnyVerb(t *testing.T) {
	stripped := strippedResult(resultWithAccount())

	for verb, rendered := range strippedForms(stripped) {
		for _, bad := range []string{leakGarminAccount, leakGarminName} {
			if strings.Contains(rendered, bad) {
				t.Errorf("stripped Result %s rendering %q leaked %q", verb, rendered, bad)
			}
		}
	}
}

// The redacted JSON encoding is what a structured log records, so it is asserted
// separately from the text renderings: an account that survived only there would
// reach exactly the place that keeps its output.
func TestResultJSONReportsPresenceAndNotTheAccount(t *testing.T) {
	encoded, err := json.Marshal(resultWithAccount())
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	rendered := string(encoded)
	for _, bad := range []string{leakGarminAccount, leakGarminName} {
		if strings.Contains(rendered, bad) {
			t.Errorf("the JSON rendering %s leaked %q", rendered, bad)
		}
	}
	if !strings.Contains(rendered, `"garminAccountPresent":true`) {
		t.Errorf("the JSON rendering %s does not report that an account is present", rendered)
	}
}

// A pending result carries a capability and no account, and an authenticated one
// carries an account and no capability. Both facts are reported as presence, so a
// log stays useful without the material.
func TestResultReportsTheShapeOfEachOutcome(t *testing.T) {
	pending := mfaPendingResult(StrategyMobileIOS, leakCapability, Pending{})
	if pending.GarminAccountID() != "" {
		t.Error("a pending result reported a garmin account")
	}
	if rendered := pending.String(); !strings.Contains(rendered, "garminAccount:absent") {
		t.Errorf("rendering %q does not report the absent account", rendered)
	}

	authenticated := resultWithAccount()
	if authenticated.TransactionID() != "" {
		t.Error("an authenticated result still carries a transaction capability")
	}
	if authenticated.GarminAccountID() != leakGarminAccount {
		t.Errorf("GarminAccountID() = %q, want %q",
			authenticated.GarminAccountID(), leakGarminAccount)
	}
	if authenticated.GarminDisplayName() != leakGarminName {
		t.Errorf("GarminDisplayName() = %q, want %q",
			authenticated.GarminDisplayName(), leakGarminName)
	}
}
