package oauthserver

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTransactionStageRendersAndValidates(t *testing.T) {
	for stage, want := range map[TransactionStage]string{
		StagePending:        "pending",
		StageAuthenticated:  "authenticated",
		TransactionStage(0): "unknown",
	} {
		if got := stage.String(); got != want {
			t.Fatalf("String() = %q, want %q", got, want)
		}
	}
	if !StagePending.IsValid() || !StageAuthenticated.IsValid() {
		t.Fatal("a defined stage reported invalid")
	}
	if TransactionStage(0).IsValid() || TransactionStage(99).IsValid() {
		t.Fatal("an undefined stage reported valid")
	}
}

func TestNewFamilyIDIsUniqueAndHighEntropy(t *testing.T) {
	seen := make(map[FamilyID]struct{}, 32)
	for range 32 {
		family, err := NewFamilyID()
		if err != nil {
			t.Fatalf("NewFamilyID: %v", err)
		}
		if len(family) < 43 {
			t.Fatalf("family id is %d characters, want a 256-bit value", len(family))
		}
		if _, dup := seen[family]; dup {
			t.Fatal("NewFamilyID returned a duplicate")
		}
		seen[family] = struct{}{}
	}
}

// testRecords builds one of each record from the same fixture material, so a single leak
// check can cover all of them.
func testRecords(t *testing.T) (map[string]fmt.Stringer, []string) {
	t.Helper()
	code, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	principal := mustPrincipal(t, testPrincipalID)
	scopes := mustScopeSet(t, testScopeProfile)
	redirect := mustRedirectURI(t, testRedirect)
	resource := mustResource(t, testResourceURI)
	challenge := mustChallenge(t)
	state := mustClientState(t)
	key := ConsentKey{
		Principal: principal, ClientID: testClientID, RedirectURI: redirect, Resource: resource,
	}

	records := map[string]fmt.Stringer{
		"Transaction": Transaction{
			Lookup: code.Lookup(), ClientID: testClientID, RedirectURI: redirect,
			Scopes: scopes, Resource: resource, Challenge: challenge, State: state,
			Stage: StageAuthenticated, Principal: principal,
			CreatedAt: testNow, ExpiresAt: testNow.Add(time.Minute),
		},
		"AuthorizationCode": AuthorizationCode{
			Lookup: code.Lookup(), ClientID: testClientID, RedirectURI: redirect,
			Scopes: scopes, Resource: resource, Challenge: challenge, Principal: principal,
			IssuedAt: testNow, ExpiresAt: testNow.Add(time.Minute),
		},
		"AccessToken": AccessToken{
			Lookup: code.Lookup(), ClientID: testClientID, Principal: principal,
			Scopes: scopes, Resource: resource, Family: "family-one",
			IssuedAt: testNow, ExpiresAt: testNow.Add(time.Minute),
		},
		"RefreshToken": RefreshToken{
			Lookup: code.Lookup(), ClientID: testClientID, Principal: principal,
			Scopes: scopes, Resource: resource, Family: "family-one", Generation: 4,
			IssuedAt: testNow, ExpiresAt: testNow.Add(time.Minute),
		},
		"Consent": Consent{Key: key, Scopes: scopes, GrantedAt: testNow},
	}
	forbidden := []string{
		code.Reveal(), code.Lookup().Hex(), testState, challenge.Value(), testVerifier,
	}
	return records, forbidden
}

// TestRecordRenderingsCarryNoCredentialMaterial is the reason the records may have
// exported fields: every field that matters is itself a redacting type, so printing a
// whole record cannot reveal a state, a challenge, a code or a token.
func TestRecordRenderingsCarryNoCredentialMaterial(t *testing.T) {
	records, forbidden := testRecords(t)

	for name, record := range records {
		t.Run(name, func(t *testing.T) {
			renderings := map[string]string{
				labelString:    record.String(),
				labelFmtV:      fmt.Sprintf("%v", record),
				labelFmtS:      fmt.Sprintf("as-%s", record),
				labelFmtPlusV:  fmt.Sprintf("%+v", record),
				labelFmtSharpV: fmt.Sprintf("%#v", record),
				"in a list":    fmt.Sprintf("%v", []fmt.Stringer{record}),
			}
			for label, rendered := range renderings {
				for _, secret := range forbidden {
					if secret != "" && strings.Contains(rendered, secret) {
						t.Fatalf("%s leaked credential material: %q", label, rendered)
					}
				}
				if !strings.Contains(rendered, testClientID) {
					t.Fatalf("%s should name the client: %q", label, rendered)
				}
			}
		})
	}
}

func TestRecordsReportAnUnresolvedPrincipal(t *testing.T) {
	tx := Transaction{ClientID: testClientID, Stage: StagePending, ExpiresAt: testNow}

	if !strings.Contains(tx.String(), "unresolved") {
		t.Fatalf("String() = %q, want it to report an unresolved principal", tx.String())
	}
	if tx.GoString() != tx.String() {
		t.Fatal("GoString must render like String")
	}
}

func TestRecordExpiryIsExclusiveOfTheExpiryInstant(t *testing.T) {
	expiry := testNow.Add(time.Minute)
	tx := Transaction{ExpiresAt: expiry}
	code := AuthorizationCode{ExpiresAt: expiry}
	access := AccessToken{ExpiresAt: expiry}
	refresh := RefreshToken{ExpiresAt: expiry}

	for name, expired := range map[string]func(time.Time) bool{
		"Transaction":       tx.IsExpired,
		"AuthorizationCode": code.IsExpired,
		"AccessToken":       access.IsExpired,
		"RefreshToken":      refresh.IsExpired,
	} {
		t.Run(name, func(t *testing.T) {
			if expired(expiry.Add(-time.Nanosecond)) {
				t.Fatal("a record expired before its expiry")
			}
			if !expired(expiry) {
				t.Fatal("a record was still live at its expiry instant")
			}
			if !expired(expiry.Add(time.Nanosecond)) {
				t.Fatal("a record was still live after its expiry")
			}
		})
	}
}

func TestConsentCoversOnlyWhatItGranted(t *testing.T) {
	consent := Consent{Scopes: mustScopeSet(t, testScopesBoth)}

	for name, tc := range map[string]struct {
		requested string
		want      bool
	}{
		"the same scopes": {testScopesBoth, true},
		"a narrower set":  {testScopeProfile, true},
		"the empty set":   {"", true},
		"another scope":   {"garmin.devices.read", false},
		"a wider set":     {testScopesBoth + " garmin.devices.read", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := consent.Covers(mustScopeSet(t, tc.requested)); got != tc.want {
				t.Fatalf("Covers(%q) = %v, want %v", tc.requested, got, tc.want)
			}
		})
	}
}

func TestClientResourcesIsACopy(t *testing.T) {
	client := mustClient(t, publicClientSpec())

	resources := client.Resources()
	if len(resources) != 1 {
		t.Fatalf("Resources() = %v", resources)
	}
	resources[0] = Resource{}

	if !client.Resources()[0].Equal(mustResource(t, testResourceURI)) {
		t.Fatal("Resources exposed the internal backing array")
	}
}
