package panel

import (
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"
)

type Agent struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    Host      string    `json:"host"`
    Token     string    `json:"token"`
    CreatedAt time.Time `json:"created_at"`
}

type Upstream struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    BaseURL   string    `json:"base_url"`
    CreatedAt time.Time `json:"created_at"`
}

type Route struct {
    ID         string    `json:"id"`
    Name       string    `json:"name"`
    PathPrefix string    `json:"path_prefix"`
    AgentID    string    `json:"agent_id"`
    UpstreamID string    `json:"upstream_id"`
    Enabled    bool      `json:"enabled"`
    CreatedAt  time.Time `json:"created_at"`
}

type AgentRouteConfig struct {
    RouteID     string `json:"route_id"`
    Name        string `json:"name"`
    PathPrefix  string `json:"path_prefix"`
    UpstreamURL string `json:"upstream_url"`
}

type State struct {
    Agents    []Agent    `json:"agents"`
    Upstreams []Upstream `json:"upstreams"`
    Routes    []Route    `json:"routes"`
}

type Store struct {
    mu   sync.RWMutex
    path string
    data State
}

func NewStore(path string) (*Store, error) {
    s := &Store{path: path}
    if err := s.load(); err != nil {
        return nil, err
    }
    return s, nil
}

func (s *Store) load() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
        return err
    }

    b, err := os.ReadFile(s.path)
    if errors.Is(err, os.ErrNotExist) {
        s.data = State{}
        return s.saveLocked()
    }
    if err != nil {
        return err
    }

    if len(strings.TrimSpace(string(b))) == 0 {
        s.data = State{}
        return s.saveLocked()
    }

    if err := json.Unmarshal(b, &s.data); err != nil {
        return err
    }
    return nil
}

func (s *Store) saveLocked() error {
    b, err := json.MarshalIndent(s.data, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(s.path, b, 0o644)
}

func (s *Store) Snapshot() State {
    s.mu.RLock()
    defer s.mu.RUnlock()

    out := s.data
    sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].CreatedAt.After(out.Agents[j].CreatedAt) })
    sort.Slice(out.Upstreams, func(i, j int) bool { return out.Upstreams[i].CreatedAt.After(out.Upstreams[j].CreatedAt) })
    sort.Slice(out.Routes, func(i, j int) bool { return out.Routes[i].CreatedAt.After(out.Routes[j].CreatedAt) })
    return out
}

func (s *Store) AddAgent(name, host string) (Agent, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    a := Agent{
        ID:        newID("ag"),
        Name:      strings.TrimSpace(name),
        Host:      strings.TrimSpace(host),
        Token:     randomHex(20),
        CreatedAt: time.Now().UTC(),
    }
    if a.Name == "" {
        return Agent{}, errors.New("agent name is required")
    }
    s.data.Agents = append(s.data.Agents, a)
    return a, s.saveLocked()
}

func (s *Store) DeleteAgent(id string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    n := s.data.Agents[:0]
    found := false
    for _, a := range s.data.Agents {
        if a.ID == id {
            found = true
            continue
        }
        n = append(n, a)
    }
    if !found {
        return errors.New("agent not found")
    }
    s.data.Agents = n

    nr := s.data.Routes[:0]
    for _, r := range s.data.Routes {
        if r.AgentID != id {
            nr = append(nr, r)
        }
    }
    s.data.Routes = nr

    return s.saveLocked()
}

func (s *Store) ResetAgentToken(id string) (Agent, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    for i := range s.data.Agents {
        if s.data.Agents[i].ID == id {
            s.data.Agents[i].Token = randomHex(20)
            return s.data.Agents[i], s.saveLocked()
        }
    }
    return Agent{}, errors.New("agent not found")
}

func (s *Store) AddUpstream(name, baseURL string) (Upstream, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    u := Upstream{
        ID:        newID("up"),
        Name:      strings.TrimSpace(name),
        BaseURL:   normalizeURL(baseURL),
        CreatedAt: time.Now().UTC(),
    }
    if u.Name == "" {
        return Upstream{}, errors.New("upstream name is required")
    }
    if u.BaseURL == "" {
        return Upstream{}, errors.New("upstream base_url is required")
    }

    s.data.Upstreams = append(s.data.Upstreams, u)
    return u, s.saveLocked()
}

func (s *Store) DeleteUpstream(id string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    n := s.data.Upstreams[:0]
    found := false
    for _, u := range s.data.Upstreams {
        if u.ID == id {
            found = true
            continue
        }
        n = append(n, u)
    }
    if !found {
        return errors.New("upstream not found")
    }
    s.data.Upstreams = n

    nr := s.data.Routes[:0]
    for _, r := range s.data.Routes {
        if r.UpstreamID != id {
            nr = append(nr, r)
        }
    }
    s.data.Routes = nr

    return s.saveLocked()
}

func (s *Store) AddRoute(name, pathPrefix, agentID, upstreamID string) (Route, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    pathPrefix = normalizePathPrefix(pathPrefix)
    if pathPrefix == "" {
        return Route{}, errors.New("path_prefix is required")
    }
    if !s.hasAgentLocked(agentID) {
        return Route{}, errors.New("agent not found")
    }
    if !s.hasUpstreamLocked(upstreamID) {
        return Route{}, errors.New("upstream not found")
    }

    for _, r := range s.data.Routes {
        if r.AgentID == agentID && r.PathPrefix == pathPrefix {
            return Route{}, errors.New("path_prefix already exists for this agent")
        }
    }

    r := Route{
        ID:         newID("rt"),
        Name:       strings.TrimSpace(name),
        PathPrefix: pathPrefix,
        AgentID:    agentID,
        UpstreamID: upstreamID,
        Enabled:    true,
        CreatedAt:  time.Now().UTC(),
    }
    if r.Name == "" {
        r.Name = pathPrefix
    }

    s.data.Routes = append(s.data.Routes, r)
    return r, s.saveLocked()
}

func (s *Store) DeleteRoute(id string) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    n := s.data.Routes[:0]
    found := false
    for _, r := range s.data.Routes {
        if r.ID == id {
            found = true
            continue
        }
        n = append(n, r)
    }
    if !found {
        return errors.New("route not found")
    }
    s.data.Routes = n
    return s.saveLocked()
}

func (s *Store) AgentConfig(agentID, token string) ([]AgentRouteConfig, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    if !s.validAgentTokenLocked(agentID, token) {
        return nil, errors.New("unauthorized")
    }

    upstreamMap := map[string]Upstream{}
    for _, u := range s.data.Upstreams {
        upstreamMap[u.ID] = u
    }

    var out []AgentRouteConfig
    for _, r := range s.data.Routes {
        if !r.Enabled || r.AgentID != agentID {
            continue
        }
        u, ok := upstreamMap[r.UpstreamID]
        if !ok {
            continue
        }
        out = append(out, AgentRouteConfig{
            RouteID:     r.ID,
            Name:        r.Name,
            PathPrefix:  r.PathPrefix,
            UpstreamURL: u.BaseURL,
        })
    }

    sort.Slice(out, func(i, j int) bool { return len(out[i].PathPrefix) > len(out[j].PathPrefix) })
    return out, nil
}

func (s *Store) GetAgent(id string) (Agent, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    for _, a := range s.data.Agents {
        if a.ID == id {
            return a, true
        }
    }
    return Agent{}, false
}

func (s *Store) hasAgentLocked(id string) bool {
    for _, a := range s.data.Agents {
        if a.ID == id {
            return true
        }
    }
    return false
}

func (s *Store) hasUpstreamLocked(id string) bool {
    for _, u := range s.data.Upstreams {
        if u.ID == id {
            return true
        }
    }
    return false
}

func (s *Store) validAgentTokenLocked(id, token string) bool {
    for _, a := range s.data.Agents {
        if a.ID == id && a.Token == token {
            return true
        }
    }
    return false
}

func normalizePathPrefix(v string) string {
    v = strings.TrimSpace(v)
    if v == "" {
        return ""
    }
    if !strings.HasPrefix(v, "/") {
        v = "/" + v
    }
    v = strings.TrimRight(v, "/")
    if v == "" {
        return "/"
    }
    return v
}

func normalizeURL(v string) string {
    v = strings.TrimSpace(v)
    return strings.TrimRight(v, "/")
}

func newID(prefix string) string {
    return prefix + "_" + randomHex(8)
}

func randomHex(n int) string {
    b := make([]byte, n)
    _, _ = rand.Read(b)
    return hex.EncodeToString(b)
}

