.PHONY: test
test: test-unit test-integration

.PHONY: test-unit
test-unit:
	go test -v -coverprofile=coverage.out . ./internal/...
	go tool cover -func=coverage.out

.PHONY: test-integration
test-integration:
	go test -v ./tests/...

.PHONY: install-protoc-gen-go
install-protoc-gen-go:
	cd tools && go install google.golang.org/protobuf/cmd/protoc-gen-go

.PHONY: generate
generate: install-protoc-gen-go
	PATH=$(shell go env GOPATH)/bin:$(PATH) protoc --go_out=. --go_opt=paths=source_relative internal/proto/*.proto

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: check
check: fmt vet tidy lint

.PHONY: install-lint
install-lint:
	cd tools && go install github.com/golangci/golangci-lint/cmd/golangci-lint

.PHONY: lint
lint: install-lint
	$(shell go env GOPATH)/bin/golangci-lint run

.PHONY: clean
clean:
	rm -rf coverage.out
