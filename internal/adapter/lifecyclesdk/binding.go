// Package lifecyclesdk adapts Lifecycle application and infrastructure
// services to the public deployment-neutral SDK.
package lifecyclesdk

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	"github.com/domainry/domainry-lifecycle-sdk/repository"
	lifecycleapplication "github.com/domainry/domainry-lifecycle/internal/application/lifecycle"
	persistence "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence"
)

type Binding struct {
	repository   persistence.LifecycleStore
	host         modulehost.Host
	transactions *lifecycleapplication.TransactionApplicationService
}

func NewBinding(host modulehost.Host) *Binding {
	return &Binding{
		repository:   persistence.NewLifecycleStore(host),
		host:         host,
		transactions: lifecycleapplication.NewTransactionApplicationService(host.Transactions()),
	}
}

func (*Binding) Descriptor() sdk.Descriptor {
	return sdk.Descriptor{
		ProtocolVersion: sdk.ProtocolVersionV1,
		Mode:            sdk.DeploymentModeModule,
		Capabilities: sdk.Capabilities{
			Governance: true, SubjectRequests: true, RetentionWorker: true,
			UploadArtifacts: true, ArchiveEvidence: true,
		},
	}
}

func (b *Binding) Repository() repository.LifecycleRepository {
	if b == nil {
		return nil
	}
	return b.repository
}

func (b *Binding) UploadArtifacts(options sdk.UploadArtifactOptions) (contract.UploadFileArtifactStore, error) {
	if b == nil || b.host == nil || strings.TrimSpace(options.Root) == "" || options.Fields == nil {
		return nil, fmt.Errorf("Lifecycle upload artifact options are incomplete")
	}
	storeOptions := make([]persistence.FileArtifactStoreOption, 0, 2)
	if options.References != nil {
		storeOptions = append(storeOptions, persistence.WithUploadArtifactReferences(options.References))
	}
	if options.ExpiredReferences != nil {
		storeOptions = append(storeOptions, persistence.WithExpiredUploadReferenceCleaner(options.ExpiredReferences))
	}
	return persistence.NewFileArtifactStore(b.host, options.Fields, options.Root, storeOptions...), nil
}

func (*Binding) SubjectArtifacts(root string) (contract.SubjectArtifactStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("Lifecycle subject artifact root is required")
	}
	return persistence.NewSubjectArtifactStore(root), nil
}

func (b *Binding) ArchiveStore() contract.ArchiveStore {
	if b == nil {
		return persistence.NewArchiveWriter(nil)
	}
	return persistence.NewArchiveWriter(b.host)
}

func (b *Binding) WithinTransaction(ctx context.Context, operation func(context.Context) error) error {
	if b == nil {
		return fmt.Errorf("Lifecycle transaction requires binding and operation")
	}
	return b.transactions.WithinTransaction(ctx, operation)
}

func (*Binding) Close(context.Context) error { return nil }

var _ sdk.Binding = (*Binding)(nil)
