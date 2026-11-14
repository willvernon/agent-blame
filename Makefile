STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.PHONY: check fmt vet lint test build install golden clean

check: fmt vet lint test

fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

lint:
	go run $(STATICCHECK) ./...

test:
	go test -race ./...

build:
	go build -ldflags "$(LDFLAGS)" -o bin/agent-blame ./cmd/agent-blame

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/agent-blame

golden:
	UPDATE_GOLDEN=1 go test ./...

clean:
	rm -rf bin dist
