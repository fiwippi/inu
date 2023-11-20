.PHONY: build
build:
	CGO_ENABLED=0 go build -o ./bin/inu cmd/main.go

.PHONY: test
test:
	go test ./... -v

.PHONY: clean
clean:
	rm -rf ./bin
	rm -f inu.db
