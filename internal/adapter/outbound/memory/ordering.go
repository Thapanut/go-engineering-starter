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
	mu        sync.Mutex
	orders    map[string]ordering.Order
	processed map[string]time.Time // event id → processed at
}

var _ orderingport.TxManager = (*OrderStore)(nil)

// NewOrderStore returns an empty store.
func NewOrderStore() *OrderStore {
	return &OrderStore{orders: map[string]ordering.Order{}, processed: map[string]time.Time{}}
}

// WithinTx runs fn against a private copy and commits it only if fn succeeds.
func (s *OrderStore) WithinTx(ctx context.Context, fn func(context.Context, orderingport.Repositories) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	orders, processed := maps.Clone(s.orders), maps.Clone(s.processed)
	if err := fn(ctx, orderingport.Repositories{Orders: orderRepo{orders: orders}, Events: processedEvents(processed)}); err != nil {
		return err
	}
	s.orders, s.processed = orders, processed
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

// GetForUpdate needs no lock: OrderStore serializes transactions.
func (r orderRepo) GetForUpdate(ctx context.Context, id string) (ordering.Order, error) {
	return r.Get(ctx, id)
}

func (r orderRepo) UpdateStatus(_ context.Context, o ordering.Order) error {
	cur, ok := r.orders[o.ID]
	if !ok || cur.Status != ordering.AwaitingPayment {
		return kernel.ErrConflict
	}
	cur.Status, cur.UpdatedAt = o.Status, o.UpdatedAt
	r.orders[o.ID] = cur
	return nil
}

type processedEvents map[string]time.Time

func (p processedEvents) MarkProcessed(_ context.Context, eventID string, at time.Time) (bool, error) {
	if _, seen := p[eventID]; seen {
		return false, nil
	}
	p[eventID] = at
	return true, nil
}
