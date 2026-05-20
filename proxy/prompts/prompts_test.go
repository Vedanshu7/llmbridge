package prompts

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Vedanshu7/llmbridge/proxy/persistence"
)

func TestCreateAndGet(t *testing.T) {
	s := NewStore()
	p, err := s.Create("greeting", "Hello, {{name}}!", []string{"name"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
	if p.Version != 1 {
		t.Errorf("version = %d, want 1", p.Version)
	}

	got, ok := s.Get(p.ID)
	if !ok {
		t.Fatal("Get returned false")
	}
	if got.Name != "greeting" {
		t.Errorf("name = %q, want %q", got.Name, "greeting")
	}
}

func TestCreateDuplicateName(t *testing.T) {
	s := NewStore()
	_, err := s.Create("foo", "tmpl", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create("foo", "other", nil, nil)
	if err == nil {
		t.Error("expected error for duplicate name")
	}
}

func TestGetByName(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("find-me", "tmpl", nil, nil)
	got, ok := s.GetByName("find-me")
	if !ok || got.ID != p.ID {
		t.Error("GetByName failed")
	}
	_, ok = s.GetByName("no-such")
	if ok {
		t.Error("expected false for missing name")
	}
}

func TestUpdate(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("upd", "old {{x}}", []string{"x"}, nil)
	p2, err := s.Update(p.ID, "new {{y}}", []string{"y"}, map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if p2.Template != "new {{y}}" {
		t.Errorf("template = %q", p2.Template)
	}
	if p2.Version != 2 {
		t.Errorf("version = %d, want 2", p2.Version)
	}
	if p2.Tags["k"] != "v" {
		t.Error("tags not updated")
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := NewStore()
	_, err := s.Update("nope", "tmpl", nil, nil)
	if err == nil {
		t.Error("expected error for missing id")
	}
}

func TestDelete(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("del", "tmpl", nil, nil)
	if !s.Delete(p.ID) {
		t.Error("Delete returned false")
	}
	if s.Delete(p.ID) {
		t.Error("second Delete should return false")
	}
}

func TestList(t *testing.T) {
	s := NewStore()
	s.Create("a", "tmpl-a", nil, nil)
	s.Create("b", "tmpl-b", nil, nil)
	list := s.List()
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}
}

func TestRender(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("r", "Hi {{name}}, you are {{age}}!", []string{"name", "age"}, nil)
	out, err := s.Render(p.ID, map[string]string{"name": "Alice", "age": "30"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hi Alice, you are 30!" {
		t.Errorf("render = %q", out)
	}
}

func TestRenderMissingVar(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("rm", "Hi {{name}}!", nil, nil)
	out, _ := s.Render(p.ID, map[string]string{})
	// Missing variables are left as-is.
	if out != "Hi {{name}}!" {
		t.Errorf("render = %q", out)
	}
}

func TestRenderNotFound(t *testing.T) {
	s := NewStore()
	_, err := s.Render("no-such", nil)
	if err == nil {
		t.Error("expected error for missing id")
	}
}

func TestPersistence(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "prompts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}

	s1 := NewStore()
	if err := s1.AttachDB(db); err != nil {
		t.Fatal(err)
	}
	p, err := s1.Create("persist-me", "Hello {{who}}", []string{"who"}, map[string]string{"env": "test"})
	if err != nil {
		t.Fatal(err)
	}

	// New store loaded from same DB.
	s2 := NewStore()
	if err := s2.AttachDB(db); err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get(p.ID)
	if !ok {
		t.Fatal("prompt not found in second store")
	}
	if got.Name != "persist-me" || got.Template != "Hello {{who}}" {
		t.Errorf("unexpected prompt: %+v", got)
	}
	if got.Tags["env"] != "test" {
		t.Error("tags not persisted")
	}
}

// ---- HTTP handler tests ----

func TestHandleCreate(t *testing.T) {
	s := NewStore()
	body, _ := json.Marshal(map[string]interface{}{
		"name":      "http-prompt",
		"template":  "Say {{word}}",
		"variables": []string{"word"},
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/prompts", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleCreate(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}
	var p Prompt
	json.NewDecoder(w.Body).Decode(&p)
	if p.ID == "" || p.Name != "http-prompt" {
		t.Errorf("unexpected prompt: %+v", p)
	}
}

func TestHandleCreateMissingFields(t *testing.T) {
	s := NewStore()
	body, _ := json.Marshal(map[string]string{"name": "only-name"})
	req := httptest.NewRequest(http.MethodPost, "/admin/prompts", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.HandleCreate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleList(t *testing.T) {
	s := NewStore()
	s.Create("p1", "t1", nil, nil)
	s.Create("p2", "t2", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/prompts", nil)
	w := httptest.NewRecorder()
	s.HandleList(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp["prompts"].([]interface{})) != 2 {
		t.Error("expected 2 prompts in list")
	}
}

func TestHandleGetAndDelete(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("getme", "tmpl", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/prompts/"+p.ID, nil)
	req.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	s.HandleGet(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET status = %d, want 200", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodDelete, "/admin/prompts/"+p.ID, nil)
	req2.SetPathValue("id", p.ID)
	w2 := httptest.NewRecorder()
	s.HandleDelete(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("DELETE status = %d, want 200", w2.Code)
	}

	req3 := httptest.NewRequest(http.MethodGet, "/admin/prompts/"+p.ID, nil)
	req3.SetPathValue("id", p.ID)
	w3 := httptest.NewRecorder()
	s.HandleGet(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE status = %d, want 404", w3.Code)
	}
}

func TestHandleUpdate(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("upd-http", "old", nil, nil)

	body, _ := json.Marshal(map[string]string{"template": "new {{x}}"})
	req := httptest.NewRequest(http.MethodPut, "/admin/prompts/"+p.ID, bytes.NewReader(body))
	req.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	s.HandleUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var got Prompt
	json.NewDecoder(w.Body).Decode(&got)
	if got.Template != "new {{x}}" || got.Version != 2 {
		t.Errorf("unexpected prompt: %+v", got)
	}
}

func TestHandleRender(t *testing.T) {
	s := NewStore()
	p, _ := s.Create("rend-http", "Hello {{name}}!", []string{"name"}, nil)

	body, _ := json.Marshal(map[string]string{"name": "Bob"})
	req := httptest.NewRequest(http.MethodPost, "/admin/prompts/"+p.ID+"/render", bytes.NewReader(body))
	req.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	s.HandleRender(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["rendered"] != "Hello Bob!" {
		t.Errorf("rendered = %q", resp["rendered"])
	}
}
