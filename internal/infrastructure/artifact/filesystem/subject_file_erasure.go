package filesystem

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

func (s *SubjectStore) DeleteSubjectFileVersion(ctx context.Context, reference lifecyclecontract.SubjectFileReference, expected lifecyclecontract.SubjectFileEvidence) (lifecyclecontract.SubjectFileEvidence, error) {
	if err := ctx.Err(); err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	workspaceID, filename, err := subjectFileIdentity(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	digest, decodeErr := hex.DecodeString(expected.SHA256)
	if decodeErr != nil || len(digest) != 32 || expected.Size < 0 || len(expected.Content) != 0 || expected.Reference != reference.Reference || expected.Filename != filename || strings.TrimSpace(reference.Reference) != "/uploads/"+filename {
		return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file erasure requires exact prepared evidence")
	}
	if s.content != nil {
		current, err := s.content.Stat(ctx, workspaceID, filename)
		if errors.Is(err, lifecyclecontract.ErrArtifactContentNotFound) {
			return expected, nil
		}
		if err != nil {
			return lifecyclecontract.SubjectFileEvidence{}, err
		}
		if !strings.EqualFold(strings.TrimSpace(current.SHA256), strings.TrimSpace(expected.SHA256)) || current.Size != expected.Size {
			return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file version changed after erasure planning")
		}
		if err := s.content.Delete(ctx, workspaceID, filename); err != nil && !errors.Is(err, lifecyclecontract.ErrArtifactContentNotFound) {
			return lifecyclecontract.SubjectFileEvidence{}, err
		}
		return expected, nil
	}
	path, _, err := s.subjectFilePath(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	// Uploads are immutable after publication. Reject filesystem aliases so a
	// pointer in one workspace cannot identify another workspace's physical file.
	for _, candidate := range []string{filepath.Dir(path), path} {
		info, statErr := os.Lstat(candidate)
		if os.IsNotExist(statErr) {
			return expected, nil
		}
		if statErr != nil {
			return lifecyclecontract.SubjectFileEvidence{}, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file erasure rejects symbolic links")
		}
	}
	current, err := s.ExportSubjectFile(ctx, reference)
	if os.IsNotExist(err) {
		return expected, nil
	}
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if current.SHA256 != expected.SHA256 || current.Size != expected.Size {
		return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file version changed after erasure planning")
	}
	if err := ctx.Err(); err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if err := localRemove(path); err != nil && !os.IsNotExist(err) {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	return expected, nil
}

var _ lifecyclecontract.SubjectFileVersionDeleter = (*SubjectStore)(nil)
