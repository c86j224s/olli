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

YES=0
DRY_RUN=0
CHECK_ONLY=0
INSTALL_SYSTEM=1
INSTALL_OLLAMA=1
INSTALL_COMFYUI=1
INSTALL_ACE_STEP=1
OLLAMA_METHOD="auto"
COMFY_CLI_VENV_REL=".local/comfy-cli-venv"
COMFY_PROJECT_REL=".local/comfyui"
ACE_STEP_PROJECT_REL=".local/ace-step"
ACE_STEP_REPO_URL="https://github.com/ACE-Step/ACE-Step-1.5.git"
LOCAL_HOME_REL=".local/home"
LOCAL_CONFIG_REL=".local/xdg-config"
LOCAL_CACHE_REL=".local/xdg-cache"
LOCAL_DATA_REL=".local/xdg-data"
PIP_CACHE_REL=".local/pip-cache"
UV_CACHE_REL=".local/uv-cache"
INSTALL_CACHE_REL=".local/install-cache"

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
Usage: scripts/install-prerequisites.sh [options]

Installs/checks prerequisites for O.L.L.I. plus local ComfyUI image generation and ACE-Step audio generation.

Options:
  -y, --yes                 Run without interactive confirmations.
      --dry-run             Print commands without executing them.
      --check-only          Only report installed/missing tools.
      --skip-system         Do not install system packages such as git/go/python.
      --skip-ollama         Do not install Ollama.
      --skip-comfyui        Do not install comfy-cli or ComfyUI.
      --skip-ace-step       Do not install ACE-Step.
      --ollama-method MODE  auto, brew, install-sh, or manual. Default: auto.
      --comfy-dir PATH      Repo-relative ComfyUI project dir. Default: .local/comfyui.
      --comfy-venv PATH     Repo-relative comfy-cli venv dir. Default: .local/comfy-cli-venv.
      --ace-step-dir PATH   Repo-relative ACE-Step project dir. Default: .local/ace-step.
  -h, --help                Show this help.

Notes:
  - The script never deletes files.
  - ComfyUI and ACE-Step are installed under the repository by default.
  - If python3 is missing or too old, Python >= 3.10 discovered through uv is used.
  - ACE-Step requires uv and Python >= 3.11,<3.13.
  - Linux Ollama install uses the official installer downloaded to .local/install-cache first.
USAGE
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        -y|--yes)
            YES=1
            ;;
        --dry-run)
            DRY_RUN=1
            ;;
        --check-only)
            CHECK_ONLY=1
            ;;
        --skip-system)
            INSTALL_SYSTEM=0
            ;;
        --skip-ollama)
            INSTALL_OLLAMA=0
            ;;
        --skip-comfyui)
            INSTALL_COMFYUI=0
            ;;
        --skip-ace-step)
            INSTALL_ACE_STEP=0
            ;;
        --ollama-method)
            shift
            [ "$#" -gt 0 ] || fail "--ollama-method requires a value"
            OLLAMA_METHOD="$1"
            case "$OLLAMA_METHOD" in
                auto|brew|install-sh|manual) ;;
                *) fail "--ollama-method must be one of: auto, brew, install-sh, manual" ;;
            esac
            ;;
        --comfy-dir)
            shift
            [ "$#" -gt 0 ] || fail "--comfy-dir requires a value"
            COMFY_PROJECT_REL="$1"
            ;;
        --comfy-venv)
            shift
            [ "$#" -gt 0 ] || fail "--comfy-venv requires a value"
            COMFY_CLI_VENV_REL="$1"
            ;;
        --ace-step-dir)
            shift
            [ "$#" -gt 0 ] || fail "--ace-step-dir requires a value"
            ACE_STEP_PROJECT_REL="$1"
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            fail "Unknown option: $1"
            ;;
    esac
    shift
done

cd "$REPO_ROOT"

[ -f "$REPO_ROOT/go.mod" ] || fail "Refusing to run outside the repository root: $REPO_ROOT"
[ -f "$REPO_ROOT/main.go" ] || fail "Refusing to run outside the repository root: $REPO_ROOT"

quote_cmd() {
    local arg
    for arg in "$@"; do
        printf " %q" "$arg"
    done
}

run() {
    printf "%b[cmd]%b" "$CYAN" "$NC"
    quote_cmd "$@"
    printf "\n"
    if [ "$DRY_RUN" -eq 0 ]; then
        "$@"
    fi
}

run_in_dir() {
    local dir="$1"
    shift
    printf "%b[cmd]%b cd" "$CYAN" "$NC"
    quote_cmd "$dir"
    printf " &&"
    quote_cmd "$@"
    printf "\n"
    if [ "$DRY_RUN" -eq 0 ]; then
        (
            cd "$dir"
            "$@"
        )
    fi
}

confirm() {
    local prompt="$1"
    local answer

    if [ "$CHECK_ONLY" -eq 1 ]; then
        return 1
    fi
    if [ "$YES" -eq 1 ]; then
        return 0
    fi
    if [ ! -t 0 ]; then
        warn "Skipping because stdin is not interactive: $prompt"
        return 1
    fi

    printf "%s [y/N] " "$prompt"
    read -r answer
    case "$answer" in
        y|Y|yes|YES) return 0 ;;
        *) return 1 ;;
    esac
}

have() {
    command -v "$1" >/dev/null 2>&1
}

required_go_version() {
    local key
    local value

    while read -r key value _; do
        if [ "$key" = "go" ]; then
            printf "%s\n" "$value"
            return 0
        fi
    done < "$REPO_ROOT/go.mod"

    return 1
}

normalize_version_part() {
    local value="$1"
    value="${value%%[^0-9]*}"
    if [ -z "$value" ]; then
        printf "0\n"
    else
        printf "%s\n" "$value"
    fi
}

version_ge() {
    local current="$1"
    local required="$2"
    local c_major c_minor c_patch
    local r_major r_minor r_patch

    IFS=. read -r c_major c_minor c_patch _ <<< "$current"
    IFS=. read -r r_major r_minor r_patch _ <<< "$required"

    c_major="$(normalize_version_part "${c_major:-0}")"
    c_minor="$(normalize_version_part "${c_minor:-0}")"
    c_patch="$(normalize_version_part "${c_patch:-0}")"
    r_major="$(normalize_version_part "${r_major:-0}")"
    r_minor="$(normalize_version_part "${r_minor:-0}")"
    r_patch="$(normalize_version_part "${r_patch:-0}")"

    if [ "$c_major" -gt "$r_major" ]; then return 0; fi
    if [ "$c_major" -lt "$r_major" ]; then return 1; fi
    if [ "$c_minor" -gt "$r_minor" ]; then return 0; fi
    if [ "$c_minor" -lt "$r_minor" ]; then return 1; fi
    [ "$c_patch" -ge "$r_patch" ]
}

go_current_version() {
    local raw

    if ! have go; then
        return 1
    fi

    raw="$(go env GOVERSION 2>/dev/null || true)"
    if [ -z "$raw" ]; then
        raw="$(go version 2>/dev/null || true)"
        raw="${raw#go version go}"
        raw="${raw%% *}"
    fi
    raw="${raw#go}"
    [ -n "$raw" ] || return 1
    printf "%s\n" "$raw"
}

go_ready() {
    local current
    local required

    current="$(go_current_version || true)"
    required="$(required_go_version || true)"
    [ -n "$current" ] || return 1
    [ -n "$required" ] || return 1
    version_ge "$current" "$required"
}

PYTHON_VERSION_REQUEST=">=3.10"
ACE_STEP_PYTHON_VERSION_REQUEST=">=3.11,<3.13"

python_version() {
    local python_cmd="$1"

    "$python_cmd" -c 'import sys; print(".".join(map(str, sys.version_info[:3])))' 2>/dev/null
}

python_version_ready() {
    local python_cmd="$1"

    "$python_cmd" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)' >/dev/null 2>&1
}

python_venv_ready() {
    local python_cmd="$1"

    "$python_cmd" -m venv --help >/dev/null 2>&1
}

system_python_cmd() {
    local candidate

    for candidate in python3 python; do
        if have "$candidate" && python_version_ready "$candidate" && python_venv_ready "$candidate"; then
            printf "%s\n" "$candidate"
            return 0
        fi
    done

    return 1
}

uv_python_cmd() {
    local candidate

    have uv || return 1
    candidate="$(uv python find "$PYTHON_VERSION_REQUEST" 2>/dev/null || true)"
    [ -n "$candidate" ] || return 1
    python_version_ready "$candidate" || return 1
    printf "%s\n" "$candidate"
}

python_provider() {
    if system_python_cmd >/dev/null; then
        printf "system\n"
        return 0
    fi
    if uv_python_cmd >/dev/null; then
        printf "uv\n"
        return 0
    fi
    return 1
}

selected_python_cmd() {
    system_python_cmd || uv_python_cmd
}

python_ready() {
    python_provider >/dev/null
}

ace_step_python_version_ready() {
    local python_cmd="$1"

    "$python_cmd" -c 'import sys; raise SystemExit(0 if (3, 11) <= sys.version_info[:2] < (3, 13) else 1)' >/dev/null 2>&1
}

ace_step_python_cmd() {
    local candidate

    have uv || return 1
    candidate="$(uv python find "$ACE_STEP_PYTHON_VERSION_REQUEST" 2>/dev/null || true)"
    [ -n "$candidate" ] || return 1
    ace_step_python_version_ready "$candidate" || return 1
    printf "%s\n" "$candidate"
}

ace_step_python_ready() {
    ace_step_python_cmd >/dev/null
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
            run mkdir -- "$cur"
        fi
    done
}

ensure_repo_parent_dir() {
    local rel="$1"
    local parent_rel

    validate_repo_relative_path "$rel"
    parent_rel="${rel%/*}"
    if [ "$parent_rel" != "$rel" ]; then
        ensure_repo_dir "$parent_rel"
    fi
}

print_detected_status() {
    local required
    local current

    required="$(required_go_version || true)"
    current="$(go_current_version || true)"

    info "Repository: $REPO_ROOT"
    info "OS: $(uname -s)"

    if have git; then ok "git found"; else warn "git missing"; fi
    if have curl; then ok "curl found"; else warn "curl missing"; fi
    if have uv; then ok "uv found"; else warn "uv missing"; fi
    if go_ready; then
        ok "Go $current satisfies go.mod requirement $required"
    else
        warn "Go missing or older than go.mod requirement $required (current: ${current:-not found})"
    fi
    case "$(python_provider || true)" in
        system)
            ok "Python $(python_version "$(system_python_cmd)") with venv support found"
            ;;
        uv)
            ok "Python $(python_version "$(uv_python_cmd)") found through uv"
            ;;
        *)
            warn "python3 >= 3.10 with venv support or Python >= 3.10 discoverable through uv missing"
            ;;
    esac
    if ace_step_python_ready; then
        ok "Python $(python_version "$(ace_step_python_cmd)") for ACE-Step found through uv"
    else
        warn "ACE-Step Python $ACE_STEP_PYTHON_VERSION_REQUEST discoverable through uv missing"
    fi
    if have ollama; then ok "ollama found"; else warn "ollama missing"; fi
}

install_system_packages_macos() {
    local packages=()

    have brew || {
        warn "Homebrew not found. Install Homebrew first or install git/go/python manually."
        return 0
    }

    have git || packages+=("git")
    have curl || packages+=("curl")
    go_ready || packages+=("go")
    python_ready || packages+=("python@3.12")

    if [ "${#packages[@]}" -eq 0 ]; then
        ok "System prerequisites already look installed"
        return 0
    fi

    if confirm "Install missing system packages with Homebrew: ${packages[*]}?"; then
        run brew install "${packages[@]}"
    else
        warn "Skipped Homebrew system package installation"
    fi
}

install_system_packages_linux() {
    local packages=()

    if have apt-get; then
        packages=(ca-certificates curl git)
        go_ready || packages+=(golang-go)
        python_ready || packages+=(python3 python3-venv python3-pip)
        if confirm "Install missing system packages with apt-get?"; then
            run sudo apt-get update
            run sudo apt-get install -y "${packages[@]}"
        else
            warn "Skipped apt-get system package installation"
        fi
        return 0
    fi

    if have dnf; then
        packages=(ca-certificates curl git)
        go_ready || packages+=(golang)
        python_ready || packages+=(python3 python3-pip)
        if confirm "Install missing system packages with dnf?"; then
            run sudo dnf install -y "${packages[@]}"
        else
            warn "Skipped dnf system package installation"
        fi
        return 0
    fi

    if have pacman; then
        packages=(ca-certificates curl git)
        go_ready || packages+=(go)
        python_ready || packages+=(python python-pip)
        if confirm "Install missing system packages with pacman?"; then
            run sudo pacman -Sy --needed "${packages[@]}"
        else
            warn "Skipped pacman system package installation"
        fi
        return 0
    fi

    warn "No supported Linux package manager found. Install git, curl, Go, python3, venv, and pip manually."
}

install_system_packages() {
    [ "$INSTALL_SYSTEM" -eq 1 ] || {
        info "Skipping system package installation"
        return 0
    }
    [ "$CHECK_ONLY" -eq 0 ] || return 0

    case "$(uname -s)" in
        Darwin) install_system_packages_macos ;;
        Linux) install_system_packages_linux ;;
        *) warn "Unsupported OS for automatic system package installation: $(uname -s)" ;;
    esac
}

install_ollama_from_script() {
    local install_cache_dir
    local installer

    have curl || fail "curl is required to download the Ollama installer"

    if confirm "Download and run the official Ollama installer from https://ollama.com/install.sh?"; then
        ensure_repo_dir "$INSTALL_CACHE_REL"
        install_cache_dir="$(repo_path "$INSTALL_CACHE_REL")"
        installer="$install_cache_dir/ollama-install.sh"
        run curl -fsSL --proto '=https' --tlsv1.2 https://ollama.com/install.sh -o "$installer"
        run chmod 0755 "$installer"
        run sh "$installer"
        ok "Ollama installer finished"
    else
        warn "Skipped Ollama install script"
    fi
}

install_ollama_macos() {
    case "$OLLAMA_METHOD" in
        auto|brew)
            if have brew; then
                if confirm "Install Ollama app with Homebrew cask ollama-app?"; then
                    run brew install --cask ollama-app
                    ok "Ollama app installation command finished"
                else
                    warn "Skipped Ollama Homebrew cask installation"
                fi
            elif [ "$OLLAMA_METHOD" = "brew" ]; then
                fail "Homebrew is required for --ollama-method brew"
            else
                warn "Homebrew not found. Install Ollama manually from https://ollama.com/download/mac or rerun with --ollama-method install-sh."
            fi
            ;;
        install-sh)
            install_ollama_from_script
            ;;
        manual)
            warn "Manual Ollama mode selected. Install from https://ollama.com/download."
            ;;
    esac
}

install_ollama_linux() {
    case "$OLLAMA_METHOD" in
        auto|install-sh)
            install_ollama_from_script
            ;;
        brew)
            have brew || fail "Homebrew is required for --ollama-method brew"
            if confirm "Install Ollama with Homebrew?"; then
                run brew install ollama
            else
                warn "Skipped Ollama Homebrew installation"
            fi
            ;;
        manual)
            warn "Manual Ollama mode selected. Install from https://ollama.com/download."
            ;;
    esac
}

install_ollama() {
    [ "$INSTALL_OLLAMA" -eq 1 ] || {
        info "Skipping Ollama installation"
        return 0
    }
    [ "$CHECK_ONLY" -eq 0 ] || return 0

    if have ollama; then
        ok "Ollama already installed"
        return 0
    fi

    case "$(uname -s)" in
        Darwin) install_ollama_macos ;;
        Linux) install_ollama_linux ;;
        *) warn "Unsupported OS for automatic Ollama installation: $(uname -s)" ;;
    esac
}

install_comfyui() {
    local comfy_venv
    local comfy_project
    local local_home
    local local_config
    local local_cache
    local local_data
    local pip_cache
    local uv_cache
    local comfy_bin
    local python_bin
    local provider
    local python_cmd

    [ "$INSTALL_COMFYUI" -eq 1 ] || {
        info "Skipping ComfyUI installation"
        return 0
    }
    [ "$CHECK_ONLY" -eq 0 ] || return 0

    if confirm "Install/upgrade comfy-cli and local ComfyUI under $COMFY_PROJECT_REL?"; then
        provider="$(python_provider || true)"
        [ -n "$provider" ] || fail "python3 >= 3.10 with venv support or Python >= 3.10 discoverable through uv is required for comfy-cli"
        python_cmd="$(selected_python_cmd)"

        ensure_repo_dir "$COMFY_CLI_VENV_REL"
        ensure_repo_dir "$COMFY_PROJECT_REL"
        ensure_repo_dir "$LOCAL_HOME_REL"
        ensure_repo_dir "$LOCAL_CONFIG_REL"
        ensure_repo_dir "$LOCAL_CACHE_REL"
        ensure_repo_dir "$LOCAL_DATA_REL"
        ensure_repo_dir "$PIP_CACHE_REL"
        ensure_repo_dir "$UV_CACHE_REL"
        ensure_repo_dir "workflows/comfyui"

        comfy_venv="$(repo_path "$COMFY_CLI_VENV_REL")"
        comfy_project="$(repo_path "$COMFY_PROJECT_REL")"
        local_home="$(repo_path "$LOCAL_HOME_REL")"
        local_config="$(repo_path "$LOCAL_CONFIG_REL")"
        local_cache="$(repo_path "$LOCAL_CACHE_REL")"
        local_data="$(repo_path "$LOCAL_DATA_REL")"
        pip_cache="$(repo_path "$PIP_CACHE_REL")"
        uv_cache="$(repo_path "$UV_CACHE_REL")"
        python_bin="$comfy_venv/bin/python"
        comfy_bin="$comfy_venv/bin/comfy"

        if [ "$provider" = "uv" ]; then
            run env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" UV_CACHE_DIR="$uv_cache" uv venv "$comfy_venv" --python "$python_cmd"
            run env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" UV_CACHE_DIR="$uv_cache" VIRTUAL_ENV="$comfy_venv" uv pip install --python "$comfy_venv" --upgrade comfy-cli
        else
            run "$python_cmd" -m venv "$comfy_venv"
            run env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" PIP_CACHE_DIR="$pip_cache" "$python_bin" -m pip install --upgrade pip
            run env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" PIP_CACHE_DIR="$pip_cache" "$python_bin" -m pip install --upgrade comfy-cli
        fi
        run env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" COMFY_WHERE=local "$comfy_bin" setup --where local --project-dir "$comfy_project" --non-interactive --skip-skills --skip-verify
        run_in_dir "$comfy_project" env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" COMFY_WHERE=local "$comfy_bin" install
        ok "ComfyUI installation command finished"
    else
        warn "Skipped ComfyUI installation"
    fi
}

install_ace_step() {
    local ace_project
    local local_home
    local local_config
    local local_cache
    local local_data
    local uv_cache
    local python_cmd

    [ "$INSTALL_ACE_STEP" -eq 1 ] || {
        info "Skipping ACE-Step installation"
        return 0
    }
    [ "$CHECK_ONLY" -eq 0 ] || return 0

    if confirm "Install/sync ACE-Step under $ACE_STEP_PROJECT_REL?"; then
        have git || fail "git is required to clone ACE-Step"
        have uv || fail "uv is required to install ACE-Step"

        ensure_repo_parent_dir "$ACE_STEP_PROJECT_REL"
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

        python_cmd="$(env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" UV_CACHE_DIR="$uv_cache" uv python find "$ACE_STEP_PYTHON_VERSION_REQUEST" 2>/dev/null || true)"
        if [ -z "$python_cmd" ]; then
            if confirm "Install Python 3.12 with uv for ACE-Step?"; then
                run env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" UV_CACHE_DIR="$uv_cache" uv python install 3.12
                if [ "$DRY_RUN" -eq 1 ]; then
                    python_cmd="python3.12"
                else
                    python_cmd="$(env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" UV_CACHE_DIR="$uv_cache" uv python find "$ACE_STEP_PYTHON_VERSION_REQUEST" 2>/dev/null || true)"
                fi
            fi
        fi
        [ -n "$python_cmd" ] || fail "ACE-Step requires Python $ACE_STEP_PYTHON_VERSION_REQUEST discoverable through uv"

        if [ -e "$ace_project" ]; then
            [ ! -L "$ace_project" ] || fail "Refusing to use symlinked ACE-Step directory: $ace_project"
            [ -d "$ace_project" ] || fail "Refusing to use non-directory ACE-Step path: $ace_project"
        else
            run env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 git -c protocol.file.allow=never -c protocol.ext.allow=never clone --depth 1 "$ACE_STEP_REPO_URL" "$ace_project"
        fi

        run_in_dir "$ace_project" env HOME="$local_home" XDG_CONFIG_HOME="$local_config" XDG_CACHE_HOME="$local_cache" XDG_DATA_HOME="$local_data" UV_CACHE_DIR="$uv_cache" uv sync --python "$python_cmd"
        ok "ACE-Step installation command finished"
    else
        warn "Skipped ACE-Step installation"
    fi
}

print_next_steps() {
    local comfy_bin
    local comfy_project
    local current
    local required

    comfy_bin="$(repo_path "$COMFY_CLI_VENV_REL")/bin/comfy"
    comfy_project="$(repo_path "$COMFY_PROJECT_REL")"
    current="$(go_current_version || true)"
    required="$(required_go_version || true)"

    printf "\n%bNext steps%b\n" "$CYAN" "$NC"
    printf "  1. Start Ollama if it is not already running:\n"
    printf "     ollama serve\n"
    printf "  2. Launch ComfyUI:\n"
    printf "     %q --where local launch\n" "$comfy_bin"
    printf "  3. Open ComfyUI at http://127.0.0.1:8188 and place models under:\n"
    printf "     %s/models/checkpoints\n" "$comfy_project"
    printf "  4. Export an API workflow JSON into workflows/comfyui/ and register it in config.json.\n"
    printf "  5. Start managed local backends when needed:\n"
    printf "     ./scripts/backends.sh start\n"
    printf "  6. ACE-Step API runs at http://127.0.0.1:8001 after running: ./scripts/backends.sh start ace-step\n"

    if ! go_ready; then
        warn "Go still does not satisfy go.mod requirement $required (current: ${current:-not found}). Install a compatible Go version before building O.L.L.I."
    fi
}

printf "%b==========================================%b\n" "$CYAN" "$NC"
printf "%b   O.L.L.I. prerequisite installer       %b\n" "$CYAN" "$NC"
printf "%b==========================================%b\n" "$CYAN" "$NC"

validate_repo_relative_path "$COMFY_CLI_VENV_REL"
validate_repo_relative_path "$COMFY_PROJECT_REL"
validate_repo_relative_path "$ACE_STEP_PROJECT_REL"

print_detected_status
install_system_packages
install_ollama
install_comfyui
install_ace_step
print_next_steps
