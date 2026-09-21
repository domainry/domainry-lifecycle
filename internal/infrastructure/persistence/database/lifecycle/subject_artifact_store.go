package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	filesystem "github.com/domainry/domainry-lifecycle/internal/infrastructure/artifact/filesystem"
	"github.com/domainry/domainry-orm/query"
)

type SubjectArtifactStore struct {
	*filesystem.SubjectStore
	host modulehost.Host
}

func NewSubjectArtifactStore(host modulehost.Host, root string, content ...lifecyclecontract.ArtifactContentStore) *SubjectArtifactStore {
	return &SubjectArtifactStore{SubjectStore: filesystem.NewSubjectStore(root, content...), host: host}
}

func (s *SubjectArtifactStore) DeleteSubjectFileVersion(ctx context.Context, ref lifecyclecontract.SubjectFileReference, expected lifecyclecontract.SubjectFileEvidence) (lifecyclecontract.SubjectFileEvidence, error) {
	if s.host == nil || s.host.Transactions() == nil {
		return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file registry transaction unavailable")
	}
	var result lifecyclecontract.SubjectFileEvidence
	err := s.host.Transactions().WithinTransaction(ctx, func(txctx context.Context, tx modulehost.DBTX) error {
		statement, args, err := query.NewWorkspaceSelectBuilder(s.host.Dialect(), "_lifecycle_file_artifacts", ref.WorkspaceID).Columns("sha256", "size_bytes").Where(query.Equal("filename", expected.Filename)).Build()
		if err != nil {
			return err
		}
		var digest string
		var size int64
		err = tx.QueryRowContext(txctx, statement, args...).Scan(&digest, &size)
		registered := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if registered && (digest != expected.SHA256 || size != expected.Size) {
			return fmt.Errorf("subject file registry version changed")
		}
		result, err = s.SubjectStore.DeleteSubjectFileVersion(txctx, ref, expected)
		if err != nil {
			return err
		}
		if !registered {
			return nil
		}
		statement, args, err = query.NewWorkspaceDeleteBuilder(s.host.Dialect(), "_lifecycle_file_artifacts", ref.WorkspaceID).
			Where(query.And(query.Equal("filename", expected.Filename), query.Equal("sha256", expected.SHA256), query.Equal("size_bytes", expected.Size))).Build()
		if err != nil {
			return err
		}
		deleted, err := tx.ExecContext(txctx, statement, args...)
		if err != nil {
			return err
		}
		count, err := deleted.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("subject file registry version changed")
		}
		return nil
	})
	return result, err
}

var _ lifecyclecontract.SubjectFileVersionDeleter = (*SubjectArtifactStore)(nil)
