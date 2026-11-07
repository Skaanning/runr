VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2

.PHONY: build test lint staticcheck cross clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/runr ./cmd/runr

test:
	go test ./...

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	@$(MAKE) --no-print-directory staticcheck

# Installed into bin/ (ignored by git) so it can run for every GOOS.
staticcheck:
	@GOBIN=$(CURDIR)/bin go install $(STATICCHECK)
	@for os in linux windows darwin; do \
		echo "staticcheck GOOS=$$os"; GOOS=$$os bin/staticcheck ./... || exit 1; \
	done

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/runr-$$os-$$arch$$ext ./cmd/runr || exit 1; \
	done

clean:
	rm -rf bin dist
