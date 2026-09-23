package lifecycle

import (
	"context"
	"fmt"
	"sync"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
)

type memoryDefinitionStore struct {
	mu          sync.Mutex
	definitions map[string]metadatasdk.Definition
	versions    map[string]metadatasdk.DefinitionVersion
	nextVersion int
}

func newMemoryDefinitionStore() *memoryDefinitionStore {
	return &memoryDefinitionStore{definitions: map[string]metadatasdk.Definition{}, versions: map[string]metadatasdk.DefinitionVersion{}}
}

func (s *memoryDefinitionStore) List(_ context.Context, query metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := []metadatasdk.Definition{}
	for _, value := range s.definitions {
		if value.Status != "active" || query.Owner != "" && value.Owner != query.Owner || query.ResourceType != "" && value.ResourceType != query.ResourceType || query.SourceID != "" && value.SourceID != query.SourceID {
			continue
		}
		values = append(values, cloneDefinition(value))
	}
	return values, nil
}

func (s *memoryDefinitionStore) Get(_ context.Context, owner, resourceType, key string) (metadatasdk.Definition, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, found := s.definitions[memoryDefinitionKey(owner, resourceType, key)]
	if !found || value.Status != "active" {
		return metadatasdk.Definition{}, false, nil
	}
	return cloneDefinition(value), true, nil
}

func (s *memoryDefinitionStore) Snapshot(ctx context.Context, query metadatasdk.DefinitionQuery) (metadatasdk.DefinitionSnapshot, error) {
	values, err := s.List(ctx, query)
	return metadatasdk.DefinitionSnapshot{Definitions: values}, err
}

func (*memoryDefinitionStore) ReplaceSourceSnapshot(context.Context, metadatasdk.ProjectionSnapshot) error {
	return fmt.Errorf("test Definition snapshot replacement is unsupported")
}

func (s *memoryDefinitionStore) Publish(_ context.Context, command metadatasdk.DefinitionPublishCommand) (metadatasdk.DefinitionPublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryDefinitionKey(command.Owner, command.ResourceType, command.ResourceKey)
	current, found := s.definitions[key]
	if !found && command.ExpectedCurrentVersionID != metadatasdk.DefinitionNoCurrentVersion || found && current.CurrentVersionID != command.ExpectedCurrentVersionID {
		return metadatasdk.DefinitionPublishResult{}, fmt.Errorf("test Definition revision conflict")
	}
	s.nextVersion++
	versionID := fmt.Sprintf("test-definition-version:%d", s.nextVersion)
	value := metadatasdk.Definition{
		Owner: command.Owner, ResourceType: command.ResourceType, ResourceKey: command.ResourceKey,
		CurrentVersionID: versionID, Status: "active", ObjectKey: command.ObjectKey, Name: command.Name,
		Payload: append([]byte(nil), command.Payload...), SchemaVersion: command.SchemaVersion, SchemaHash: command.SchemaHash,
		SourceKind: command.SourceKind, SourceID: command.SourceID, PublishedBy: command.PublishedBy,
	}
	s.definitions[key] = value
	s.versions[versionID] = metadatasdk.DefinitionVersion{
		ID: versionID, Owner: command.Owner, ResourceType: command.ResourceType, ResourceKey: command.ResourceKey,
		SchemaVersion: command.SchemaVersion, SchemaHash: command.SchemaHash, Payload: append([]byte(nil), command.Payload...),
	}
	return metadatasdk.DefinitionPublishResult{Definition: cloneDefinition(value), CurrentVersionID: versionID}, nil
}

func (s *memoryDefinitionStore) Disable(_ context.Context, command metadatasdk.DefinitionDisableCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryDefinitionKey(command.Owner, command.ResourceType, command.ResourceKey)
	value, found := s.definitions[key]
	if !found || value.CurrentVersionID != command.ExpectedCurrentVersionID {
		return fmt.Errorf("test Definition revision conflict")
	}
	value.Status = "disabled"
	s.definitions[key] = value
	return nil
}

func (s *memoryDefinitionStore) GetVersion(_ context.Context, query metadatasdk.DefinitionVersionQuery) (metadatasdk.DefinitionVersion, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, found := s.versions[query.VersionID]
	if !found {
		return metadatasdk.DefinitionVersion{}, false, nil
	}
	value.Payload = append([]byte(nil), value.Payload...)
	return value, true, nil
}

func memoryDefinitionKey(owner, resourceType, key string) string {
	return owner + "\x00" + resourceType + "\x00" + key
}

func cloneDefinition(value metadatasdk.Definition) metadatasdk.Definition {
	value.Payload = append([]byte(nil), value.Payload...)
	return value
}
