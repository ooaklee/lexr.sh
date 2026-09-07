package companion

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepositoryCompanionKeepsCompilerEmbeds detects newly added go:embed
// inputs missing from the controlled compiler/source snapshot. It uses the
// real directives rather than a manually duplicated list of adapter files.
func TestRepositoryCompanionKeepsCompilerEmbeds(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := collectSourceFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	admitted := map[string]bool{}
	for _, source := range sources {
		admitted[source.portablePath] = true
	}
	for _, source := range sources {
		if !strings.HasSuffix(source.portablePath, ".go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), source.absolutePath, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, group := range parsed.Comments {
			for _, comment := range group.List {
				directive, ok := strings.CutPrefix(comment.Text, "//go:embed ")
				if !ok {
					continue
				}
				for _, pattern := range strings.Fields(directive) {
					pattern = strings.TrimPrefix(pattern, "all:")
					matches, err := fs.Glob(os.DirFS(root), path.Join(path.Dir(source.portablePath), pattern))
					if err != nil {
						t.Fatal(err)
					}
					if len(matches) == 0 {
						t.Fatalf("%s: embed %q has no inputs", source.portablePath, pattern)
					}
					for _, match := range matches {
						if !admitted[match] {
							t.Errorf("%s: embedded compiler input %s is missing from the companion snapshot", source.portablePath, match)
						}
					}
				}
			}
		}
	}
}
