BINARY   := jmp
MODULE   := github.com/biyan113/jmp
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build clean install test lint man

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) .

# Cross-compile targets
build-all: build-darwin-amd64 build-darwin-arm64 build-linux-amd64 build-linux-arm64 build-windows-amd64

build-darwin-amd64:
	GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 .

build-darwin-arm64:
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 .

build-linux-amd64:
	GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 .

build-linux-arm64:
	GOOS=linux   GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-arm64 .

build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe .

install: build
	install -m 755 $(BINARY) /usr/local/bin/$(BINARY)

# Generate man pages (jmp.1, jmp-sync.1, ...) into man/
man:
	go run . man man

clean:
	rm -f $(BINARY)
	rm -rf dist/ man/

test:
	go test ./...

lint:
	go vet ./...
