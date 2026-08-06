#!/usr/bin/env bash
set -e

# Color definitions
CYAN='\033[036m'
GREEN='\033[032m'
YELLOW='\033[033m'
RED='\033[031m'
NC='\033[0m'

APP_NAME="olli"
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
BUILD_DIR="$SCRIPT_DIR/bin"
APP_PATH="$SCRIPT_DIR/$APP_NAME"
TMP_BUILD_OUTPUT=""

cd "$SCRIPT_DIR"

if [ ! -f "$SCRIPT_DIR/go.mod" ] || [ ! -f "$SCRIPT_DIR/main.go" ]; then
    echo -e "${RED}[Error] Refusing to run outside the repository root: $SCRIPT_DIR${NC}"
    exit 1
fi

safe_remove_repo_artifact() {
    local target="$1"
    local parent
    local parent_abs
    local target_abs

    parent="$(dirname -- "$target")"
    parent_abs="$(cd -- "$parent" && pwd -P)"
    target_abs="$parent_abs/$(basename -- "$target")"

    case "$target_abs" in
        "$SCRIPT_DIR/bin"|"$SCRIPT_DIR/olli")
            rm -rf -- "$target_abs"
            ;;
        *)
            echo -e "${RED}[Error] Refusing to remove path outside repo build artifacts: $target_abs${NC}"
            exit 1
            ;;
    esac
}

cleanup_tmp_output() {
    if [ -n "${TMP_BUILD_OUTPUT:-}" ] && [ -e "$TMP_BUILD_OUTPUT" ]; then
        case "$TMP_BUILD_OUTPUT" in
            "$SCRIPT_DIR/.olli.tmp."*|"$SCRIPT_DIR/bin/.build.tmp."*)
                rm -f -- "$TMP_BUILD_OUTPUT"
                ;;
        esac
    fi
}
trap cleanup_tmp_output EXIT

ensure_build_dir() {
    if [ -L "$BUILD_DIR" ]; then
        echo -e "${RED}[Error] Refusing to use symlinked build dir: $BUILD_DIR${NC}"
        exit 1
    fi
    if [ -e "$BUILD_DIR" ] && [ ! -d "$BUILD_DIR" ]; then
        echo -e "${RED}[Error] Refusing to use non-directory build path: $BUILD_DIR${NC}"
        exit 1
    fi
    mkdir -p "$BUILD_DIR"

    local build_abs
    build_abs="$(cd -P -- "$BUILD_DIR" && pwd)"
    if [ "$build_abs" != "$SCRIPT_DIR/bin" ]; then
        echo -e "${RED}[Error] Refusing build dir outside repository: $build_abs${NC}"
        exit 1
    fi
}

ensure_build_output_safe() {
    local target="$1"
    local parent_abs
    local target_abs

    ensure_build_dir
    parent_abs="$(cd -P -- "$(dirname -- "$target")" && pwd)"
    target_abs="$parent_abs/$(basename -- "$target")"
    if [ "$parent_abs" != "$SCRIPT_DIR/bin" ]; then
        echo -e "${RED}[Error] Refusing build output outside repo bin dir: $target_abs${NC}"
        exit 1
    fi
    if [ -L "$target_abs" ]; then
        echo -e "${RED}[Error] Refusing to overwrite symlinked build output: $target_abs${NC}"
        exit 1
    fi
    if [ -e "$target_abs" ] && [ ! -f "$target_abs" ]; then
        echo -e "${RED}[Error] Refusing to overwrite non-file build output: $target_abs${NC}"
        exit 1
    fi
}

ensure_app_path_safe() {
    local parent_abs
    local target_abs

    parent_abs="$(cd -P -- "$(dirname -- "$APP_PATH")" && pwd)"
    target_abs="$parent_abs/$(basename -- "$APP_PATH")"
    if [ "$target_abs" != "$SCRIPT_DIR/$APP_NAME" ]; then
        echo -e "${RED}[Error] Refusing app output outside repository: $target_abs${NC}"
        exit 1
    fi
    if [ -L "$target_abs" ]; then
        echo -e "${RED}[Error] Refusing to overwrite symlinked app output: $target_abs${NC}"
        exit 1
    fi
    if [ -e "$target_abs" ] && [ ! -f "$target_abs" ]; then
        echo -e "${RED}[Error] Refusing to overwrite non-file app output: $target_abs${NC}"
        exit 1
    fi
}

build_binary() {
    local output="$1"
    shift

    ensure_build_output_safe "$output"
    TMP_BUILD_OUTPUT="$(mktemp "$BUILD_DIR/.build.tmp.XXXXXX")"
    env "$@" go build -ldflags="-s -w" -o "$TMP_BUILD_OUTPUT" .
    ensure_build_output_safe "$output"
    mv -f -- "$TMP_BUILD_OUTPUT" "$output"
    TMP_BUILD_OUTPUT=""
}

copy_app_binary() {
    ensure_app_path_safe
    TMP_BUILD_OUTPUT="$(mktemp "$SCRIPT_DIR/.olli.tmp.XXXXXX")"
    cp "$BUILD_DIR/$APP_NAME" "$TMP_BUILD_OUTPUT"
    chmod 0755 "$TMP_BUILD_OUTPUT"
    ensure_app_path_safe
    mv -f -- "$TMP_BUILD_OUTPUT" "$APP_PATH"
    TMP_BUILD_OUTPUT=""
}

echo -e "${CYAN}==========================================${NC}"
echo -e "${CYAN}   🤖 Ollama Toy Agent Build Script       ${NC}"
echo -e "${CYAN}==========================================${NC}"

# Check Go installation
if ! command -v go &> /dev/null; then
    echo -e "${RED}[Error] Go is not installed or not in PATH.${NC}"
    exit 1
fi

ACTION="${1:-build}"

case "$ACTION" in
    test)
        echo -e "\n${YELLOW}🧪 Running unit tests...${NC}"
        go test -v ./...
        echo -e "${GREEN}✅ All tests passed!${NC}"
        ;;

    clean)
        echo -e "\n${YELLOW}🧹 Cleaning build artifacts...${NC}"
        safe_remove_repo_artifact "$BUILD_DIR"
        safe_remove_repo_artifact "$APP_PATH"
        echo -e "${GREEN}✅ Clean completed.${NC}"
        ;;

	run)
	    echo -e "\n${YELLOW}🔨 Building fresh $APP_NAME binary...${NC}"
	    build_binary "$BUILD_DIR/$APP_NAME"
	    copy_app_binary
	    echo -e "\n${GREEN}🚀 Running ./$APP_NAME...${NC}"
	    "$APP_PATH"
	    ;;

	cross)
	    echo -e "\n${YELLOW}🌐 Cross-compiling for multiple platforms...${NC}"
	    ensure_build_dir

	    echo -e "  • Building for macOS (Darwin/ARM64)..."
	    build_binary "$BUILD_DIR/${APP_NAME}-darwin-arm64" GOOS=darwin GOARCH=arm64

	    echo -e "  • Building for macOS (Darwin/AMD64)..."
	    build_binary "$BUILD_DIR/${APP_NAME}-darwin-amd64" GOOS=darwin GOARCH=amd64

	    echo -e "  • Building for Linux (AMD64)..."
	    build_binary "$BUILD_DIR/${APP_NAME}-linux-amd64" GOOS=linux GOARCH=amd64

	    echo -e "  • Building for Windows (AMD64)..."
	    build_binary "$BUILD_DIR/${APP_NAME}-windows-amd64.exe" GOOS=windows GOARCH=amd64

	    echo -e "\n${GREEN}🎉 Cross-compilation completed! Binaries saved in $BUILD_DIR:${NC}"
	    ls -lh "$BUILD_DIR"
        ;;

    build|*)
        echo -e "\n${YELLOW}🧪 Step 1: Running unit tests...${NC}"
	    go test ./...

	    echo -e "\n${YELLOW}🔨 Step 2: Building $APP_NAME binary...${NC}"
	    build_binary "$BUILD_DIR/$APP_NAME"
	    copy_app_binary

	    echo -e "\n${GREEN}🎉 Build successful! Binary created at ./$APP_NAME and $BUILD_DIR/$APP_NAME${NC}"
	    ;;
esac
