# domainry-lifecycle

Reusable, in-process data lifecycle governance for Domainry SaaS hosts.

The module owns its `lifecycle_*` tables and submits ORM-rendered migrations to
the host. It borrows the host database, SQL dialect renderer, transaction
boundary, migration lock, and the single `_schema_migrations` ledger. It never
opens a database and never creates a module-specific migration ledger.

Hosts depend on `github.com/domainry/domainry-lifecycle-sdk` and inject
`module.NewFactory()` only at the composition root. The resulting SDK Binding
is the sole access path to the Lifecycle repository, artifact stores,
transactions, and archive writer; persistence packages are internal.

Source modules retain ownership of their business tables and register narrow
cleanup, export, and erasure adapters through SDK contracts.
