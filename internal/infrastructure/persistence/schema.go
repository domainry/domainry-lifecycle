package persistence

import (
	"context"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	migration "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/migration"
)

const MigrationOwner = migration.Owner

func SchemaMigrations(dialect modulehost.Dialect) ([]modulehost.SchemaMigration, error) {
	return migration.Migrations(dialect)
}

func ApplySchema(ctx context.Context, host modulehost.Host) error {
	return migration.Apply(ctx, host)
}
