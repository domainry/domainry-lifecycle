package application

import (
	"context"
	"fmt"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle/access"
	lifecyclepolicy "github.com/domainry/domainry-lifecycle/policy"
)

func (s *LifecycleApplicationService) InstallDefaultPolicies(ctx context.Context, workspaceID string, principal lifecycleaccess.Principal, now time.Time) error {
	if s == nil || s.policies == nil {
		return fmt.Errorf("lifecycle repository unavailable")
	}
	if err := lifecycleAuthorizeWorkspaceOrSystem(principal, workspaceID, PermissionPolicyManage); err != nil {
		return err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	for _, version := range lifecyclepolicy.DefaultPolicyCatalog(workspaceID, principal.UserID, now) {
		if _, found, err := s.policies.LatestPolicy(ctx, workspaceID, version.Policy.Key); err != nil {
			return err
		} else if found {
			continue
		}
		if err := s.policies.SavePolicy(ctx, version); err != nil {
			return err
		}
	}
	return s.audit(ctx, workspaceID, "lifecycle.policy.defaults_installed", principal.UserID, workspaceID, "", map[string]any{"workspace_id": workspaceID})
}
