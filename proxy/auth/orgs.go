package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Org is a top-level tenant that can contain multiple teams.
type Org struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	Budget       float64   `json:"budget"`        // 0 = unlimited
	CurrentSpend float64   `json:"current_spend"` // accumulated USD spend
}

// Team is a sub-group within an Org.
type Team struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	Budget       float64   `json:"budget"`        // 0 = unlimited
	CurrentSpend float64   `json:"current_spend"` // accumulated USD spend
}

// OrgStore is a thread-safe store for orgs and teams backed by an optional SQLite database.
type OrgStore struct {
	mu    sync.RWMutex
	orgs  map[string]*Org
	teams map[string]*Team
	db    *sql.DB // nil = in-memory only
}

// NewOrgStore returns an in-memory-only OrgStore.
func NewOrgStore() *OrgStore {
	return &OrgStore{
		orgs:  make(map[string]*Org),
		teams: make(map[string]*Team),
	}
}

// NewOrgStoreWithDB returns an OrgStore backed by db.
// All existing rows in the orgs and teams tables are loaded into memory.
func NewOrgStoreWithDB(db *sql.DB) (*OrgStore, error) {
	s := &OrgStore{
		orgs:  make(map[string]*Org),
		teams: make(map[string]*Team),
		db:    db,
	}
	if err := s.loadFromDB(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *OrgStore) loadFromDB() error {
	rows, err := s.db.Query(`SELECT id, name, created_at, budget, current_spend FROM orgs`)
	if err != nil {
		return fmt.Errorf("auth: load orgs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var org Org
		var createdAt int64
		if err := rows.Scan(&org.ID, &org.Name, &createdAt, &org.Budget, &org.CurrentSpend); err != nil {
			return fmt.Errorf("auth: scan org: %w", err)
		}
		org.CreatedAt = time.Unix(createdAt, 0)
		s.orgs[org.ID] = &org
	}
	if err := rows.Err(); err != nil {
		return err
	}

	trows, err := s.db.Query(`SELECT id, org_id, name, created_at, budget, current_spend FROM teams`)
	if err != nil {
		return fmt.Errorf("auth: load teams: %w", err)
	}
	defer trows.Close()
	for trows.Next() {
		var team Team
		var createdAt int64
		if err := trows.Scan(&team.ID, &team.OrgID, &team.Name, &createdAt, &team.Budget, &team.CurrentSpend); err != nil {
			return fmt.Errorf("auth: scan team: %w", err)
		}
		team.CreatedAt = time.Unix(createdAt, 0)
		s.teams[team.ID] = &team
	}
	return trows.Err()
}

func (s *OrgStore) persistOrg(org *Org) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(
		`INSERT OR REPLACE INTO orgs (id, name, created_at, budget, current_spend) VALUES (?,?,?,?,?)`,
		org.ID, org.Name, org.CreatedAt.Unix(), org.Budget, org.CurrentSpend,
	)
}

func (s *OrgStore) persistTeam(team *Team) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(
		`INSERT OR REPLACE INTO teams (id, org_id, name, created_at, budget, current_spend) VALUES (?,?,?,?,?,?)`,
		team.ID, team.OrgID, team.Name, team.CreatedAt.Unix(), team.Budget, team.CurrentSpend,
	)
}

// CreateOrg creates a new Org with the given name and optional budget (0 = unlimited).
func (s *OrgStore) CreateOrg(name string, budget float64) (*Org, error) {
	id, err := newID("org")
	if err != nil {
		return nil, err
	}
	org := &Org{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
		Budget:    budget,
	}
	s.mu.Lock()
	s.orgs[id] = org
	s.persistOrg(org)
	s.mu.Unlock()
	return org, nil
}

// GetOrg retrieves an Org by ID.
func (s *OrgStore) GetOrg(id string) (*Org, bool) {
	s.mu.RLock()
	org, ok := s.orgs[id]
	s.mu.RUnlock()
	return org, ok
}

// ListOrgs returns all orgs.
func (s *OrgStore) ListOrgs() []*Org {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Org, 0, len(s.orgs))
	for _, o := range s.orgs {
		out = append(out, o)
	}
	return out
}

// CreateTeam creates a new Team inside the given org.
// Returns an error if orgID does not exist.
func (s *OrgStore) CreateTeam(orgID, name string, budget float64) (*Team, error) {
	id, err := newID("team")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orgs[orgID]; !ok {
		return nil, fmt.Errorf("org %s not found", orgID)
	}
	team := &Team{
		ID:        id,
		OrgID:     orgID,
		Name:      name,
		CreatedAt: time.Now(),
		Budget:    budget,
	}
	s.teams[id] = team
	s.persistTeam(team)
	return team, nil
}

// GetTeam retrieves a Team by ID.
func (s *OrgStore) GetTeam(id string) (*Team, bool) {
	s.mu.RLock()
	team, ok := s.teams[id]
	s.mu.RUnlock()
	return team, ok
}

// ListTeams returns all teams. Pass a non-empty orgID to filter by org.
func (s *OrgStore) ListTeams(orgID string) []*Team {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Team
	for _, t := range s.teams {
		if orgID == "" || t.OrgID == orgID {
			out = append(out, t)
		}
	}
	return out
}

// RecordTeamSpend adds cost to both the team and its parent org.
// Returns an error if either budget is exceeded.
func (s *OrgStore) RecordTeamSpend(teamID string, cost float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	team, ok := s.teams[teamID]
	if !ok {
		return nil
	}
	team.CurrentSpend += cost
	s.persistTeam(team)
	if team.Budget > 0 && team.CurrentSpend > team.Budget {
		return fmt.Errorf("team %s exceeded budget: $%.6f of $%.6f",
			teamID, team.CurrentSpend, team.Budget)
	}
	org, ok := s.orgs[team.OrgID]
	if !ok {
		return nil
	}
	org.CurrentSpend += cost
	s.persistOrg(org)
	if org.Budget > 0 && org.CurrentSpend > org.Budget {
		return fmt.Errorf("org %s exceeded budget: $%.6f of $%.6f",
			team.OrgID, org.CurrentSpend, org.Budget)
	}
	return nil
}

// RecordOrgSpend adds cost directly to an org (for keys with OrgID but no TeamID).
// Returns an error if the org budget is exceeded.
func (s *OrgStore) RecordOrgSpend(orgID string, cost float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	org, ok := s.orgs[orgID]
	if !ok {
		return nil
	}
	org.CurrentSpend += cost
	s.persistOrg(org)
	if org.Budget > 0 && org.CurrentSpend > org.Budget {
		return fmt.Errorf("org %s exceeded budget: $%.6f of $%.6f",
			orgID, org.CurrentSpend, org.Budget)
	}
	return nil
}

func newID(prefix string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}
