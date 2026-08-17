package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// This file covers the housekeeping half of the remote deployment: the periodic
// removal of expired authorization state. Nothing depends on it for correctness —
// every read applies its own expiry predicate — so what is asserted here is that it
// actually runs, that it stops with the server, and that it says nothing about who
// the removed rows belonged to.

// Synthetic seed material. None of it is a real handle, client or account.
const (
	expiredHandle    = "cleanup-transaction-handle-1"
	expiredChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	// seedAge is how far in the past the seeding clock sits, which is well past the
	// ten-minute transaction lifetime.
	seedAge = 72 * time.Hour
	// pollStep is how often a test looks for the record it is waiting for.
	pollStep = 5 * time.Millisecond
	// pollDeadline bounds that wait, so a broken loop fails instead of hanging.
	pollDeadline = 30 * time.Second
)

// recordingSink collects log records a deployment writes from its own goroutines.
type recordingSink struct {
	mu      sync.Mutex
	records strings.Builder
}

func (s *recordingSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records.Write(p)
}

func (s *recordingSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records.String()
}

// seedExpiredTransaction writes one authorization transaction that is already past
// its lifetime.
//
// The row is written through a second store whose clock sits in the past, so the
// stored expiry has genuinely elapsed against the real clock the served deployment
// runs on. That is what lets this test move no time and stub no clock: the
// deployment under test is the ordinary one.
func seedExpiredTransaction(t *testing.T, cfg config.Config) {
	t.Helper()

	key, err := cryptostore.LoadOrCreateKey(filepath.Join(cfg.StateDir, "keys"), defaultActiveKeyVersion)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	past := time.Now().Add(-seedAge)
	seeder, err := store.OpenSQLite(t.Context(), store.SQLiteConfig{
		Path: cfg.DatabasePath,
		Key:  key,
		Now:  func() time.Time { return past },
	})
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer func() {
		if err := seeder.Close(); err != nil {
			t.Errorf("closing the seeding store: %v", err)
		}
	}()

	err = seeder.PutAuthTransaction(t.Context(), store.AuthTransactionDraft{
		Handle:        store.NewSecret(expiredHandle),
		ClientID:      cfg.OAuthClients[0].ID,
		RedirectURI:   remoteRedirectURI,
		Scopes:        []string{remoteScope},
		CodeChallenge: expiredChallenge,
		Lifetime:      10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("PutAuthTransaction: %v", err)
	}
}

// waitForRecord blocks until sink holds want, or fails the test.
func waitForRecord(t *testing.T, sink *recordingSink, want string) {
	t.Helper()

	deadline := time.After(pollDeadline)
	for !strings.Contains(sink.String(), want) {
		select {
		case <-deadline:
			t.Fatalf("no %q record appeared; the logs hold: %s", want, sink.String())
		case <-time.After(pollStep):
		}
	}
}

// servedCleanup serves the deployment until its cleanup reported something, then
// stops it and returns what it logged.
func servedCleanup(t *testing.T, remote *remoteDeployment, sink *recordingSink) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- remote.serveOn(ctx, &http.Server{Handler: remote.handler}, listener) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serveOn returned %v, want nil for a cancelled context", err)
		}
	}()

	waitForRecord(t, sink, cleanupMessage)
	return sink.String()
}

// TestPeriodicCleanupRemovesExpiredStateWhileTheDeploymentServes is the proof the
// job runs: the expired row is gone by the time the server stops, and a direct
// Cleanup afterwards finds nothing left to remove.
func TestPeriodicCleanupRemovesExpiredStateWhileTheDeploymentServes(t *testing.T) {
	cfg := remoteConfig(t)
	seedExpiredTransaction(t, cfg)

	var sink recordingSink
	remote := buildRemoteWithLogs(t, cfg, &sink)
	remote.cleanup.interval = pollStep

	records := servedCleanup(t, remote, &sink)
	if !strings.Contains(records, "transactions=1") {
		t.Errorf("the cleanup record does not report the removed transaction: %s", records)
	}

	// The row is gone because the served deployment removed it, not because this
	// test did: a direct sweep now finds nothing.
	left, err := remote.sqlite.Cleanup(t.Context(), 0)
	if err != nil {
		t.Fatalf("Cleanup after serving: %v", err)
	}
	if left != (store.CleanupStats{}) {
		t.Errorf("a direct sweep still removed %+v, so the periodic job did not run", left)
	}
}

// TestPeriodicCleanupLogsCountsAndNothingElse is the redaction guarantee: an
// operator learns how much was removed and nothing about whose it was.
func TestPeriodicCleanupLogsCountsAndNothingElse(t *testing.T) {
	cfg := remoteConfig(t)
	seedExpiredTransaction(t, cfg)

	var sink recordingSink
	remote := buildRemoteWithLogs(t, cfg, &sink)
	remote.cleanup.interval = pollStep
	records := servedCleanup(t, remote, &sink)

	forbidden := map[string]string{
		"the transaction handle": expiredHandle,
		"the client identifier":  cfg.OAuthClients[0].ID,
		"the redirect URI":       remoteRedirectURI,
		"the PKCE challenge":     expiredChallenge,
	}
	for what, material := range forbidden {
		if strings.Contains(records, material) {
			t.Errorf("the cleanup records quote %s", what)
		}
	}
	for _, count := range []string{"transactions=", "codes=", "tokens=", "families="} {
		if !strings.Contains(records, count) {
			t.Errorf("the cleanup record is missing the %s count", count)
		}
	}
}

// TestCleanupStopsWithTheServer proves the ticker is cancellable: the run loop ends
// with the server's context rather than outliving it.
func TestCleanupStopsWithTheServer(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))
	remote.cleanup.interval = pollStep

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- remote.cleanup.Run(ctx) }()

	time.Sleep(4 * pollStep)
	stop()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil for a cancelled context", err)
		}
	case <-time.After(pollDeadline):
		t.Fatal("the cleanup loop did not stop with its context")
	}
}

// TestNewStoreCleanerRefusesAMissingStore is why stdio starts none: there is no
// multi-user store to sweep, and a cleaner without one would tick over nothing.
func TestNewStoreCleanerRefusesAMissingStore(t *testing.T) {
	t.Parallel()

	if _, err := newStoreCleaner(nil, nil); !errors.Is(err, ErrMissingCleanupStore) {
		t.Fatalf("newStoreCleaner(nil) = %v, want ErrMissingCleanupStore", err)
	}
}

// failingCleaner reports a failure whose text names something that must not reach a
// log line.
type failingCleaner struct{}

func (failingCleaner) Cleanup(context.Context, int) (store.CleanupStats, error) {
	return store.CleanupStats{}, errors.New("synthetic failure naming " + remoteClientName)
}

// TestCleanupKeepsRunningAfterAFailedSweep proves one failed sweep neither stops the
// loop nor quotes the failure text.
func TestCleanupKeepsRunningAfterAFailedSweep(t *testing.T) {
	var sink recordingSink
	cleaner, err := newStoreCleaner(failingCleaner{},
		slog.New(slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err != nil {
		t.Fatalf("newStoreCleaner: %v", err)
	}
	cleaner.interval = pollStep

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- cleaner.Run(ctx) }()

	waitForRecord(t, &sink, cleanupFailureMessage)
	stop()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v after a failed sweep, want nil", err)
	}
	if strings.Contains(sink.String(), remoteClientName) {
		t.Errorf("the failure record quotes the cause text: %s", sink.String())
	}
}

// buildRemoteWithLogs is buildRemote with a captured log sink.
func buildRemoteWithLogs(t *testing.T, cfg config.Config, logs io.Writer) *remoteDeployment {
	t.Helper()

	remote, err := newRemoteDeployment(t.Context(), cfg, &wiring{Logs: logs})
	if err != nil {
		t.Fatalf("newRemoteDeployment returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := remote.close(); err != nil {
			t.Errorf("close returned error: %v", err)
		}
	})
	return remote
}

// TestTheStdioGraphHoldsNothingToSweep is the structural half of the deployment
// split. The stdio composition root builds no cleaner, and the reason is that its
// dependency graph holds no store with anything to sweep: its state is the
// single-user encrypted file store, whose records are the account's own tokens and
// not issued authorization state.
func TestTheStdioGraphHoldsNothingToSweep(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	cfg := config.Default()
	cfg.StateDir = dir
	deps, err := newDependencies(cfg, &wiring{Logs: io.Discard})
	if err != nil {
		t.Fatalf("newDependencies: %v", err)
	}
	t.Cleanup(deps.close)

	graph := reflect.ValueOf(deps).Elem()
	sweepable := reflect.TypeFor[expiredStateStore]()
	for index := range graph.NumField() {
		field := graph.Type().Field(index)
		if field.Type.Implements(sweepable) {
			t.Errorf("the stdio graph holds %s, which is sweepable: it would need a cleaner",
				field.Name)
		}
	}
}
