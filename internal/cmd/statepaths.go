package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tamcore/garmin-mcp/internal/config"
)

// stateDirName is the per-user directory this binary owns inside the platform's
// configuration directory. It is used only when the operator configured no
// explicit state directory.
const stateDirName = "garmin-mcp"

// Sub-directory names inside the state directory. They are separate so a backup
// or a permission audit can treat key material and records differently.
const (
	keysDirName   = "keys"
	tokensDirName = "tokens"
)

// keyVersion is the encryption key version this build reads and writes.
//
// It is fixed at 1 deliberately: internal/cryptostore supports staged rotation,
// but nothing here re-seals existing records, so raising this constant alone
// would make every stored record unreadable instead of migrating it.
const keyVersion = 1

// statePaths are the resolved filesystem locations one invocation works with. The
// value is immutable: every field is set once, by [resolveStatePaths].
type statePaths struct {
	// root is the state directory holding everything below it.
	root string
	// keys is the directory the versioned key material lives in.
	keys string
	// tokens is the directory the encrypted per-principal records live in.
	tokens string
}

// keyFile reports the file the current key version is stored in. The name is
// chosen by internal/cryptostore, which owns the versioned layout, so this is a
// report rather than a setting.
func (p statePaths) keyFile() string {
	return filepath.Join(p.keys, "key-v"+strconv.Itoa(keyVersion)+".json")
}

// resolveStatePaths decides where state lives for cfg.
//
// An explicit state directory wins. Otherwise the platform's per-user
// configuration directory is used, which keeps a local deployment out of the
// working directory and inside a location the operating system already protects.
//
// A configured master key file selects the directory the versioned key material is
// read from. internal/cryptostore owns the file name inside that directory, so the
// setting chooses the location and not the name; [statePaths.keyFile] reports what
// the effective file is.
func resolveStatePaths(cfg config.Config) (statePaths, error) {
	root, err := resolveStateRoot(cfg)
	if err != nil {
		return statePaths{}, err
	}

	keys := filepath.Join(root, keysDirName)
	if cfg.MasterKeyPath != "" {
		keys = filepath.Dir(cfg.MasterKeyPath)
	}

	return statePaths{
		root:   root,
		keys:   keys,
		tokens: filepath.Join(root, tokensDirName),
	}, nil
}

// resolveStateRoot reports the state directory, or why it cannot be determined.
func resolveStateRoot(cfg config.Config) (string, error) {
	if cfg.StateDir != "" {
		return cfg.StateDir, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("%w: no state directory is configured and the "+
			"per-user configuration directory is unavailable: %w", ErrUnresolvedState, err)
	}
	return filepath.Join(base, stateDirName), nil
}
