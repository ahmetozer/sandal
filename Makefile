BINARY := sandal
ENTITLEMENTS := entitlements.plist
CODESIGN_IDENTITY := -
VERSION := $(shell git rev-parse --short HEAD)
LDFLAGS := -s -w -X github.com/ahmetozer/sandal/pkg/cmd.BuildVersion=$(VERSION)
UNAME_S := $(shell uname -s)

.PHONY: build generate build-darwin build-linux build-linux-vm sign clean

# macOS: build darwin binary + cross-compile linux binary (for VM init) + codesign
# Linux: build linux binary only
ifeq ($(UNAME_S),Darwin)
build: generate build-darwin build-linux-vm sign
else
build: generate build-linux
endif

generate:
	go generate ./pkg/vm/kernel/

build-darwin:
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

build-linux:
	CGO_ENABLED=0 go build -tags preinit -ldflags "$(LDFLAGS)" -o $(BINARY) .

# Cross-compile Linux binary from macOS (used as /init inside VZ VM)
build-linux-vm:
	GOOS=linux CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(HOME)/.sandal/lib/bin/sandal .

sign:
	codesign --entitlements $(ENTITLEMENTS) --force -s "$(CODESIGN_IDENTITY)" $(BINARY)

clean:
	rm -f $(BINARY)
