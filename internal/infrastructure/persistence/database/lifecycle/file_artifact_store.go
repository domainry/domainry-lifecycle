package lifecycle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
)

const uploadArtifactGracePeriod = 24 * time.Hour

var errUploadArtifactIdentityConflict = errors.New("lifecycle upload artifact identity conflict")

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
	if existing, found, lookupErr := s.findRegisteredUpload(ctx, artifact.WorkspaceID, "id", artifact.ID); lookupErr != nil {
		return lookupErr
	} else if found {
		if sameUploadArtifactIdentity(existing, artifact) {
			return nil
		}
		return errUploadArtifactIdentityConflict
	}
	if _, found, lookupErr := s.findRegisteredUpload(ctx, artifact.WorkspaceID, "filename", artifact.Filename); lookupErr != nil {
		return lookupErr
	} else if found {
		return errUploadArtifactIdentityConflict
	}
	createdAt := artifact.CreatedAt.UTC().Format(time.RFC3339Nano)
	queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(s.renderer, "_lifecycle_file_artifacts", artifact.WorkspaceID).
		Columns("id", "object_key", "field_key", "filename", "content_type", "sha256", "size_bytes", "status", "scan_status", "scan_provider", "scan_evidence_ref", "scanned_at", "created_at", "last_referenced_at", "delete_after", "deleted_at").
		Values(artifact.ID, artifact.ObjectKey, artifact.FieldKey, artifact.Filename, artifact.ContentType, artifact.SHA256, artifact.Size, "staged", lifecyclecontract.FileScanPending, "", "", "", createdAt, "", artifact.CreatedAt.UTC().Add(uploadArtifactGracePeriod).Format(time.RFC3339Nano), "").Build()
	if buildErr != nil {
		return fmt.Errorf("build upload artifact insert: %w", buildErr)
	}
	if _, err = s.database(ctx).ExecContext(ctx, queryValue, args...); err == nil {
		return nil
	}
	// Resolve concurrent identical registration into the same idempotent result.
	if existing, found, lookupErr := s.findRegisteredUpload(ctx, artifact.WorkspaceID, "id", artifact.ID); lookupErr == nil && found {
		if sameUploadArtifactIdentity(existing, artifact) {
			return nil
		}
		return errUploadArtifactIdentityConflict
	}
	if _, found, lookupErr := s.findRegisteredUpload(ctx, artifact.WorkspaceID, "filename", artifact.Filename); lookupErr == nil && found {
		return errUploadArtifactIdentityConflict
	}
	return err
}

func (s *FileArtifactStore) findRegisteredUpload(ctx context.Context, workspaceID, field, value string) (lifecyclecontract.UploadArtifact, bool, error) {
	queryValue, args, err := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_file_artifacts", workspaceID).
		Columns("id", "workspace_id", "object_key", "field_key", "filename", "content_type", "sha256", "size_bytes").Where(query.Equal(field, value)).Build()
	if err != nil {
		return lifecyclecontract.UploadArtifact{}, false, fmt.Errorf("build upload artifact lookup: %w", err)
	}
	var artifact lifecyclecontract.UploadArtifact
	err = s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&artifact.ID, &artifact.WorkspaceID, &artifact.ObjectKey, &artifact.FieldKey, &artifact.Filename, &artifact.ContentType, &artifact.SHA256, &artifact.Size)
	if err == sql.ErrNoRows {
		return lifecyclecontract.UploadArtifact{}, false, nil
	}
	return artifact, err == nil, err
}

func sameUploadArtifactIdentity(left, right lifecyclecontract.UploadArtifact) bool {
	return left.ID == right.ID && left.WorkspaceID == right.WorkspaceID && left.ObjectKey == right.ObjectKey && left.FieldKey == right.FieldKey && left.Filename == right.Filename &&
		strings.EqualFold(strings.TrimSpace(left.ContentType), strings.TrimSpace(right.ContentType)) && strings.EqualFold(strings.TrimSpace(left.SHA256), strings.TrimSpace(right.SHA256)) && left.Size == right.Size
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
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_file_artifacts", workspace.String()).Columns(columns...).
		Where(query.And(query.Equal("id", strings.TrimSpace(fileID)), query.Equal("deleted_at", ""))).Build()
	if buildErr != nil {
		return lifecyclecontract.FileScanEvidence{}, fmt.Errorf("build file scan lookup: %w", buildErr)
	}
	var evidence lifecyclecontract.FileScanEvidence
	var scannedAt string
	err = s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&evidence.FileID, &evidence.WorkspaceID, &evidence.Filename, &evidence.ContentType, &evidence.ObjectKey, &evidence.FieldKey, &evidence.SHA256, &evidence.Size, &evidence.Status, &evidence.Provider, &evidence.EvidenceRef, &scannedAt)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	if scannedAt != "" {
		evidence.ScannedAt, err = time.Parse(time.RFC3339Nano, scannedAt)
	}
	return evidence, err
}

func (s *FileArtifactStore) PendingFileScans(ctx context.Context, scope lifecycleaccess.SystemScope, limit int) ([]lifecyclecontract.FileScanEvidence, error) {
	if s == nil || s.host == nil || s.db == nil || s.renderer == nil {
		return nil, fmt.Errorf("upload artifact store unavailable")
	}
	if _, err := lifecycleaccess.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	columns := []string{"id", "workspace_id", "filename", "content_type", "object_key", "field_key", "sha256", "size_bytes", "scan_status", "scan_provider", "scan_evidence_ref", "scanned_at"}
	queryValue, args, buildErr := query.NewSelectBuilder(s.renderer, "_lifecycle_file_artifacts").Columns(columns...).
		Where(query.And(query.Equal("scan_status", lifecyclecontract.FileScanPending), query.Equal("deleted_at", ""))).
		OrderBy(query.Ascending("created_at"), query.Ascending("id")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build pending file scan query: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]lifecyclecontract.FileScanEvidence, 0, limit)
	for rows.Next() {
		var evidence lifecyclecontract.FileScanEvidence
		var scannedAt string
		if err := rows.Scan(&evidence.FileID, &evidence.WorkspaceID, &evidence.Filename, &evidence.ContentType, &evidence.ObjectKey, &evidence.FieldKey, &evidence.SHA256, &evidence.Size, &evidence.Status, &evidence.Provider, &evidence.EvidenceRef, &scannedAt); err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	return result, rows.Err()
}

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
	if current.Status != lifecyclecontract.FileScanPending {
		if current.Status == evidence.Status && current.Provider == strings.TrimSpace(evidence.Provider) && current.EvidenceRef == strings.TrimSpace(evidence.EvidenceRef) {
			return nil
		}
		return fmt.Errorf("file scan already has terminal evidence")
	}
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_file_artifacts", current.WorkspaceID).
		Set("scan_status", evidence.Status).Set("scan_provider", strings.TrimSpace(evidence.Provider)).Set("scan_evidence_ref", strings.TrimSpace(evidence.EvidenceRef)).
		Set("scanned_at", evidence.ScannedAt.UTC().Format(time.RFC3339Nano)).Where(query.And(query.Equal("id", current.FileID), query.Equal("sha256", current.SHA256), query.Equal("size_bytes", current.Size), query.Equal("scan_status", lifecyclecontract.FileScanPending))).Build()
	if buildErr != nil {
		return fmt.Errorf("build file scan update: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		latest, findErr := s.FindFileScan(ctx, evidence.WorkspaceID, evidence.FileID)
		if findErr == nil && latest.Status == evidence.Status && latest.Provider == strings.TrimSpace(evidence.Provider) && latest.EvidenceRef == strings.TrimSpace(evidence.EvidenceRef) {
			return nil
		}
		return fmt.Errorf("file scan already has terminal evidence")
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
	statement, args, buildErr := query.NewSelectBuilder(s.renderer, "_lifecycle_file_artifacts").Columns(columns...).
		Where(query.NotEqual("status", "deleted")).OrderBy(query.Ascending("created_at"), query.Ascending("id")).Limit(limit).Build()
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
	update := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_file_artifacts", workspaceID).Set("status", status).Set("delete_after", deletion)
	if lastReferenced != "" {
		update.Set("last_referenced_at", lastReferenced)
	}
	queryValue, args, buildErr := update.Where(query.Equal("id", id)).Build()
	if buildErr != nil {
		return fmt.Errorf("build upload artifact state update: %w", buildErr)
	}
	_, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	return err
}

func (s *FileArtifactStore) markArtifactDeleted(ctx context.Context, workspaceID, id string, now time.Time) error {
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_file_artifacts", workspaceID).
		Set("status", "deleted").Set("delete_after", "").Set("deleted_at", now.UTC().Format(time.RFC3339Nano)).Where(query.Equal("id", id)).Build()
	if buildErr != nil {
		return fmt.Errorf("build upload artifact deletion: %w", buildErr)
	}
	_, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
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
