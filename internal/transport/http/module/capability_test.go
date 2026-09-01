package modulehttptransport

import (
	"testing"

	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulecapability/contracttest"
)

func TestLifecycleCapabilityTracksOwnerRoutesWithoutInventedAuthoringValidation(t *testing.T) {
	binding, err := NewCapabilityBinding()
	if err != nil {
		t.Fatal(err)
	}
	contracttest.VerifyBinding(t, binding)
	summary, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Identity.SupportedDeploymentModes) != 1 || summary.Identity.SupportedDeploymentModes[0] != modulecapability.DeploymentModeModule {
		t.Fatalf("Lifecycle topology=%v", summary.Identity.SupportedDeploymentModes)
	}
	operations := 0
	for _, category := range summary.Categories {
		operations += category.OperationCount
		if len(category.ValidationScopes) != 0 {
			t.Fatalf("Lifecycle runtime request DTOs leaked into model validation scopes: %v", category.ValidationScopes)
		}
	}
	routes, err := lifecycleRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if operations != len(routes) {
		t.Fatalf("Lifecycle disclosure operations=%d routes=%d", operations, len(routes))
	}
}
