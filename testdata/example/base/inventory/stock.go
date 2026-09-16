package inventory

import (
	"errors"
	"sync"
)

var ErrInsufficient = errors.New("insufficient stock")

type Store struct {
	mu    sync.Mutex
	items map[string]int
}

func NewStore() *Store {
	return &Store{items: make(map[string]int)}
}

func (s *Store) Add(sku string, qty int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[sku] += qty
}

func (s *Store) Reserve(sku string, qty int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[sku] < qty {
		return ErrInsufficient
	}
	s.items[sku] -= qty
	return nil
}

func (s *Store) ReleaseAll(sku string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.items[sku]
	delete(s.items, sku)
	return n
}
