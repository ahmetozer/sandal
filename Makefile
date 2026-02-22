BINARY := sandal
ENTITLEMENTS := entitlements.plist
CODESIGN_IDENTITY := -

build:
	go build -o $(BINARY) .

build-darwin:
	CGO_ENABLED=1 go build -o $(BINARY) .

sign: build-darwin
	codesign --entitlements $(ENTITLEMENTS) --force -s "$(CODESIGN_IDENTITY)" $(BINARY)

clean:
	rm -f $(BINARY)
