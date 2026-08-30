package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/query"
)

// ArchiveWriter is the narrow host-owned port used by source-owner executors
// to append archive evidence without owning lifecycle_archive_entries.
type ArchiveWriter struct {
	store modulehost.Host
}

func NewArchiveWriter(store modulehost.Host) ArchiveWriter { return ArchiveWriter{store: store} }

func (w ArchiveWriter) Archived(ctx context.Context, workspaceID, source, resourceID, policyKey string) (bool, error) {
	if w.store == nil {
		return false, fmt.Errorf("lifecycle archive store unavailable")
	}
	check, args, err := ormbuilder.NewWorkspaceSelectBuilder(w.store.Dialect(), "lifecycle_archive_entries", workspaceID).
		Projections(ormbuilder.Project(ormbuilder.CountAll())).
		Where(ormbuilder.And(ormbuilder.Equal("source_table", source), ormbuilder.Equal("resource_id", resourceID), ormbuilder.Equal("policy_key", policyKey))).Build()
	if err != nil {
		return false, err
	}
	var exists int
	database := modulehost.ExecutorFromContext(ctx, w.store.Database())
	if err := database.QueryRowContext(ctx, check, args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (w ArchiveWriter) ArchivePayload(ctx context.Context, owner string, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, source, resourceID string, payload []byte) (bool, error) {
	if w.store == nil {
		return false, fmt.Errorf("lifecycle archive store unavailable")
	}
	exists, err := w.Archived(ctx, job.WorkspaceID, source, resourceID, policy.Policy.Key)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	renderer := w.store.Dialect()
	db := modulehost.ExecutorFromContext(ctx, w.store.Database())
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
