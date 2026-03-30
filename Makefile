BIN_DIR := bin
BINARY  := lyra

# Detect OS: add .exe on Windows
ifeq ($(OS),Windows_NT)
    EXT := .exe
else
    EXT :=
endif

TARGET := $(BIN_DIR)/$(BINARY)$(EXT)

.PHONY: build clean run

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
