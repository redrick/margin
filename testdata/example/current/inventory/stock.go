package inventory

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrInsufficient    = errors.New("insufficient stock")
	ErrInvalidQuantity = errors.New("quantity must be positive")
)

type Store struct {
	mu    sync.Mutex
	items map[string]int
	log   *AuditLog
}

func NewStore(log *AuditLog) *Store {
	return &Store{items: make(map[string]int), log: log}
}

func (s *Store) Add(sku string, qty int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[sku] += qty
}

// Reserve takes qty units of sku out of stock, or fails without changing anything.
func (s *Store) Reserve(sku string, qty int) error {
	if qty <= 0 {
		return ErrInvalidQuantity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	available := s.items[sku]
	if available < qty {
		return fmt.Errorf("%w: %s has %d, want %d", ErrInsufficient, sku, available, qty)
	}
	s.items[sku] = available - qty
	s.log.Record(sku, -qty)
	return nil
}
