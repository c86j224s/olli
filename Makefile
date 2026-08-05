.PHONY: build run test clean cross help

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

help:
	@echo "Available make targets:"
	@echo "  make build  - Run tests and compile binary"
	@echo "  make run    - Run application directly (go run)"
	@echo "  make test   - Run unit tests"
	@echo "  make cross  - Cross-compile for macOS, Linux, and Windows"
	@echo "  make clean  - Remove build binaries"
