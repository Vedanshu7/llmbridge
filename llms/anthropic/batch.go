// Package anthropic provides a base.LLM backed by the Anthropic Messages API.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Vedanshu7/llmbridge/exceptions"
	"github.com/Vedanshu7/llmbridge/llms/anthropic/chat"
	"github.com/Vedanshu7/llmbridge/types"
)

const defaultBatchesURL = "https://api.anthropic.com/v1/messages/batches"

// AntBatchRequestItem is a single request within a batch create call.
type AntBatchRequestItem struct {
	CustomID string          `json:"custom_id"`
	Params   chat.AntRequest `json:"params"`
}

// AntBatchStatusResponse is returned by GET .../messages/batches/{id}.
type AntBatchStatusResponse struct {
	ID               string `json:"id"`
	ProcessingStatus string `json:"processing_status"` // "in_progress" | "canceling" | "ended"
	RequestCounts    struct {
		Processing int `json:"processing"`
		Succeeded  int `json:"succeeded"`
		Errored    int `json:"errored"`
		Canceled   int `json:"canceled"`
		Expired    int `json:"expired"`
	} `json:"request_counts"`
}

// AntBatchResultLine is one JSONL line from the batch results endpoint.
type AntBatchResultLine struct {
	CustomID string `json:"custom_id"`
	Result   struct {
		Type    string            `json:"type"` // "succeeded"|"errored"|"canceled"|"expired"
		Message *chat.AntResponse `json:"message,omitempty"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	} `json:"result"`
}

// BatchCreate implements base.BatchProvider by submitting requests inline
// (Anthropic's Message Batches API takes requests directly, unlike OpenAI's
// file-upload flow).
func (p *Provider) BatchCreate(ctx context.Context, reqs []types.Request) (string, error) {
	items := make([]AntBatchRequestItem, len(reqs))
	for i, r := range reqs {
		items[i] = AntBatchRequestItem{
			CustomID: fmt.Sprintf("req-%d", i),
			Params:   chat.ToAntRequest(r, p.model, false),
		}
	}
	body, err := json.Marshal(map[string]interface{}{"requests": items})
	if err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, "marshal batch: "+err.Error(), err)
	}

	raw, err := p.postBatch(p.batchesBaseURL(), body)
	if err != nil {
		return "", err
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", exceptions.NewProviderError(p.Name(), 0, "parse batch response: "+err.Error(), err)
	}
	return resp.ID, nil
}

// BatchStatus implements base.BatchProvider.
func (p *Provider) BatchStatus(ctx context.Context, batchID string) (string, map[string]int, error) {
	st, err := p.fetchBatchStatus(batchID)
	if err != nil {
		return "", nil, err
	}
	statusMap := map[string]string{
		"in_progress": "in_progress",
		"canceling":   "cancelled",
		"ended":       "completed",
	}
	status, ok := statusMap[st.ProcessingStatus]
	if !ok {
		status = st.ProcessingStatus
	}
	counts := map[string]int{
		"total": st.RequestCounts.Processing + st.RequestCounts.Succeeded +
			st.RequestCounts.Errored + st.RequestCounts.Canceled + st.RequestCounts.Expired,
		"completed":  st.RequestCounts.Succeeded,
		"failed":     st.RequestCounts.Errored + st.RequestCounts.Expired + st.RequestCounts.Canceled,
		"processing": st.RequestCounts.Processing,
	}
	return status, counts, nil
}

// BatchResults implements base.BatchProvider, parsing JSONL results from a
// completed batch.
func (p *Provider) BatchResults(ctx context.Context, batchID string) ([]types.BatchResult, error) {
	st, err := p.fetchBatchStatus(batchID)
	if err != nil {
		return nil, err
	}
	if st.ProcessingStatus != "ended" {
		return nil, exceptions.NewProviderError(p.Name(), 0, "batch not completed: "+st.ProcessingStatus, nil)
	}

	raw, err := p.getBatch(p.batchesBaseURL() + "/" + batchID + "/results")
	if err != nil {
		return nil, err
	}

	var results []types.BatchResult
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var out AntBatchResultLine
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			continue
		}
		idx := 0
		_, _ = fmt.Sscanf(out.CustomID, "req-%d", &idx)
		if out.Result.Type == "succeeded" && out.Result.Message != nil {
			results = append(results, types.BatchResult{
				Response: chat.FromAntResponse(out.Result.Message, p.Name()),
				Index:    idx,
			})
			continue
		}
		msg := out.Result.Type
		if out.Result.Error != nil {
			msg = out.Result.Error.Message
		}
		results = append(results, types.BatchResult{
			Err:   exceptions.NewProviderError(p.Name(), 0, msg, nil),
			Index: idx,
		})
	}
	return results, nil
}

func (p *Provider) fetchBatchStatus(batchID string) (*AntBatchStatusResponse, error) {
	raw, err := p.getBatch(p.batchesBaseURL() + "/" + batchID)
	if err != nil {
		return nil, err
	}
	var st AntBatchStatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "parse batch status: "+err.Error(), err)
	}
	return &st, nil
}

// batchesBaseURL returns the Message Batches endpoint root, honoring the
// same p.baseURL test override used by Complete/Stream.
func (p *Provider) batchesBaseURL() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return defaultBatchesURL
}

func (p *Provider) postBatch(url string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	return p.doBatchRequest(req)
}

func (p *Provider) getBatch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	return p.doBatchRequest(req)
}

func (p *Provider) doBatchRequest(req *http.Request) ([]byte, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, err.Error(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, exceptions.NewProviderError(p.Name(), 0, "read body: "+err.Error(), err)
	}
	if resp.StatusCode != 200 {
		return nil, exceptions.ClassifyHTTPError(p.Name(), resp.StatusCode, raw)
	}
	return raw, nil
}
