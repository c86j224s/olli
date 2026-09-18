.PHONY: build run test vet test-safety sandbox-smoke macos-security-integration desktop desktop-clean clean cross prerequisites prereq backends backend-status stop-backends agy antigravity help

APP_NAME=olli
BUILD_DIR=./bin

all: build

build:
	@./build.sh build

run:
	@./build.sh run

test:
	@./build.sh test

vet:
	@./scripts/safe-vet ./...

test-safety:
	@./scripts/check-test-safety

sandbox-smoke:
	@./scripts/safe-exec-test

macos-security-integration:
	@./scripts/verify-macos-sandbox-integration

desktop:
	@./scripts/build-macos-app build

desktop-clean:
	@./scripts/build-macos-app clean

clean:
	@./build.sh clean

cross:
	@./build.sh cross

prerequisites prereq:
	@./scripts/install-prerequisites.sh

backends:
	@./scripts/backends.sh start

backend-status:
	@./scripts/backends.sh status

stop-backends:
	@./scripts/backends.sh stop

agy antigravity:
	@./agy-safe.sh

help:
	@echo "Available make targets:"
	@echo "  make build  - Verify tests and compilation in a disposable macOS sandbox"
	@echo "  make run    - Refuse host execution; use a dedicated disposable VM"
	@echo "  make test   - Run unit tests in a disposable macOS sandbox"
	@echo "  make vet    - Run go vet in a disposable macOS sandbox"
	@echo "  make test-safety - Scan tests for high-blast-radius patterns"
	@echo "  make sandbox-smoke - Verify checkout, external-read/write, and network boundaries"
	@echo "  make macos-security-integration - Print VM-only integration instructions and refuse host execution"
	@echo "  make desktop - Build the unsigned local macOS SwiftUI app bundle"
	@echo "  make desktop-clean - Remove the local macOS app bundle"
	@echo "  make cross  - Refused until platform sandboxes are implemented"
	@echo "  make prereq - Install/check local prerequisites"
	@echo "  make backends - Start Ollama, ComfyUI, and ACE-Step backends"
	@echo "  make backend-status - Show backend status"
	@echo "  make stop-backends - Stop managed backends"
	@echo "  make agy    - Launch Antigravity CLI in repo sandbox mode"
	@echo "  make clean  - Remove build binaries"
