package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

func okResponse(content, model string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []map[string]interface{}{
			{
				"message":       map[string]string{"role": "assistant", "content": content},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     10,
			"completion_tokens": 5,
			"total_tokens":      15,
		},
		"model": model,
	}
}

func newMockServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewCompatible("openai-test", srv.URL, "test-key", "gpt-4o")
	return srv, p
}

func TestCompleteBasic(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okResponse("hello world", "gpt-4o"))
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello world" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
}

func TestCompleteUsage(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(okResponse("reply", "gpt-4o"))
	})
	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage data")
	}
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestCompleteRequestShape(t *testing.T) {
	var captured map[string]interface{}
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(okResponse("ok", "gpt-4o"))
	})

	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		System:   "You are helpful.",
		Messages: []types.Message{{Role: "user", Content: "Hello"}},
		Model:    "gpt-4o-mini",
	})

	msgs, _ := captured["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %d", len(msgs))
	}
	first := msgs[0].(map[string]interface{})
	if first["role"] != "system" {
		t.Fatalf("first message should be system, got %v", first["role"])
	}
}

func TestCompleteToolCall(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]interface{}{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]string{
									"name":      "get_weather",
									"arguments": `{"location":"London"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
		})
	})

	resp, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "weather?"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", resp.ToolCalls[0].Name)
	}
}

func TestCompleteHTTPError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"authentication_error"}}`))
	})
	_, err := p.Complete(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestCompleteAuthHeader(t *testing.T) {
	var authHeader string
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(okResponse("ok", "gpt-4o"))
	})
	p.Complete(context.Background(), types.Request{ //nolint:errcheck
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if authHeader != "Bearer test-key" {
		t.Fatalf("unexpected auth header: %q", authHeader)
	}
}

// ---- New / Name / ValidateEnvironment ----

func TestNewDefaultModel(t *testing.T) {
	p := New("", "key")
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want openai", p.Name())
	}
	if p.model != defaultModel {
		t.Errorf("model = %q, want %q", p.model, defaultModel)
	}
}

func TestNewCustomModel(t *testing.T) {
	p := New("gpt-4o", "key")
	if p.model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", p.model)
	}
}

func TestValidateEnvironmentMissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	p := New("gpt-4o", "")
	if err := p.ValidateEnvironment(); err == nil {
		t.Fatal("expected error when key is empty and env not set")
	}
}

func TestValidateEnvironmentFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	p := New("gpt-4o", "")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("unexpected error when key is in env: %v", err)
	}
}

func TestValidateEnvironmentCompatibleNoKey(t *testing.T) {
	// Compatible providers (non-"openai" name) do not require a key.
	p := NewCompatible("groq", "http://localhost/v1", "", "llama3")
	if err := p.ValidateEnvironment(); err != nil {
		t.Fatalf("compatible provider should not require key, got: %v", err)
	}
}

// ---- Embed ----

// newEmbedServer creates a mock that routes /v1/embeddings separately from /v1/chat/completions.
func newEmbedServer(t *testing.T, embedHandler http.HandlerFunc) *Provider {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not used", http.StatusNotFound)
	})
	mux.HandleFunc("/v1/embeddings", embedHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewCompatible("openai-test", srv.URL+"/v1/chat/completions", "test-key", "gpt-4o")
}

func TestEmbedSuccess(t *testing.T) {
	p := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
				{"embedding": []float64{0.4, 0.5, 0.6}},
			},
		})
	})

	result, err := p.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(result))
	}
	if len(result[0]) != 3 || result[0][0] != 0.1 {
		t.Errorf("unexpected embedding[0]: %v", result[0])
	}
}

func TestEmbedHTTPError(t *testing.T) {
	p := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestEmbedInvalidJSON(t *testing.T) {
	p := newEmbedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json{{"))
	})
	_, err := p.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error on malformed JSON response")
	}
}

// ---- Stream ----

func TestStreamSuccess(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	})

	ch, err := p.Stream(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var got string
	for d := range ch {
		if d.Err != nil {
			t.Fatalf("stream error: %v", d.Err)
		}
		got += d.Content
		if d.Done {
			break
		}
	}
	if got != "hello world" {
		t.Errorf("streamed content = %q, want %q", got, "hello world")
	}
}

func TestStreamHTTPError(t *testing.T) {
	_, p := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	_, err := p.Stream(context.Background(), types.Request{
		Messages: []types.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from Stream on 403")
	}
}

// ---- CostForResponse ----

func TestCostForResponseKnown(t *testing.T) {
	resp := &types.Response{
		Model:    "gpt-4o",
		Provider: "openai",
		Usage:    &types.UsageData{PromptTokens: 1000, CompletionTokens: 500},
	}
	cost, err := CostForResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost <= 0 {
		t.Errorf("expected positive cost, got %f", cost)
	}
}

func TestCostForResponseNilUsage(t *testing.T) {
	resp := &types.Response{Model: "gpt-4o", Provider: "openai"}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for nil usage")
	}
}

func TestCostForResponseUnknownModel(t *testing.T) {
	resp := &types.Response{
		Model:    "gpt-99-ultra",
		Provider: "openai",
		Usage:    &types.UsageData{PromptTokens: 100, CompletionTokens: 50},
	}
	_, err := CostForResponse(resp)
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

// redirectTransport rewrites every request's host/scheme to a fixed base URL.
type redirectTransport struct {
	base string
}

func (t *redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	parsed, _ := r.URL.Parse(t.base)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = parsed.Scheme
	r2.URL.Host = parsed.Host
	return http.DefaultTransport.RoundTrip(r2)
}

// newURLServer creates a mock server and wires a redirectTransport into p so that
// postURL / getURL calls (which use hardcoded api.openai.com URLs) go to the mock.
func newURLServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Provider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("gpt-4o", "test-key")
	p.client.Transport = &redirectTransport{base: srv.URL}
	return srv, p
}

// ---- ImageGenerate ----

func TestImageGenerateSuccess(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]string{{"url": "https://example.com/img.png", "revised_prompt": "a cat"}},
		})
	})
	resp, err := p.ImageGenerate(context.Background(), types.ImageRequest{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("ImageGenerate: %v", err)
	}
	if len(resp.Images) != 1 || resp.Images[0].URL != "https://example.com/img.png" {
		t.Errorf("unexpected images: %v", resp.Images)
	}
}

func TestImageGenerateHTTPError(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	_, err := p.ImageGenerate(context.Background(), types.ImageRequest{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

// ---- TextComplete ----

func TestTextCompleteSuccess(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"text": "The answer is 42"}},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 5, "total_tokens": 10},
		})
	})
	resp, err := p.TextComplete(context.Background(), types.TextRequest{Prompt: "What is 6*7?"})
	if err != nil {
		t.Fatalf("TextComplete: %v", err)
	}
	if resp.Text != "The answer is 42" {
		t.Errorf("Text = %q, want %q", resp.Text, "The answer is 42")
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 10 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestTextCompleteHTTPError(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limit", http.StatusTooManyRequests)
	})
	_, err := p.TextComplete(context.Background(), types.TextRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error on 429")
	}
}

// ---- Speech ----

func TestSpeechSuccess(t *testing.T) {
	audioBytes := []byte("fake-audio-data")
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(audioBytes)
	})
	resp, err := p.Speech(context.Background(), types.SpeechRequest{Input: "Hello world"})
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if string(resp.Audio) != string(audioBytes) {
		t.Errorf("Audio = %q, want %q", resp.Audio, audioBytes)
	}
	if resp.Format != "mp3" {
		t.Errorf("Format = %q, want mp3", resp.Format)
	}
}

func TestSpeechHTTPError(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	_, err := p.Speech(context.Background(), types.SpeechRequest{Input: "Hello"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
}

// ---- Moderate ----

func TestModerateSuccess(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    "modr-1",
			"model": "omni-moderation-latest",
			"results": []map[string]interface{}{
				{
					"flagged":          false,
					"categories":       map[string]bool{"hate": false},
					"category_scores":  map[string]float64{"hate": 0.001},
				},
			},
		})
	})
	resp, err := p.Moderate(context.Background(), types.ModerationRequest{Input: "Hello"})
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Flagged {
		t.Errorf("unexpected moderation results: %+v", resp.Results)
	}
}

func TestModerateHTTPError(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	})
	_, err := p.Moderate(context.Background(), types.ModerationRequest{Input: "hi"})
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

// ---- Transcribe ----

func TestTranscribeSuccess(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "hello world"})
	})
	resp, err := p.Transcribe(context.Background(), types.TranscriptionRequest{
		AudioData: []byte("fake-audio"),
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if resp.Text != "hello world" {
		t.Errorf("Text = %q, want hello world", resp.Text)
	}
}

func TestTranscribeTextFormat(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("plain text response"))
	})
	resp, err := p.Transcribe(context.Background(), types.TranscriptionRequest{
		AudioData: []byte("fake-audio"),
		Format:    "text",
	})
	if err != nil {
		t.Fatalf("Transcribe text format: %v", err)
	}
	if resp.Text != "plain text response" {
		t.Errorf("Text = %q, want 'plain text response'", resp.Text)
	}
}

func TestTranscribeHTTPError(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	_, err := p.Transcribe(context.Background(), types.TranscriptionRequest{AudioData: []byte("x")})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

// ---- Batch API ----

func TestBatchCreateSuccess(t *testing.T) {
	callN := 0
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		callN++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "files") {
			// uploadBatchFile
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "file-123"})
			return
		}
		// create batch
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "batch-456"})
	})
	batchID, err := p.BatchCreate(context.Background(), []types.Request{
		{Messages: []types.Message{{Role: "user", Content: "hi"}}},
	})
	if err != nil {
		t.Fatalf("BatchCreate: %v", err)
	}
	if batchID != "batch-456" {
		t.Errorf("batchID = %q, want batch-456", batchID)
	}
}

func TestBatchCreateUploadError(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	})
	_, err := p.BatchCreate(context.Background(), []types.Request{
		{Messages: []types.Message{{Role: "user", Content: "hi"}}},
	})
	if err == nil {
		t.Fatal("expected error when file upload fails")
	}
}

func TestBatchStatusSuccess(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "completed",
			"request_counts": map[string]int{"total": 5, "completed": 5, "failed": 0},
		})
	})
	status, counts, err := p.BatchStatus(context.Background(), "batch-123")
	if err != nil {
		t.Fatalf("BatchStatus: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
	if counts["total"] != 5 {
		t.Errorf("total = %d, want 5", counts["total"])
	}
}

func TestBatchResultsSuccess(t *testing.T) {
	jsonlLine := `{"custom_id":"req-0","response":{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"model":"gpt-4o"}}`
	callN := 0
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		callN++
		w.Header().Set("Content-Type", "application/json")
		if callN == 1 {
			// First call: GET /v1/batches/{id}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":         "completed",
				"output_file_id": "file-out-789",
			})
			return
		}
		// Second call: GET /v1/files/{id}/content
		_, _ = w.Write([]byte(jsonlLine))
	})
	results, err := p.BatchResults(context.Background(), "batch-123")
	if err != nil {
		t.Fatalf("BatchResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Response == nil || results[0].Response.Content != "hi" {
		t.Errorf("unexpected result: %+v", results[0])
	}
}

func TestBatchResultsNotCompleted(t *testing.T) {
	_, p := newURLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "in_progress",
		})
	})
	_, err := p.BatchResults(context.Background(), "batch-pending")
	if err == nil {
		t.Fatal("expected error for non-completed batch")
	}
}
