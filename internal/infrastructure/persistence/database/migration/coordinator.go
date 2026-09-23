// Package migration submits source-owned Lifecycle migrations through the
// host registrar. The host owns the lock and the sole _schema_migrations
// ledger.
package migration

import (
	"context"
	"fmt"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	schema "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/schema"
)

const Owner = schema.MigrationOwner

func Migrations(renderer modulehost.Dialect) ([]modulehost.SchemaMigration, error) {
	return schema.Migrations(renderer)
}

func Apply(ctx context.Context, host modulehost.Host) error {
	if host == nil || host.Migrations() == nil {
		return fmt.Errorf("lifecycle migrations require host registrar")
	}
	values, err := Migrations(host.Dialect())
	if err != nil {
		return err
	}
	return host.Migrations().ApplyOwnedMigrations(ctx, Owner, values)
}
