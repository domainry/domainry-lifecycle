package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLifecycleUsesInternalLayeredLayout(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, required := range []string{
		"cmd/lifecycle-server",
		"internal/application/lifecycle",
		"internal/domain/lifecycle/model",
		"internal/domain/lifecycle/repository",
		"internal/domain/lifecycle/service",
		"internal/assembly/module",
		"internal/assembly/saas",
		"internal/adapter/lifecyclesdk",
		"internal/transport/http/module",
		"internal/transport/http/saas",
		"internal/infrastructure/persistence/database/lifecycle",
		"internal/infrastructure/persistence/database/migration",
		"internal/infrastructure/persistence/database/schema",
		"internal/infrastructure/persistence/sqlite",
		"internal/infrastructure/persistence/mysql",
		"internal/infrastructure/persistence/postgres",
		"module",
	} {
		if info, err := os.Stat(filepath.Join(root, required)); err != nil || !info.IsDir() {
			t.Errorf("required Lifecycle boundary %q is missing: %v", required, err)
		}
	}
	for _, legacy := range []string{"access", "application", "artifact", "contract", "migrations", "model", "modulehost", "persistence", "policy", "repository"} {
		if _, err := os.Stat(filepath.Join(root, legacy)); !os.IsNotExist(err) {
			t.Errorf("implementation still exposes legacy public package %q", legacy)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "replace ") || strings.Contains(string(raw), "../domainry-") {
		t.Fatal("Lifecycle implementation must resolve published tags, not local directories")
	}
}

func TestPublicModuleIsThinFacade(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "module"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || entry.Name() == "module.go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		t.Errorf("public module package contains implementation file %q", entry.Name())
	}
}

func TestLifecycleBusinessPersistenceUsesORMBuilders(t *testing.T) {
	root := filepath.Join("..", "infrastructure", "persistence", "database")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				return true
			}
			upper := strings.ToUpper(strings.TrimSpace(value))
			for _, prefix := range []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "CREATE ", "ALTER ", "DROP "} {
				if strings.HasPrefix(upper, prefix) {
					t.Errorf("hand-written SQL in %s; use domainry-orm", path)
					break
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
