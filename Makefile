LOCAL_BIN := $(shell pwd)/bin
PROTOC_GEN_GO := $(LOCAL_BIN)/protoc-gen-go
GOLANGCI_LINT := $(LOCAL_BIN)/golangci-lint
GOMARKDOC := $(LOCAL_BIN)/gomarkdoc

$(LOCAL_BIN):
	mkdir -p $(LOCAL_BIN)

$(PROTOC_GEN_GO): tools/go.mod tools/go.sum tools/tools.go | $(LOCAL_BIN)
	cd tools && go build -o $(PROTOC_GEN_GO) google.golang.org/protobuf/cmd/protoc-gen-go

$(GOLANGCI_LINT): tools/go.mod tools/go.sum tools/tools.go | $(LOCAL_BIN)
	cd tools && go build -o $(GOLANGCI_LINT) github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(GOMARKDOC): tools/go.mod tools/go.sum tools/tools.go | $(LOCAL_BIN)
	cd tools && go build -o $(GOMARKDOC) github.com/princjef/gomarkdoc/cmd/gomarkdoc

.PHONY: test
test: test-unit test-integration

.PHONY: test-unit
test-unit:
	go test -v -coverprofile=coverage.out . ./internal/...
	go tool cover -func=coverage.out

.PHONY: test-integration
test-integration:
	go test -v ./tests/...

.PHONY: bench
bench:
	go test -bench=. -benchmem -v ./benchmarks/...

.PHONY: generate
generate: $(PROTOC_GEN_GO)
	PATH=$(LOCAL_BIN):$(PATH) protoc --go_out=. --go_opt=paths=source_relative internal/proto/*.proto

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
check: fmt vet tidy lint docs-check

.PHONY: lint
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

.PHONY: docs
docs: $(GOMARKDOC)
	$(GOMARKDOC) --output docs/api.md .

.PHONY: docs-check
docs-check: docs
	git diff --exit-code -- docs/api.md

.PHONY: clean
clean:
	rm -rf coverage.out
	rm -rf $(LOCAL_BIN)
	rm -rf docs/api.md
