BINARY := vellora
CMD := ./cmd/vellora

.PHONY: build run test lint fmt clean

build:
	mkdir -p bin
	go build -o bin/$(BINARY) $(CMD)

run:
	go run $(CMD) --help

test:
	go test ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w $$(find cmd internal test -name '*.go' -type f)

clean:
	rm -rf bin
