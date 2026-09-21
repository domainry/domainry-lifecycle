package capability

import (
	"encoding/json"
	"fmt"

	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulehttp"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclehttp "github.com/domainry/domainry-lifecycle/internal/transport/http/module"
)

const (
	lifecycleGovernanceCategory = lifecyclesdk.CapabilityLifecycleGovernance
	lifecycleSubjectsCategory   = lifecyclesdk.CapabilityLifecycleSubjects
)

func openContract(_ Inputs) (*modulecapability.StaticBinding, error) {
	routes, err := lifecyclehttp.CapabilityRoutes()
	if err != nil {
		return nil, err
	}
	groups := map[string][]modulehttp.Route{}
	for _, route := range routes {
		groups[route.Action.CapabilityKey] = append(groups[route.Action.CapabilityKey], route)
	}
	byAction := lifecyclehttp.CapabilityOpenAPIOperationsByAction()
	operations := make(map[string]map[string]any, len(routes))
	for _, route := range routes {
		operation, found := byAction[route.Action.Key]
		if !found {
			return nil, fmt.Errorf("Lifecycle Action %q has no OpenAPI operation", route.Action.Key)
		}
		operations[route.Pattern()] = operation
		delete(byAction, route.Action.Key)
	}
	if len(byAction) != 0 {
		return nil, fmt.Errorf("Lifecycle OpenAPI operations have no Action manifest entries")
	}
	definitions := []struct {
		key, name, description string
		assembly               []string
		validation             []string
	}{
		{
			key: lifecycleGovernanceCategory, name: "Retention governance", description: "Publish retention policies, place legal holds, preview and execute governed cleanup, and inspect retained evidence.",
			assembly:   []string{"owner_retention_contract_to_lifecycle_policy", "lifecycle_cleanup_to_owner_executor", "lifecycle_evidence_to_audit_review"},
			validation: []string{},
		},
		{
			key: lifecycleSubjectsCategory, name: "Data-subject lifecycle", description: "Create, verify, approve, execute, and reconcile governed subject export or erasure requests.",
			assembly:   []string{"identity_resolution_to_subject_request", "subject_request_to_owner_handlers", "external_erasure_to_integration_connector"},
			validation: []string{},
		},
	}
	documents := make([]modulecapability.CategoryDocument, 0, len(definitions))
	for _, definition := range definitions {
		document, err := modulecapability.CategoryFromHTTPRoutes(modulecapability.HTTPRouteCategory{
			Owner:    "lifecycle",
			Category: modulecapability.CategorySummary{Key: definition.key, Name: definition.name, Description: definition.description, AssemblyChains: definition.assembly, ValidationScopes: definition.validation},
			Routes:   groups[definition.key], Operations: operations,
			Components: map[string]map[string]json.RawMessage{
				"securitySchemes": {"BearerAuth": json.RawMessage(`{"type":"http","scheme":"bearer","bearerFormat":"JWT"}`)},
			},
		})
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	summary := modulecapability.ModuleSummary{
		Identity: modulecapability.ModuleIdentity{
			Key: "lifecycle", SourceOwner: "lifecycle", ModuleVersion: "domainry-lifecycle-protocol-v1",
			ValidationRevision: "lifecycle-owner-validation-v1", SupportedDeploymentModes: []modulecapability.DeploymentMode{modulecapability.DeploymentModeModule},
		},
		Name: "Lifecycle", Description: "Retention, legal-hold, cleanup, subject-export, and erasure governance across source-owned module data.",
		Composition: modulecapability.ModuleComposition{
			ProvidedCapabilities: []string{"lifecycle.policy", "lifecycle.legal_hold", "lifecycle.cleanup", "lifecycle.subject_export", "lifecycle.subject_erasure", "lifecycle.deletion_replay"},
			RequiredModules:      []string{"identity"}, OptionalModules: []string{"audit", "integration"}, ConflictingModules: []string{},
			AssemblyChains:   []string{"owner_retention_contract_to_lifecycle_policy", "lifecycle_cleanup_to_owner_executor", "identity_resolution_to_subject_request", "subject_request_to_owner_handlers", "external_erasure_to_integration_connector", "lifecycle_evidence_to_audit_review"},
			ValidationScopes: []string{},
		},
	}
	return modulecapability.NewStaticBinding(summary, documents, nil)
}
