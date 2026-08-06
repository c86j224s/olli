package tools

import (
	"context"
	"os"
	"os/exec"
	accountuser "os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSecurityChecks(t *testing.T) {
	workspace := t.TempDir()
	subdir := filepath.Join(workspace, "subdir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("hello"), 0600); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	testCmds := []string{
		"cd subdir",
		"pwd",
		"ls .",
	}

	for _, cmd := range testCmds {
		err := ValidateCommandSafety(cmd, workspace)
		if err != nil {
			t.Errorf("ValidateCommandSafety failed for '%s': %v", cmd, err)
		} else {
			t.Logf("ValidateCommandSafety PASSED for '%s'", cmd)
		}

		out, newWs, err := ExecuteCommandWithWorkspace(nil, cmd, workspace, workspace)
		t.Logf("ExecuteCommandWithWorkspace for '%s' -> err: %v, newWs: %s, out: %s", cmd, err, newWs, out)
		if err != nil {
			t.Fatalf("ExecuteCommandWithWorkspace failed for %q: %v", cmd, err)
		}
	}

	testPaths := []string{
		".",
		"subdir",
		filepath.Join(workspace, "subdir", "file.txt"),
	}
	for _, p := range testPaths {
		safeP, err := IsPathSafe(p, workspace)
		if err != nil {
			t.Errorf("IsPathSafe failed for '%s': %v", p, err)
		} else {
			t.Logf("IsPathSafe PASSED for '%s' -> %s", p, safeP)
		}
	}
}

func TestIsPathSafeFromRejectsEscapesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatalf("failed to create base: %v", err)
	}
	outside := t.TempDir()

	rejected := []string{
		filepath.Join("..", "..", "outside"),
		filepath.Join(outside, "secret.txt"),
		"~/secret.txt",
		"/",
	}
	for _, target := range rejected {
		if _, err := IsPathSafeFrom(target, base, root); err == nil {
			t.Fatalf("expected path %q to be rejected", target)
		}
	}

	inside, err := IsPathSafeFrom("../base", base, root)
	if err != nil {
		t.Fatalf("expected inside-root parent traversal to pass: %v", err)
	}
	wantBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("failed to canonicalize base: %v", err)
	}
	if inside != wantBase {
		t.Fatalf("expected %s, got %s", wantBase, inside)
	}

	linkPath := filepath.Join(root, "outside_link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := IsPathSafeFrom("outside_link/file.txt", root, root); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestIsWorkspaceLocationSafeRejectsAccountHome(t *testing.T) {
	current, err := accountuser.Current()
	if err != nil || strings.TrimSpace(current.HomeDir) == "" {
		t.Skipf("account home unavailable: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	if err := IsWorkspaceLocationSafe(current.HomeDir); err == nil {
		t.Fatal("expected account home directory to be rejected as workspace root")
	}
}

func TestValidateCommandSafetyBlocksDestructiveCommands(t *testing.T) {
	workspace := t.TempDir()

	testCmds := []string{
		"rm -rf .",
		"sudo rm -rf .",
		`rm -rf "$PWD"`,
		"rm -rf ${PWD}",
		`echo ok | rm -rf "$PWD"`,
		"echo $(rm -rf .)",
		"find . -delete",
		"find . -exec rm -rf {} ;",
		"printf x | xargs rm",
	}

	for _, cmd := range testCmds {
		err := ValidateCommandSafety(cmd, workspace)
		if err == nil {
			t.Fatalf("expected destructive command %q to be blocked", cmd)
		}
		if !strings.Contains(err.Error(), "SECURITY BLOCK") {
			t.Fatalf("expected security block for %q, got: %v", cmd, err)
		}
	}
}

func TestValidateCommandSafetyAllowsNonDestructiveMentions(t *testing.T) {
	workspace := t.TempDir()

	testCmds := []string{
		`rg "rm -rf ." .`,
		`printf "rm -rf .\n"`,
		"ls -la",
	}

	for _, cmd := range testCmds {
		if err := ValidateCommandSafety(cmd, workspace); err != nil {
			t.Fatalf("expected non-destructive command %q to be allowed, got: %v", cmd, err)
		}
	}
}

func TestExecuteCommandWithWorkspaceBlocksBeforeExecution(t *testing.T) {
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "marker.txt")
	if err := os.WriteFile(marker, []byte("still here"), 0600); err != nil {
		t.Fatalf("failed to create marker: %v", err)
	}

	_, _, err := ExecuteCommandWithWorkspace(context.Background(), `rm -rf "$PWD"`, workspace, workspace)
	if err == nil {
		t.Fatal("expected destructive command to be blocked")
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("guard did not block before execution; marker missing: %v", statErr)
	}
}

func TestExecuteCommandWithWorkspaceRequiresExplicitCDPath(t *testing.T) {
	workspace := t.TempDir()
	_, newWs, err := ExecuteCommandWithWorkspace(context.Background(), "cd", workspace, workspace)
	if err == nil {
		t.Fatal("expected bare cd to be rejected")
	}
	if newWs != workspace {
		t.Fatalf("workspace changed on rejected cd: %s", newWs)
	}
}

func TestExecuteCommandWithWorkspaceRebindsGoCacheUnderRoot(t *testing.T) {
	workspace := t.TempDir()
	output, _, err := ExecuteCommandWithWorkspace(context.Background(), "go env GOCACHE", workspace, workspace)
	if err != nil {
		t.Fatalf("go env GOCACHE failed: %v\n%s", err, output)
	}
	got := strings.TrimSpace(output)
	wantPrefix, err := filepath.EvalSymlinks(filepath.Join(workspace, ".olli_sandbox"))
	if err != nil {
		t.Fatalf("failed to canonicalize sandbox path: %v", err)
	}
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("expected GOCACHE under %s, got %s", wantPrefix, got)
	}
}

func TestExecuteCommandWithWorkspaceBlocksUnsafeCommands(t *testing.T) {
	workspace := t.TempDir()
	blocked := []string{
		"sh -c pwd",
		"bash -c pwd",
		"python -c print",
		"node -e console.log",
		"make test",
		"./build.sh",
		"rm -rf .",
		"find . -delete",
		"git reset --hard",
		"git diff --ext-diff",
		"git -c core.fsmonitor=evil status",
		"git branch new-branch",
		"git branch --edit-description",
		"rg --pre ./evil pattern .",
		"rg -z pattern .",
		"rg --search-zip pattern .",
		"go run .",
		"ls . | cat",
		"cat /etc/passwd",
	}

	for _, cmd := range blocked {
		_, _, err := ExecuteCommandWithWorkspace(context.Background(), cmd, workspace, workspace)
		if err == nil {
			t.Fatalf("expected command %q to be blocked", cmd)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "security block") {
			t.Fatalf("expected security block for %q, got: %v", cmd, err)
		}
	}
}

func TestSandboxEnvDropsCommandHelperVariables(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "badbin"))
	t.Setenv("GIT_EDITOR", filepath.Join(root, "editor"))
	t.Setenv("GIT_SSH_COMMAND", filepath.Join(root, "ssh"))
	t.Setenv("RIPGREP_CONFIG_PATH", filepath.Join(root, "ripgreprc"))
	t.Setenv("DYLD_INSERT_LIBRARIES", filepath.Join(root, "lib.dylib"))
	t.Setenv("LD_PRELOAD", filepath.Join(root, "lib.so"))
	t.Setenv("EDITOR", filepath.Join(root, "editor"))
	t.Setenv("VISUAL", filepath.Join(root, "visual"))
	t.Setenv("PAGER", filepath.Join(root, "pager"))

	env, err := sandboxEnv(root)
	if err != nil {
		t.Fatalf("sandboxEnv failed: %v", err)
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for _, key := range []string{
		"GIT_EDITOR", "GIT_SSH_COMMAND", "RIPGREP_CONFIG_PATH", "DYLD_INSERT_LIBRARIES",
		"LD_PRELOAD", "EDITOR", "VISUAL",
	} {
		if value, ok := values[key]; ok && value != "" {
			t.Fatalf("expected %s to be dropped, got %q", key, value)
		}
	}
	if strings.Contains(values["PATH"], root) {
		t.Fatalf("expected PATH to be sanitized, got %q", values["PATH"])
	}
	if values["GIT_PAGER"] != "cat" {
		t.Fatalf("expected safe git pager, got %q", values["GIT_PAGER"])
	}
	if values["PAGER"] != "cat" {
		t.Fatalf("expected safe pager, got %q", values["PAGER"])
	}
}

func TestExecuteCommandWithWorkspaceDarwinSandboxBlocksAbsoluteOutsideWrites(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-specific")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}

	workspace := t.TempDir()
	outside := t.TempDir()
	outsideMarker := filepath.Join(outside, "should_not_exist")

	goMod := []byte("module sandboxwrite\n\ngo 1.21\n")
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), goMod, 0600); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	testSource := `package sandboxwrite

import (
	"os"
	"testing"
)

func TestOutsideWrite(t *testing.T) {
	if err := os.WriteFile("` + outsideMarker + `", []byte("bad"), 0600); err != nil {
		t.Fatalf("outside write blocked: %v", err)
	}
}
`
	if err := os.WriteFile(filepath.Join(workspace, "sandbox_test.go"), []byte(testSource), 0600); err != nil {
		t.Fatalf("failed to write sandbox test: %v", err)
	}

	output, _, err := ExecuteCommandWithWorkspace(context.Background(), "go test ./...", workspace, workspace)
	if err == nil {
		t.Fatalf("expected sandboxed go test to fail when writing outside root; output:\n%s", output)
	}
	if _, statErr := os.Stat(outsideMarker); !os.IsNotExist(statErr) {
		t.Fatalf("sandbox allowed outside write; stat err: %v", statErr)
	}
}

func TestSessionAndSubagentLogReadersRejectSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.jsonl")
	if err := os.WriteFile(secret, []byte(`{"role":"user","content":"secret"}`+"\n"), 0600); err != nil {
		t.Fatalf("failed to write outside secret: %v", err)
	}

	sessionDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(sessionDir, "leak.jsonl")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if matches, err := SearchSessionLogs(sessionDir, "secret", root); err != nil {
		t.Fatalf("session search should skip symlink entries without failing: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("expected symlinked session log to be skipped, got %v", matches)
	}

	reportDir := filepath.Join(root, "sessions", "subagents")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		t.Fatalf("failed to create report dir: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(reportDir, "leak.jsonl")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if reportList, err := ListSubagentReports(root, root); err != nil {
		t.Fatalf("report list should skip symlink entries without failing: %v", err)
	} else if strings.Contains(reportList, "leak.jsonl") {
		t.Fatalf("expected symlinked report to be skipped, got %s", reportList)
	}
	if _, err := ViewSubagentReport(root, "leak.jsonl", root); err == nil {
		t.Fatal("expected direct symlinked report read to be rejected")
	}
}
