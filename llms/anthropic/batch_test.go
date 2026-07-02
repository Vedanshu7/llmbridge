package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func TestBatchCreateSuccess(t *testing.T) {
	var gotBody map[string]interface{}
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "batch_abc123"})
	})

	id, err := p.BatchCreate(context.Background(), []types.Request{
		{Messages: []types.Message{{Role: "user", Content: "hi"}}},
	})
	if err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}
	if id != "batch_abc123" {
		t.Fatalf("id = %q, want batch_abc123", id)
	}

	reqs, ok := gotBody["requests"].([]interface{})
	if !ok || len(reqs) != 1 {
		t.Fatalf("unexpected requests array: %+v", gotBody)
	}
	item, ok := reqs[0].(map[string]interface{})
	if !ok || item["custom_id"] != "req-0" {
		t.Fatalf("unexpected batch item: %+v", item)
	}
	if _, ok := item["params"]; !ok {
		t.Fatalf("expected params field in batch item: %+v", item)
	}
}

func TestBatchCreateHTTPError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
	})

	if _, err := p.BatchCreate(context.Background(), []types.Request{
		{Messages: []types.Message{{Role: "user", Content: "hi"}}},
	}); err == nil {
		t.Fatal("expected error for HTTP 400 response")
	}
}

func TestBatchStatusMapsProcessingStatus(t *testing.T) {
	cases := []struct {
		wire string
		want string
	}{
		{"in_progress", "in_progress"},
		{"canceling", "cancelled"},
		{"ended", "completed"},
	}
	for _, tc := range cases {
		t.Run(tc.wire, func(t *testing.T) {
			_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":                "batch_1",
					"processing_status": tc.wire,
					"request_counts": map[string]int{
						"processing": 1, "succeeded": 2, "errored": 0, "canceled": 0, "expired": 0,
					},
				})
			})

			status, counts, err := p.BatchStatus(context.Background(), "batch_1")
			if err != nil {
				t.Fatalf("BatchStatus: %v", err)
			}
			if status != tc.want {
				t.Errorf("status = %q, want %q", status, tc.want)
			}
			if counts["total"] != 3 {
				t.Errorf("total = %d, want 3", counts["total"])
			}
			if counts["completed"] != 2 {
				t.Errorf("completed = %d, want 2", counts["completed"])
			}
		})
	}
}

func TestBatchResultsSuccessAndErrorLines(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/results") {
			w.Header().Set("Content-Type", "application/x-ndjson")
			lines := []string{
				`{"custom_id":"req-0","result":{"type":"succeeded","message":{"content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-6"}}}`,
				`{"custom_id":"req-1","result":{"type":"errored","error":{"message":"rate limited"}}}`,
			}
			_, _ = w.Write([]byte(strings.Join(lines, "\n")))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "batch_1", "processing_status": "ended",
			"request_counts": map[string]int{"processing": 0, "succeeded": 1, "errored": 1, "canceled": 0, "expired": 0},
		})
	})

	results, err := p.BatchResults(context.Background(), "batch_1")
	if err != nil {
		t.Fatalf("BatchResults: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Index != 0 || results[0].Response == nil || results[0].Response.Content != "ok" {
		t.Errorf("unexpected first result: %+v", results[0])
	}
	if results[1].Index != 1 || results[1].Err == nil {
		t.Errorf("unexpected second result: %+v", results[1])
	}
}

func TestBatchResultsNotYetEnded(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/results") {
			t.Fatal("results endpoint should not be called before the batch has ended")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "batch_1", "processing_status": "in_progress",
			"request_counts": map[string]int{"processing": 1, "succeeded": 0, "errored": 0, "canceled": 0, "expired": 0},
		})
	})

	if _, err := p.BatchResults(context.Background(), "batch_1"); err == nil {
		t.Fatal("expected error for a batch that has not ended")
	}
}
