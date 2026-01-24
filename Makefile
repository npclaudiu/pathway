.PHONY: test generate install-protoc-gen-go examples

test:
	go test ./... -v

install-protoc-gen-go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

generate: install-protoc-gen-go
	PATH=$(shell go env GOPATH)/bin:$(PATH) protoc --go_out=. --go_opt=paths=source_relative internal/proto/*.proto

examples:
	mkdir -p bin/examples
	go build -o bin/examples/ ./examples/...
