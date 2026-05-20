// Package openai provides a base.LLM backed by the OpenAI chat completions API.
// The same adapter handles any OpenAI-compatible endpoint via NewCompatible.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/openai/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const (
	defaultURL   = "https://api.openai.com/v1/chat/completions"
	defaultModel = "gpt-4o-mini"
)

// Provider calls the OpenAI chat completions API.
// Construct with New or NewCompatible; do not create the struct directly.
type Provider struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// New returns a Provider backed by OpenAI.
// If model is empty, "gpt-4o-mini" is used.
func New(model, apiKey string) *Provider {
	if model == "" {
		model = defaultModel
	}
	return &Provider{
		name:    "openai",
		baseURL: defaultURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// NewCompatible returns a Provider for any OpenAI-compatible endpoint.
//   - name: label shown in logs and errors (e.g. "groq", "together").
//   - baseURL: full chat completions URL.
//   - apiKey: Bearer token; may be empty for unauthenticated local servers.
//   - model: model identifier required by the endpoint.
func NewCompatible(name, baseURL, apiKey, model string) *Provider {
	return &Provider{
		name:    name,
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements base.LLM.
func (p *Provider) Name() string { return p.name }

// ValidateEnvironment implements base.LLM.
func (p *Provider) ValidateEnvironment() error {
	if p.name == "openai" && p.apiKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		return fmt.Errorf("openai: OPENAI_API_KEY is not set")
	}
	return nil
}

// Complete sends a blocking request and returns the full response.
// On rate-limit or server errors it retries once after a 2-second backoff.
func (p *Provider) Complete(ctx context.Context, req types.Request) (*types.Response, error) {
	wireReq := chat.ToOAIRequest(req, p.model, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}

	var raw *chat.OAIResponse
	for attempt := range 2 {
		raw, err = p.post(body)
		if err == nil {
			break
		}
		var rl *exceptions.RateLimitError
		if attempt == 0 && errors.As(err, &rl) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		return nil, err
	}

	if len(raw.Choices) == 0 {
		return nil, exceptions.NewProviderError(p.name, 0, "empty choices in response", nil)
	}
	return chat.FromOAIResponse(raw, p.name, wireReq.Model), nil
}

// ImageGenerate generates images using the DALL-E API.
func (p *Provider) ImageGenerate(ctx context.Context, req types.ImageRequest) (*types.ImageResponse, error) {
	model := req.Model
	if model == "" {
		model = "dall-e-3"
	}
	n := req.N
	if n <= 0 {
		n = 1
	}
	size := req.Size
	if size == "" {
		size = "1024x1024"
	}
	wireReq := map[string]interface{}{
		"model":  model,
		"prompt": req.Prompt,
		"n":      n,
		"size":   size,
	}
	if req.Quality != "" {
		wireReq["quality"] = req.Quality
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}
	raw, err := p.postURL("https://api.openai.com/v1/images/generations", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []struct {
			URL           string `json:"url"`
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "parse image response: "+err.Error(), err)
	}
	images := make([]types.GeneratedImage, len(result.Data))
	for i, d := range result.Data {
		images[i] = types.GeneratedImage{
			URL:           d.URL,
			B64JSON:       d.B64JSON,
			RevisedPrompt: d.RevisedPrompt,
		}
	}
	return &types.ImageResponse{Images: images, Provider: p.name, Model: model}, nil
}

// Transcribe converts audio to text using the Whisper API.
// AudioData must be the raw bytes of a supported audio file (mp3, mp4, wav, webm, etc.).
func (p *Provider) Transcribe(ctx context.Context, req types.TranscriptionRequest) (*types.TranscriptionResponse, error) {
	model := req.Model
	if model == "" {
		model = "whisper-1"
	}
	format := req.Format
	if format == "" {
		format = "json"
	}
	// Build multipart form.
	body, contentType, err := buildTranscribeForm(req.AudioData, model, req.Language, format)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "build form: "+err.Error(), err)
	}
	raw, err := p.postURLContentType("https://api.openai.com/v1/audio/transcriptions", body, contentType)
	if err != nil {
		return nil, err
	}
	if format == "text" {
		return &types.TranscriptionResponse{Text: string(raw), Provider: p.name, Model: model}, nil
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "parse transcription: "+err.Error(), err)
	}
	return &types.TranscriptionResponse{Text: result.Text, Provider: p.name, Model: model}, nil
}

// TextComplete sends a legacy (non-chat) text completion request.
func (p *Provider) TextComplete(ctx context.Context, req types.TextRequest) (*types.TextResponse, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	wireReq := map[string]interface{}{
		"model":  model,
		"prompt": req.Prompt,
	}
	if req.MaxTokens > 0 {
		wireReq["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		wireReq["temperature"] = req.Temperature
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}
	raw, err := p.postURL("https://api.openai.com/v1/completions", body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "parse text completion: "+err.Error(), err)
	}
	out := &types.TextResponse{Provider: p.name, Model: model}
	if len(result.Choices) > 0 {
		out.Text = result.Choices[0].Text
	}
	if result.Usage != nil {
		out.Usage = &types.UsageData{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}
	return out, nil
}

// Speech converts text to audio using the OpenAI TTS API.
func (p *Provider) Speech(ctx context.Context, req types.SpeechRequest) (*types.SpeechResponse, error) {
	model := req.Model
	if model == "" {
		model = "tts-1"
	}
	voice := req.Voice
	if voice == "" {
		voice = "alloy"
	}
	format := req.ResponseFormat
	if format == "" {
		format = "mp3"
	}
	wireReq := map[string]interface{}{
		"model":           model,
		"input":           req.Input,
		"voice":           voice,
		"response_format": format,
	}
	if req.Speed > 0 {
		wireReq["speed"] = req.Speed
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}
	audio, err := p.postURL("https://api.openai.com/v1/audio/speech", body)
	if err != nil {
		return nil, err
	}
	return &types.SpeechResponse{Audio: audio, Format: format, Provider: p.name, Model: model}, nil
}

// Moderate implements base.Moderator using the OpenAI /v1/moderations endpoint.
func (p *Provider) Moderate(ctx context.Context, req types.ModerationRequest) (*types.ModerationResponse, error) {
	model := req.Model
	if model == "" {
		model = "omni-moderation-latest"
	}
	wireReq := map[string]interface{}{
		"input": req.Input,
		"model": model,
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}
	raw, err := p.postURL("https://api.openai.com/v1/moderations", body)
	if err != nil {
		return nil, err
	}
	var wire struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Results []struct {
			Flagged        bool               `json:"flagged"`
			Categories     map[string]bool    `json:"categories"`
			CategoryScores map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "unmarshal moderation: "+err.Error(), err)
	}
	results := make([]types.ModerationResult, len(wire.Results))
	for i, r := range wire.Results {
		results[i] = types.ModerationResult{
			Flagged:        r.Flagged,
			Categories:     r.Categories,
			CategoryScores: r.CategoryScores,
		}
	}
	return &types.ModerationResponse{
		ID:       wire.ID,
		Model:    wire.Model,
		Results:  results,
		Provider: p.name,
	}, nil
}

// BatchCreate uploads a JSONL file and submits an OpenAI native batch job.
// Returns the batch ID that can be polled via BatchStatus.
func (p *Provider) BatchCreate(ctx context.Context, reqs []types.Request) (string, error) {
	// Build JSONL payload: one line per request.
	var buf strings.Builder
	for i, req := range reqs {
		wireReq := map[string]interface{}{
			"custom_id": fmt.Sprintf("req-%d", i),
			"method":    "POST",
			"url":       "/v1/chat/completions",
			"body":      chat.ToOAIRequest(req, p.model, false),
		}
		line, err := json.Marshal(wireReq)
		if err != nil {
			return "", exceptions.NewProviderError(p.name, 0, "marshal batch line: "+err.Error(), err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	jsonlBytes := []byte(buf.String())

	// Upload the file.
	fileID, err := p.uploadBatchFile(jsonlBytes)
	if err != nil {
		return "", err
	}

	// Create the batch.
	createReq := map[string]interface{}{
		"input_file_id":    fileID,
		"endpoint":         "/v1/chat/completions",
		"completion_window": "24h",
	}
	body, err := json.Marshal(createReq)
	if err != nil {
		return "", exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}
	raw, err := p.postURL("https://api.openai.com/v1/batches", body)
	if err != nil {
		return "", err
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", exceptions.NewProviderError(p.name, 0, "parse batch create: "+err.Error(), err)
	}
	return resp.ID, nil
}

// BatchStatus returns the current status and request counts for a batch.
func (p *Provider) BatchStatus(ctx context.Context, batchID string) (string, map[string]int, error) {
	raw, err := p.getURL("https://api.openai.com/v1/batches/" + batchID)
	if err != nil {
		return "", nil, err
	}
	var resp struct {
		Status        string `json:"status"`
		RequestCounts struct {
			Total     int `json:"total"`
			Completed int `json:"completed"`
			Failed    int `json:"failed"`
		} `json:"request_counts"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", nil, exceptions.NewProviderError(p.name, 0, "parse batch status: "+err.Error(), err)
	}
	counts := map[string]int{
		"total":     resp.RequestCounts.Total,
		"completed": resp.RequestCounts.Completed,
		"failed":    resp.RequestCounts.Failed,
	}
	return resp.Status, counts, nil
}

// BatchResults fetches and parses the output file for a completed batch.
func (p *Provider) BatchResults(ctx context.Context, batchID string) ([]types.BatchResult, error) {
	// First fetch the batch to get output_file_id.
	raw, err := p.getURL("https://api.openai.com/v1/batches/" + batchID)
	if err != nil {
		return nil, err
	}
	var batchResp struct {
		Status       string `json:"status"`
		OutputFileID string `json:"output_file_id"`
	}
	if err := json.Unmarshal(raw, &batchResp); err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "parse batch: "+err.Error(), err)
	}
	if batchResp.Status != "completed" {
		return nil, exceptions.NewProviderError(p.name, 0, "batch not completed: "+batchResp.Status, nil)
	}
	content, err := p.getURL("https://api.openai.com/v1/files/" + batchResp.OutputFileID + "/content")
	if err != nil {
		return nil, err
	}

	// Parse JSONL output.
	var results []types.BatchResult
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		var out struct {
			CustomID string          `json:"custom_id"`
			Response *chat.OAIResponse `json:"response"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			results = append(results, types.BatchResult{Err: err, Index: i})
			continue
		}
		if out.Error != nil {
			results = append(results, types.BatchResult{
				Err:   exceptions.NewProviderError(p.name, 0, out.Error.Message, nil),
				Index: i,
			})
			continue
		}
		if out.Response != nil && len(out.Response.Choices) > 0 {
			resp := chat.FromOAIResponse(out.Response, p.name, p.model)
			results = append(results, types.BatchResult{Response: resp, Index: i})
		}
	}
	return results, nil
}

func (p *Provider) uploadBatchFile(jsonl []byte) (string, error) {
	// Build multipart/form-data with fields: purpose=batch, file=<jsonl>
	var bufBody bytes.Buffer
	w := multipart.NewWriter(&bufBody)
	_ = w.WriteField("purpose", "batch")
	fw, err := w.CreateFormFile("file", "batch.jsonl")
	if err != nil {
		return "", exceptions.NewProviderError(p.name, 0, "create form file: "+err.Error(), err)
	}
	if _, err := fw.Write(jsonl); err != nil {
		return "", exceptions.NewProviderError(p.name, 0, "write jsonl: "+err.Error(), err)
	}
	w.Close()
	raw, err := p.postURLContentType("https://api.openai.com/v1/files", bufBody.Bytes(), w.FormDataContentType())
	if err != nil {
		return "", err
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", exceptions.NewProviderError(p.name, 0, "parse file upload: "+err.Error(), err)
	}
	return resp.ID, nil
}

// Embed implements base.EmbedProvider using the OpenAI /v1/embeddings endpoint.
// The default model is text-embedding-3-small.
func (p *Provider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	model := "text-embedding-3-small"
	embedURL := strings.Replace(p.baseURL, "chat/completions", "embeddings", 1)
	wireReq := map[string]interface{}{
		"model": model,
		"input": texts,
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}
	raw, err := p.postURL(embedURL, body)
	if err != nil {
		return nil, err
	}
	var wire struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "parse embeddings: "+err.Error(), err)
	}
	out := make([][]float64, len(wire.Data))
	for i, d := range wire.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// Stream implements base.Streamer for token-by-token output via SSE.
func (p *Provider) Stream(ctx context.Context, req types.Request) (<-chan types.Delta, error) {
	wireReq := chat.ToOAIRequest(req, p.model, true)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, exceptions.NewProviderError(p.name, 0, "marshal: "+err.Error(), err)
	}

	resp, err := p.newStreamConn(body)
	if err != nil {
		return nil, err
	}

	ch := make(chan types.Delta, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		chat.ReadSSE(ctx, p.name, resp.Body, ch)
	}()
	return ch, nil
}
