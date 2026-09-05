APP := iothunter

.PHONY: test vet fmt build build-all demo serve desktop capabilities

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

capabilities:
	go run ./cmd/iothunter capabilities
