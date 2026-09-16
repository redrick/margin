package inventory

import (
	"errors"
	"testing"
	"time"
)

func TestReserve(t *testing.T) {
	t.Run("takes stock", func(t *testing.T) {
		s := NewStore(nil)
		s.Add("apple", 3)
		if err := s.Reserve("apple", 2); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects non-positive quantity", func(t *testing.T) {
		s := NewStore(nil)
		if err := s.Reserve("apple", 0); !errors.Is(err, ErrInvalidQuantity) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("insufficient stock leaves store unchanged", func(t *testing.T) {
		s := NewStore(nil)
		s.Add("pear", 1)
		if err := s.Reserve("pear", 2); !errors.Is(err, ErrInsufficient) {
			t.Fatalf("got %v", err)
		}
		if err := s.Reserve("pear", 1); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAuditLogRecords(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	log := NewAuditLog(func() time.Time { return now })
	s := NewStore(log)
	s.Add("fig", 5)
	if err := s.Reserve("fig", 2); err != nil {
		t.Fatal(err)
	}
	if got := log.Entries(); len(got) != 1 || got[0].Delta != -2 {
		t.Fatalf("entries = %+v", got)
	}
}
