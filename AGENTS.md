# Development rules

- Answer architecture and implementation questions from the current repository code and tests, not from memory.
- Every database has exactly one host-owned migration ledger named `_schema_migrations`. Embedded Lifecycle migrations must go through the host migration registrar.
- Persistence DDL and DML must use `github.com/domainry/domainry-orm`. Raw SQL is allowed only when the ORM has no equivalent, with a local justification and dialect-focused tests.
- Embedded Lifecycle uses the host database, transaction boundary, SQL dialect, migration lock, and migration ledger. Lifecycle retains ownership of its source and business tables.
- Keep `module` as a thin public facade. Composition, SDK adapters, transports, and persistence implementations remain under `internal`.
