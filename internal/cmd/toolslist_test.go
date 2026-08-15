package cmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The synthetic tools the listing tests declare. They are deliberately not real
// tool names, so the assertions describe the listing's behavior rather than the
// current contents of the tool package.
const (
	catalogReadTool        = "fake_read_thing"
	catalogWriteTool       = "fake_write_thing"
	catalogDestructiveTool = "fake_delete_thing"
)

// testCatalog is the catalog a listing test injects: one tool per tier, declared
// out of order so the command's own ordering is what the assertions observe.
func testCatalog() []cmd.ToolEntry {
	return []cmd.ToolEntry{
		{
			Name:       catalogWriteTool,
			Tier:       policy.TierWrite,
			Idempotent: true,
		},
		{
			Name:       catalogDestructiveTool,
			Tier:       policy.TierDestructive,
			Idempotent: true,
		},
		{
			Name:       catalogReadTool,
			Tier:       policy.TierReadOnly,
			Idempotent: true,
		},
	}
}

// runToolsList executes `tools list` with the injected catalog and reports both
// streams.
func runToolsList(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer
	root := cmd.NewRootCommand(cmd.Options{
		BuildInfo: cmd.BuildInfo{Version: testVersion, Commit: testCommit},
		Args:      append([]string{cmdTools, cmdList}, args...),
		Stdout:    &out,
		Stderr:    &errOut,
		Catalog:   testCatalog,
	})
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

// TestToolsListNamesEveryToolWithItsTier is what the command exists for.
func TestToolsListNamesEveryToolWithItsTier(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runToolsList(t)
	if err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}

	tiers := map[string]string{
		catalogReadTool:        tierReadOnly,
		catalogWriteTool:       "write",
		catalogDestructiveTool: "destructive",
	}
	for name, tier := range tiers {
		line := lineContaining(t, stdout, name)
		if !strings.Contains(line, tier) {
			t.Errorf("line %q for %s does not report the %s tier", line, name, tier)
		}
	}
}

// TestToolsListReportsTheEffectOfEachTier keeps the listing useful to somebody
// deciding what to enable: the tier name alone does not say that a destructive
// call is confirmed with the client first.
func TestToolsListReportsTheEffectOfEachTier(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runToolsList(t)
	if err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}

	if line := lineContaining(t, stdout, catalogDestructiveTool); !strings.Contains(line, "confirm") {
		t.Errorf("line %q does not say a destructive call is confirmed first", line)
	}
	if line := lineContaining(t, stdout, catalogWriteTool); !strings.Contains(line, "write") {
		t.Errorf("line %q does not describe the write effect", line)
	}
	if line := lineContaining(t, stdout, catalogReadTool); !strings.Contains(line, "read") {
		t.Errorf("line %q does not describe the read effect", line)
	}
}

// TestToolsListIncludesTheBuiltInTool keeps the listing honest about what a running
// server registers: the server contributes server_info itself, so a listing that
// omitted it would understate the surface.
func TestToolsListIncludesTheBuiltInTool(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runToolsList(t)
	if err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}
	if !strings.Contains(stdout, "server_info") {
		t.Errorf("stdout = %q, want the built-in tool listed", stdout)
	}
}

// TestToolsListReportsWhetherTheHigherTiersAreEnabled makes the listing answer the
// question an operator actually has: not only which tools exist, but whether this
// configuration would let any of them run.
func TestToolsListReportsWhetherTheHigherTiersAreEnabled(t *testing.T) {
	clearGarminEnv(t)

	disabled, err := runToolsList(t)
	if err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}
	if !strings.Contains(disabled, "disabled") {
		t.Errorf("stdout = %q, want the default read-only stance reported", disabled)
	}

	enabled, err := runToolsList(t, "--enable-write-tools")
	if err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}
	if !strings.Contains(enabled, "enabled") {
		t.Errorf("stdout = %q, want the enabled write tier reported", enabled)
	}
}

// TestToolsListNeedsNoDatabase is the privilege rule: listing the surface must not
// require the storage a serving deployment needs, so a database path that could not
// possibly be opened changes nothing.
func TestToolsListNeedsNoDatabase(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runToolsList(t, databaseFlag+"/nonexistent-directory/state.db")
	if err != nil {
		t.Fatalf("tools list = %v, want the listing without a database", err)
	}
	if !strings.Contains(stdout, catalogReadTool) {
		t.Errorf("stdout = %q, want the listing", stdout)
	}
	assertNoDatabaseInWorkingDirectory(t)
}

// TestToolsListIsOrderedByTierThenName keeps the output diffable: a script that
// compares two builds must see a change only when the surface changed.
func TestToolsListIsOrderedByTierThenName(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runToolsList(t)
	if err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}

	read := strings.Index(stdout, catalogReadTool)
	write := strings.Index(stdout, catalogWriteTool)
	destructive := strings.Index(stdout, catalogDestructiveTool)
	if read > write || write > destructive {
		t.Errorf("tools are ordered read=%d write=%d destructive=%d, want increasing tiers",
			read, write, destructive)
	}
}

// TestToolsListWithoutACatalogReportsAnEmptySurface fixes the honest answer for a
// build that contributes no tool: the listing says so rather than failing, because
// contributing no tool is a supported wiring and not a fault.
func TestToolsListWithoutACatalogReportsAnEmptySurface(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runCommand(t, cmdTools, cmdList)
	if err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}
	if strings.Contains(stdout, catalogReadTool) {
		t.Errorf("stdout = %q, want no tool from a build that registers none", stdout)
	}
	if !strings.Contains(stdout, "server_info") {
		t.Errorf("stdout = %q, want the built-in tool, which is always registered", stdout)
	}
}

// TestToolsListSeparatesRepeatableWritesFromRepeatableCreates is the distinction a
// caller acts on: repeating one write converges on the same end state, and
// repeating the other adds a second record.
func TestToolsListSeparatesRepeatableWritesFromRepeatableCreates(t *testing.T) {
	clearGarminEnv(t)

	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.Options{
		Args:   []string{cmdTools, cmdList},
		Stdout: &out,
		Stderr: &bytes.Buffer{},
		Catalog: func() []cmd.ToolEntry {
			return []cmd.ToolEntry{
				{Name: "fake_create_thing", Tier: policy.TierWrite},
				{Name: catalogWriteTool, Tier: policy.TierWrite, Idempotent: true},
			}
		},
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}

	creates := lineContaining(t, out.String(), "fake_create_thing")
	if !strings.Contains(creates, "creates another") {
		t.Errorf("line %q does not warn that repeating creates another record", creates)
	}
	updates := lineContaining(t, out.String(), catalogWriteTool)
	if !strings.Contains(updates, "converges") {
		t.Errorf("line %q does not say that repeating converges", updates)
	}
}

// TestToolsListReportsAToolThatHasNoTier keeps the listing honest about a wiring
// defect: a tool in no tier is refused on every call, and the listing has to say
// so rather than render an enum value nobody can act on.
func TestToolsListReportsAToolThatHasNoTier(t *testing.T) {
	clearGarminEnv(t)

	var out bytes.Buffer
	root := cmd.NewRootCommand(cmd.Options{
		Args:   []string{cmdTools, cmdList},
		Stdout: &out,
		Stderr: &bytes.Buffer{},
		Catalog: func() []cmd.ToolEntry {
			return []cmd.ToolEntry{{Name: "fake_untiered_thing"}}
		},
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("tools list = %v, want the listing", err)
	}

	line := lineContaining(t, out.String(), "fake_untiered_thing")
	if !strings.Contains(line, "unknown") {
		t.Errorf("line %q does not report the tool as untiered", line)
	}
	if !strings.Contains(line, "refused") {
		t.Errorf("line %q does not say the call would be refused", line)
	}
}

// TestToolsListValidatesConfigurationFirst keeps a misconfigured deployment
// distinguishable from a listing.
func TestToolsListValidatesConfigurationFirst(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runToolsList(t, "--log-level=trace")
	if err == nil {
		t.Fatal("an invalid log level was accepted")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing before the refusal", stdout)
	}
}

// TestToolsWithoutASubcommandShowsHelp keeps the group command informative instead
// of silently succeeding.
func TestToolsWithoutASubcommandShowsHelp(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runCommand(t, cmdTools)
	if err != nil {
		t.Fatalf("tools = %v, want the help text", err)
	}
	if !strings.Contains(stdout, cmdList) {
		t.Errorf("stdout = %q, want it to mention the list subcommand", stdout)
	}
}

// TestRootShowsHelpOnStdout keeps a bare invocation discoverable.
func TestRootShowsHelpOnStdout(t *testing.T) {
	clearGarminEnv(t)

	stdout, err := runCommand(t)
	if err != nil {
		t.Fatalf("bare invocation = %v, want the help text", err)
	}
	for _, want := range []string{cmdServe, cmdAuth, cmdDoctor, cmdTools, cmdMigrate, cmdVersion} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not list the %q command", want)
		}
	}
}

// lineContaining returns the single output line holding needle.
func lineContaining(t *testing.T, out, needle string) string {
	t.Helper()

	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("output %q has no line containing %q", out, needle)
	return ""
}
