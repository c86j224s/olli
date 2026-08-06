# AGENTS.md

Instructions for AI coding agents working in this repository.

## Project Context

This repository is `O.L.L.I.`, a Go-based local Ollama agent with tool execution, subagent delegation, session persistence, and filesystem access. Treat it as security-sensitive infrastructure. A prior agent caused destructive filesystem damage, so safety rules in this file are mandatory.

## Non-Negotiable Safety Rules

- Work only inside this repository unless the user explicitly authorizes a different path in the current turn.
- Never run destructive cleanup against `~`, `$HOME`, `/`, `/Users`, `/home`, system directories, or paths derived from untrusted input.
- Never run `rm -rf`, `git reset --hard`, `git clean`, `git checkout --`, or equivalent destructive commands unless the user explicitly asks for that exact operation. Prefer repo-owned cleanup scripts after inspecting their guards.
- Never write a test that executes a destructive payload against real user or system paths, even when the test expects the guard to block it.
- Do not follow symlinks when writing, deleting, renaming, or creating build/session/log artifacts. Validate with `Lstat`-style checks before writes and again after path resolution when race-sensitive.
- Do not add `sh -c`, shell pipelines, command substitution, or string-built shell execution to agent-exposed tool paths. Use parsed argv, explicit allowlists, and fail-closed validation.
- Do not weaken workspace containment, home-directory rejection, system-root rejection, command allowlists, sandbox profiles, permission prompts, or subagent tool isolation without explicit user approval and security tests.
- Treat `run_terminal_command`, file edit/write tools, directory changes, and mutation-capable subagents as sensitive even if a config whitelist says otherwise.

## Filesystem and Sandbox Expectations

- Canonicalize paths before use and enforce containment inside the immutable workspace root.
- Reject user home directories and OS roots as workspace roots, including symlinked or alternate-spelling paths.
- Keep command execution sandboxed. On macOS this means `sandbox-exec` must fail closed if unavailable; on unsupported platforms command execution must fail closed until an equivalent OS sandbox is implemented.
- Rebind `HOME`, temp dirs, Go caches, XDG dirs, and tool helper environment variables into workspace-local sandbox directories for executed commands.
- For `git` and `rg`, prevent helper execution through config, external diff/textconv, pre-processors, zip search, editors, pagers, hooks, or inherited environment.
- Session logs and subagent reports must stay under `sessions/` within the workspace root and must reject symlink entries.
- Build outputs must stay under repo-local `bin/` and top-level `olli`; reject symlinked artifact paths.

## Coding Workflow

- Before editing, inspect the relevant files and the current `git status --short`.
- Use small, focused patches. Do not rewrite unrelated files or revert user changes.
- Use `rg` for search and `go test`/`go vet` for validation.
- Prefer structured path APIs over string prefix checks. On case-insensitive filesystems, string comparisons are not enough for home/system-root checks; use canonical paths and same-file checks where possible.
- Add or update regression tests whenever changing command execution, filesystem access, sessions, subagents, config permissions, or build cleanup.

## Regression Test Safety

Regression tests must prove the guard without making the test itself dangerous.

- Dangerous strings such as `rm -rf ~`, `rm -rf /`, or writes to real home/system paths may be used only as inert input to pure validation/parsing functions that cannot execute processes or mutate files.
- Tests that call the execution path (`ExecuteCommandWithWorkspace`, registry tool execution, subagent tool loops, build scripts, or CLI entrypoints) must use only `t.TempDir()` or repo-local marker files as possible mutation targets.
- A test is unacceptable if a guard regression would delete, overwrite, chmod, rename, or create files in the user's home directory, system roots, or any path outside a test-owned temp directory.
- For destructive-command blocking, prefer direct validator tests plus execution-path tests using a temp workspace and harmless marker files.
- For sandbox escape tests, the attempted outside target must be another test-owned temp directory, and the assertion must prove no outside marker was created.
- For symlink tests, create symlinks only inside temp directories or repo-local smoke fixtures, and assert that outside temp targets remain unchanged.
- Before adding a safety regression test, state the failure blast radius in a comment or test name. If the answer is anything broader than temp/repo-local files, redesign the test.

## Verification

For security-related changes, run the smallest relevant subset first, then the full checks when feasible:

```bash
go test ./...
go vet ./...
bash -n build.sh
git diff --check
```

If touching build cleanup or artifact paths, also run a symlink smoke test that proves `bin`, `bin/olli`, and `olli` symlinks are rejected without creating or modifying outside targets.

## Review Standard

Default to an adversarial review stance for this project. Look for:

- workspace escape through symlinks, `..`, alternate home paths, or case-insensitive aliases
- shell injection or helper execution through `git`, `rg`, Go toolchain, editors, pagers, hooks, or environment variables
- fail-open behavior when sandbox setup fails
- tests that prove safety by actually risking user data
- cleanup scripts that can follow symlinks or run from the wrong directory

When uncertain, stop and ask. Do not guess around destructive filesystem behavior.
