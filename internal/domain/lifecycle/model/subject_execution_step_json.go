package model

import (
	"encoding/json"
	"time"
)

type subjectExecutionStepJSON struct {
	WorkspaceID string          `json:"workspace_id"`
	RequestID   string          `json:"request_id"`
	Owner       string          `json:"owner"`
	Operation   string          `json:"operation"`
	Payload     json.RawMessage `json:"payload"`
	CompletedAt int64           `json:"completed_at"`
}

func (value SubjectExecutionStep) MarshalJSON() ([]byte, error) {
	completedAt := int64(0)
	if !value.CompletedAt.IsZero() {
		completedAt = value.CompletedAt.UTC().UnixMilli()
	}
	return json.Marshal(subjectExecutionStepJSON{
		WorkspaceID: value.WorkspaceID, RequestID: value.RequestID, Owner: value.Owner,
		Operation: value.Operation, Payload: value.Payload, CompletedAt: completedAt,
	})
}

func (value *SubjectExecutionStep) UnmarshalJSON(raw []byte) error {
	var stored subjectExecutionStepJSON
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	completedAt := time.Time{}
	if stored.CompletedAt != 0 {
		completedAt = time.UnixMilli(stored.CompletedAt).UTC()
	}
	*value = SubjectExecutionStep{
		WorkspaceID: stored.WorkspaceID, RequestID: stored.RequestID, Owner: stored.Owner,
		Operation: stored.Operation, Payload: stored.Payload, CompletedAt: completedAt,
	}
	return nil
}
