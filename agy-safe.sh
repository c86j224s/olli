#!/usr/bin/env bash
set -euo pipefail

die() {
    printf 'agy-safe: %s\n' "$*" >&2
    exit 1
}

SOURCE="${BASH_SOURCE[0]}"
while [ -L "$SOURCE" ]; do
    SOURCE_DIR="$(cd -P -- "$(dirname -- "$SOURCE")" && pwd)"
    SOURCE="$(readlink "$SOURCE")"
    case "$SOURCE" in
        /*) ;;
        *) SOURCE="$SOURCE_DIR/$SOURCE" ;;
    esac
done

REPO_ROOT="$(cd -P -- "$(dirname -- "$SOURCE")" && pwd)"
cd "$REPO_ROOT"

[ -f "$REPO_ROOT/AGENTS.md" ] || die "AGENTS.md is missing; refusing to launch Antigravity without repo safety rules."
[ -f "$REPO_ROOT/go.mod" ] || die "go.mod is missing; refusing to launch outside the repo root."
[ -f "$REPO_ROOT/main.go" ] || die "main.go is missing; refusing to launch outside the repo root."

AGY_BIN="$(command -v agy || true)"
[ -n "$AGY_BIN" ] || die "Antigravity CLI 'agy' is not installed or not on PATH."

if ! "$AGY_BIN" --help 2>&1 | grep -q -- '--sandbox'; then
    die "installed agy does not advertise --sandbox; update Antigravity CLI before using this repo."
fi

args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
    arg="${args[$i]}"
    case "$arg" in
        --dangerously-skip-permissions|--dangerously-skip-permissions=*)
            die "--dangerously-skip-permissions is forbidden for this repo."
            ;;
        --add-dir|--add-dir=*)
            die "--add-dir is forbidden here; keep Antigravity scoped to this repo root only."
            ;;
        --new-project|--new-project=*)
            die "--new-project is forbidden here; launch from the checked-out repo root."
            ;;
        --log-file|--log-file=*)
            die "--log-file is blocked to avoid writing Antigravity logs outside this repo."
            ;;
        --mode=accept-edits)
            die "--mode=accept-edits is blocked; use review-first operation for this repo."
            ;;
        --mode)
            next="${args[$((i + 1))]:-}"
            if [ "$next" = "accept-edits" ]; then
                die "--mode accept-edits is blocked; use review-first operation for this repo."
            fi
            ;;
    esac
done

unset GIT_EDITOR GIT_SEQUENCE_EDITOR GIT_SSH_COMMAND GIT_ASKPASS
unset EDITOR VISUAL MANPAGER RIPGREP_CONFIG_PATH
unset DYLD_INSERT_LIBRARIES LD_PRELOAD
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null
export GIT_EXTERNAL_DIFF=
export GIT_PAGER=cat
export PAGER=cat

printf 'Starting Antigravity CLI in safe repo mode.\n' >&2
printf '  repo: %s\n' "$REPO_ROOT" >&2
printf '  sandbox: forced via --sandbox\n' >&2
printf '  rules: AGENTS.md loaded from repo root\n' >&2
printf 'Do not approve "run without sandbox restrictions" prompts in this repo.\n\n' >&2

BOOTSTRAP_PROMPT='Before doing anything, read and follow AGENTS.md. Confirm that this session is scoped to the current repository only, terminal sandboxing is enabled, non-workspace access is not needed, and destructive filesystem tests must use only temp or repo-local marker targets.'

if [ "$#" -eq 0 ]; then
    exec "$AGY_BIN" --sandbox --prompt-interactive "$BOOTSTRAP_PROMPT"
fi

exec "$AGY_BIN" --sandbox "$@"
