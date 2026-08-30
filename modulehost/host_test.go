package modulehost

import (
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func TestDialectContractAcceptsEveryORMRenderer(t *testing.T) {
	for _, name := range []ormdialect.Name{ormdialect.SQLite, ormdialect.Postgres, ormdialect.MySQL} {
		dialect, err := ormdialect.New(name)
		if err != nil {
			t.Fatal(err)
		}
		var renderer Dialect = dialect.WithSchema("app")
		if renderer.Name() != name {
			t.Fatalf("renderer name = %q, want %q", renderer.Name(), name)
		}
	}
}
