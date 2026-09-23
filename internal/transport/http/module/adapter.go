package modulehttptransport

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/modulehttp"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleapplication "github.com/domainry/domainry-lifecycle/internal/application/lifecycle"
)

type lifecycleHTTPAdapter struct {
	handler http.Handler
	routes  []modulehttp.Route
}

func (*lifecycleHTTPAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (*lifecycleHTTPAdapter) Owner() string           { return "lifecycle" }
func (*lifecycleHTTPAdapter) Name() string            { return "lifecycle_governance" }
func (s *lifecycleHTTPAdapter) Handler() http.Handler { return s.handler }
func (s *lifecycleHTTPAdapter) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
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
	for _, route := range routes {
		key := strings.TrimSpace(route.Action.Key)
		implementation, found := handlers[key]
		if !found {
			return nil, fmt.Errorf("Lifecycle Action %q has no HTTP handler", key)
		}
		handler.mux.HandleFunc(route.Pattern(), implementation)
		delete(handlers, key)
	}
	if len(handlers) != 0 {
		keys := make([]string, 0, len(handlers))
		for key := range handlers {
			keys = append(keys, key)
		}
		return nil, fmt.Errorf("Lifecycle HTTP handlers have no Action route entries: %v", keys)
	}
	return &lifecycleHTTPAdapter{handler: handler.mux, routes: routes}, nil
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
