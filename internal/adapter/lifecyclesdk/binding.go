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
	sdkpersistence "github.com/domainry/domainry-lifecycle-sdk/persistence"
	lifecycleapplication "github.com/domainry/domainry-lifecycle/internal/application/lifecycle"
	infra "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence"
)

type Binding struct {
	repository   infra.LifecycleStore
	host         modulehost.Host
	transactions *lifecycleapplication.TransactionApplicationService
}

func NewBinding(host modulehost.Host) *Binding {
	return &Binding{
		repository:   infra.NewLifecycleStore(host),
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

func (b *Binding) Repository() sdkpersistence.LifecycleRepository {
	if b == nil {
		return nil
	}
	return b.repository
}

func (b *Binding) UploadArtifacts(options sdk.UploadArtifactOptions) (contract.UploadFileArtifactStore, error) {
	if b == nil || b.host == nil || strings.TrimSpace(options.Root) == "" || options.Fields == nil {
		return nil, fmt.Errorf("Lifecycle upload artifact options are incomplete")
	}
	storeOptions := make([]infra.FileArtifactStoreOption, 0, 2)
	if options.References != nil {
		storeOptions = append(storeOptions, infra.WithUploadArtifactReferences(options.References))
	}
	if options.ExpiredReferences != nil {
		storeOptions = append(storeOptions, infra.WithExpiredUploadReferenceCleaner(options.ExpiredReferences))
	}
	return infra.NewFileArtifactStore(b.host, options.Fields, options.Root, storeOptions...), nil
}

func (*Binding) SubjectArtifacts(root string) (contract.SubjectArtifactStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("Lifecycle subject artifact root is required")
	}
	return infra.NewSubjectArtifactStore(root), nil
}

func (b *Binding) ArchiveStore() contract.ArchiveStore {
	if b == nil {
		return infra.NewArchiveWriter(nil)
	}
	return infra.NewArchiveWriter(b.host)
}

func (b *Binding) WithinTransaction(ctx context.Context, operation func(context.Context) error) error {
	if b == nil {
		return fmt.Errorf("Lifecycle transaction requires binding and operation")
	}
	return b.transactions.WithinTransaction(ctx, operation)
}

func (*Binding) Close(context.Context) error { return nil }

var _ sdk.Binding = (*Binding)(nil)
