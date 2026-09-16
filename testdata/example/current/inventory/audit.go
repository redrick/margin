package inventory

import (
	"sync"
	"time"
)

type Entry struct {
	At    time.Time
	SKU   string
	Delta int
}

type AuditLog struct {
	mu      sync.Mutex
	entries []Entry
	now     func() time.Time
}

func NewAuditLog(now func() time.Time) *AuditLog {
	return &AuditLog{now: now}
}

func (l *AuditLog) Record(sku string, delta int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, Entry{At: l.now(), SKU: sku, Delta: delta})
}

func (l *AuditLog) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Entry(nil), l.entries...)
}
