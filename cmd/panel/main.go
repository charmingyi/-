package main

import (
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
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
	mux.HandleFunc("/api/upstreams/probe", s.auth(s.handleUpstreamProbe))
	mux.HandleFunc("/api/routes", s.auth(s.handleRoutes))
	mux.HandleFunc("/api/routes/", s.auth(s.handleRouteByID))
	mux.HandleFunc("/api/routes/verify", s.auth(s.handleRouteVerify))
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
		Name      string `json:"name"`
		BaseURL   string `json:"base_url"`
		Host      string `json:"host"`
		Port      int    `json:"port"`
		Scheme    string `json:"scheme"`
		Path      string `json:"path"`
		TLSMode   string `json:"tls_mode"`
		CACertPEM string `json:"ca_cert_pem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	baseURL, err := buildUpstreamURL(in.BaseURL, in.Host, in.Scheme, in.Path, in.Port)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	u, err := s.store.AddUpstream(in.Name, baseURL, in.TLSMode, in.CACertPEM)
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

func (s *server) handleUpstreamProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var in struct {
		BaseURL   string `json:"base_url"`
		Host      string `json:"host"`
		Port      int    `json:"port"`
		Scheme    string `json:"scheme"`
		Path      string `json:"path"`
		TLSMode   string `json:"tls_mode"`
		CACertPEM string `json:"ca_cert_pem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	baseURL, err := buildUpstreamURL(in.BaseURL, in.Host, in.Scheme, in.Path, in.Port)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ok, reason := probeEmby(baseURL, in.TLSMode, in.CACertPEM)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      ok,
		"base_url": baseURL,
		"reason":  reason,
	})
}

func (s *server) handleRoutes(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        methodNotAllowed(w)
        return
    }
	var in struct {
		Name       string `json:"name"`
		Domain     string `json:"domain"`
		PathPrefix string `json:"path_prefix"`
		AgentID    string `json:"agent_id"`
		UpstreamID string `json:"upstream_id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        writeErr(w, http.StatusBadRequest, err)
        return
    }
	route, err := s.store.AddRoute(in.Name, in.Domain, in.PathPrefix, in.AgentID, in.UpstreamID)
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

func (s *server) handleRouteVerify(w http.ResponseWriter, r *http.Request) {
	routeID := r.URL.Query().Get("id")
	if strings.TrimSpace(routeID) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("id is required"))
		return
	}
	st := s.store.Snapshot()
	var rt *panel.Route
	for i := range st.Routes {
		if st.Routes[i].ID == routeID {
			rt = &st.Routes[i]
			break
		}
	}
	if rt == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("route not found"))
		return
	}
	var ag *panel.Agent
	for i := range st.Agents {
		if st.Agents[i].ID == rt.AgentID {
			ag = &st.Agents[i]
			break
		}
	}
	if ag == nil || strings.TrimSpace(ag.Host) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("agent host is required for verify"))
		return
	}
	targetURL := "http://" + normalizeUpstreamHost(ag.Host) + ":19073/"
	client := &http.Client{Timeout: 10 * time.Second}
	if rt.Domain == "" {
		targetURL = "http://" + normalizeUpstreamHost(ag.Host) + ":19073" + rt.PathPrefix
	} else {
		targetURL = "https://" + rt.Domain + "/"
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	req, _ := http.NewRequest(http.MethodGet, targetURL, nil)
	if rt.Domain != "" && strings.HasPrefix(targetURL, "http://") {
		req.Host = rt.Domain
	}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "status": 0, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	ok, reason := detectEmbyPayload(resp.StatusCode, resp.Header, body)
	result := map[string]any{
		"ok":     ok,
		"status": resp.StatusCode,
		"reason": reason,
	}
	if !ok {
		result["sample"] = summarizeBody(body)
	}
	writeJSON(w, http.StatusOK, result)
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
	cmd := fmt.Sprintf("sudo env PANEL_URL='%s' AGENT_ID='%s' AGENT_TOKEN='%s' bash -c 'tmp=$(mktemp); curl -fsSL %s/install/agent-menu.sh -o \"$tmp\"; bash \"$tmp\" menu; rm -f \"$tmp\"'", s.panelURL, a.ID, a.Token, s.panelURL)
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

func normalizeUpstreamHost(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		if u, err := url.Parse(v); err == nil {
			v = u.Host
		}
	}
	v = strings.TrimSuffix(v, "/")
	if strings.Contains(v, "/") {
		v = strings.Split(v, "/")[0]
	}
	if strings.Contains(v, ":") {
		host, _, found := strings.Cut(v, ":")
		if !found {
			return v
		}
		if host != "" {
			return host
		}
	}
	return v
}

func normalizeUpstreamPath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "/" {
		return ""
	}
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	return strings.TrimRight(v, "/")
}

func buildUpstreamURL(baseURL, host, scheme, path string, port int) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/"), nil
	}
	host = normalizeUpstreamHost(host)
	if host == "" || port <= 0 {
		return "", fmt.Errorf("host and port are required")
	}
	scheme = strings.TrimSpace(strings.ToLower(scheme))
	if scheme == "" {
		if port == 443 {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https")
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, host, port, normalizeUpstreamPath(path)), nil
}

func transportForTLSMode(mode, caCertPEM string) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "insecure":
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	case "custom_ca":
		pool := x509.NewCertPool()
		raw := strings.TrimSpace(caCertPEM)
		if strings.HasPrefix(raw, "LS0tLS") {
			if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
				raw = string(b)
			}
		}
		if pool.AppendCertsFromPEM([]byte(raw)) {
			t.TLSClientConfig = &tls.Config{RootCAs: pool}
		}
	}
	return t
}

func probeEmby(baseURL, mode, caCertPEM string) (bool, string) {
	client := &http.Client{
		Timeout:   12 * time.Second,
		Transport: transportForTLSMode(mode, caCertPEM),
	}
	targets, err := probeTargets(baseURL)
	if err != nil {
		return false, err.Error()
	}
	for _, u := range targets {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		if ok, reason := detectEmbyPayload(resp.StatusCode, resp.Header, body); ok {
			return true, reason
		}
	}
	return false, "No Emby signature found on tested endpoints"
}

func probeTargets(baseURL string) ([]string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("invalid upstream url")
	}
	basePath := strings.TrimRight(u.Path, "/")
	candidates := []string{basePath}
	switch {
	case basePath == "", basePath == "/":
		candidates = append(candidates, "/emby/System/Info/Public", "/System/Info/Public", "/web", "/web/index.html", "/")
	case strings.HasSuffix(basePath, "/web"):
		candidates = append(candidates, basePath+"/index.html", basePath+"/#!/startup/login.html", "/System/Info/Public", "/emby/System/Info/Public")
	case strings.HasSuffix(basePath, "/emby"):
		candidates = append(candidates, basePath+"/System/Info/Public", "/System/Info/Public", basePath+"/web/index.html")
	default:
		candidates = append(candidates, basePath+"/System/Info/Public", basePath+"/web", basePath+"/web/index.html", "/System/Info/Public")
	}

	seen := map[string]bool{}
	targets := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if p == "" {
			p = "/"
		}
		candidate := *u
		candidate.Path = p
		candidate.RawQuery = ""
		candidate.Fragment = ""
		raw := candidate.String()
		if !seen[raw] {
			seen[raw] = true
			targets = append(targets, raw)
		}
	}
	return targets, nil
}

func detectEmbyPayload(status int, header http.Header, body []byte) (bool, string) {
	ct := strings.ToLower(header.Get("Content-Type"))
	server := strings.ToLower(header.Get("Server"))
	location := strings.ToLower(header.Get("Location"))
	if strings.Contains(server, "emby") {
		return true, "Emby response headers detected"
	}
	if strings.Contains(location, "/web/index.html") && strings.Contains(location, "startup") {
		return true, "Emby redirect detected"
	}
	if strings.Contains(ct, "application/json") {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			if _, ok := payload["ServerName"]; ok {
				return true, "Emby API detected"
			}
			if _, ok := payload["Version"]; ok {
				return true, "Emby API detected"
			}
		}
		return false, ""
	}
	bs := strings.ToLower(string(body))
	switch {
	case strings.Contains(bs, "manuallogin.html"):
		return true, "Emby login page detected"
	case strings.Contains(bs, "serverid=") && strings.Contains(bs, "startup"):
		return true, "Emby startup page detected"
	case strings.Contains(bs, "emby") && (strings.Contains(bs, "serverid=") || strings.Contains(bs, "startup/") || strings.Contains(bs, "manuallogin") || strings.Contains(bs, "emby-webcomponents")):
		return true, "Emby web detected"
	case status >= 500:
		return false, "Upstream returned server error"
	default:
		return false, ""
	}
}

func summarizeBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}

