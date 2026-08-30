// Package access contains host-neutral caller and system-scope values. Hosts
// translate their identity/principal model at the module boundary.
package access

import (
	"fmt"
	"strings"
)

const InstallationWorkspaceID = "default"

type SystemScopeKind string

const (
	SystemScopeGlobal       SystemScopeKind = "global"
	SystemScopeBootstrap    SystemScopeKind = "bootstrap"
	SystemScopeInstallation SystemScopeKind = "installation"
)

type SystemScope struct {
	Kind    SystemScopeKind
	Purpose string
}

func NewSystemScope(kind SystemScopeKind, purpose string) SystemScope {
	return SystemScope{Kind: kind, Purpose: strings.TrimSpace(purpose)}
}

func (s SystemScope) Valid() bool {
	switch s.Kind {
	case SystemScopeGlobal, SystemScopeBootstrap, SystemScopeInstallation:
		return strings.TrimSpace(s.Purpose) != ""
	default:
		return false
	}
}

type Principal struct {
	UserID      string
	WorkspaceID string
	Known       bool
	SystemScope SystemScope
	Permissions map[string]struct{}
}

func (p Principal) HasPermission(permission string) bool {
	_, ok := p.Permissions[strings.TrimSpace(permission)]
	return ok
}

func NewSystemPrincipal(userID string, scope SystemScope, permissions ...string) Principal {
	grants := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		if permission = strings.TrimSpace(permission); permission != "" {
			grants[permission] = struct{}{}
		}
	}
	return Principal{UserID: strings.TrimSpace(userID), Known: scope.Valid(), SystemScope: scope, Permissions: grants}
}

func ValidateWorkspaceID(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("workspace id is required")
	}
	return nil
}

func ValidateSystemScope(scope SystemScope) error {
	if !scope.Valid() {
		return fmt.Errorf("valid system scope is required")
	}
	return nil
}

type WorkspaceID struct{ value string }

func NewWorkspaceID(value string) (WorkspaceID, error) {
	if err := ValidateWorkspaceID(value); err != nil {
		return WorkspaceID{}, err
	}
	return WorkspaceID{value: strings.TrimSpace(value)}, nil
}

func (id WorkspaceID) String() string { return id.value }

type SystemCommandScope struct{ scope SystemScope }
type SystemQueryScope struct{ scope SystemScope }

func NewSystemCommandScope(scope SystemScope) (SystemCommandScope, error) {
	if err := ValidateSystemScope(scope); err != nil {
		return SystemCommandScope{}, err
	}
	return SystemCommandScope{scope: scope}, nil
}

func NewSystemQueryScope(scope SystemScope) (SystemQueryScope, error) {
	if err := ValidateSystemScope(scope); err != nil {
		return SystemQueryScope{}, err
	}
	return SystemQueryScope{scope: scope}, nil
}
