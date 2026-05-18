package management

import (
	"encoding/json"
	"net/http"
	"sync"
)

// RouterDeployment describes a named router configuration.
type RouterDeployment struct {
	Name      string   `json:"name"`
	Providers []string `json:"providers"`
	Strategy  string   `json:"strategy"`
}

// RouterConfig stores named router deployments for the proxy.
type RouterConfig struct {
	mu          sync.RWMutex
	deployments map[string]*RouterDeployment
}

// NewRouterConfig returns an empty RouterConfig.
func NewRouterConfig() *RouterConfig {
	return &RouterConfig{deployments: make(map[string]*RouterDeployment)}
}

// Deploy registers or replaces a router deployment.
func (rc *RouterConfig) Deploy(d RouterDeployment) {
	rc.mu.Lock()
	rc.deployments[d.Name] = &d
	rc.mu.Unlock()
}

// Get returns a deployment by name.
func (rc *RouterConfig) Get(name string) (*RouterDeployment, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	d, ok := rc.deployments[name]
	return d, ok
}

// List returns all deployments.
func (rc *RouterConfig) List() []*RouterDeployment {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make([]*RouterDeployment, 0, len(rc.deployments))
	for _, d := range rc.deployments {
		out = append(out, d)
	}
	return out
}

// HandleDeploy handles POST /admin/router — registers a deployment.
func (rc *RouterConfig) HandleDeploy(w http.ResponseWriter, r *http.Request) {
	var d RouterDeployment
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil || d.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name field required"})
		return
	}
	rc.Deploy(d)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deployed", "name": d.Name})
}

// HandleList handles GET /admin/router — lists all deployments.
func (rc *RouterConfig) HandleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"deployments": rc.List()})
}
