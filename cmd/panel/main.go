package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

    "emby-proxy-hub/internal/panel"
)

//go:embed web/*
var webFS embed.FS

//go:embed install/agent-menu.sh
var installScript string

type server struct {
	store      *panel.Store
	adminToken string
	panelURL   string
	hbMu       sync.RWMutex
	lastSeen   map[string]time.Time
}

func main() {
    listen := getEnv("PANEL_LISTEN", ":18473")
    dataFile := getEnv("PANEL_DATA", "./data/panel.json")

    st, err := panel.NewStore(dataFile)
    if err != nil {
        log.Fatalf("load store: %v", err)
    }

	s := &server{
		store:      st,
		adminToken: strings.TrimSpace(os.Getenv("ADMIN_TOKEN")),
		panelURL:   strings.TrimRight(getEnv("PANEL_PUBLIC_URL", "http://127.0.0.1:18473"), "/"),
		lastSeen:   map[string]time.Time{},
	}

    mux := http.NewServeMux()
    mux.HandleFunc("/", s.handleIndex)
    mux.HandleFunc("/api/state", s.auth(s.handleState))
    mux.HandleFunc("/api/agents", s.auth(s.handleAgents))
    mux.HandleFunc("/api/agents/", s.auth(s.handleAgentByID))
    mux.HandleFunc("/api/upstreams", s.auth(s.handleUpstreams))
    mux.HandleFunc("/api/upstreams/", s.auth(s.handleUpstreamByID))
    mux.HandleFunc("/api/routes", s.auth(s.handleRoutes))
    mux.HandleFunc("/api/routes/", s.auth(s.handleRouteByID))
	mux.HandleFunc("/api/agent/install-command", s.auth(s.handleAgentInstallCommand))
	mux.HandleFunc("/api/agent/config", s.handleAgentConfig)
	mux.HandleFunc("/api/agent/heartbeat", s.handleAgentHeartbeat)
	mux.HandleFunc("/install/agent-menu.sh", s.handleInstallScript)
	mux.Handle("/downloads/", s.noCache(http.StripPrefix("/downloads/", http.FileServer(http.Dir("./releases")))))
	mux.Handle("/static/", s.noCache(http.StripPrefix("/static/", http.FileServer(http.FS(webFS)))))

	log.Printf("panel listening on %s", listen)
	if err := http.ListenAndServe(listen, logReq(s.noCache(mux))); err != nil {
		log.Fatal(err)
	}
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path != "/" {
        http.NotFound(w, r)
        return
    }
    b, err := webFS.ReadFile("web/index.html")
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    _, _ = w.Write(b)
}

func (s *server) handleState(w http.ResponseWriter, _ *http.Request) {
	snap := s.store.Snapshot()

	type agentView struct {
		panel.Agent
		Online   bool   `json:"online"`
		LastSeen string `json:"last_seen"`
	}

	now := time.Now()
	agents := make([]agentView, 0, len(snap.Agents))
	for _, a := range snap.Agents {
		last, ok := s.getLastSeen(a.ID)
		v := agentView{Agent: a, Online: false, LastSeen: ""}
		if ok {
			v.LastSeen = last.Format(time.RFC3339)
			if now.Sub(last) <= 75*time.Second {
				v.Online = true
			}
		}
		agents = append(agents, v)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agents":    agents,
		"upstreams": snap.Upstreams,
		"routes":    snap.Routes,
	})
}

func (s *server) handleAgents(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        methodNotAllowed(w)
        return
    }
    var in struct {
        Name string `json:"name"`
        Host string `json:"host"`
    }
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
    a, err := s.store.AddAgent(in.Name, in.Host)
    if err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
    writeJSON(w, http.StatusOK, a)
}

func (s *server) handleAgentByID(w http.ResponseWriter, r *http.Request) {
    id := path.Base(r.URL.Path)
    if strings.HasSuffix(r.URL.Path, "/reset-token") {
        id = path.Base(path.Dir(r.URL.Path))
        if r.Method != http.MethodPost {
            methodNotAllowed(w)
            return
        }
        a, err := s.store.ResetAgentToken(id)
        if err != nil {
            writeErr(w, http.StatusBadRequest, err)
            return
        }
        writeJSON(w, http.StatusOK, a)
        return
    }

    if r.Method != http.MethodDelete {
        methodNotAllowed(w)
        return
    }
    if err := s.store.DeleteAgent(id); err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
    writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleUpstreams(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        methodNotAllowed(w)
        return
    }
	var in struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Host    string `json:"host"`
		Port    int    `json:"port"`
		Scheme  string `json:"scheme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	baseURL := strings.TrimSpace(in.BaseURL)
	if baseURL == "" {
		host := strings.TrimSpace(in.Host)
		if host != "" && in.Port > 0 {
			scheme := strings.TrimSpace(in.Scheme)
			if scheme == "" {
				scheme = "http"
			}
			baseURL = scheme + "://" + host + ":" + strconv.Itoa(in.Port)
		}
	}

	u, err := s.store.AddUpstream(in.Name, baseURL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
    writeJSON(w, http.StatusOK, u)
}

func (s *server) handleUpstreamByID(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodDelete {
        methodNotAllowed(w)
        return
    }
    id := path.Base(r.URL.Path)
    if err := s.store.DeleteUpstream(id); err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
    writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleRoutes(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        methodNotAllowed(w)
        return
    }
    var in struct {
        Name       string `json:"name"`
        PathPrefix string `json:"path_prefix"`
        AgentID    string `json:"agent_id"`
        UpstreamID string `json:"upstream_id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
    route, err := s.store.AddRoute(in.Name, in.PathPrefix, in.AgentID, in.UpstreamID)
    if err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
    writeJSON(w, http.StatusOK, route)
}

func (s *server) handleRouteByID(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodDelete {
        methodNotAllowed(w)
        return
    }
    id := path.Base(r.URL.Path)
    if err := s.store.DeleteRoute(id); err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
    writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	token := r.URL.Query().Get("token")
	cfg, err := s.store.AgentConfig(agentID, token)
    if err != nil {
        writeErr(w, http.StatusUnauthorized, err)
        return
    }
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agentID,
		"routes":   cfg,
	})
}

func (s *server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	agentID := r.URL.Query().Get("agent_id")
	token := r.URL.Query().Get("token")
	if !s.store.ValidateAgent(agentID, token) {
		writeErr(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}
	s.hbMu.Lock()
	s.lastSeen[agentID] = time.Now()
	s.hbMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAgentInstallCommand(w http.ResponseWriter, r *http.Request) {
    agentID := r.URL.Query().Get("id")
    a, ok := s.store.GetAgent(agentID)
    if !ok {
        writeErr(w, http.StatusNotFound, fmt.Errorf("agent not found"))
        return
    }
    cmd := fmt.Sprintf("curl -fsSL %s/install/agent-menu.sh | sudo env PANEL_URL='%s' AGENT_ID='%s' AGENT_TOKEN='%s' bash -s -- menu", s.panelURL, s.panelURL, a.ID, a.Token)
    writeJSON(w, http.StatusOK, map[string]string{"command": cmd})
}

func (s *server) handleInstallScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(installScript))
}

func (s *server) noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}

func (s *server) getLastSeen(agentID string) (time.Time, bool) {
	s.hbMu.RLock()
	defer s.hbMu.RUnlock()
	t, ok := s.lastSeen[agentID]
	return t, ok
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if s.adminToken == "" {
            next(w, r)
            return
        }
        if r.Header.Get("X-Admin-Token") != s.adminToken {
            writeErr(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
            return
        }
        next(w, r)
    }
}

func writeJSON(w http.ResponseWriter, status int, data any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(data)
}

func writeErr(w http.ResponseWriter, status int, err error) {
    writeJSON(w, status, map[string]string{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
    writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
}

func logReq(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        log.Printf("%s %s", r.Method, r.URL.Path)
        next.ServeHTTP(w, r)
    })
}

func getEnv(k, d string) string {
    v := strings.TrimSpace(os.Getenv(k))
    if v == "" {
        return d
    }
    return v
}

