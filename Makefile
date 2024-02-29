.PHONY: build
build:
	CGO_ENABLED=0 go build -o ./bin/inu cmd/inu/*
	CGO_ENABLED=0 go build -o ./bin/evaluate cmd/evaluate/*

.PHONY: clean
clean:
	rm -rf bin
	go clean -testcache

.PHONY: test
test:
	go test ./... -race -p 1

.PHONY: image
image:
	docker image build . -t inu:latest

.PHONY: evaluate
evaluate: build
	./bin/evaluate -k-var=true -alpha-var=true
