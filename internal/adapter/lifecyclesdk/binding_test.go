package lifecyclesdk

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	sdk "github.com/domainry/domainry-lifecycle-sdk"
)

func TestAdaptErrorMapsIndependentApprovalToStableConflict(t *testing.T) {
	err := adaptError(apperror.New(apperror.KindConflict, "lifecycle.subject_request.independent_approval_required", errors.New("subject request requires independent approval"), nil))
	var lifecycleErr *sdk.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("adapted error type=%T", err)
	}
	if lifecycleErr.Class != sdk.ErrorClassConflict || lifecycleErr.Code != "lifecycle.subject_request.independent_approval_required" {
		t.Fatalf("adapted error=%#v", lifecycleErr)
	}
}

func TestAdaptErrorMapsRequirementFailuresToInvalidRequest(t *testing.T) {
	err := adaptError(apperror.New(apperror.KindBadRequest, "lifecycle.invalid_request", errors.New("retention shortening requires approval and change plan"), nil))
	var lifecycleErr *sdk.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("adapted error type=%T", err)
	}
	if lifecycleErr.Class != sdk.ErrorClassBadRequest || lifecycleErr.Code != "lifecycle.invalid_request" {
		t.Fatalf("adapted error=%#v", lifecycleErr)
	}
}
