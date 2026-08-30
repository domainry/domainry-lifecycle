package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle/contract"
	"github.com/domainry/domainry-lifecycle/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/builder"
)

const uploadArtifactGracePeriod = 24 * time.Hour

type FileArtifactStore struct {
	host       modulehost.Host
	db         modulehost.Database
	renderer   modulehost.Dialect
	fields     lifecyclecontract.UploadFieldCatalog
	references lifecyclecontract.UploadArtifactReferenceResolver
	cleaner    lifecyclecontract.ExpiredUploadReferenceCleaner
	uploadRoot string
	removeFile func(string) error
	absPath    func(string) (string, error)
	relPath    func(string, string) (string, error)
	contextErr func(context.Context) error
}

type FileArtifactStoreOption func(*FileArtifactStore)

func WithUploadArtifactReferences(references lifecyclecontract.UploadArtifactReferenceResolver) FileArtifactStoreOption {
	return func(store *FileArtifactStore) { store.references = references }
}

func WithExpiredUploadReferenceCleaner(cleaner lifecyclecontract.ExpiredUploadReferenceCleaner) FileArtifactStoreOption {
	return func(store *FileArtifactStore) { store.cleaner = cleaner }
}

func NewFileArtifactStore(host modulehost.Host, fields lifecyclecontract.UploadFieldCatalog, uploadRoot string, options ...FileArtifactStoreOption) *FileArtifactStore {
	result := &FileArtifactStore{host: host, fields: fields, uploadRoot: uploadRoot, removeFile: os.Remove, absPath: filepath.Abs, relPath: filepath.Rel, contextErr: func(ctx context.Context) error { return ctx.Err() }}
	if host != nil {
		result.db, result.renderer = host.Database(), host.Dialect()
	}
	for _, option := range options {
		if option != nil {
			option(result)
		}
	}
	return result
}

func (s *FileArtifactStore) database(ctx context.Context) modulehost.DBTX {
	return modulehost.ExecutorFromContext(ctx, s.db)
}

func (s *FileArtifactStore) RegisterUpload(ctx context.Context, artifact lifecyclecontract.UploadArtifact) error {
	if s == nil || s.host == nil || s.db == nil || s.renderer == nil {
		return fmt.Errorf("upload artifact store unavailable")
	}
	workspace, err := lifecycleaccess.NewWorkspaceID(artifact.WorkspaceID)
	if err != nil {
		return err
	}
	artifact.WorkspaceID = workspace.String()
	artifact.ID, artifact.ObjectKey, artifact.FieldKey, artifact.Filename = strings.TrimSpace(artifact.ID), strings.TrimSpace(artifact.ObjectKey), strings.TrimSpace(artifact.FieldKey), strings.TrimSpace(artifact.Filename)
	if artifact.ID == "" {
		artifact.ID = requestcontext.NewRequestID()
	}
	if s.fields == nil || !s.fields.HasUploadField(artifact.ObjectKey, artifact.FieldKey) {
		return fmt.Errorf("upload artifact owner field is not declared")
	}
	if artifact.ID == "" || artifact.Filename == "" || filepath.Base(artifact.Filename) != artifact.Filename || artifact.SHA256 == "" || artifact.Size < 0 || artifact.CreatedAt.IsZero() {
		return fmt.Errorf("upload artifact evidence is incomplete")
	}
	createdAt := artifact.CreatedAt.UTC().Format(time.RFC3339Nano)
	var id string
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.renderer, "lifecycle_file_artifacts", artifact.WorkspaceID).
		Columns("id").Where(ormbuilder.Equal("filename", artifact.Filename)).Build()
	if buildErr != nil {
		return fmt.Errorf("build upload artifact lookup: %w", buildErr)
	}
	err = s.database(ctx).QueryRowContext(ctx, query, args...).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		query, args, buildErr = ormbuilder.NewWorkspaceUpdateBuilder(s.renderer, "lifecycle_file_artifacts", artifact.WorkspaceID).
			Set("object_key", artifact.ObjectKey).Set("field_key", artifact.FieldKey).Set("content_type", artifact.ContentType).Set("sha256", artifact.SHA256).Set("size_bytes", artifact.Size).
			Set("status", "staged").Set("scan_status", lifecyclecontract.FileScanPending).Set("scan_provider", "").Set("scan_evidence_ref", "").Set("scanned_at", "").
			Set("created_at", createdAt).Set("last_referenced_at", "").Set("delete_after", artifact.CreatedAt.UTC().Add(uploadArtifactGracePeriod).Format(time.RFC3339Nano)).Set("deleted_at", "").
			Where(ormbuilder.Equal("id", id)).Build()
		if buildErr != nil {
			return fmt.Errorf("build upload artifact update: %w", buildErr)
		}
		_, err = s.database(ctx).ExecContext(ctx, query, args...)
		return err
	}
	query, args, buildErr = ormbuilder.NewWorkspaceInsertBuilder(s.renderer, "lifecycle_file_artifacts", artifact.WorkspaceID).
		Columns("id", "object_key", "field_key", "filename", "content_type", "sha256", "size_bytes", "status", "scan_status", "scan_provider", "scan_evidence_ref", "scanned_at", "created_at", "last_referenced_at", "delete_after", "deleted_at").
		Values(artifact.ID, artifact.ObjectKey, artifact.FieldKey, artifact.Filename, artifact.ContentType, artifact.SHA256, artifact.Size, "staged", lifecyclecontract.FileScanPending, "", "", "", createdAt, "", artifact.CreatedAt.UTC().Add(uploadArtifactGracePeriod).Format(time.RFC3339Nano), "").Build()
	if buildErr != nil {
		return fmt.Errorf("build upload artifact insert: %w", buildErr)
	}
	_, err = s.database(ctx).ExecContext(ctx, query, args...)
	return err
}

func (s *FileArtifactStore) FindFileScan(ctx context.Context, workspaceID, fileID string) (lifecyclecontract.FileScanEvidence, error) {
	if s == nil || s.host == nil || s.db == nil || s.renderer == nil {
		return lifecyclecontract.FileScanEvidence{}, fmt.Errorf("upload artifact store unavailable")
	}
	workspace, err := lifecycleaccess.NewWorkspaceID(workspaceID)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	columns := []string{"id", "workspace_id", "filename", "content_type", "object_key", "field_key", "sha256", "size_bytes", "scan_status", "scan_provider", "scan_evidence_ref", "scanned_at"}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.renderer, "lifecycle_file_artifacts", workspace.String()).Columns(columns...).
		Where(ormbuilder.And(ormbuilder.Equal("id", strings.TrimSpace(fileID)), ormbuilder.Equal("deleted_at", ""))).Build()
	if buildErr != nil {
		return lifecyclecontract.FileScanEvidence{}, fmt.Errorf("build file scan lookup: %w", buildErr)
	}
	var evidence lifecyclecontract.FileScanEvidence
	var scannedAt string
	err = s.database(ctx).QueryRowContext(ctx, query, args...).Scan(&evidence.FileID, &evidence.WorkspaceID, &evidence.Filename, &evidence.ContentType, &evidence.ObjectKey, &evidence.FieldKey, &evidence.SHA256, &evidence.Size, &evidence.Status, &evidence.Provider, &evidence.EvidenceRef, &scannedAt)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	if scannedAt != "" {
		evidence.ScannedAt, err = time.Parse(time.RFC3339Nano, scannedAt)
	}
	return evidence, err
}

// RecordFileScan is an internal scanner port. No HTTP or generated Action
// binding exposes it; only trusted Runtime scanner adapters may call it.
func (s *FileArtifactStore) RecordFileScan(ctx context.Context, evidence lifecyclecontract.FileScanEvidence) error {
	switch evidence.Status {
	case lifecyclecontract.FileScanClean, lifecyclecontract.FileScanQuarantined, lifecyclecontract.FileScanFailed:
	default:
		return fmt.Errorf("terminal file scan status required")
	}
	if strings.TrimSpace(evidence.Provider) == "" || strings.TrimSpace(evidence.EvidenceRef) == "" || evidence.ScannedAt.IsZero() {
		return fmt.Errorf("file scan evidence is incomplete")
	}
	current, err := s.FindFileScan(ctx, evidence.WorkspaceID, evidence.FileID)
	if err != nil {
		return err
	}
	if current.SHA256 != strings.TrimSpace(evidence.SHA256) || current.Size != evidence.Size {
		return fmt.Errorf("file scan content identity mismatch")
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.renderer, "lifecycle_file_artifacts", current.WorkspaceID).
		Set("scan_status", evidence.Status).Set("scan_provider", strings.TrimSpace(evidence.Provider)).Set("scan_evidence_ref", strings.TrimSpace(evidence.EvidenceRef)).
		Set("scanned_at", evidence.ScannedAt.UTC().Format(time.RFC3339Nano)).Where(ormbuilder.And(ormbuilder.Equal("id", current.FileID), ormbuilder.Equal("sha256", current.SHA256), ormbuilder.Equal("size_bytes", current.Size))).Build()
	if buildErr != nil {
		return fmt.Errorf("build file scan update: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("file scan content identity mismatch")
	}
	return nil
}

func (s *FileArtifactStore) ReconcileUploadArtifacts(ctx context.Context, scope lifecycleaccess.SystemScope, now time.Time, limit int) (lifecyclecontract.UploadCleanupResult, error) {
	result := lifecyclecontract.UploadCleanupResult{}
	if s == nil || s.host == nil || s.db == nil || s.renderer == nil {
		return result, fmt.Errorf("upload artifact store unavailable")
	}
	if _, err := lifecycleaccess.NewSystemCommandScope(scope); err != nil {
		return result, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if s.cleaner != nil {
		expiredDownloads, err := s.cleaner.ExpireUploadReferences(ctx, now, limit)
		if err != nil {
			return result, err
		}
		result.ExpiredDownloads = expiredDownloads
	}
	columns := []string{"id", "workspace_id", "object_key", "field_key", "filename", "status", "created_at", "delete_after"}
	statement, args, buildErr := ormbuilder.NewSelectBuilder(s.renderer, "lifecycle_file_artifacts").Columns(columns...).
		Where(ormbuilder.NotEqual("status", "deleted")).OrderBy(ormbuilder.Ascending("created_at"), ormbuilder.Ascending("id")).Limit(limit).Build()
	if buildErr != nil {
		return result, fmt.Errorf("build upload artifact reconciliation query: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, statement, args...)
	if err != nil {
		return result, err
	}
	type candidate struct{ id, workspaceID, objectKey, fieldKey, filename, status, createdAt, deleteAfter string }
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.workspaceID, &item.objectKey, &item.fieldKey, &item.filename, &item.status, &item.createdAt, &item.deleteAfter); err != nil {
			_ = rows.Close()
			return result, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	_ = rows.Close()
	for _, item := range candidates {
		if err := s.contextErr(ctx); err != nil {
			return result, err
		}
		result.Scanned++
		referenced, err := s.artifactReferenced(ctx, item.workspaceID, item.objectKey, item.fieldKey, item.filename)
		if err != nil {
			return result, err
		}
		if referenced {
			if err := s.updateArtifactState(ctx, item.workspaceID, item.id, "referenced", now, time.Time{}); err != nil {
				return result, err
			}
			result.Referenced++
			continue
		}
		deleteAfter, _ := time.Parse(time.RFC3339Nano, item.deleteAfter)
		if item.status == "referenced" || deleteAfter.IsZero() {
			if err := s.updateArtifactState(ctx, item.workspaceID, item.id, "orphaned", time.Time{}, now.Add(uploadArtifactGracePeriod)); err != nil {
				return result, err
			}
			result.Orphaned++
			continue
		}
		if now.Before(deleteAfter) {
			continue
		}
		path, err := s.artifactPath(item.workspaceID, item.filename)
		if err != nil {
			return result, err
		}
		if err := s.removeFile(path); err != nil && !os.IsNotExist(err) {
			return result, err
		}
		if err := s.markArtifactDeleted(ctx, item.workspaceID, item.id, now); err != nil {
			return result, err
		}
		result.Deleted++
	}
	return result, nil
}

func (s *FileArtifactStore) artifactReferenced(ctx context.Context, workspaceID, objectKey, fieldKey, filename string) (bool, error) {
	if s.references == nil {
		return false, nil
	}
	return s.references.UploadArtifactReferenced(ctx, workspaceID, objectKey, fieldKey, filename)
}

// expireDownloadTasks is kept as an internal compatibility seam for existing
// adapter tests. The business operation is delegated to its source owner.
func (s *FileArtifactStore) expireDownloadTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	if s.cleaner == nil {
		return 0, nil
	}
	return s.cleaner.ExpireUploadReferences(ctx, now, limit)
}

func (s *FileArtifactStore) updateArtifactState(ctx context.Context, workspaceID, id, status string, referencedAt, deleteAfter time.Time) error {
	lastReferenced, deletion := "", ""
	if !referencedAt.IsZero() {
		lastReferenced = referencedAt.UTC().Format(time.RFC3339Nano)
	}
	if !deleteAfter.IsZero() {
		deletion = deleteAfter.UTC().Format(time.RFC3339Nano)
	}
	update := ormbuilder.NewWorkspaceUpdateBuilder(s.renderer, "lifecycle_file_artifacts", workspaceID).Set("status", status).Set("delete_after", deletion)
	if lastReferenced != "" {
		update.Set("last_referenced_at", lastReferenced)
	}
	query, args, buildErr := update.Where(ormbuilder.Equal("id", id)).Build()
	if buildErr != nil {
		return fmt.Errorf("build upload artifact state update: %w", buildErr)
	}
	_, err := s.database(ctx).ExecContext(ctx, query, args...)
	return err
}

func (s *FileArtifactStore) markArtifactDeleted(ctx context.Context, workspaceID, id string, now time.Time) error {
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.renderer, "lifecycle_file_artifacts", workspaceID).
		Set("status", "deleted").Set("delete_after", "").Set("deleted_at", now.UTC().Format(time.RFC3339Nano)).Where(ormbuilder.Equal("id", id)).Build()
	if buildErr != nil {
		return fmt.Errorf("build upload artifact deletion: %w", buildErr)
	}
	_, err := s.database(ctx).ExecContext(ctx, query, args...)
	return err
}

func (s *FileArtifactStore) artifactPath(workspaceID, filename string) (string, error) {
	workspace, err := lifecycleaccess.NewWorkspaceID(workspaceID)
	if err != nil {
		return "", err
	}
	if filename == "" || filepath.Base(filename) != filename {
		return "", fmt.Errorf("invalid upload artifact filename")
	}
	root, err := s.absPath(strings.TrimSpace(s.uploadRoot))
	if err != nil || strings.TrimSpace(s.uploadRoot) == "" {
		return "", fmt.Errorf("upload root is required")
	}
	digest := sha256.Sum256([]byte(workspace.String()))
	path := filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]), filename)
	relative, err := s.relPath(root, path)
	if err != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("upload artifact path escapes root")
	}
	return path, nil
}
