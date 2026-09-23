package module

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
)

type integrationDefinitionStore struct{ db *sql.DB }

func newIntegrationDefinitionStore(t interface {
	Helper()
	Fatal(...any)
}, db *sql.DB) *integrationDefinitionStore {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE _definitions (owner TEXT NOT NULL, kind TEXT NOT NULL, definition_key TEXT NOT NULL, current_version_id TEXT NOT NULL, status TEXT NOT NULL, object_key TEXT NOT NULL, name TEXT NOT NULL, payload_json TEXT NOT NULL, schema_version TEXT NOT NULL, schema_hash TEXT NOT NULL, source_kind TEXT NOT NULL, source_id TEXT NOT NULL, published_by TEXT NOT NULL, PRIMARY KEY (owner, kind, definition_key))`,
		`CREATE TABLE _definition_versions (id TEXT PRIMARY KEY, owner TEXT NOT NULL, kind TEXT NOT NULL, definition_key TEXT NOT NULL, schema_version TEXT NOT NULL, schema_hash TEXT NOT NULL, payload_json TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return &integrationDefinitionStore{db: db}
}

func (s *integrationDefinitionStore) executor(ctx context.Context) metadatamodulehost.DBTX {
	return metadatamodulehost.ExecutorFromContext(ctx, s.db)
}

func (s *integrationDefinitionStore) List(ctx context.Context, query metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	rows, err := s.executor(ctx).QueryContext(ctx, `SELECT owner, kind, definition_key, current_version_id, status, object_key, name, payload_json, schema_version, schema_hash, source_kind, source_id, published_by FROM _definitions WHERE owner = ? AND kind = ? AND source_id = ? AND status = 'active' ORDER BY definition_key`, query.Owner, query.ResourceType, query.SourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []metadatasdk.Definition{}
	for rows.Next() {
		value, err := scanIntegrationDefinition(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *integrationDefinitionStore) Get(ctx context.Context, owner, resourceType, key string) (metadatasdk.Definition, bool, error) {
	row := s.executor(ctx).QueryRowContext(ctx, `SELECT owner, kind, definition_key, current_version_id, status, object_key, name, payload_json, schema_version, schema_hash, source_kind, source_id, published_by FROM _definitions WHERE owner = ? AND kind = ? AND definition_key = ? AND status = 'active'`, owner, resourceType, key)
	value, err := scanIntegrationDefinition(row)
	if err == sql.ErrNoRows {
		return metadatasdk.Definition{}, false, nil
	}
	return value, err == nil, err
}

func (s *integrationDefinitionStore) Snapshot(ctx context.Context, query metadatasdk.DefinitionQuery) (metadatasdk.DefinitionSnapshot, error) {
	values, err := s.List(ctx, query)
	return metadatasdk.DefinitionSnapshot{Definitions: values}, err
}

func (*integrationDefinitionStore) ReplaceSourceSnapshot(context.Context, metadatasdk.ProjectionSnapshot) error {
	return fmt.Errorf("integration Definition snapshot replacement is unsupported")
}

func (s *integrationDefinitionStore) Publish(ctx context.Context, command metadatasdk.DefinitionPublishCommand) (metadatasdk.DefinitionPublishResult, error) {
	digest := sha256.Sum256(append([]byte(command.Owner+"\x00"+command.ResourceType+"\x00"+command.ResourceKey+"\x00"+command.SchemaVersion+"\x00"), command.Payload...))
	versionID := "definition-version:" + hex.EncodeToString(digest[:16])
	executor := s.executor(ctx)
	if _, err := executor.ExecContext(ctx, `INSERT OR IGNORE INTO _definition_versions (id, owner, kind, definition_key, schema_version, schema_hash, payload_json) VALUES (?, ?, ?, ?, ?, ?, ?)`, versionID, command.Owner, command.ResourceType, command.ResourceKey, command.SchemaVersion, command.SchemaHash, string(command.Payload)); err != nil {
		return metadatasdk.DefinitionPublishResult{}, err
	}
	var result sql.Result
	var err error
	if command.ExpectedCurrentVersionID == metadatasdk.DefinitionNoCurrentVersion {
		result, err = executor.ExecContext(ctx, `INSERT OR IGNORE INTO _definitions (owner, kind, definition_key, current_version_id, status, object_key, name, payload_json, schema_version, schema_hash, source_kind, source_id, published_by) VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?)`, command.Owner, command.ResourceType, command.ResourceKey, versionID, command.ObjectKey, command.Name, string(command.Payload), command.SchemaVersion, command.SchemaHash, command.SourceKind, command.SourceID, command.PublishedBy)
	} else {
		result, err = executor.ExecContext(ctx, `UPDATE _definitions SET current_version_id = ?, status = 'active', object_key = ?, name = ?, payload_json = ?, schema_version = ?, schema_hash = ?, source_kind = ?, source_id = ?, published_by = ? WHERE owner = ? AND kind = ? AND definition_key = ? AND current_version_id = ? AND status = 'active'`, versionID, command.ObjectKey, command.Name, string(command.Payload), command.SchemaVersion, command.SchemaHash, command.SourceKind, command.SourceID, command.PublishedBy, command.Owner, command.ResourceType, command.ResourceKey, command.ExpectedCurrentVersionID)
	}
	if err != nil {
		return metadatasdk.DefinitionPublishResult{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return metadatasdk.DefinitionPublishResult{}, err
	}
	if affected != 1 {
		return metadatasdk.DefinitionPublishResult{}, fmt.Errorf("integration Definition revision conflict")
	}
	value, found, err := s.Get(ctx, command.Owner, command.ResourceType, command.ResourceKey)
	if err != nil || !found {
		return metadatasdk.DefinitionPublishResult{}, err
	}
	return metadatasdk.DefinitionPublishResult{Definition: value, CurrentVersionID: versionID}, nil
}

func (s *integrationDefinitionStore) Disable(ctx context.Context, command metadatasdk.DefinitionDisableCommand) error {
	result, err := s.executor(ctx).ExecContext(ctx, `UPDATE _definitions SET status = 'disabled' WHERE owner = ? AND kind = ? AND definition_key = ? AND current_version_id = ? AND status = 'active'`, command.Owner, command.ResourceType, command.ResourceKey, command.ExpectedCurrentVersionID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("integration Definition revision conflict")
	}
	return nil
}

func (s *integrationDefinitionStore) GetVersion(ctx context.Context, query metadatasdk.DefinitionVersionQuery) (metadatasdk.DefinitionVersion, bool, error) {
	var value metadatasdk.DefinitionVersion
	var payload string
	err := s.executor(ctx).QueryRowContext(ctx, `SELECT id, owner, kind, definition_key, schema_version, schema_hash, payload_json FROM _definition_versions WHERE id = ? AND owner = ? AND kind = ? AND definition_key = ?`, query.VersionID, query.Owner, query.ResourceType, query.ResourceKey).Scan(&value.ID, &value.Owner, &value.ResourceType, &value.ResourceKey, &value.SchemaVersion, &value.SchemaHash, &payload)
	if err == sql.ErrNoRows {
		return metadatasdk.DefinitionVersion{}, false, nil
	}
	if err != nil {
		return metadatasdk.DefinitionVersion{}, false, err
	}
	value.Payload = json.RawMessage(payload)
	return value, true, nil
}

type integrationDefinitionScanner interface{ Scan(...any) error }

func scanIntegrationDefinition(row integrationDefinitionScanner) (metadatasdk.Definition, error) {
	var value metadatasdk.Definition
	var payload string
	err := row.Scan(&value.Owner, &value.ResourceType, &value.ResourceKey, &value.CurrentVersionID, &value.Status, &value.ObjectKey, &value.Name, &payload, &value.SchemaVersion, &value.SchemaHash, &value.SourceKind, &value.SourceID, &value.PublishedBy)
	value.Payload = json.RawMessage(payload)
	return value, err
}
