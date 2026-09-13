package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

func (s *SubjectStore) DeleteSubjectExport(ctx context.Context, workspaceID, reference string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if workspaceID == "" {
		return fmt.Errorf("subject export workspace is required")
	}
	path, err := s.path(reference)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("subject export is not a regular file")
	}
	raw, err := localReadFile(path)
	if err != nil {
		return err
	}
	var artifact lifecycleSubjectArtifact
	if json.Unmarshal(raw, &artifact) != nil || artifact.WorkspaceID != workspaceID {
		return fmt.Errorf("subject export workspace mismatch")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := localRemove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
