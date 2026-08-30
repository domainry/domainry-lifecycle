// Package modulehost defines the infrastructure capabilities borrowed by an
// embedded Lifecycle module. The module never opens or closes the host DB and
// never owns a second migration ledger.
package modulehost

import (
	"context"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	"github.com/domainry/domainry-orm/sqlhost"
)

type Executor = sqlhost.Executor
type Queryer = sqlhost.Queryer
type DBTX = sqlhost.DBTX
type Database = sqlhost.Database

type Dialect interface {
	ormbuilder.Renderer
	Name() ormdialect.Name
}

type SchemaMigration = ormmigration.Migration
type SchemaBaseline = ormmigration.Baseline
type SchemaTable = ormmigration.Table
type SchemaColumn = ormmigration.Column
type SchemaIndex = ormmigration.Index

// MigrationRegistrar applies Lifecycle-owned migrations using the host's
// migration lock and sole _schema_migrations ledger.
type MigrationRegistrar interface {
	ApplyOwnedMigrations(context.Context, string, []SchemaMigration) error
}

// Transactor preserves the host transaction boundary. Lifecycle must not
// begin an independent transaction when invoked inside a host operation.
type Transactor interface {
	WithinTransaction(context.Context, func(context.Context, DBTX) error) error
}

type Host interface {
	Database() Database
	Dialect() Dialect
	Migrations() MigrationRegistrar
	Transactions() Transactor
}
