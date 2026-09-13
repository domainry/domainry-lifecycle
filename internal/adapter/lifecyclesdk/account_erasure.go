package lifecyclesdk

import (
	"context"
	"time"

	"github.com/domainry/domainry-lifecycle-sdk/access"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	model "github.com/domainry/domainry-lifecycle-sdk/model"
	application "github.com/domainry/domainry-lifecycle/internal/application/lifecycle"
)

func (b *Binding) AccountErasures() contract.AccountErasures {
	if service := b.application(); service != nil {
		return accountErasures{service}
	}
	return nil
}

type accountErasures struct {
	service *application.LifecycleApplicationService
}

func (b accountErasures) StageApprovedAccountErasure(ctx context.Context, approval contract.AccountErasureApproval, scope access.SystemScope) (model.SubjectRequest, error) {
	request, err := b.service.StageApprovedAccountErasure(ctx, approval, scope)
	if err != nil {
		return model.SubjectRequest{}, err
	}
	return convert[model.SubjectRequest](request)
}
func (b accountErasures) GetAccountErasure(ctx context.Context, reference contract.AccountErasureReference, scope access.SystemScope) (model.SubjectRequest, error) {
	result, err := b.service.GetAccountErasure(ctx, reference, scope)
	if err != nil {
		return model.SubjectRequest{}, err
	}
	return convert[model.SubjectRequest](result)
}
func (b accountErasures) ProcessApprovedAccountErasures(ctx context.Context, owner string, limit int, now time.Time, scope access.SystemScope) (int, error) {
	return b.service.ProcessApprovedAccountErasures(ctx, owner, limit, now, scope)
}

var _ contract.AccountErasures = accountErasures{}
