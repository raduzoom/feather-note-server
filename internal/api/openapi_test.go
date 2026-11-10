package api

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
)

// specPath is ../universal/specs/sync-protocol.openapi.yaml, next to this repo.
func specPath(t testing.TB) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "universal", "specs", "sync-protocol.openapi.yaml")
}

func loadSpec(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatalf("OpenAPI file not found at %s: check out feather-note-universal next to server (%v)", specPath(t), err)
	}
	return data
}

func TestEveryOpenAPIOperationIsRouted(t *testing.T) {
	doc, err := libopenapi.NewDocument(loadSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	model, err := doc.BuildV3Model()
	if err != nil {
		t.Fatal(err)
	}
	var spec []string
	for pair := model.Model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
		for op := pair.Value().GetOperations().First(); op != nil; op = op.Next() {
			spec = append(spec, strings.ToUpper(op.Key())+" "+pair.Key())
		}
	}
	got := Routes()
	sort.Strings(spec)
	sort.Strings(got)
	if strings.Join(spec, "\n") != strings.Join(got, "\n") {
		t.Fatalf("OpenAPI operations:\n%s\n\nrouted:\n%s", strings.Join(spec, "\n"), strings.Join(got, "\n"))
	}
}
