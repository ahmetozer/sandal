BINARY := sandal
ENTITLEMENTS := entitlements.plist
CODESIGN_IDENTITY := -
VERSION := $(shell git rev-parse --short HEAD)

build:
	go build -ldflags "-s -w -X github.com/ahmetozer/sandal/pkg/cmd.BuildVersion=$(VERSION)" -o $(BINARY) .

build-darwin:
	CGO_ENABLED=1 go build -ldflags "-s -w -X github.com/ahmetozer/sandal/pkg/cmd.BuildVersion=$(VERSION)" -o $(BINARY) .


build-linux:
	GOOS=linux CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/ahmetozer/sandal/pkg/cmd.BuildVersion=$(VERSION)" -o $(HOME)/.sandal-vm/bin/sandal .

sign: build-darwin build-linux
	codesign --entitlements $(ENTITLEMENTS) --force -s "$(CODESIGN_IDENTITY)" $(BINARY)

clean:
	rm -f $(BINARY)
