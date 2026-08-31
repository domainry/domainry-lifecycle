// Package module exposes the stable in-process Lifecycle module factory.
// Implementation details remain under internal/assembly.
package module

import (
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	moduleassembly "github.com/domainry/domainry-lifecycle/internal/assembly/module"
	persistence "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence"
)

type Options = moduleassembly.Options
type Factory = moduleassembly.Factory

func OptionsFromEnvironment() Options { return moduleassembly.OptionsFromEnvironment() }

func NewFactory(options ...Options) *Factory {
	if len(options) == 0 {
		return moduleassembly.NewFactory(OptionsFromEnvironment())
	}
	return moduleassembly.NewFactory(options[0])
}

const MigrationOwner = persistence.MigrationOwner

// SchemaMigrations exposes Lifecycle-owned DDL to hosts that assemble the
// shared database before opening the full Module Binding. The host remains
// the sole owner of the migration lock and _schema_migrations ledger.
func SchemaMigrations(dialect modulehost.Dialect) ([]modulehost.SchemaMigration, error) {
	return persistence.SchemaMigrations(dialect)
}
