GO ?= go

.PHONY: run lint test tidy

# Pure adapter library (no binary) — `run` compiles all packages as a smoke check.
run:
	$(GO) build ./...

lint:
	$(GO) vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not installed — ran go vet only"; fi

test:
	$(GO) test ./...

tidy:
	$(GO) mod tidy
