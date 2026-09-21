package modulehttptransport

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/modulehttp"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleapplication "github.com/domainry/domainry-lifecycle/internal/application/lifecycle"
)

type lifecycleHTTPAdapter struct {
	handler    http.Handler
	routes     []modulehttp.Route
	operations map[string]map[string]any
}

func (*lifecycleHTTPAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (*lifecycleHTTPAdapter) Owner() string           { return "lifecycle" }
func (*lifecycleHTTPAdapter) Name() string            { return "lifecycle_governance" }
func (s *lifecycleHTTPAdapter) Handler() http.Handler { return s.handler }
func (s *lifecycleHTTPAdapter) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
}

func (s *lifecycleHTTPAdapter) OpenAPIOperations() map[string]map[string]any {
	return s.operations
}

func NewAdapter(governance lifecyclesdk.Governance) (modulehttp.Adapter, error) {
	if governance == nil {
		return nil, errors.New("Lifecycle module HTTP governance is unavailable")
	}
	handler := &lifecycleHTTPHandler{governance: governance, mux: http.NewServeMux()}
	routes, err := lifecycleRoutes()
	if err != nil {
		return nil, err
	}
	handlers := handler.handlers()
	byAction := lifecycleOpenAPIOperationsByAction()
	operations := make(map[string]map[string]any, len(routes))
	for _, route := range routes {
		key := strings.TrimSpace(route.Action.Key)
		implementation, found := handlers[key]
		if !found {
			return nil, fmt.Errorf("Lifecycle Action %q has no HTTP handler", key)
		}
		operation, found := byAction[key]
		if !found {
			return nil, fmt.Errorf("Lifecycle Action %q has no OpenAPI operation", key)
		}
		handler.mux.HandleFunc(route.Pattern(), implementation)
		operations[route.Pattern()] = operation
		delete(handlers, key)
		delete(byAction, key)
	}
	if len(handlers) != 0 || len(byAction) != 0 {
		keys := make([]string, 0, len(handlers)+len(byAction))
		for key := range handlers {
			keys = append(keys, "handler:"+key)
		}
		for key := range byAction {
			keys = append(keys, "openapi:"+key)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("Lifecycle implementations have no Action manifest entries: %v", keys)
	}
	return &lifecycleHTTPAdapter{handler: handler.mux, routes: routes, operations: operations}, nil
}

func lifecycleRoutes() ([]modulehttp.Route, error) {
	definitions, err := lifecycleapplication.AuthorizationActions()
	if err != nil {
		return nil, err
	}
	routes := make([]modulehttp.Route, 0, len(definitions))
	for _, definition := range definitions {
		if definition.HTTP == nil {
			continue
		}
		route, err := modulehttp.RouteFromAction(definition)
		if err != nil {
			return nil, fmt.Errorf("project Lifecycle Action %q: %w", definition.Key, err)
		}
		routes = append(routes, route)
	}
	return routes, nil
}

// CapabilityRoutes returns the immutable source-owned route manifest used by
// the public capability contract.
func CapabilityRoutes() ([]modulehttp.Route, error) {
	return lifecycleRoutes()
}

var _ modulehttp.Adapter = (*lifecycleHTTPAdapter)(nil)
var _ modulehttp.OpenAPIProvider = (*lifecycleHTTPAdapter)(nil)
