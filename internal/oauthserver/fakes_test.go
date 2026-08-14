package oauthserver

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// testNow is the instant every fake clock in this package starts at.
var testNow = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

// The fixture vocabulary. These are named once so a change to the fixture registration
// cannot leave a stale literal behind in an assertion.
const (
	testScopeProfile   = "garmin.profile.read"
	testScopeHealth    = "garmin.health.read"
	testScopesBoth     = testScopeProfile + " " + testScopeHealth
	testClientName     = "Test MCP Client"
	testRedirectHost   = "client.example"
	testOtherClientID  = "second-registered-client"
	testUnknownClient  = "not-registered"
	testMalformedScope = `bad"scope`
	testOtherResource  = "https://mcp.example/other"
	testIssuerURI      = "https://mcp.example"
	testNoStore        = "no-store"

	// The labels the redaction tests key their rendering maps by. They are shared
	// because every such test checks the same verbs, and a mismatched label would make
	// a failure report point at the wrong rendering.
	labelString    = "String"
	labelGoString  = "GoString"
	labelFmtV      = "fmt %v"
	labelFmtS      = "fmt %s"
	labelFmtPlusV  = "fmt %+v"
	labelFmtSharpV = "fmt %#v"
)

func mustPrincipal(t *testing.T, id string) identity.Principal {
	t.Helper()
	principal, err := identity.NewPrincipal(id)
	if err != nil {
		t.Fatalf("identity.NewPrincipal: %v", err)
	}
	return principal
}

// identityZeroPrincipal is the unresolved principal. identity.Principal cannot be
// built by struct literal outside its own package, so the zero value is the only way
// to express "no principal", and it is what AttachPrincipal must refuse.
func identityZeroPrincipal() identity.Principal { return identity.Principal{} }

type codeEntry struct {
	code AuthorizationCode
	used bool
}

type refreshEntry struct {
	token    RefreshToken
	consumed bool
}

// fakeStore is the in-memory storage this package's tests run against. It is the
// executable specification of the interfaces in store.go: the SQLite
// implementation must make the same promises, in particular the atomicity of
// ConsumeCode and RotateRefreshToken and the version check in UpdateTransaction.
type fakeStore struct {
	mu           sync.Mutex
	clients      map[string]Client
	consents     map[ConsentKey]Consent
	transactions map[Lookup]Transaction
	codes        map[Lookup]codeEntry
	access       map[Lookup]AccessToken
	refresh      map[Lookup]refreshEntry
	families     map[FamilyID]bool

	// failOn injects a storage failure for the named method, so a test can assert
	// that a storage error never becomes a token.
	failOn map[string]error
	// rotations counts successful refresh rotations, for the race test.
	rotations int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		clients:      make(map[string]Client),
		consents:     make(map[ConsentKey]Consent),
		transactions: make(map[Lookup]Transaction),
		codes:        make(map[Lookup]codeEntry),
		access:       make(map[Lookup]AccessToken),
		refresh:      make(map[Lookup]refreshEntry),
		families:     make(map[FamilyID]bool),
		failOn:       make(map[string]error),
	}
}

func (f *fakeStore) fail(method string) error {
	if err, ok := f.failOn[method]; ok {
		return err
	}
	return nil
}

func (f *fakeStore) addClient(client Client) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients[client.ID()] = client
}

func (f *fakeStore) Client(_ context.Context, clientID string) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Client"); err != nil {
		return Client{}, err
	}
	client, ok := f.clients[clientID]
	if !ok {
		return Client{}, fmt.Errorf("no client %q: %w", clientID, ErrUnknownClient)
	}
	return client, nil
}

func (f *fakeStore) CreateTransaction(_ context.Context, tx Transaction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreateTransaction"); err != nil {
		return err
	}
	if _, exists := f.transactions[tx.Lookup]; exists {
		return fmt.Errorf("transaction already exists: %w", ErrTransactionConflict)
	}
	f.transactions[tx.Lookup] = tx
	return nil
}

func (f *fakeStore) Transaction(_ context.Context, lookup Lookup) (Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Transaction"); err != nil {
		return Transaction{}, err
	}
	tx, ok := f.transactions[lookup]
	if !ok {
		return Transaction{}, ErrTransactionNotFound
	}
	return tx, nil
}

func (f *fakeStore) UpdateTransaction(_ context.Context, tx Transaction, expectVersion uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("UpdateTransaction"); err != nil {
		return err
	}
	stored, ok := f.transactions[tx.Lookup]
	if !ok {
		return ErrTransactionNotFound
	}
	if stored.Version != expectVersion {
		return fmt.Errorf("stored version %d, expected %d: %w",
			stored.Version, expectVersion, ErrTransactionConflict)
	}
	tx.Version = expectVersion + 1
	f.transactions[tx.Lookup] = tx
	return nil
}

// ConsumeTransaction is the atomic claim. Exactly one concurrent caller receives the
// record; every other one sees ErrTransactionNotFound.
func (f *fakeStore) ConsumeTransaction(_ context.Context, lookup Lookup) (Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ConsumeTransaction"); err != nil {
		return Transaction{}, err
	}
	tx, ok := f.transactions[lookup]
	if !ok {
		return Transaction{}, ErrTransactionNotFound
	}
	delete(f.transactions, lookup)
	return tx, nil
}

func (f *fakeStore) Consent(_ context.Context, key ConsentKey) (Consent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Consent"); err != nil {
		return Consent{}, err
	}
	consent, ok := f.consents[key]
	if !ok {
		return Consent{}, ErrConsentNotFound
	}
	return consent, nil
}

func (f *fakeStore) SaveConsent(_ context.Context, consent Consent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SaveConsent"); err != nil {
		return err
	}
	f.consents[consent.Key] = consent
	return nil
}

// RevokeConsent deletes the consent and revokes every token family that belongs
// to the same principal, client and resource, in one critical section. That is the
// transactional cascade the interface documents.
func (f *fakeStore) RevokeConsent(_ context.Context, key ConsentKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("RevokeConsent"); err != nil {
		return err
	}
	delete(f.consents, key)
	for _, entry := range f.refresh {
		if entry.token.ClientID == key.ClientID &&
			entry.token.Principal == key.Principal &&
			entry.token.Resource.Equal(key.Resource) {
			f.families[entry.token.Family] = true
		}
	}
	for _, token := range f.access {
		if token.ClientID == key.ClientID &&
			token.Principal == key.Principal &&
			token.Resource.Equal(key.Resource) {
			f.families[token.Family] = true
		}
	}
	return nil
}

func (f *fakeStore) SaveTokenPair(_ context.Context, access AccessToken, refresh RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SaveTokenPair"); err != nil {
		return err
	}
	f.access[access.Lookup] = access
	f.refresh[refresh.Lookup] = refreshEntry{token: refresh}
	return nil
}

func (f *fakeStore) AccessToken(_ context.Context, lookup Lookup) (AccessToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("AccessToken"); err != nil {
		return AccessToken{}, err
	}
	token, ok := f.access[lookup]
	if !ok {
		return AccessToken{}, ErrTokenNotFound
	}
	if f.families[token.Family] {
		return AccessToken{}, ErrTokenRevoked
	}
	return token, nil
}

func (f *fakeStore) RefreshToken(_ context.Context, lookup Lookup) (RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("RefreshToken"); err != nil {
		return RefreshToken{}, err
	}
	entry, ok := f.refresh[lookup]
	if !ok {
		return RefreshToken{}, ErrTokenNotFound
	}
	if f.families[entry.token.Family] {
		return RefreshToken{}, ErrTokenRevoked
	}
	// A consumed token is returned rather than refused: reuse is RotateRefreshToken's
	// to detect, so that the family dies in the same transaction.
	return entry.token, nil
}

// RotateRefreshToken is the atomic step the whole rotation scheme rests on. A
// presented token that was already consumed, or whose family is revoked, revokes
// the family here, inside the same critical section that detected it.
func (f *fakeStore) RotateRefreshToken(
	_ context.Context, presented Lookup, access AccessToken, refresh RefreshToken,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("RotateRefreshToken"); err != nil {
		return err
	}
	entry, ok := f.refresh[presented]
	if !ok {
		return ErrTokenNotFound
	}
	if entry.consumed || f.families[entry.token.Family] {
		f.families[entry.token.Family] = true
		return fmt.Errorf("family %s revoked on reuse: %w", entry.token.Family, ErrRefreshTokenReused)
	}
	entry.consumed = true
	f.refresh[presented] = entry
	f.access[access.Lookup] = access
	f.refresh[refresh.Lookup] = refreshEntry{token: refresh}
	f.rotations++
	return nil
}

func (f *fakeStore) RevokeFamily(_ context.Context, family FamilyID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("RevokeFamily"); err != nil {
		return err
	}
	f.families[family] = true
	return nil
}

func (f *fakeStore) RevokePrincipal(_ context.Context, principal identity.Principal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("RevokePrincipal"); err != nil {
		return err
	}
	for _, entry := range f.refresh {
		if entry.token.Principal == principal {
			f.families[entry.token.Family] = true
		}
	}
	for _, token := range f.access {
		if token.Principal == principal {
			f.families[token.Family] = true
		}
	}
	for key := range f.consents {
		if key.Principal == principal {
			delete(f.consents, key)
		}
	}
	return nil
}

func (f *fakeStore) SaveCode(_ context.Context, code AuthorizationCode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SaveCode"); err != nil {
		return err
	}
	f.codes[code.Lookup] = codeEntry{code: code}
	return nil
}

// ConsumeCode marks the code used and returns it. The second call reports
// ErrCodeAlreadyUsed, which is what makes a code single-use even under a race.
func (f *fakeStore) ConsumeCode(_ context.Context, lookup Lookup) (AuthorizationCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ConsumeCode"); err != nil {
		return AuthorizationCode{}, err
	}
	entry, ok := f.codes[lookup]
	if !ok {
		return AuthorizationCode{}, ErrCodeNotFound
	}
	if entry.used {
		return AuthorizationCode{}, ErrCodeAlreadyUsed
	}
	entry.used = true
	f.codes[lookup] = entry
	return entry.code, nil
}

func (f *fakeStore) familyRevoked(family FamilyID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.families[family]
}

func (f *fakeStore) consentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.consents)
}

func (f *fakeStore) transactionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.transactions)
}
