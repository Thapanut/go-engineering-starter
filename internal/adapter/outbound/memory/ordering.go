package memory

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// OrderStore implements the ordering module's TxManager in memory, with the same
// copy-on-write transactions as Store. It shares nothing with Store: each module
// owns its data (ADR-0005).
type OrderStore struct {
	mu     sync.Mutex
	orders map[string]ordering.Order
}

var _ orderingport.TxManager = (*OrderStore)(nil)

// NewOrderStore returns an empty store.
func NewOrderStore() *OrderStore { return &OrderStore{orders: map[string]ordering.Order{}} }

// WithinTx runs fn against a private copy and commits it only if fn succeeds.
func (s *OrderStore) WithinTx(ctx context.Context, fn func(context.Context, orderingport.Repositories) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	work := maps.Clone(s.orders)
	if err := fn(ctx, orderingport.Repositories{Orders: orderRepo{orders: work}}); err != nil {
		return err
	}
	s.orders = work
	return nil
}

type orderRepo struct{ orders map[string]ordering.Order }

// cloneOrder copies the lines so callers never share a slice with the store.
func cloneOrder(o ordering.Order) ordering.Order {
	o.Lines = slices.Clone(o.Lines)
	return o
}

func (r orderRepo) Create(_ context.Context, o ordering.Order) error {
	if _, dup := r.orders[o.ID]; dup {
		return kernel.ErrConflict
	}
	r.orders[o.ID] = cloneOrder(o)
	return nil
}

func (r orderRepo) Get(_ context.Context, id string) (ordering.Order, error) {
	o, ok := r.orders[id]
	if !ok {
		return ordering.Order{}, kernel.ErrNotFound
	}
	return cloneOrder(o), nil
}

func (r orderRepo) SetInvoiceNo(_ context.Context, id, invoiceNo string, at time.Time) error {
	o, ok := r.orders[id]
	if !ok {
		return kernel.ErrNotFound
	}
	o.InvoiceNo, o.UpdatedAt = invoiceNo, at
	r.orders[id] = o
	return nil
}
