BINARY := sandal
ENTITLEMENTS := entitlements.plist
CODESIGN_IDENTITY := -

build:
	go build -o $(BINARY) .

build-darwin:
	CGO_ENABLED=1 go build -o $(BINARY) .

sign: build-darwin
	codesign --entitlements $(ENTITLEMENTS) --force -s "$(CODESIGN_IDENTITY)" $(BINARY)

build-linux:
	GOOS=linux CGO_ENABLED=0 go build -o $(HOME)/.sandal-vm/bin/sandal .

clean:
	rm -f $(BINARY)
