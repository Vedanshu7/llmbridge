// Package prompts provides server-side prompt template storage with versioning.
// Templates are stored in-memory; optionally persisted to SQLite via AttachDB.
package prompts

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Prompt is a stored, versioned prompt template.
type Prompt struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Template  string            `json:"template"`   // text with {{variable}} placeholders
	Variables []string          `json:"variables"`  // declared variable names
	Tags      map[string]string `json:"tags"`       // arbitrary metadata
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Store holds prompt templates.
type Store struct {
	mu      sync.RWMutex
	prompts map[string]*Prompt // id → latest version
	db      *sql.DB
}

// NewStore returns an empty in-memory Store.
func NewStore() *Store {
	return &Store{prompts: make(map[string]*Prompt)}
}

// AttachDB wires a SQLite database for persistence.
// It creates the required table and loads any existing prompts.
func (s *Store) AttachDB(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS prompts (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		template   TEXT NOT NULL,
		variables  TEXT NOT NULL DEFAULT '[]',
		tags       TEXT NOT NULL DEFAULT '{}',
		version    INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("prompts: create table: %w", err)
	}
	s.db = db
	return s.loadFromDB()
}

func (s *Store) loadFromDB() error {
	rows, err := s.db.Query(
		`SELECT id, name, template, variables, tags, version, created_at, updated_at FROM prompts`)
	if err != nil {
		return fmt.Errorf("prompts: load: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p Prompt
		var vars, tags string
		var createdAt, updatedAt int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Template, &vars, &tags, &p.Version, &createdAt, &updatedAt); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(vars), &p.Variables)
		_ = json.Unmarshal([]byte(tags), &p.Tags)
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		s.prompts[p.ID] = &p
	}
	return rows.Err()
}

func (s *Store) persist(p *Prompt) {
	if s.db == nil {
		return
	}
	vars, _ := json.Marshal(p.Variables)
	tags, _ := json.Marshal(p.Tags)
	_, _ = s.db.Exec(
		`INSERT OR REPLACE INTO prompts
		 (id, name, template, variables, tags, version, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Template, string(vars), string(tags),
		p.Version, p.CreatedAt.Unix(), p.UpdatedAt.Unix(),
	)
}

func (s *Store) deleteFromDB(id string) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(`DELETE FROM prompts WHERE id = ?`, id)
}

// Create adds a new prompt and returns it. Returns an error if a prompt with
// the same name already exists.
func (s *Store) Create(name, template string, variables []string, tags map[string]string) (*Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.prompts {
		if p.Name == name {
			return nil, fmt.Errorf("prompts: name %q already exists (id=%s)", name, p.ID)
		}
	}
	now := time.Now().UTC()
	p := &Prompt{
		ID:        generateID(),
		Name:      name,
		Template:  template,
		Variables: variables,
		Tags:      tags,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if p.Variables == nil {
		p.Variables = []string{}
	}
	if p.Tags == nil {
		p.Tags = map[string]string{}
	}
	s.prompts[p.ID] = p
	s.persist(p)
	return p, nil
}

// Get retrieves a prompt by ID.
func (s *Store) Get(id string) (*Prompt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.prompts[id]
	return p, ok
}

// GetByName retrieves a prompt by name.
func (s *Store) GetByName(name string) (*Prompt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.prompts {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// Update replaces a prompt's template, variables, and tags, incrementing version.
func (s *Store) Update(id, template string, variables []string, tags map[string]string) (*Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.prompts[id]
	if !ok {
		return nil, fmt.Errorf("prompts: id %q not found", id)
	}
	p.Template = template
	p.Version++
	p.UpdatedAt = time.Now().UTC()
	if variables != nil {
		p.Variables = variables
	}
	if tags != nil {
		p.Tags = tags
	}
	s.persist(p)
	return p, nil
}

// Delete removes a prompt by ID.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.prompts[id]; !ok {
		return false
	}
	delete(s.prompts, id)
	s.deleteFromDB(id)
	return true
}

// List returns all stored prompts.
func (s *Store) List() []*Prompt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Prompt, 0, len(s.prompts))
	for _, p := range s.prompts {
		out = append(out, p)
	}
	return out
}

// Render fills in template variables and returns the rendered string.
// Missing variables are left as-is.
func (s *Store) Render(id string, vars map[string]string) (string, error) {
	p, ok := s.Get(id)
	if !ok {
		return "", fmt.Errorf("prompts: id %q not found", id)
	}
	return renderTemplate(p.Template, vars), nil
}

// ---- HTTP handlers ----

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]interface{}{
		"error": map[string]string{"message": msg},
	})
}

// HandleCreate handles POST /admin/prompts.
func (s *Store) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string            `json:"name"`
		Template  string            `json:"template"`
		Variables []string          `json:"variables"`
		Tags      map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Template == "" {
		writeError(w, http.StatusBadRequest, "name and template fields required")
		return
	}
	p, err := s.Create(body.Name, body.Template, body.Variables, body.Tags)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// HandleList handles GET /admin/prompts.
func (s *Store) HandleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"prompts": s.List()})
}

// HandleGet handles GET /admin/prompts/{id}.
func (s *Store) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// HandleUpdate handles PUT /admin/prompts/{id}.
func (s *Store) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Template  string            `json:"template"`
		Variables []string          `json:"variables"`
		Tags      map[string]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Template == "" {
		writeError(w, http.StatusBadRequest, "template field required")
		return
	}
	p, err := s.Update(id, body.Template, body.Variables, body.Tags)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// HandleDelete handles DELETE /admin/prompts/{id}.
func (s *Store) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.Delete(id) {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// HandleRender handles POST /admin/prompts/{id}/render.
func (s *Store) HandleRender(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var vars map[string]string
	_ = json.NewDecoder(r.Body).Decode(&vars)
	text, err := s.Render(id, vars)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"rendered": text})
}
