package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
)

type transactionStub struct {
	called bool
}

func (s *transactionStub) WithinTransaction(ctx context.Context, operation func(context.Context, modulehost.DBTX) error) error {
	s.called = true
	return operation(ctx, nil)
}

func TestTransactionApplicationServiceDelegatesToHost(t *testing.T) {
	host := &transactionStub{}
	service := NewTransactionApplicationService(host)
	want := errors.New("operation")
	if err := service.WithinTransaction(t.Context(), func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if !host.called {
		t.Fatal("host transaction boundary was not used")
	}
}

func TestTransactionApplicationServiceRejectsIncompleteRequest(t *testing.T) {
	if err := (*TransactionApplicationService)(nil).WithinTransaction(t.Context(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("nil service accepted transaction")
	}
	if err := NewTransactionApplicationService(&transactionStub{}).WithinTransaction(t.Context(), nil); err == nil {
		t.Fatal("nil operation accepted transaction")
	}
}
