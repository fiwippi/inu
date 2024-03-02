.PHONY: build
build:
	CGO_ENABLED=0 go build -o ./bin/inu cmd/inu/*
	CGO_ENABLED=0 go build -o ./bin/evaluate cmd/evaluate/*

.PHONY: clean
clean:
	rm -rf bin
	go clean -testcache
	sudo ip -4 route del local 10.41.0.0/16 dev lo

.PHONY: test
test:
	go test ./... -race -p 1

.PHONY: image
image:
	docker image build . -t inu:latest

.PHONY: evaluate
evaluate: build
	sudo ip -4 route replace local 10.41.0.0/16 dev lo
	./bin/evaluate -k-var=true -alpha-var=true
