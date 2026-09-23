package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

const lifecycleSubjectFileLimit = 5 << 20

type SubjectStore struct {
	uploadRoot string
	content    lifecyclecontract.ArtifactContentStore
}

func NewSubjectStore(directory string, content ...lifecyclecontract.ArtifactContentStore) *SubjectStore {
	var storage lifecyclecontract.ArtifactContentStore
	if len(content) > 0 {
		storage = content[0]
	}
	return &SubjectStore{uploadRoot: directory, content: storage}
}

// DeleteExpiredUploadStaging removes only interrupted upload temporary files.
// Final content-addressed files are deliberately excluded until the upload
// owner has a durable reference registry capable of proving orphan status.
func (s *SubjectStore) DeleteExpiredUploadStaging(ctx context.Context, now time.Time) (int, error) {
	if strings.TrimSpace(s.uploadRoot) == "" {
		return 0, fmt.Errorf("upload root is required")
	}
	root, err := localAbsPath(strings.TrimSpace(s.uploadRoot))
	if err != nil {
		return 0, fmt.Errorf("upload root is required")
	}
	entries, err := localReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-time.Hour)
	deleted := 0
	for _, workspace := range entries {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if !workspace.IsDir() || !strings.HasPrefix(workspace.Name(), "workspace-") {
			continue
		}
		workspacePath := filepath.Join(root, workspace.Name())
		files, readErr := localReadDir(workspacePath)
		if readErr != nil {
			return deleted, readErr
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasPrefix(file.Name(), ".upload-") {
				continue
			}
			info, infoErr := localDirEntryInfo(file)
			if infoErr != nil {
				return deleted, infoErr
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			if removeErr := localRemove(filepath.Join(workspacePath, file.Name())); removeErr != nil {
				return deleted, removeErr
			}
			deleted++
		}
	}
	return deleted, nil
}

func (s *SubjectStore) ExportSubjectFile(ctx context.Context, reference lifecyclecontract.SubjectFileReference) (lifecyclecontract.SubjectFileEvidence, error) {
	workspaceID, filename, err := subjectFileIdentity(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if s.content != nil {
		if err := ctx.Err(); err != nil {
			return lifecyclecontract.SubjectFileEvidence{}, err
		}
		reader, err := s.content.Open(ctx, workspaceID, filename)
		if err != nil {
			return lifecyclecontract.SubjectFileEvidence{}, err
		}
		defer reader.Close()
		content, err := io.ReadAll(io.LimitReader(reader, lifecycleSubjectFileLimit+1))
		if err != nil {
			return lifecyclecontract.SubjectFileEvidence{}, err
		}
		if len(content) > lifecycleSubjectFileLimit {
			return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file exceeds governed upload limit")
		}
		digest := sha256.Sum256(content)
		return lifecyclecontract.SubjectFileEvidence{Reference: reference.Reference, Filename: filename, ContentType: http.DetectContentType(content), Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), Content: content}, nil
	}
	path, filename, err := s.subjectFilePath(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if err := ctx.Err(); err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	file, err := localOpenFile(path)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if info.Size() > lifecycleSubjectFileLimit {
		return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file exceeds governed upload limit")
	}
	content, err := localReadFile(path)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	digest := sha256.Sum256(content)
	return lifecyclecontract.SubjectFileEvidence{Reference: reference.Reference, Filename: filename, ContentType: http.DetectContentType(content), Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), Content: content}, nil
}

func (s *SubjectStore) DeleteSubjectFile(ctx context.Context, reference lifecyclecontract.SubjectFileReference) (lifecyclecontract.SubjectFileEvidence, error) {
	workspaceID, filename, err := subjectFileIdentity(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	if s.content != nil {
		evidence, err := s.ExportSubjectFile(ctx, reference)
		if err != nil {
			return lifecyclecontract.SubjectFileEvidence{}, err
		}
		evidence.Content = nil
		if err := s.content.Delete(ctx, workspaceID, filename); err != nil {
			return lifecyclecontract.SubjectFileEvidence{}, err
		}
		return evidence, nil
	}
	path, _, err := s.subjectFilePath(reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	evidence, err := s.ExportSubjectFile(ctx, reference)
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	evidence.Content = nil
	if err := localRemove(path); err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	return evidence, nil
}

func (s *SubjectStore) subjectFilePath(reference lifecyclecontract.SubjectFileReference) (string, string, error) {
	workspaceID, filename, err := subjectFileIdentity(reference)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(workspaceID))
	workspaceDirectory := "workspace-" + hex.EncodeToString(digest[:16])
	if strings.TrimSpace(s.uploadRoot) == "" {
		return "", "", fmt.Errorf("upload root is required")
	}
	root, err := localAbsPath(strings.TrimSpace(s.uploadRoot))
	if err != nil {
		return "", "", fmt.Errorf("upload root is required")
	}
	path := filepath.Join(root, workspaceDirectory, filename)
	return path, filename, nil
}

func subjectFileIdentity(reference lifecyclecontract.SubjectFileReference) (string, string, error) {
	workspace, err := lifecycleaccess.NewWorkspaceID(reference.WorkspaceID)
	if err != nil {
		return "", "", err
	}
	parsed, err := url.Parse(strings.TrimSpace(reference.Reference))
	if err != nil {
		return "", "", err
	}
	filename := filepath.Base(parsed.Path)
	if filename == "." || filename == ".." || filename == string(filepath.Separator) || strings.Contains(filename, "..") {
		return "", "", fmt.Errorf("invalid subject file reference")
	}
	return workspace.String(), filename, nil
}
