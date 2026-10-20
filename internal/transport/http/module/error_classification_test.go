package modulehttptransport

import (
	"net/http/httptest"
	"testing"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
)

type classificationCapture struct {
	*httptest.ResponseRecorder
	kind, code string
}

func (capture *classificationCapture) RecordError(kind, code string) {
	capture.kind, capture.code = kind, code
}

func TestLifecycleErrorClassificationMatrix(t *testing.T) {
	tests := []struct {
		status int
		kind   string
		code   string
	}{
		{503, "unavailable", "lifecycle.database_unavailable"},
		{502, "unavailable", "lifecycle.owner_dependency_unavailable"},
		{409, "conflict", "lifecycle.execution_fence_rejected"},
		{400, "bad_request", "lifecycle.validation_failed"},
		{403, "forbidden", "lifecycle.permission_denied"},
	}
	for _, test := range tests {
		capture := &classificationCapture{ResponseRecorder: httptest.NewRecorder()}
		writeError(capture, &lifecyclesdk.Error{StatusCode: test.status, Code: test.code})
		if capture.kind != test.kind || capture.code != test.code {
			t.Fatalf("status=%d kind=%q code=%q", test.status, capture.kind, capture.code)
		}
	}
}
