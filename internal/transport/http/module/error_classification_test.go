package modulehttptransport

import (
	"net/http"
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
		class  lifecyclesdk.ErrorClass
		status int
		kind   string
		code   string
	}{
		{lifecyclesdk.ErrorClassUnavailable, http.StatusServiceUnavailable, "unavailable", "lifecycle.database_unavailable"},
		{lifecyclesdk.ErrorClassConflict, http.StatusConflict, "conflict", "lifecycle.execution_fence_rejected"},
		{lifecyclesdk.ErrorClassBadRequest, http.StatusBadRequest, "bad_request", "lifecycle.validation_failed"},
		{lifecyclesdk.ErrorClassForbidden, http.StatusForbidden, "forbidden", "lifecycle.permission_denied"},
	}
	for _, test := range tests {
		capture := &classificationCapture{ResponseRecorder: httptest.NewRecorder()}
		writeError(capture, &lifecyclesdk.Error{Class: test.class, Code: test.code})
		if capture.Code != test.status || capture.kind != test.kind || capture.code != test.code {
			t.Fatalf("status=%d kind=%q code=%q", capture.Code, capture.kind, capture.code)
		}
	}
}
