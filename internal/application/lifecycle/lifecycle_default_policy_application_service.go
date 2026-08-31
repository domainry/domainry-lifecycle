package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclepolicy "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/service"
)

func (s *LifecycleApplicationService) InstallDefaultPolicies(ctx context.Context, workspaceID string, principal lifecycleaccess.Principal, now time.Time) error {
	if s == nil || s.policies == nil {
		return fmt.Errorf("lifecycle repository unavailable")
	}
	if err := lifecycleAuthorizeWorkspaceOrSystem(principal, workspaceID, PermissionPolicyManage); err != nil {
		return err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	return s.withinTransaction(ctx, func(transactionContext context.Context) error {
		for _, version := range lifecyclepolicy.DefaultPolicyCatalog(workspaceID, principal.UserID, now) {
			if _, found, err := s.policies.LatestPolicy(transactionContext, workspaceID, version.Policy.Key); err != nil {
				return err
			} else if found {
				continue
			}
			if err := s.policies.SavePolicy(transactionContext, version); err != nil {
				return err
			}
		}
		return s.audit(transactionContext, workspaceID, "lifecycle.policy.defaults_installed", principal.UserID, workspaceID, "", map[string]any{"workspace_id": workspaceID})
	})
}
