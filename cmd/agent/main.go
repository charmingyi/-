package main

import (
    "encoding/json"
    "io"
    "log"
    "net/http"
    "net/http/httputil"
    "net/url"
    "os"
    "sort"
    "strings"
    "sync"
    "time"
)

type routeCfg struct {
    RouteID     string `json:"route_id"`
    Name        string `json:"name"`
    PathPrefix  string `json:"path_prefix"`
    UpstreamURL string `json:"upstream_url"`
}

type configResp struct {
    AgentID string     `json:"agent_id"`
    Routes  []routeCfg `json:"routes"`
}

type routeProxy struct {
    PathPrefix string
    Upstream   *url.URL
    Proxy      *httputil.ReverseProxy
}

type routerState struct {
    mu     sync.RWMutex
    routes []routeProxy
}

func main() {
    panelURL := env("PANEL_URL", "")
    agentID := env("AGENT_ID", "")
    token := env("AGENT_TOKEN", "")
    listen := env("LISTEN_ADDR", ":19073")
    syncInterval := envDuration("SYNC_INTERVAL", 30*time.Second)

    if panelURL == "" || agentID == "" || token == "" {
        log.Fatal("missing PANEL_URL / AGENT_ID / AGENT_TOKEN")
    }
    panelURL = strings.TrimRight(panelURL, "/")

    state := &routerState{}
    if err := syncConfig(panelURL, agentID, token, state); err != nil {
        log.Printf("initial sync failed: %v", err)
    }

    go func() {
        ticker := time.NewTicker(syncInterval)
        defer ticker.Stop()
        for range ticker.C {
            if err := syncConfig(panelURL, agentID, token, state); err != nil {
                log.Printf("sync failed: %v", err)
            }
        }
    }()

    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
    mux.HandleFunc("/", state.handleProxy)

    log.Printf("agent %s listening on %s", agentID, listen)
    if err := http.ListenAndServe(listen, logReq(mux)); err != nil {
        log.Fatal(err)
    }
}

func (s *routerState) handleProxy(w http.ResponseWriter, r *http.Request) {
    rp := s.match(r.URL.Path)
    if rp == nil {
        http.Error(w, "no route matched", http.StatusNotFound)
        return
    }
    rp.Proxy.ServeHTTP(w, r)
}

func (s *routerState) match(path string) *routeProxy {
    s.mu.RLock()
    defer s.mu.RUnlock()
    for i := range s.routes {
        p := s.routes[i].PathPrefix
        if path == p || strings.HasPrefix(path, p+"/") {
            return &s.routes[i]
        }
    }
    return nil
}

func syncConfig(panelURL, agentID, token string, s *routerState) error {
    endpoint := panelURL + "/api/agent/config?agent_id=" + url.QueryEscape(agentID) + "&token=" + url.QueryEscape(token)
    req, _ := http.NewRequest(http.MethodGet, endpoint, nil)

    resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
        return &statusErr{StatusCode: resp.StatusCode, Body: string(b)}
    }

    var c configResp
    if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
        return err
    }

    routes := make([]routeProxy, 0, len(c.Routes))
    for _, rc := range c.Routes {
        up, err := url.Parse(rc.UpstreamURL)
        if err != nil || up.Scheme == "" || up.Host == "" {
            log.Printf("skip invalid upstream %q: %v", rc.UpstreamURL, err)
            continue
        }

        upCopy := *up
        prefixCopy := normalizePrefix(rc.PathPrefix)
        proxy := httputil.NewSingleHostReverseProxy(&upCopy)
        original := proxy.Director
        proxy.Director = func(req *http.Request) {
            original(req)
            req.Host = upCopy.Host
            req.URL.Path = joinPath(upCopy.Path, stripPrefix(req.URL.Path, prefixCopy))
        }

        routes = append(routes, routeProxy{PathPrefix: prefixCopy, Upstream: &upCopy, Proxy: proxy})
    }

    sort.Slice(routes, func(i, j int) bool { return len(routes[i].PathPrefix) > len(routes[j].PathPrefix) })

    s.mu.Lock()
    s.routes = routes
    s.mu.Unlock()

    log.Printf("synced routes: %d", len(routes))
    return nil
}

func normalizePrefix(v string) string {
    v = strings.TrimSpace(v)
    if v == "" {
        return "/"
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

func stripPrefix(path, prefix string) string {
    if prefix == "/" {
        if path == "" {
            return "/"
        }
        return path
    }
    if strings.HasPrefix(path, prefix) {
        out := strings.TrimPrefix(path, prefix)
        if out == "" {
            return "/"
        }
        if !strings.HasPrefix(out, "/") {
            out = "/" + out
        }
        return out
    }
    return path
}

func joinPath(basePath, incoming string) string {
    b := strings.TrimRight(basePath, "/")
    i := strings.TrimLeft(incoming, "/")
    if b == "" {
        return "/" + i
    }
    if i == "" {
        return b
    }
    return b + "/" + i
}

type statusErr struct {
    StatusCode int
    Body       string
}

func (e *statusErr) Error() string {
    return http.StatusText(e.StatusCode) + ": " + e.Body
}

func logReq(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        log.Printf("%s %s", r.Method, r.URL.Path)
        next.ServeHTTP(w, r)
    })
}

func env(k, d string) string {
    v := strings.TrimSpace(os.Getenv(k))
    if v == "" {
        return d
    }
    return v
}

func envDuration(k string, d time.Duration) time.Duration {
    raw := strings.TrimSpace(os.Getenv(k))
    if raw == "" {
        return d
    }
    if !strings.ContainsAny(raw, "hms") {
        raw += "s"
    }
    v, err := time.ParseDuration(raw)
    if err != nil {
        return d
    }
    if v < 5*time.Second {
        return 5 * time.Second
    }
    return v
}

