// Package lifecycle coordinates Lifecycle use cases over domain ports.
package lifecycle

import (
	"context"
	"fmt"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
)

// TransactionApplicationService keeps transaction orchestration out of the
// SDK adapter and delegates the physical boundary to the embedding host.
type TransactionApplicationService struct {
	transactions modulehost.Transactor
}

func NewTransactionApplicationService(transactions modulehost.Transactor) *TransactionApplicationService {
	return &TransactionApplicationService{transactions: transactions}
}

func (s *TransactionApplicationService) WithinTransaction(ctx context.Context, operation func(context.Context) error) error {
	if s == nil || s.transactions == nil || operation == nil {
		return fmt.Errorf("Lifecycle transaction requires binding and operation")
	}
	return s.transactions.WithinTransaction(ctx, func(transactionContext context.Context, _ modulehost.DBTX) error {
		return operation(transactionContext)
	})
}
