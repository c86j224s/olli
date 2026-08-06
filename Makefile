.PHONY: build run test clean cross agy antigravity help

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

agy antigravity:
	@./agy-safe.sh

help:
	@echo "Available make targets:"
	@echo "  make build  - Run tests and compile binary"
	@echo "  make run    - Run application directly (go run)"
	@echo "  make test   - Run unit tests"
	@echo "  make cross  - Cross-compile for macOS, Linux, and Windows"
	@echo "  make agy    - Launch Antigravity CLI in repo sandbox mode"
	@echo "  make clean  - Remove build binaries"
