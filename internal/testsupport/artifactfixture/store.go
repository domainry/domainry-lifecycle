package artifactfixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

// Store is a process-local shared Artifact port used only by Lifecycle module
// tests. Runtime coverage exercises the real SQL adapter.
type Store struct {
	mu       sync.Mutex
	values   map[string]sharedartifact.Artifact
	bindings map[string]sharedartifact.Binding
}

func NewStore() *Store {
	return &Store{values: map[string]sharedartifact.Artifact{}, bindings: map[string]sharedartifact.Binding{}}
}

func artifactKey(workspaceID, id string) string {
	return strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(id)
}

func (s *Store) Register(_ context.Context, value sharedartifact.Artifact) (sharedartifact.Artifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := artifactKey(value.WorkspaceID, value.ID)
	if existing, found := s.values[key]; found {
		if sameRegistration(existing, value) {
			return cloneArtifact(existing), false, nil
		}
		return sharedartifact.Artifact{}, false, sharedartifact.ErrIdentityConflict
	}
	for _, existing := range s.values {
		if existing.WorkspaceID != value.WorkspaceID || existing.Owner != value.Owner || existing.Kind != value.Kind {
			continue
		}
		if existing.IdempotencyKey == value.IdempotencyKey || existing.StorageReference == value.StorageReference {
			return sharedartifact.Artifact{}, false, sharedartifact.ErrIdentityConflict
		}
	}
	s.values[key] = cloneArtifact(value)
	return cloneArtifact(value), true, nil
}

func (s *Store) ByID(_ context.Context, workspaceID, id string) (sharedartifact.Artifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, found := s.values[artifactKey(workspaceID, id)]
	return cloneArtifact(value), found, nil
}

func (s *Store) ByDownloadTokenHash(_ context.Context, workspaceID, tokenHash string) (sharedartifact.Artifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range s.values {
		if value.WorkspaceID == workspaceID && value.DownloadTokenSHA256 == tokenHash {
			return cloneArtifact(value), true, nil
		}
	}
	return sharedartifact.Artifact{}, false, nil
}

func (s *Store) Transition(_ context.Context, workspaceID, id string, expected, next sharedartifact.Status, scan sharedartifact.ScanStatus, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := artifactKey(workspaceID, id)
	value, found := s.values[key]
	if !found || value.Status != expected {
		return false, nil
	}
	value.Status, value.ScanStatus, value.UpdatedAt = next, scan, at
	s.values[key] = value
	return true, nil
}

func (s *Store) Bind(_ context.Context, value sharedartifact.Binding) (sharedartifact.Binding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.values[artifactKey(value.WorkspaceID, value.ArtifactID)]; !found {
		return sharedartifact.Binding{}, false, sharedartifact.ErrBindingConflict
	}
	key := artifactKey(value.WorkspaceID, value.ID)
	if existing, found := s.bindings[key]; found {
		if sameBinding(existing, value) {
			return cloneBinding(existing), false, nil
		}
		return sharedartifact.Binding{}, false, sharedartifact.ErrBindingConflict
	}
	s.bindings[key] = cloneBinding(value)
	return cloneBinding(value), true, nil
}

func (s *Store) Bindings(_ context.Context, workspaceID, artifactID string) ([]sharedartifact.Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []sharedartifact.Binding{}
	for _, value := range s.bindings {
		if value.WorkspaceID == workspaceID && value.ArtifactID == artifactID {
			result = append(result, cloneBinding(value))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (s *Store) List(_ context.Context, workspaceID string, query sharedartifact.Query) ([]sharedartifact.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []sharedartifact.Artifact{}
	for _, value := range s.values {
		if workspaceID != "" && value.WorkspaceID != workspaceID {
			continue
		}
		if query.Owner != "" && value.Owner != query.Owner || query.Kind != "" && value.Kind != query.Kind ||
			query.Filename != "" && value.Filename != query.Filename || query.StorageReference != "" && value.StorageReference != query.StorageReference {
			continue
		}
		if !containsStatus(query.Statuses, value.Status) || !containsScanStatus(query.ScanStatuses, value.ScanStatus) {
			continue
		}
		if !query.ExpiresAtOrBefore.IsZero() && (value.ExpiresAt.IsZero() || value.ExpiresAt.After(query.ExpiresAtOrBefore)) {
			continue
		}
		if (len(query.CreatedBy) > 0 || len(query.OwnerOrgIDs) > 0) && !containsString(query.CreatedBy, value.CreatedBy) && !containsString(query.OwnerOrgIDs, value.OwnerOrgID) {
			continue
		}
		if query.Binding != nil && !s.hasBinding(value, *query.Binding) {
			continue
		}
		result = append(result, cloneArtifact(value))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			if query.NewestFirst {
				return result[i].ID > result[j].ID
			}
			return result[i].ID < result[j].ID
		}
		if query.NewestFirst {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	limit := query.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Store) hasBinding(artifact sharedartifact.Artifact, query sharedartifact.BindingQuery) bool {
	for _, value := range s.bindings {
		if value.WorkspaceID != artifact.WorkspaceID || value.ArtifactID != artifact.ID {
			continue
		}
		if query.Owner != "" && value.Owner != query.Owner || query.Kind != "" && value.Kind != query.Kind ||
			query.ResourceType != "" && value.ResourceType != query.ResourceType || query.ResourceID != "" && value.ResourceID != query.ResourceID ||
			query.FieldKey != "" && value.FieldKey != query.FieldKey {
			continue
		}
		return true
	}
	return false
}

func (s *Store) Update(_ context.Context, mutation sharedartifact.Mutation) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := artifactKey(mutation.WorkspaceID, mutation.ID)
	value, found := s.values[key]
	if !found || value.Owner != mutation.Owner || value.Kind != mutation.Kind || value.Status != mutation.ExpectedStatus || value.ScanStatus != mutation.ExpectedScanStatus {
		return false, nil
	}
	value.Status, value.ScanStatus, value.ExpiresAt = mutation.Status, mutation.ScanStatus, mutation.ExpiresAt
	value.Metadata = append(value.Metadata[:0:0], mutation.Metadata...)
	value.UpdatedAt = mutation.UpdatedAt
	s.values[key] = value
	return true, nil
}

func containsStatus(values []sharedartifact.Status, value sharedartifact.Status) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsScanStatus(values []sharedartifact.ScanStatus, value sharedartifact.ScanStatus) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}

func sameRegistration(left, right sharedartifact.Artifact) bool {
	return left.WorkspaceID == right.WorkspaceID && left.ID == right.ID && left.Owner == right.Owner && left.Kind == right.Kind &&
		left.IdempotencyKey == right.IdempotencyKey && left.Filename == right.Filename && left.MediaType == right.MediaType &&
		left.ContentSHA256 == right.ContentSHA256 && left.SizeBytes == right.SizeBytes && left.StorageReference == right.StorageReference
}

func sameBinding(left, right sharedartifact.Binding) bool {
	return left.WorkspaceID == right.WorkspaceID && left.ID == right.ID && left.ArtifactID == right.ArtifactID && left.Owner == right.Owner &&
		left.Kind == right.Kind && left.ResourceType == right.ResourceType && left.ResourceID == right.ResourceID && left.FieldKey == right.FieldKey
}

func cloneArtifact(value sharedartifact.Artifact) sharedartifact.Artifact {
	value.Metadata = append(value.Metadata[:0:0], value.Metadata...)
	return value
}

func cloneBinding(value sharedartifact.Binding) sharedartifact.Binding {
	value.Metadata = append(value.Metadata[:0:0], value.Metadata...)
	return value
}

type Content struct {
	mu     sync.Mutex
	values map[string][]byte
}

func NewContent() *Content { return &Content{values: map[string][]byte{}} }

func (s *Content) PutImmutable(_ context.Context, workspaceID, key string, content []byte) (lifecyclecontract.ArtifactContentInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := artifactKey(workspaceID, key)
	if existing, found := s.values[identity]; found && !bytes.Equal(existing, content) {
		return lifecyclecontract.ArtifactContentInfo{}, sharedartifact.ErrIdentityConflict
	}
	s.values[identity] = append([]byte(nil), content...)
	return contentInfo(key, content), nil
}

func (s *Content) Open(_ context.Context, workspaceID, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, found := s.values[artifactKey(workspaceID, key)]
	if !found {
		return nil, lifecyclecontract.ErrArtifactContentNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), content...))), nil
}

func (s *Content) Stat(_ context.Context, workspaceID, key string) (lifecyclecontract.ArtifactContentInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, found := s.values[artifactKey(workspaceID, key)]
	if !found {
		return lifecyclecontract.ArtifactContentInfo{}, lifecyclecontract.ErrArtifactContentNotFound
	}
	return contentInfo(key, content), nil
}

func (s *Content) Delete(_ context.Context, workspaceID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, artifactKey(workspaceID, key))
	return nil
}

func contentInfo(key string, content []byte) lifecyclecontract.ArtifactContentInfo {
	digest := sha256.Sum256(content)
	return lifecyclecontract.ArtifactContentInfo{Reference: key, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}
}

var _ sharedartifact.ManagedStore = (*Store)(nil)
var _ lifecyclecontract.ArtifactContentStore = (*Content)(nil)
var _ lifecyclecontract.ArtifactContentWriter = (*Content)(nil)
