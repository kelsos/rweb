BIN         := rweb
INSTALL_DIR ?= $(HOME)/.local/bin

.PHONY: build install vet test tidy clean hooks check

## build: compile the rweb binary into the repo root
build:
	go build -o $(BIN) ./cmd/rweb

## install: build into a user bin under $HOME (no sudo)
install:
	@mkdir -p $(INSTALL_DIR)
	go build -o $(INSTALL_DIR)/$(BIN) ./cmd/rweb
	@echo "installed $(INSTALL_DIR)/$(BIN)"

## vet: run go vet
vet:
	go vet ./...

## test: run unit tests
test:
	go test ./...

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy

## check: run the full pre-commit suite (fmt check, vet, build, test)
check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	go build ./...
	go test ./...

## hooks: enable the repo git hooks (pre-commit checks)
hooks:
	git config core.hooksPath .githooks
	@echo "git hooks enabled (core.hooksPath=.githooks)"

## clean: remove build artifacts
clean:
	rm -f $(BIN)
