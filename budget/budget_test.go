package budget

import (
	"errors"
	"testing"

	"github.com/Vedanshu7/llmbridge/exceptions"
)

func TestRecordSpendNoLimit(t *testing.T) {
	m := NewManager()
	if err := m.RecordSpend("k", 1.00); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.GetSpend("k"); got != 1.00 {
		t.Fatalf("got %.2f want 1.00", got)
	}
}

func TestBudgetExceeded(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 1.00)
	_ = m.RecordSpend("k", 0.50)
	err := m.RecordSpend("k", 0.60) // total 1.10 > 1.00
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	var be *exceptions.BudgetExceededError
	if !errors.As(err, &be) {
		t.Fatalf("wrong error type: %T", err)
	}
}

func TestBudgetNotExceeded(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 2.00)
	if err := m.RecordSpend("k", 1.99); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReset(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 1.00)
	_ = m.RecordSpend("k", 0.90)
	m.Reset("k")
	if m.GetSpend("k") != 0 {
		t.Fatal("expected spend to be reset to 0")
	}
	// Should no longer be over budget.
	if err := m.RecordSpend("k", 0.50); err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
}

func TestRemoveLimit(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 0.01)
	m.RemoveLimit("k")
	if err := m.RecordSpend("k", 999); err != nil {
		t.Fatalf("unexpected error after removing limit: %v", err)
	}
}

func TestGetLimit(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 5.00)
	l, ok := m.GetLimit("k")
	if !ok || l != 5.00 {
		t.Fatalf("got limit=%.2f ok=%v want 5.00/true", l, ok)
	}
	_, ok = m.GetLimit("no-such-key")
	if ok {
		t.Fatal("expected no limit for unknown key")
	}
}

func TestSummary(t *testing.T) {
	m := NewManager()
	m.SetLimit("a", 10.00)
	_ = m.RecordSpend("a", 12.00)
	_ = m.RecordSpend("b", 3.00)
	s := m.Summary()
	if !s["a"].Over {
		t.Fatal("expected a to be over budget")
	}
	if s["b"].Over {
		t.Fatal("expected b not to be over budget (no limit)")
	}
}
