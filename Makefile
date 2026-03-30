BIN_DIR  := bin
BINARY   := lyra
TEST_SCRIPT := test/e2e.sh

# Detect OS: add .exe on Windows
ifeq ($(OS),Windows_NT)
    EXT  := .exe
    SHELL_CMD := bash
else
    EXT  :=
    SHELL_CMD := bash
endif

TARGET := $(BIN_DIR)/$(BINARY)$(EXT)

.PHONY: build clean run test test-ssh test-ftp test-cloud help

## build: compile and place binary in bin/
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags="-s -w" -o $(TARGET) .
	@echo "Built $(TARGET)"

## clean: remove the bin/ directory
clean:
	rm -rf $(BIN_DIR)

## run: build then run with provided ARGS (e.g. make run ARGS="ls .")
run: build
	./$(TARGET) $(ARGS)

## test: build then run local e2e tests
test: build
	$(SHELL_CMD) $(TEST_SCRIPT)

## test-ssh: build then run local + SSH e2e tests
test-ssh: build
	$(SHELL_CMD) $(TEST_SCRIPT) --ssh

## test-ftp: build then run local + FTP e2e tests
test-ftp: build
	$(SHELL_CMD) $(TEST_SCRIPT) --ftp

## test-cloud: build then run local + cloud e2e tests
test-cloud: build
	$(SHELL_CMD) $(TEST_SCRIPT) --cloud

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
