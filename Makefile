BINARY := sandal
ENTITLEMENTS := entitlements.plist
CODESIGN_IDENTITY := -

build:
	go build -o $(BINARY) .

build-darwin:
	CGO_ENABLED=1 go build -o $(BINARY) .


build-linux:
	GOOS=linux CGO_ENABLED=0 go build -o $(HOME)/.sandal-vm/bin/sandal .

sign: build-darwin build-linux
	codesign --entitlements $(ENTITLEMENTS) --force -s "$(CODESIGN_IDENTITY)" $(BINARY)

clean:
	rm -f $(BINARY)
