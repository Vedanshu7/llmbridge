package budget

import (
	"testing"
)

func TestAlertFires(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 10.00)
	m.SetAlertThreshold("k", 0.8) // fire at 80%

	var fired bool
	m.OnAlert(func(keyID string, spend, limit float64) {
		if keyID == "k" && spend >= 8.0 && limit == 10.0 {
			fired = true
		}
	})

	_ = m.RecordSpend("k", 8.01) // crosses 80%
	if !fired {
		t.Fatal("expected alert to fire at 80% threshold")
	}
}

func TestAlertFiresOnce(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 10.00)
	m.SetAlertThreshold("k", 0.5)

	count := 0
	m.OnAlert(func(keyID string, spend, limit float64) { count++ })

	_ = m.RecordSpend("k", 6.0) // first cross
	_ = m.RecordSpend("k", 1.0) // still above threshold
	if count != 1 {
		t.Fatalf("expected alert to fire once, fired %d times", count)
	}
}

func TestAlertResetAfterReset(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 10.00)
	m.SetAlertThreshold("k", 0.5)

	count := 0
	m.OnAlert(func(keyID string, spend, limit float64) { count++ })

	_ = m.RecordSpend("k", 6.0) // fires
	m.Reset("k")
	_ = m.RecordSpend("k", 6.0) // should fire again after reset
	if count != 2 {
		t.Fatalf("expected 2 alerts after reset, got %d", count)
	}
}

func TestAlertNotFiredBelowThreshold(t *testing.T) {
	m := NewManager()
	m.SetLimit("k", 10.00)
	m.SetAlertThreshold("k", 0.8)

	fired := false
	m.OnAlert(func(keyID string, spend, limit float64) { fired = true })

	_ = m.RecordSpend("k", 7.99) // 79.9%, below threshold
	if fired {
		t.Fatal("alert should not fire below threshold")
	}
}

func TestAlertNoLimitNoFire(t *testing.T) {
	m := NewManager()
	m.SetAlertThreshold("k", 0.5) // threshold with no limit

	fired := false
	m.OnAlert(func(keyID string, spend, limit float64) { fired = true })

	_ = m.RecordSpend("k", 999)
	if fired {
		t.Fatal("alert should not fire without a limit set")
	}
}
