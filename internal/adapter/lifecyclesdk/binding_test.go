package lifecyclesdk

import (
	"errors"
	"testing"

	sdk "github.com/domainry/domainry-lifecycle-sdk"
)

func TestAdaptErrorMapsIndependentApprovalToStableConflict(t *testing.T) {
	err := adaptError(errors.New("subject request requires independent approval"))
	var lifecycleErr *sdk.Error
	if !errors.As(err, &lifecycleErr) {
		t.Fatalf("adapted error type=%T", err)
	}
	if lifecycleErr.StatusCode != 409 || lifecycleErr.Code != "lifecycle.subject_request.independent_approval_required" {
		t.Fatalf("adapted error=%#v", lifecycleErr)
	}
}
