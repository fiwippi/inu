.PHONY: build
build:
	CGO_ENABLED=0 go build -o ./bin/inu cmd/inu/*.go

.PHONY: clean
clean:
	rm -rf bin
	go clean -testcache

.PHONY: test
test:
	go test ./... -race -p 1
