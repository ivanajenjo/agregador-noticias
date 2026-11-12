.PHONY: build test run arm64 docker
build:
	go build -o agregador ./cmd/agregador
test:
	go vet ./... && go test -race ./...
run: build
	./agregador
arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/agregador-arm64 ./cmd/agregador
docker:
	docker buildx build --platform linux/arm64 -f deploy/Dockerfile -t agregador:latest .
