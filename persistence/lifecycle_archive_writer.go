package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/model"
	"github.com/domainry/domainry-lifecycle/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/builder"
)

// ArchiveWriter is the narrow host-owned port used by source-owner executors
// to append archive evidence without owning lifecycle_archive_entries.
type ArchiveWriter struct {
	store modulehost.Host
}

func NewArchiveWriter(store modulehost.Host) ArchiveWriter { return ArchiveWriter{store: store} }

func (w ArchiveWriter) ArchivePayload(ctx context.Context, owner string, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, source, resourceID string, payload []byte) (bool, error) {
	if w.store == nil {
		return false, fmt.Errorf("lifecycle archive store unavailable")
	}
	renderer, db := w.store.Dialect(), w.store.Database()
	check, args, err := ormbuilder.NewWorkspaceSelectBuilder(renderer, "lifecycle_archive_entries", job.WorkspaceID).
		Projections(ormbuilder.Project(ormbuilder.CountAll())).
		Where(ormbuilder.And(ormbuilder.Equal("source_table", source), ormbuilder.Equal("resource_id", resourceID), ormbuilder.Equal("policy_key", policy.Policy.Key))).Build()
	if err != nil {
		return false, err
	}
	var exists int
	if err := db.QueryRowContext(ctx, check, args...).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}
	digest := sha256.Sum256(payload)
	insert, args, err := ormbuilder.NewWorkspaceInsertBuilder(renderer, "lifecycle_archive_entries", job.WorkspaceID).
		Columns("id", "owner", "source_table", "resource_id", "policy_key", "policy_version", "job_id", "payload_hash", "payload_json", "archived_at").
		Values(requestcontext.NewRequestID(), owner, source, resourceID, policy.Policy.Key, policy.Policy.Version, job.ID, hex.EncodeToString(digest[:]), string(payload), time.Now().UTC().Format(time.RFC3339Nano)).Build()
	if err != nil {
		return false, err
	}
	if _, err := db.ExecContext(ctx, insert, args...); err != nil {
		return false, fmt.Errorf("archive %s %s: %w", source, resourceID, err)
	}
	return true, nil
}
