#!/usr/bin/env bash
set -e

# Color definitions
CYAN='\033[036m'
GREEN='\033[032m'
YELLOW='\033[033m'
RED='\033[031m'
NC='\033[0m'

APP_NAME="olli"
BUILD_DIR="./bin"

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
        rm -rf "$BUILD_DIR" "$APP_NAME"
        echo -e "${GREEN}✅ Clean completed.${NC}"
        ;;

    run)
        echo -e "\n${YELLOW}🔨 Building fresh $APP_NAME binary...${NC}"
        mkdir -p "$BUILD_DIR"
        go build -ldflags="-s -w" -o "$BUILD_DIR/$APP_NAME" .
        cp "$BUILD_DIR/$APP_NAME" "./$APP_NAME"
        echo -e "\n${GREEN}🚀 Running ./$APP_NAME...${NC}"
        ./"$APP_NAME"
        ;;

    cross)
        echo -e "\n${YELLOW}🌐 Cross-compiling for multiple platforms...${NC}"
        mkdir -p "$BUILD_DIR"

        echo -e "  • Building for macOS (Darwin/ARM64)..."
        GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o "$BUILD_DIR/${APP_NAME}-darwin-arm64" .

        echo -e "  • Building for macOS (Darwin/AMD64)..."
        GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o "$BUILD_DIR/${APP_NAME}-darwin-amd64" .

        echo -e "  • Building for Linux (AMD64)..."
        GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$BUILD_DIR/${APP_NAME}-linux-amd64" .

        echo -e "  • Building for Windows (AMD64)..."
        GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o "$BUILD_DIR/${APP_NAME}-windows-amd64.exe" .

        echo -e "\n${GREEN}🎉 Cross-compilation completed! Binaries saved in $BUILD_DIR:${NC}"
        ls -lh "$BUILD_DIR"
        ;;

    build|*)
        echo -e "\n${YELLOW}🧪 Step 1: Running unit tests...${NC}"
        go test ./...

        echo -e "\n${YELLOW}🔨 Step 2: Building $APP_NAME binary...${NC}"
        mkdir -p "$BUILD_DIR"
        go build -ldflags="-s -w" -o "$BUILD_DIR/$APP_NAME" .
        cp "$BUILD_DIR/$APP_NAME" "./$APP_NAME"

        echo -e "\n${GREEN}🎉 Build successful! Binary created at ./$APP_NAME and $BUILD_DIR/$APP_NAME${NC}"
        ;;
esac
