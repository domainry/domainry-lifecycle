package lifecycle

import (
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
)

func TestAuthorizationActionsFreezeAsOneExactManifest(t *testing.T) {
	definitions, err := AuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 21 {
		t.Fatalf("Action count=%d", len(definitions))
	}
	registry := actioncontract.NewRegistry()
	if err := registry.Register(definitions...); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	if permissions := registry.PermissionDefinitions(); len(permissions) != 17 {
		t.Fatalf("Permission count=%d", len(permissions))
	}

	httpActions := 0
	for _, definition := range registry.Definitions() {
		if definition.Owner != LifecycleAuthorizationOwner {
			t.Fatalf("Action %q owner=%q", definition.Key, definition.Owner)
		}
		if len(definition.NonHTTP) != 1 || definition.NonHTTP[0].Kind != "sdk" || definition.NonHTTP[0].InvocationKey != definition.Key {
			t.Fatalf("Action %q SDK binding=%#v", definition.Key, definition.NonHTTP)
		}
		if definition.Permission == nil {
			if definition.HTTP != nil || definition.Authorization.Strategy != actioncontract.AuthorizationSigned {
				t.Fatalf("system Action is not operations-only: %#v", definition)
			}
			continue
		}
		httpActions++
		permission := definition.Permission
		if definition.HTTP == nil || definition.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || permission.Key != definition.Key || permission.Key != permission.ResourceKey+"."+permission.OperationKey || strings.Contains(permission.Key, "*") {
			t.Fatalf("role Action is not exact: Action=%#v Permission=%#v", definition, permission)
		}
	}
	if httpActions != 17 {
		t.Fatalf("HTTP role Action count=%d", httpActions)
	}
}

func TestOperationsActionsRequireSystemScopeInsteadOfRolePermission(t *testing.T) {
	rolePrincipal := lifecycleaccess.Principal{
		Known: true, WorkspaceID: "workspace-a", UserID: "operator-a",
		Permissions: map[string]struct{}{lifecyclesdk.ActionLifecycleCleanupJobsProcess: {}},
	}
	if err := lifecycleAuthorizeSystem(rolePrincipal); err == nil {
		t.Fatal("role Permission authorized a system-only Lifecycle Action")
	}
	systemPrincipal := lifecycleaccess.NewSystemPrincipal(
		"lifecycle-worker",
		lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "process cleanup jobs"),
	)
	if err := lifecycleAuthorizeSystem(systemPrincipal); err != nil {
		t.Fatalf("valid system scope was denied: %v", err)
	}
}
