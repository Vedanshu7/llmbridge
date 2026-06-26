package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vedanshu7/llmbridge/types"
)

// ---- Multi-capability stub ----

// fullStub combines stubProvider with optional Speech, Moderate, and Stream.
type fullStub struct {
	stubProvider
	// Speech
	speechAudio  []byte
	speechFormat string
	speechErr    error
	// Moderate
	modResp *types.ModerationResponse
	modErr  error
	// Stream
	streamDeltas []string
	streamErr    error
	// ImageGenerate
	imgResp *types.ImageResponse
	imgErr  error
}

func (f *fullStub) Speech(_ context.Context, req types.SpeechRequest) (*types.SpeechResponse, error) {
	if f.speechErr != nil {
		return nil, f.speechErr
	}
	return &types.SpeechResponse{Audio: f.speechAudio, Format: f.speechFormat}, nil
}

func (f *fullStub) Moderate(_ context.Context, req types.ModerationRequest) (*types.ModerationResponse, error) {
	if f.modErr != nil {
		return nil, f.modErr
	}
	return f.modResp, nil
}

func (f *fullStub) ImageGenerate(_ context.Context, req types.ImageRequest) (*types.ImageResponse, error) {
	if f.imgErr != nil {
		return nil, f.imgErr
	}
	return f.imgResp, nil
}

func (f *fullStub) Stream(_ context.Context, req types.Request) (<-chan types.Delta, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	ch := make(chan types.Delta, len(f.streamDeltas)+1)
	for _, d := range f.streamDeltas {
		ch <- types.Delta{Content: d}
	}
	ch <- types.Delta{Done: true}
	return ch, nil
}

func newFullTestServer(f *fullStub) (*Server, string) {
	srv := NewServer(f)
	key, _ := srv.keyStore.GenerateAPIKey([]string{"completion", "admin"})
	return srv, key
}

// ---- handleSpeech tests ----

func TestSpeechEndpointSuccess(t *testing.T) {
	audio := []byte{0x49, 0x44, 0x33} // fake mp3 bytes
	f := &fullStub{speechAudio: audio, speechFormat: "mp3"}
	f.resp = &types.Response{Content: "ok"}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]interface{}{
		"input": "Hello world", "model": "tts-1", "voice": "alloy",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("speech: got %d, want 200: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), audio) {
		t.Error("audio body mismatch")
	}
}

func TestSpeechEndpointMissingInput(t *testing.T) {
	f := &fullStub{}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]string{"model": "tts-1"}) // no input
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing input, got %d", w.Code)
	}
}

func TestSpeechEndpointProviderError(t *testing.T) {
	f := &fullStub{speechErr: fmt.Errorf("tts unavailable")}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]string{"input": "hi", "model": "tts-1", "voice": "nova"})
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500 for provider error, got %d", w.Code)
	}
}

func TestSpeechEndpointNotSupported(t *testing.T) {
	// Plain stubProvider doesn't implement SpeechProvider.
	p := &stubProvider{resp: &types.Response{}}
	srv, key := newTestServer(p)

	body, _ := json.Marshal(map[string]string{"input": "hi", "model": "tts-1", "voice": "nova"})
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for unsupported speech, got %d", w.Code)
	}
}

// ---- handleImageGenerate tests ----

func TestImageGenerationsSuccess(t *testing.T) {
	f := &fullStub{imgResp: &types.ImageResponse{
		Images: []types.GeneratedImage{{URL: "https://example.com/img.png", RevisedPrompt: "a cat"}},
	}}
	f.resp = &types.Response{Content: "ok"}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]interface{}{
		"prompt": "a cat", "model": "dall-e-3", "n": 1, "size": "1024x1024",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("images: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var out struct {
		Created int64 `json:"created"`
		Data    []struct {
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].URL != "https://example.com/img.png" {
		t.Errorf("unexpected data: %+v", out.Data)
	}
}

func TestImageGenerationsMissingPrompt(t *testing.T) {
	f := &fullStub{}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]string{"model": "dall-e-3"}) // no prompt
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing prompt, got %d", w.Code)
	}
}

func TestImageGenerationsProviderError(t *testing.T) {
	f := &fullStub{imgErr: fmt.Errorf("image service unavailable")}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]string{"prompt": "a cat", "model": "dall-e-3"})
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500 for provider error, got %d", w.Code)
	}
}

func TestImageGenerationsNotSupported(t *testing.T) {
	// Plain stubProvider doesn't implement ImageGenerator.
	p := &stubProvider{resp: &types.Response{}}
	srv, key := newTestServer(p)

	body, _ := json.Marshal(map[string]string{"prompt": "a cat", "model": "dall-e-3"})
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for unsupported image generation, got %d", w.Code)
	}
}

func TestSpeechFormatOpus(t *testing.T) {
	f := &fullStub{speechAudio: []byte("ogg"), speechFormat: "opus"}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]interface{}{"input": "test", "model": "tts-1", "voice": "echo", "response_format": "opus"})
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Header().Get("Content-Type") != "audio/ogg" {
		t.Errorf("Content-Type = %q, want audio/ogg", w.Header().Get("Content-Type"))
	}
}

// ---- handleModerations tests ----

func TestModerationsEndpointSuccess(t *testing.T) {
	modResp := &types.ModerationResponse{
		Results: []types.ModerationResult{{
			Flagged:        false,
			Categories:     map[string]bool{"hate": false},
			CategoryScores: map[string]float64{"hate": 0.01},
		}},
	}
	f := &fullStub{modResp: modResp}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]string{"input": "Hello there", "model": "text-moderation-latest"})
	req := httptest.NewRequest(http.MethodPost, "/v1/moderations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	results := resp["results"].([]interface{})
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestModerationsEndpointMissingInput(t *testing.T) {
	f := &fullStub{}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]string{"model": "text-moderation-latest"})
	req := httptest.NewRequest(http.MethodPost, "/v1/moderations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing input, got %d", w.Code)
	}
}

func TestModerationsEndpointNotSupported(t *testing.T) {
	p := &stubProvider{resp: &types.Response{}}
	srv, key := newTestServer(p)

	body, _ := json.Marshal(map[string]string{"input": "hi", "model": "text-moderation-latest"})
	req := httptest.NewRequest(http.MethodPost, "/v1/moderations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for unsupported moderation, got %d", w.Code)
	}
}

func TestModerationsEndpointProviderError(t *testing.T) {
	f := &fullStub{modErr: fmt.Errorf("moderation service down")}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	body, _ := json.Marshal(map[string]string{"input": "hi", "model": "text-moderation-latest"})
	req := httptest.NewRequest(http.MethodPost, "/v1/moderations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

// ---- streamChatCompletion tests ----

func TestStreamChatCompletion(t *testing.T) {
	f := &fullStub{streamDeltas: []string{"hello ", "world"}}
	f.resp = &types.Response{Content: "hello world"}
	srv, key := newFullTestServer(f)

	reqBody := map[string]interface{}{
		"model":    "gpt-4o",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("stream: got %d: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Scan SSE lines for content chunks.
	var sawDone bool
	scanner := bufio.NewScanner(w.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: [DONE]") {
			sawDone = true
		}
	}
	if !sawDone {
		t.Error("expected [DONE] sentinel in stream response")
	}
}

func TestStreamChatCompletionNotSupported(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, key := newTestServer(p)

	reqBody := map[string]interface{}{
		"model":    "gpt-4o",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for non-streaming provider, got %d", w.Code)
	}
}

func TestStreamChatCompletionProviderError(t *testing.T) {
	f := &fullStub{streamErr: fmt.Errorf("stream failed")}
	f.resp = &types.Response{}
	srv, key := newFullTestServer(f)

	reqBody := map[string]interface{}{
		"model":    "gpt-4o",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500 for stream error, got %d: %s", w.Code, w.Body.String())
	}
}

// ---- Prompt endpoints via proxy ----

func TestPromptCreateViaProxy(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	adminKey, _ := srv.keyStore.GenerateAPIKey([]string{"admin", "completion"})

	body, _ := json.Marshal(map[string]interface{}{
		"name": "test-prompt", "template": "Hi {{name}}!", "variables": []string{"name"},
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/prompts", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create prompt: got %d: %s", w.Code, w.Body.String())
	}
}

func TestWebhookRegisterViaProxy(t *testing.T) {
	p := &stubProvider{resp: &types.Response{Content: "ok"}}
	srv, _ := newTestServer(p)
	adminKey, _ := srv.keyStore.GenerateAPIKey([]string{"admin"})

	body, _ := json.Marshal(map[string]interface{}{
		"url": "https://example.com/hook", "events": []string{"completion"},
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/webhooks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("register webhook: got %d: %s", w.Code, w.Body.String())
	}
}
