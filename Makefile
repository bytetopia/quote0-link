.PHONY: test mac windows package-mac package-windows

FIRMWARE_BUILD ?= firmware/build

test:
	cd app && go test ./...

mac:
	mkdir -p "dist/Quote0 Link.app/Contents/MacOS"
	cp app/Info.plist "dist/Quote0 Link.app/Contents/Info.plist"
	cd app && CGO_ENABLED=1 go build -o "../dist/Quote0 Link.app/Contents/MacOS/quote0-link" ./cmd/quote0-link
	codesign --force --sign - "dist/Quote0 Link.app"

windows:
	mkdir -p dist
	cd app && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-H=windowsgui" -o "../dist/Quote0 Link.exe" ./cmd/quote0-link

package-mac: mac
	python3 scripts/package.py mac --firmware-build "$(FIRMWARE_BUILD)"

package-windows: windows
	python3 scripts/package.py windows --firmware-build "$(FIRMWARE_BUILD)"
