package modulehttptransport

import (
	"errors"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/modulehttp"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
)

const (
	readAudit     = "owner_read_audit_policy"
	mutationAudit = "mutation_audit_required"
	noIdempotency = "not_applicable"
	callerKey     = "caller_key_required"
)

type lifecycleHTTPSurface struct {
	handler http.Handler
	routes  []modulehttp.Route
}

func (*lifecycleHTTPSurface) ContractVersion() string { return modulehttp.ContractVersion }
func (*lifecycleHTTPSurface) Owner() string           { return "lifecycle" }
func (*lifecycleHTTPSurface) Name() string            { return "lifecycle_governance" }
func (s *lifecycleHTTPSurface) Handler() http.Handler { return s.handler }
func (s *lifecycleHTTPSurface) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
}

func (s *lifecycleHTTPSurface) OpenAPIOperations() map[string]map[string]any {
	return lifecycleOpenAPIOperations()
}

func NewSurface(governance lifecyclesdk.Governance) (modulehttp.Surface, error) {
	if governance == nil {
		return nil, errors.New("Lifecycle module HTTP governance is unavailable")
	}
	handler := &lifecycleHTTPHandler{governance: governance, mux: http.NewServeMux()}
	handler.register()
	routes := []modulehttp.Route{
		readRoute("GET /operations/lifecycle/policies", lifecyclesdk.PermissionPolicyManage),
		writeRoute("POST /operations/lifecycle/policies", lifecyclesdk.PermissionPolicyManage, modulehttp.HighRiskReasonRequired),
		writeRoute("POST /operations/lifecycle/legal-holds", lifecyclesdk.PermissionPolicyManage, modulehttp.HighRiskReasonRequired),
		writeRoute("POST /operations/lifecycle/legal-holds/{holdID}/end", lifecyclesdk.PermissionPolicyManage, modulehttp.HighRiskReasonRequired),
		readRoute("GET /operations/lifecycle/cleanup/preview", lifecyclesdk.PermissionCleanupRun),
		writeRoute("POST /operations/lifecycle/cleanup/jobs", lifecyclesdk.PermissionCleanupRun, modulehttp.HighRiskReasonRequired),
		readRoute("GET /operations/lifecycle/metrics", lifecyclesdk.PermissionPolicyManage),
		readRoute("GET /operations/lifecycle/archive", lifecyclesdk.PermissionPolicyManage),
		writeRoute("POST /operations/lifecycle/subjects", lifecyclesdk.PermissionSubjectManage, modulehttp.HighRiskReasonRequired),
		writeRoute("POST /operations/lifecycle/subjects/{requestID}/verify", lifecyclesdk.PermissionSubjectManage, modulehttp.HighRiskReasonRequired),
		readPOSTRoute("POST /operations/lifecycle/subjects/{requestID}/preview", lifecyclesdk.PermissionSubjectManage),
		writeRoute("POST /operations/lifecycle/subjects/{requestID}/approve", lifecyclesdk.PermissionSubjectManage, modulehttp.HighRiskConfirmationRequired),
		writeRoute("POST /operations/lifecycle/subjects/{requestID}/execute", lifecyclesdk.PermissionSubjectManage, modulehttp.HighRiskConfirmationRequired),
		readRoute("GET /operations/lifecycle/subjects/{requestID}/download", lifecyclesdk.PermissionSubjectManage),
		readRoute("GET /operations/lifecycle/external-erasures", lifecyclesdk.PermissionSubjectManage),
		writeRoute("POST /operations/lifecycle/external-erasures/{erasureID}/reconcile", lifecyclesdk.PermissionSubjectManage, modulehttp.HighRiskConfirmationRequired),
		writeRoute("POST /operations/lifecycle/deletions/replay", lifecyclesdk.PermissionSubjectManage, modulehttp.HighRiskConfirmationRequired),
	}
	return &lifecycleHTTPSurface{handler: handler.mux, routes: routes}, nil
}

func readRoute(pattern, permission string) modulehttp.Route {
	return lifecycleRoute(pattern, permission, modulehttp.EffectRead, modulehttp.HighRiskNone, noIdempotency, readAudit)
}

func readPOSTRoute(pattern, permission string) modulehttp.Route {
	return readRoute(pattern, permission)
}

func writeRoute(pattern, permission string, risk modulehttp.HighRiskPolicy) modulehttp.Route {
	return lifecycleRoute(pattern, permission, modulehttp.EffectWrite, risk, callerKey, mutationAudit)
}

func lifecycleRoute(pattern, permission string, effect modulehttp.EffectClass, risk modulehttp.HighRiskPolicy, idempotency, audit string) modulehttp.Route {
	return modulehttp.Route{
		Pattern: pattern,
		Exposures: []modulehttp.Exposure{
			modulehttp.ExposureTenantAdmin,
			modulehttp.ExposureOps,
		},
		Authentication: modulehttp.AuthenticationAuthenticated,
		Permission:     strings.TrimSpace(permission),
		Governance: &modulehttp.Governance{
			EffectClass: effect, HighRiskPolicy: risk,
			IdempotencyDecision: idempotency, AuditClass: audit,
		},
	}
}

var _ modulehttp.Surface = (*lifecycleHTTPSurface)(nil)
var _ modulehttp.OpenAPIProvider = (*lifecycleHTTPSurface)(nil)
