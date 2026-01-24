.PHONY: test test-unit test-integration generate install-protoc-gen-go fmt vet tidy check install-lint lint

test: test-unit test-integration

test-unit:
	go test -v -coverprofile=coverage.out . ./internal/...
	go tool cover -func=coverage.out

test-integration:
	go test -v ./tests/...

install-protoc-gen-go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

generate: install-protoc-gen-go
	PATH=$(shell go env GOPATH)/bin:$(PATH) protoc --go_out=. --go_opt=paths=source_relative internal/proto/*.proto

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

check: fmt vet tidy lint

install-lint:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

lint: install-lint
	$(shell go env GOPATH)/bin/golangci-lint run
