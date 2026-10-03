// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Maintenance belongs to the acceptance test operator. Provider runtime must
// not acquire execution/container dependencies or import disposable helpers.
func TestProviderRuntimeExcludesMaintenance(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate provider source")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(source), "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatal("list provider source")
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal("parse provider imports")
		}
		for _, imp := range file.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal("decode provider import")
			}
			if name == "os/exec" || strings.Contains(name, "/internal/acceptance") || strings.Contains(name, "/testbootstrap") || strings.Contains(name, "testcontainers") || strings.Contains(name, "docker/") || strings.Contains(name, "moby/") {
				t.Fatalf("provider runtime file %s imports forbidden maintenance/execution dependency %s", filepath.Base(path), name)
			}
		}
	}
}
