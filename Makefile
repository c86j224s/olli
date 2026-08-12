.PHONY: build run test clean cross prerequisites prereq backends backend-status stop-backends agy antigravity help

APP_NAME=olli
BUILD_DIR=./bin

all: build

build:
	@./build.sh build

run:
	@./build.sh run

test:
	@./build.sh test

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
	@echo "  make build  - Run tests and compile binary"
	@echo "  make run    - Run application directly (go run)"
	@echo "  make test   - Run unit tests"
	@echo "  make cross  - Cross-compile for macOS, Linux, and Windows"
	@echo "  make prereq - Install/check local prerequisites"
	@echo "  make backends - Start Ollama, ComfyUI, and ACE-Step backends"
	@echo "  make backend-status - Show backend status"
	@echo "  make stop-backends - Stop managed backends"
	@echo "  make agy    - Launch Antigravity CLI in repo sandbox mode"
	@echo "  make clean  - Remove build binaries"
