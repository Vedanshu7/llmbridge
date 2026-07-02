package voyage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("voyage-3", "test-key")
	p.baseURL = srv.URL
	return srv, p
}

// ---- ValidateEnvironment ----

func TestValidateEnvironmentWithKey(t *testing.T) {
	p := New("voyage-3", "test-key")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	t.Setenv("VOYAGE_API_KEY", "")
	p := &Provider{model: "voyage-3", apiKey: ""}
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error for missing api key")
	}
}

func TestNewReadsAPIKeyFromEnv(t *testing.T) {
	t.Setenv("VOYAGE_API_KEY", "env-key")
	p := New("voyage-3", "")
	if p.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want env-key", p.apiKey)
	}
}

// ---- Name / defaults ----

func TestName(t *testing.T) {
	p := New("", "k")
	if p.Name() != "voyage" {
		t.Errorf("Name() = %q, want voyage", p.Name())
	}
}

func TestDefaultModel(t *testing.T) {
	p := New("", "k")
	if p.model != defaultModel {
		t.Errorf("model = %q, want %q", p.model, defaultModel)
	}
}

// ---- Embed ----

func TestEmbedSuccess(t *testing.T) {
	var gotAuth string
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.4, 0.5}, "index": 1},
				{"embedding": []float64{0.1, 0.2}, "index": 0},
			},
		})
	})

	out, err := p.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization header = %q, want Bearer test-key", gotAuth)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(out))
	}
	if out[0][0] != 0.1 || out[1][0] != 0.4 {
		t.Errorf("embeddings not returned in index order: %+v", out)
	}
}

func TestEmbedHTTPError(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
	})

	if _, err := p.Embed(context.Background(), []string{"hello"}); err == nil {
		t.Fatal("expected error for HTTP 400 response")
	}
}

func TestEmbedEmptyInput(t *testing.T) {
	_, p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
	})

	out, err := p.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected 0 embeddings, got %d", len(out))
	}
}

// ---- Cost ----

func TestCostForEmbedding(t *testing.T) {
	cases := []struct {
		model     string
		tokens    int
		wantCost  float64
		wantError bool
	}{
		{"voyage-3", 1000, 1000 * 0.00000006, false},
		{"voyage-3-lite", 1000, 1000 * 0.00000002, false},
		{"unknown-model", 1000, 0, true},
	}
	for _, tc := range cases {
		got, err := CostForEmbedding(tc.model, tc.tokens)
		if tc.wantError {
			if err == nil {
				t.Errorf("%s: expected error, got nil", tc.model)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.model, err)
		}
		const epsilon = 1e-12
		if diff := got - tc.wantCost; diff > epsilon || diff < -epsilon {
			t.Errorf("%s: cost = %v, want %v", tc.model, got, tc.wantCost)
		}
	}
}
