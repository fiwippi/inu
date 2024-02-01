.PHONY: build
build:
	CGO_ENABLED=0 go build -o ./bin/inu cmd/*

.PHONY: test
test:
	go test ./... -race -p 1

.PHONY: clean
clean:
	rm -rf ./bin
	rm -rf scratch
	rm -f inu.db
	go clean -testcache

.PHONY: play
play:
	mkdir scratch
	docker compose up --build