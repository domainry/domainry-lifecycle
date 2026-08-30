# domainry-lifecycle

Reusable, in-process data lifecycle governance for Domainry SaaS hosts.

The module owns its `lifecycle_*` tables and submits ORM-rendered migrations to
the host. It borrows the host database, SQL dialect renderer, transaction
boundary, migration lock, and the single `_schema_migrations` ledger. It never
opens a database and never creates a module-specific migration ledger.

Hosts embed it through `module.Bind`. Source modules retain ownership of their
business tables and register narrow cleanup, export, and erasure adapters.
