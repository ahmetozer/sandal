BINARY := sandal
ENTITLEMENTS := entitlements.plist
CODESIGN_IDENTITY := -
VERSION := $(shell git rev-parse --short HEAD)
LDFLAGS := -s -w -X github.com/ahmetozer/sandal/pkg/cmd.BuildVersion=$(VERSION)
UNAME_S := $(shell uname -s)

.PHONY: build build-darwin build-linux generate sign clean

# macOS: generate the embedded linux binary, build the darwin binary
# (which embeds it via go:embed), then codesign.
# Linux: build linux binary only.
ifeq ($(UNAME_S),Darwin)
build: generate build-darwin sign
else
build: build-linux
endif

build-darwin:
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

build-linux:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# Cross-compile the linux sandal binary into pkg/sandal/vmbin/linux-sandal
# via the //go:generate directive in that package.
generate:
	go generate ./pkg/sandal/vmbin

sign:
	codesign --entitlements $(ENTITLEMENTS) --force -s "$(CODESIGN_IDENTITY)" $(BINARY)

clean:
	rm -f $(BINARY)
