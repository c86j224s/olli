#!/usr/bin/env bash
set -euo pipefail

CYAN='\033[036m'
GREEN='\033[032m'
YELLOW='\033[033m'
RED='\033[031m'
NC='\033[0m'

SOURCE="${BASH_SOURCE[0]}"
while [ -L "$SOURCE" ]; do
    SOURCE_DIR="$(cd -P -- "$(dirname -- "$SOURCE")" && pwd)"
    SOURCE="$(readlink "$SOURCE")"
    case "$SOURCE" in
        /*) ;;
        *) SOURCE="$SOURCE_DIR/$SOURCE" ;;
    esac
done
SCRIPT_DIR="$(cd -P -- "$(dirname -- "$SOURCE")" && pwd)"
REPO_ROOT="$(cd -P -- "$SCRIPT_DIR/.." && pwd)"

STATE_DIR_REL=".local/backends"
PID_DIR_REL="$STATE_DIR_REL/pids"
LOG_DIR_REL="$STATE_DIR_REL/logs"
LOCAL_HOME_REL=".local/home"
LOCAL_CONFIG_REL=".local/xdg-config"
LOCAL_CACHE_REL=".local/xdg-cache"
LOCAL_DATA_REL=".local/xdg-data"
UV_CACHE_REL=".local/uv-cache"
COMFY_CLI_VENV_REL=".local/comfy-cli-venv"
COMFY_PROJECT_REL=".local/comfyui"
ACE_STEP_PROJECT_REL=".local/ace-step"

OLLAMA_HOST_VALUE="127.0.0.1:11434"
OLLAMA_URL="http://127.0.0.1:11434"
COMFY_URL="http://127.0.0.1:8188"
ACE_STEP_URL="http://127.0.0.1:8001"
STARTUP_WAIT_SECONDS="${STARTUP_WAIT_SECONDS:-30}"
STOP_WAIT_SECONDS="${STOP_WAIT_SECONDS:-15}"
DRY_RUN=0

info() {
    printf "%b[info]%b %s\n" "$CYAN" "$NC" "$*"
}

ok() {
    printf "%b[ok]%b %s\n" "$GREEN" "$NC" "$*"
}

warn() {
    printf "%b[warn]%b %s\n" "$YELLOW" "$NC" "$*" >&2
}

fail() {
    printf "%b[error]%b %s\n" "$RED" "$NC" "$*" >&2
    exit 1
}

usage() {
    cat <<'USAGE'
Usage: scripts/backends.sh [--dry-run] <command> [target] [options]

Commands:
  start [all|ollama|comfyui|ace-step]      Start managed backends. Default target: all.
  stop [all|ollama|comfyui|ace-step]       Stop only processes started by this script.
  restart [all|ollama|comfyui|ace-step]    Stop then start managed backends.
  status [all|ollama|comfyui|ace-step]     Show endpoint and managed process status.
  logs [all|ollama|comfyui|ace-step]       Print backend logs.

Log options:
  -f, --follow                    Follow logs. Requires target ollama, comfyui, or ace-step.
  -n, --lines N                   Number of lines to show. Default: 80.

Examples:
  scripts/backends.sh start
  scripts/backends.sh status
  scripts/backends.sh logs comfyui -f

Notes:
  - Runtime state stays under .local/backends/.
  - Stop refuses to kill processes that do not match the expected backend command.
  - Externally started Ollama, ComfyUI, or ACE-Step processes are reported but not stopped.
USAGE
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            break
            ;;
    esac
done

COMMAND="${1:-status}"
if [ "$#" -gt 0 ]; then
    shift
fi

cd "$REPO_ROOT"

[ -f "$REPO_ROOT/go.mod" ] || fail "Refusing to run outside the repository root: $REPO_ROOT"
[ -f "$REPO_ROOT/main.go" ] || fail "Refusing to run outside the repository root: $REPO_ROOT"

quote_cmd() {
    local arg
    for arg in "$@"; do
        printf " %q" "$arg"
    done
}

print_cmd() {
    printf "%b[cmd]%b" "$CYAN" "$NC"
    quote_cmd "$@"
    printf "\n"
}

run_cmd() {
    print_cmd "$@"
    if [ "$DRY_RUN" -eq 0 ]; then
        "$@"
    fi
}

have() {
    command -v "$1" >/dev/null 2>&1
}

validate_repo_relative_path() {
    local rel="$1"
    local part
    local old_ifs="$IFS"
    local parts

    [ -n "$rel" ] || fail "Path must not be empty"
    case "$rel" in
        /*) fail "Path must be relative to the repository: $rel" ;;
    esac

    IFS='/'
    read -r -a parts <<< "$rel"
    IFS="$old_ifs"

    for part in "${parts[@]}"; do
        case "$part" in
            ""|"."|"..") fail "Path must not contain empty, '.', or '..' segments: $rel" ;;
        esac
    done
}

repo_path() {
    local rel="$1"
    validate_repo_relative_path "$rel"
    printf "%s/%s\n" "$REPO_ROOT" "$rel"
}

ensure_repo_dir() {
    local rel="$1"
    local part
    local cur="$REPO_ROOT"
    local old_ifs="$IFS"
    local parts

    validate_repo_relative_path "$rel"
    IFS='/'
    read -r -a parts <<< "$rel"
    IFS="$old_ifs"

    for part in "${parts[@]}"; do
        cur="$cur/$part"
        if [ -L "$cur" ]; then
            fail "Refusing to use symlinked repo-local directory: $cur"
        fi
        if [ -e "$cur" ]; then
            [ -d "$cur" ] || fail "Refusing to use non-directory path: $cur"
        else
            run_cmd mkdir -- "$cur"
        fi
    done
}

require_repo_dir() {
    local rel="$1"
    local part
    local cur="$REPO_ROOT"
    local old_ifs="$IFS"
    local parts

    validate_repo_relative_path "$rel"
    IFS='/'
    read -r -a parts <<< "$rel"
    IFS="$old_ifs"

    for part in "${parts[@]}"; do
        cur="$cur/$part"
        [ ! -L "$cur" ] || fail "Refusing to use symlinked repo-local directory: $cur"
        [ -d "$cur" ] || fail "Required directory is missing: $cur"
    done
}

ensure_repo_file_parent() {
    local rel="$1"
    local parent_rel

    validate_repo_relative_path "$rel"
    parent_rel="${rel%/*}"
    if [ "$parent_rel" != "$rel" ]; then
        ensure_repo_dir "$parent_rel"
    fi
}

ensure_repo_file_for_write() {
    local rel="$1"
    local path

    ensure_repo_file_parent "$rel"
    path="$(repo_path "$rel")"
    [ ! -L "$path" ] || fail "Refusing to write symlinked repo-local file: $path"
    if [ -e "$path" ]; then
        [ -f "$path" ] || fail "Refusing to write non-file path: $path"
    fi
}

ensure_log_file() {
    local rel="$1"
    local path

    ensure_repo_file_for_write "$rel"
    path="$(repo_path "$rel")"
    if [ "$DRY_RUN" -eq 0 ] && [ ! -e "$path" ]; then
        ( set -C; : > "$path" ) 2>/dev/null || fail "Refusing to create log file: $path"
    fi
    [ ! -L "$path" ] || fail "Refusing to use symlinked log file: $path"
}

pid_rel() {
    case "$1" in
        ollama) printf "%s/ollama.pid\n" "$PID_DIR_REL" ;;
        comfyui) printf "%s/comfyui.pid\n" "$PID_DIR_REL" ;;
        ace-step) printf "%s/ace-step.pid\n" "$PID_DIR_REL" ;;
        *) fail "Unknown backend: $1" ;;
    esac
}

log_rel() {
    case "$1" in
        ollama) printf "%s/ollama.log\n" "$LOG_DIR_REL" ;;
        comfyui) printf "%s/comfyui.log\n" "$LOG_DIR_REL" ;;
        ace-step) printf "%s/ace-step.log\n" "$LOG_DIR_REL" ;;
        *) fail "Unknown backend: $1" ;;
    esac
}

backend_url() {
    case "$1" in
        ollama) printf "%s\n" "$OLLAMA_URL" ;;
        comfyui) printf "%s\n" "$COMFY_URL" ;;
        ace-step) printf "%s\n" "$ACE_STEP_URL" ;;
        *) fail "Unknown backend: $1" ;;
    esac
}

read_pid() {
    local rel="$1"
    local path
    local pid=""

    path="$(repo_path "$rel")"
    [ ! -L "$path" ] || fail "Refusing to read symlinked pid file: $path"
    [ -f "$path" ] || return 1
    IFS= read -r pid < "$path" || true
    case "$pid" in
        ""|*[!0-9]*) return 1 ;;
        *) printf "%s\n" "$pid" ;;
    esac
}

write_pid() {
    local rel="$1"
    local pid="$2"
    local path

    ensure_repo_file_for_write "$rel"
    path="$(repo_path "$rel")"
    print_cmd printf "%s\\\\n" "$pid" ">" "$path"
    if [ "$DRY_RUN" -eq 0 ]; then
        printf "%s\n" "$pid" > "$path"
        [ ! -L "$path" ] || fail "Refusing symlinked pid file after write: $path"
    fi
}

clear_pid() {
    local rel="$1"
    local path

    ensure_repo_file_for_write "$rel"
    path="$(repo_path "$rel")"
    print_cmd truncate -s 0 "$path"
    if [ "$DRY_RUN" -eq 0 ]; then
        : > "$path"
        [ ! -L "$path" ] || fail "Refusing symlinked pid file after truncate: $path"
    fi
}

pid_running() {
    local pid="$1"
    kill -0 "$pid" >/dev/null 2>&1
}

process_command() {
    local pid="$1"
    ps -p "$pid" -o command= 2>/dev/null || true
}

pid_matches_backend() {
    local backend="$1"
    local pid="$2"
    local command

    command="$(process_command "$pid")"
    [ -n "$command" ] || return 1

    case "$backend" in
        ollama)
            case "$command" in
                *ollama*" "serve*|*ollama*serve*) return 0 ;;
            esac
            ;;
        comfyui)
            case "$command" in
                *comfy*launch*|*ComfyUI*|*main.py*) return 0 ;;
            esac
            ;;
        ace-step)
            case "$command" in
                *acestep-api*|*acestep.api_server*|*ACE-Step*) return 0 ;;
            esac
            ;;
    esac

    return 1
}

curl_ok() {
    local url="$1"

    have curl || return 1
    curl --noproxy '*' -fsS --max-time 2 "$url" >/dev/null 2>&1
}

backend_healthy() {
    case "$1" in
        ollama)
            curl_ok "$OLLAMA_URL/api/tags"
            ;;
        comfyui)
            curl_ok "$COMFY_URL/system_stats" || curl_ok "$COMFY_URL/"
            ;;
        ace-step)
            curl_ok "$ACE_STEP_URL/health"
            ;;
        *)
            fail "Unknown backend: $1"
            ;;
    esac
}

managed_pid() {
    local backend="$1"
    local rel
    local pid

    rel="$(pid_rel "$backend")"
    pid="$(read_pid "$rel" || true)"
    [ -n "$pid" ] || return 1
    pid_running "$pid" || return 1
    pid_matches_backend "$backend" "$pid" || return 1
    printf "%s\n" "$pid"
}

run_background_in_dir() {
    local cwd="$1"
    local backend="$2"
    local log_path
    local pid
    local old_pwd
    local log_file_rel
    local pid_file_rel

    shift 2
    log_file_rel="$(log_rel "$backend")"
    pid_file_rel="$(pid_rel "$backend")"
    ensure_log_file "$log_file_rel"
    ensure_repo_file_for_write "$pid_file_rel"
    log_path="$(repo_path "$log_file_rel")"

    printf "%b[cmd]%b cd" "$CYAN" "$NC"
    quote_cmd "$cwd"
    printf " && nohup"
    quote_cmd "$@"
    printf " >>"
    quote_cmd "$log_path"
    printf " 2>&1 &\n"

    if [ "$DRY_RUN" -eq 0 ]; then
        old_pwd="$PWD"
        cd "$cwd"
        nohup "$@" >> "$log_path" 2>&1 < /dev/null &
        pid="$!"
        cd "$old_pwd"
        write_pid "$pid_file_rel" "$pid"
    fi
}

wait_for_backend() {
    local backend="$1"
    local elapsed=0

    if [ "$DRY_RUN" -eq 1 ]; then
        return 0
    fi

    while [ "$elapsed" -lt "$STARTUP_WAIT_SECONDS" ]; do
        if backend_healthy "$backend"; then
            ok "$backend is listening at $(backend_url "$backend")"
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    warn "$backend did not become healthy within ${STARTUP_WAIT_SECONDS}s. Check logs: $(repo_path "$(log_rel "$backend")")"
}

start_ollama() {
    local pid

    if backend_healthy ollama; then
        ok "ollama already responds at $OLLAMA_URL"
        return 0
    fi
    pid="$(managed_pid ollama || true)"
    if [ -n "$pid" ]; then
        ok "ollama already managed with pid $pid"
        return 0
    fi
    have ollama || fail "ollama not found. Run scripts/install-prerequisites.sh first."

    ensure_repo_dir "$STATE_DIR_REL"
    run_background_in_dir "$REPO_ROOT" ollama env OLLAMA_HOST="$OLLAMA_HOST_VALUE" ollama serve
    wait_for_backend ollama
}

start_comfyui() {
    local comfy_bin
    local comfy_project
    local local_home
    local local_config
    local local_cache
    local local_data
    local pid

    if backend_healthy comfyui; then
        ok "comfyui already responds at $COMFY_URL"
        return 0
    fi
    pid="$(managed_pid comfyui || true)"
    if [ -n "$pid" ]; then
        ok "comfyui already managed with pid $pid"
        return 0
    fi

    require_repo_dir "$COMFY_PROJECT_REL"
    ensure_repo_dir "$LOCAL_HOME_REL"
    ensure_repo_dir "$LOCAL_CONFIG_REL"
    ensure_repo_dir "$LOCAL_CACHE_REL"
    ensure_repo_dir "$LOCAL_DATA_REL"

    comfy_bin="$(repo_path "$COMFY_CLI_VENV_REL")/bin/comfy"
    [ ! -L "$comfy_bin" ] || fail "Refusing to use symlinked comfy executable: $comfy_bin"
    [ -x "$comfy_bin" ] || fail "comfy executable not found. Run scripts/install-prerequisites.sh first: $comfy_bin"

    comfy_project="$(repo_path "$COMFY_PROJECT_REL")"
    local_home="$(repo_path "$LOCAL_HOME_REL")"
    local_config="$(repo_path "$LOCAL_CONFIG_REL")"
    local_cache="$(repo_path "$LOCAL_CACHE_REL")"
    local_data="$(repo_path "$LOCAL_DATA_REL")"

    ensure_repo_dir "$STATE_DIR_REL"
    run_background_in_dir "$comfy_project" comfyui env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" COMFY_WHERE=local "$comfy_bin" --where local launch
    wait_for_backend comfyui
}

start_ace_step() {
    local ace_project
    local local_home
    local local_config
    local local_cache
    local local_data
    local uv_cache
    local pid

    if backend_healthy ace-step; then
        ok "ace-step already responds at $ACE_STEP_URL"
        return 0
    fi
    pid="$(managed_pid ace-step || true)"
    if [ -n "$pid" ]; then
        ok "ace-step already managed with pid $pid"
        return 0
    fi

    have uv || fail "uv not found. Run scripts/install-prerequisites.sh first."
    require_repo_dir "$ACE_STEP_PROJECT_REL"
    ensure_repo_dir "$LOCAL_HOME_REL"
    ensure_repo_dir "$LOCAL_CONFIG_REL"
    ensure_repo_dir "$LOCAL_CACHE_REL"
    ensure_repo_dir "$LOCAL_DATA_REL"
    ensure_repo_dir "$UV_CACHE_REL"

    ace_project="$(repo_path "$ACE_STEP_PROJECT_REL")"
    local_home="$(repo_path "$LOCAL_HOME_REL")"
    local_config="$(repo_path "$LOCAL_CONFIG_REL")"
    local_cache="$(repo_path "$LOCAL_CACHE_REL")"
    local_data="$(repo_path "$LOCAL_DATA_REL")"
    uv_cache="$(repo_path "$UV_CACHE_REL")"

    ensure_repo_dir "$STATE_DIR_REL"
    run_background_in_dir "$ace_project" ace-step env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" UV_CACHE_DIR="$uv_cache" ACESTEP_API_HOST=127.0.0.1 ACESTEP_API_PORT=8001 ACESTEP_TMPDIR=.cache/acestep/tmp TRITON_CACHE_DIR=.cache/acestep/triton TORCHINDUCTOR_CACHE_DIR=.cache/acestep/torchinductor uv run acestep-api
    wait_for_backend ace-step
}

start_backend() {
    case "$1" in
        ollama) start_ollama ;;
        comfyui) start_comfyui ;;
        ace-step) start_ace_step ;;
        *) fail "Unknown backend: $1" ;;
    esac
}

stop_backend() {
    local backend="$1"
    local rel
    local pid
    local elapsed=0

    rel="$(pid_rel "$backend")"
    pid="$(read_pid "$rel" || true)"
    if [ -z "$pid" ]; then
        if backend_healthy "$backend"; then
            warn "$backend is responding at $(backend_url "$backend"), but it was not started by scripts/backends.sh; leaving it running"
        else
            ok "$backend is not running"
        fi
        return 0
    fi
    if ! pid_running "$pid"; then
        warn "$backend pid file is stale: $pid"
        clear_pid "$rel"
        return 0
    fi
    if ! pid_matches_backend "$backend" "$pid"; then
        fail "Refusing to stop pid $pid because it does not look like $backend: $(process_command "$pid")"
    fi

    run_cmd kill "$pid"
    if [ "$DRY_RUN" -eq 1 ]; then
        return 0
    fi

    while [ "$elapsed" -lt "$STOP_WAIT_SECONDS" ]; do
        if ! pid_running "$pid"; then
            clear_pid "$rel"
            ok "$backend stopped"
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    warn "$backend pid $pid is still running after TERM; leaving pid file intact"
}

show_status() {
    local backend="$1"
    local rel
    local pid
    local endpoint="down"
    local managed="not-managed"
    local command=""

    rel="$(pid_rel "$backend")"
    if backend_healthy "$backend"; then
        endpoint="healthy"
    fi

    pid="$(read_pid "$rel" || true)"
    if [ -n "$pid" ]; then
        if pid_running "$pid"; then
            if pid_matches_backend "$backend" "$pid"; then
                managed="managed pid=$pid"
            else
                managed="pid-mismatch pid=$pid"
            fi
            command="$(process_command "$pid")"
        else
            managed="stale pid=$pid"
        fi
    fi

    printf "%-8s endpoint=%-7s url=%s state=%s\n" "$backend" "$endpoint" "$(backend_url "$backend")" "$managed"
    if [ -n "$command" ]; then
        printf "         command=%s\n" "$command"
    fi
    printf "         log=%s\n" "$(repo_path "$(log_rel "$backend")")"
}

show_logs() {
    local backend="$1"
    local lines="$2"
    local follow="$3"
    local rel
    local path

    rel="$(log_rel "$backend")"
    path="$(repo_path "$rel")"
    [ ! -L "$path" ] || fail "Refusing to read symlinked log file: $path"
    if [ ! -f "$path" ]; then
        warn "No log file for $backend yet: $path"
        return 0
    fi
    if [ "$follow" -eq 1 ]; then
        run_cmd tail -n "$lines" -f "$path"
    else
        printf "%b==> %s <==%b\n" "$CYAN" "$backend" "$NC"
        run_cmd tail -n "$lines" "$path"
    fi
}

normalize_target() {
    case "${1:-all}" in
        all|ollama|comfyui) printf "%s\n" "${1:-all}" ;;
        ace-step|acestep|ace_step) printf "ace-step\n" ;;
        *) fail "Unknown backend target: $1" ;;
    esac
}

for_target() {
    local target="$1"
    local action="$2"

    case "$target" in
        all)
            "$action" ollama
            "$action" comfyui
            "$action" ace-step
            ;;
        ollama|comfyui|ace-step)
            "$action" "$target"
            ;;
        *)
            fail "Unknown backend target: $target"
            ;;
    esac
}

parse_target_command() {
    local raw_target="${1:-all}"
    local target

    target="$(normalize_target "$raw_target")"
    for_target "$target" "$2"
}

parse_logs() {
    local target="all"
    local lines="80"
    local follow=0

    while [ "$#" -gt 0 ]; do
        case "$1" in
            -f|--follow)
                follow=1
                ;;
            -n|--lines)
                shift
                [ "$#" -gt 0 ] || fail "--lines requires a value"
                case "$1" in
                    ""|*[!0-9]*) fail "--lines must be a non-negative integer" ;;
                esac
                lines="$1"
                ;;
            all|ollama|comfyui)
                target="$1"
                ;;
            ace-step|acestep|ace_step)
                target="ace-step"
                ;;
            *)
                fail "Unknown logs option or target: $1"
                ;;
        esac
        shift
    done

    if [ "$follow" -eq 1 ] && [ "$target" = "all" ]; then
        fail "logs --follow requires target ollama, comfyui, or ace-step"
    fi
    case "$target" in
        all)
            show_logs ollama "$lines" 0
            show_logs comfyui "$lines" 0
            show_logs ace-step "$lines" 0
            ;;
        ollama|comfyui|ace-step)
            show_logs "$target" "$lines" "$follow"
            ;;
    esac
}

case "$COMMAND" in
    start|up)
        [ "$#" -le 1 ] || fail "start accepts at most one target"
        parse_target_command "${1:-all}" start_backend
        ;;
    stop|down)
        [ "$#" -le 1 ] || fail "stop accepts at most one target"
        parse_target_command "${1:-all}" stop_backend
        ;;
    restart)
        [ "$#" -le 1 ] || fail "restart accepts at most one target"
        target="$(normalize_target "${1:-all}")"
        for_target "$target" stop_backend
        for_target "$target" start_backend
        ;;
    status|ps)
        [ "$#" -le 1 ] || fail "status accepts at most one target"
        parse_target_command "${1:-all}" show_status
        ;;
    logs)
        parse_logs "$@"
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        fail "Unknown command: $COMMAND"
        ;;
esac
