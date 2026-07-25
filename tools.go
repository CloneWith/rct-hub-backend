//go:build tools

// Package tools keeps gqlgen code generator dependencies in go.mod/go.sum.
// This file is excluded from normal builds via the build tag.
package tools

import (
	_ "github.com/99designs/gqlgen"
	_ "github.com/99designs/gqlgen/api"
	_ "github.com/99designs/gqlgen/codegen/config"
	_ "github.com/99designs/gqlgen/internal/imports"
)
