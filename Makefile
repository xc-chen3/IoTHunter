APP := iothunter

.PHONY: test vet fmt build build-all demo serve desktop client-install client capabilities frontend-install frontend-build wails-build-linux desktop-package desktop-dist

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

build:
	mkdir -p bin
	go build -buildvcs=false -o bin/$(APP) ./cmd/iothunter

build-all:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o dist/$(APP)-linux-amd64 ./cmd/iothunter
	GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o dist/$(APP)-linux-arm64 ./cmd/iothunter
	GOOS=darwin GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o dist/$(APP)-darwin-amd64 ./cmd/iothunter
	GOOS=darwin GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o dist/$(APP)-darwin-arm64 ./cmd/iothunter
	GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o dist/$(APP)-windows-amd64.exe ./cmd/iothunter

demo:
	go run ./cmd/iothunter demo

serve:
	go run ./cmd/iothunter serve

desktop:
	go run ./cmd/iothunter desktop

client-install:
	npm --prefix desktop install
	npm --prefix desktop/frontend install

frontend-install:
	npm --prefix desktop/frontend install

frontend-build:
	npm --prefix desktop/frontend run build
	cp -R desktop/frontend/dist/. desktop/wails/frontend-dist/

wails-build-linux: frontend-build
	cd desktop/wails && go build -tags 'production webkit2_41' -trimpath -ldflags '-s -w' -o ../../bin/iothunter-wails .

client:
	npm --prefix desktop start

desktop-package: build frontend-build
	npm --prefix desktop run package

desktop-dist: build frontend-build
	npm --prefix desktop run dist

capabilities:
	go run ./cmd/iothunter capabilities
