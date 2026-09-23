// Package module exposes the stable in-process Lifecycle module factory.
// Implementation details remain under internal/assembly.
package module

import (
	"github.com/domainry/domainry-foundation/schemaownership"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	moduleassembly "github.com/domainry/domainry-lifecycle/internal/assembly/module"
	storeschema "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/schema"
)

type Options = moduleassembly.Options
type Factory = moduleassembly.Factory

const MigrationOwner = storeschema.MigrationOwner

func OptionsFromEnvironment() Options { return moduleassembly.OptionsFromEnvironment() }

func NewFactory(options ...Options) *Factory {
	if len(options) == 0 {
		return moduleassembly.NewFactory(OptionsFromEnvironment())
	}
	return moduleassembly.NewFactory(options[0])
}

func SchemaOwnership() []schemaownership.Table { return storeschema.SchemaOwnership() }

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }

func SchemaMigrations(renderer modulehost.Dialect) ([]modulehost.SchemaMigration, error) {
	return storeschema.Migrations(renderer)
}
