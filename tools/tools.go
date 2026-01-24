//go:build tools
// +build tools

package tools

import (
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/princjef/gomarkdoc/cmd/gomarkdoc"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
)
