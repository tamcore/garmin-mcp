package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/securefile"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// keyMaterialFileMode is the owner-only mode enforced on one versioned key
// file. It matches internal/cryptostore's own unexported keyFileMode: that
// package owns the file's shape and does not export the mode, so this is
// stated again here the same way keysDirMode already restates
// cryptostore's key-directory mode (see activekeyversion.go).
const keyMaterialFileMode fs.FileMode = 0o600

// secretHashFileMode is the mode a confidential OAuth client's
// secret-hash-file is compared against — but only as a ceiling, never an
// exact requirement. See [repairTarget.noGroupOrOther].
const secretHashFileMode fs.FileMode = 0o600

// keyFileNamePattern matches one versioned key file's name, exactly the shape
// cryptostore's keyFilePath produces: "key-v" + strconv.Itoa of a positive
// int + ".json". strconv.Itoa never emits a leading zero, and cryptostore
// refuses version 0 (GenerateKey, LoadKey and LoadOrCreateKey all reject
// version <= 0), so "key-v0.json" and "key-v00.json" are never a name the
// server itself creates or reads. Matching [0-9]+ used to accept both, which
// meant this command could chmod a file the server ignores. The canonical
// positive integer is a single digit 1-9, or a longer run that starts with
// one — exactly what strconv.Itoa emits.
//
// It is used only to recognize which entries in the key directory are key
// material to check, never to parse or trust their content.
//
// It is built per call rather than kept in a package variable, the same
// discipline internal/store's migrationNamePattern already follows: a
// compiled regexp is package-level state, and this package keeps none. The
// key directory holds a handful of files, checked once per run, so the
// compile is not on any hot path.
func keyFileNamePattern() *regexp.Regexp {
	return regexp.MustCompile(`^key-v[1-9][0-9]*\.json$`)
}

// tokenRecordFileNamePattern matches a FileStore record file
// ("<sha256 hex>.json") or its advisory lock file
// ("<sha256 hex>.json.lock"), the two shapes store.FileStore's recordPath and
// lockPath produce. Built per call for the same reason as
// keyFileNamePattern.
func tokenRecordFileNamePattern() *regexp.Regexp {
	return regexp.MustCompile(`^[0-9a-f]{64}\.json(\.lock)?$`)
}

// repairTarget names one filesystem object the secure file layer guards, and
// the mode it must carry.
//
// A target may not exist yet — a fresh deployment has no key, no token
// record, and no database — and that is not a problem this command reports:
// see [inspectTarget].
type repairTarget struct {
	path  string
	mode  fs.FileMode
	isDir bool
	// noGroupOrOther, when true, means mode is a ceiling rather than an
	// exact required value: the target is wrong only when it grants group
	// or other access, matching the check securefile.ReadFile itself
	// applies (checkOwnerOnly) to material this command did not create and
	// owns no single correct value for. Such a target is never auto-fixed
	// — see finding.fixable — because forcing an exact mode this command
	// invented could widen a file the operator deliberately narrowed (say,
	// 0400), which "tighten" must never do.
	noGroupOrOther bool
}

// dirScan is one directory whose contents this command must enumerate to
// discover further targets: the key directory and the token records
// directory. It is resolved separately from repairTargets because
// enumerating it can require first repairing the directory's own mode — see
// resolveScan.
type dirScan struct {
	target   repairTarget
	pattern  *regexp.Regexp
	fileMode fs.FileMode
}

// repairTargets reports every already-known path under cfg's configured
// state directory (and, when configured, the database and any confidential
// OAuth client's secret-hash-file) that internal/securefile guards, plus the
// directories whose contents must still be enumerated to find the rest — see
// [dirScan] and resolveScan.
//
// Nothing here opens or reads any of them for content: a directory listing
// (os.ReadDir) reports names and file types only, and building this list
// creates nothing that was not already there.
func repairTargets(cfg config.Config) ([]repairTarget, []dirScan, error) {
	paths, err := resolveStatePaths(cfg)
	if err != nil {
		return nil, nil, err
	}

	// A symlinked state root must never be followed: every securefile
	// operation refuses a symlinked ancestor at the moment it is used, so an
	// ancestry this command left unchecked while serve enforces it is
	// exactly the false "exit 0" this command exists to prevent. Checking
	// both paths.keys and paths.tokens covers every existing ancestor of
	// both, including a --master-key-file naming a keys directory entirely
	// outside --state-dir.
	if err := securefile.CheckAncestry(paths.keys); err != nil {
		return nil, nil, err
	}
	if err := securefile.CheckAncestry(paths.tokens); err != nil {
		return nil, nil, err
	}

	keysTarget := repairTarget{path: paths.keys, mode: keysDirMode, isDir: true}
	targets := []repairTarget{
		keysTarget,
		{path: activeKeyVersionPath(paths), mode: activeVersionMode},
	}

	targets = append(targets, repairTarget{path: paths.tokens, mode: store.SecureDirMode, isDir: true})
	records := filepath.Join(paths.tokens, store.RecordsDirName)
	recordsTarget := repairTarget{path: records, mode: store.SecureDirMode, isDir: true}
	targets = append(targets, recordsTarget)

	scans := []dirScan{
		{target: keysTarget, pattern: keyFileNamePattern(), fileMode: keyMaterialFileMode},
		{target: recordsTarget, pattern: tokenRecordFileNamePattern(), fileMode: store.TokenRecordFileMode},
	}

	if cfg.DatabasePath != "" {
		dbTargets, err := databaseTargets(cfg.DatabasePath)
		if err != nil {
			return nil, nil, err
		}
		targets = append(targets, dbTargets...)
	}

	targets = append(targets, clientSecretHashTargets(cfg)...)

	return targets, scans, nil
}

// clientSecretHashTargets reports one report-only target per distinct
// configured confidential OAuth client secret-hash-file. See
// secretHashFileMode and repairTarget.noGroupOrOther.
func clientSecretHashTargets(cfg config.Config) []repairTarget {
	var targets []repairTarget
	seen := make(map[string]bool)
	for _, client := range cfg.OAuthClients {
		if client.SecretHashPath == "" || seen[client.SecretHashPath] {
			continue
		}
		seen[client.SecretHashPath] = true
		targets = append(targets, repairTarget{
			path: client.SecretHashPath, mode: secretHashFileMode, noGroupOrOther: true,
		})
	}
	return targets
}

// databaseTargets resolves the configured database path and reports its
// parent directory, the database file, and its "-wal" and "-shm" sidecars.
//
// store.DatabaseFiles performs the identical resolution OpenDatabase itself
// applies — expanding "~", refusing a symlinked ancestor — without opening or
// creating anything, which is what lets this command inspect a database it
// must never connect to.
func databaseTargets(configuredPath string) ([]repairTarget, error) {
	files, err := store.DatabaseFiles(configuredPath)
	if err != nil {
		return nil, err
	}

	targets := []repairTarget{
		{path: filepath.Dir(files[0]), mode: store.SecureDirMode, isDir: true},
	}
	for _, file := range files {
		targets = append(targets, repairTarget{path: file, mode: store.DatabaseFileMode})
	}
	return targets, nil
}

// matchEntries reports every entry whose name matches pattern, as a repair
// target with mode, regardless of the entry's type: a directory, FIFO, or
// symlink carrying a key or record name is exactly the kind of foreign
// object inspectTarget's wrongType check exists to catch, and skipping it
// here would let it slip through unreported instead.
func matchEntries(dir string, entries []os.DirEntry, pattern *regexp.Regexp, mode fs.FileMode) []repairTarget {
	var targets []repairTarget
	for _, entry := range entries {
		if !pattern.MatchString(entry.Name()) {
			continue
		}
		targets = append(targets, repairTarget{path: filepath.Join(dir, entry.Name()), mode: mode})
	}
	return targets
}
