GO ?= go

.PHONY: test tidy

test:
	$(GO) test ./...

tidy:
	$(GO) mod tidy
