package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type routeCfg struct {
	RouteID     string `json:"route_id"`
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	PathPrefix  string `json:"path_prefix"`
	UpstreamURL string `json:"upstream_url"`
	TLSMode     string `json:"tls_mode"`
	CACertPEM   string `json:"ca_cert_pem"`
	InsecureTLS bool   `json:"insecure_tls"`
}

type configResp struct {
    AgentID string     `json:"agent_id"`
    Routes  []routeCfg `json:"routes"`
}

type routeProxy struct {
	Domain     string
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
	_ = sendHeartbeat(panelURL, agentID, token)

    go func() {
		ticker := time.NewTicker(syncInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := syncConfig(panelURL, agentID, token, state); err != nil {
				log.Printf("sync failed: %v", err)
			}
			if err := sendHeartbeat(panelURL, agentID, token); err != nil {
				log.Printf("heartbeat failed: %v", err)
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
	rp := s.match(r.Host, r.URL.Path)
	if rp == nil {
		http.Error(w, "no route matched", http.StatusNotFound)
		return
	}
	rp.Proxy.ServeHTTP(w, r)
}

func (s *routerState) match(host, path string) *routeProxy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	host = normalizeHost(host)
	for i := range s.routes {
		p := s.routes[i]
		if p.Domain != "" && p.Domain == host {
			return &s.routes[i]
		}
	}
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

	if err := syncCaddyRoutes(c.Routes); err != nil {
		log.Printf("sync caddy failed: %v", err)
	}

	routes := make([]routeProxy, 0, len(c.Routes))
	for _, rc := range c.Routes {
		if strings.TrimSpace(rc.Domain) != "" {
			continue
		}
		up, err := url.Parse(rc.UpstreamURL)
		if err != nil || up.Scheme == "" || up.Host == "" {
			log.Printf("skip invalid upstream %q: %v", rc.UpstreamURL, err)
			continue
		}

		upCopy := *up
		prefixCopy := normalizePrefix(rc.PathPrefix)
		domainCopy := normalizeHost(rc.Domain)
		proxy := httputil.NewSingleHostReverseProxy(&upCopy)
		if baseTransport, ok := http.DefaultTransport.(*http.Transport); ok {
			t := baseTransport.Clone()
			mode := strings.ToLower(strings.TrimSpace(rc.TLSMode))
			if mode == "" && rc.InsecureTLS {
				mode = "insecure"
			}
			if mode == "insecure" {
				t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
				proxy.Transport = t
			} else if mode == "custom_ca" {
				raw := strings.TrimSpace(rc.CACertPEM)
				if strings.HasPrefix(raw, "LS0tLS") {
					if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
						raw = string(b)
					}
				}
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM([]byte(raw)) {
					t.TLSClientConfig = &tls.Config{RootCAs: pool}
					proxy.Transport = t
				}
			}
		}
		original := proxy.Director
		proxy.Director = func(req *http.Request) {
			original(req)
			req.Host = upCopy.Host
			if domainCopy != "" {
				if rewrite := domainRootRewrite(upCopy.Path, req.URL.Path); rewrite != "" {
					req.URL.Path = rewrite
					return
				}
				if shouldPrefixDomainPath(upCopy.Path, req.URL.Path) {
					req.URL.Path = joinPath(upCopy.Path, req.URL.Path)
				}
				return
			}
			req.URL.Path = joinPath(upCopy.Path, stripPrefix(req.URL.Path, prefixCopy))
		}

		routes = append(routes, routeProxy{Domain: domainCopy, PathPrefix: prefixCopy, Upstream: &upCopy, Proxy: proxy})
	}

	sort.Slice(routes, func(i, j int) bool { return len(routes[i].PathPrefix) > len(routes[j].PathPrefix) })

	s.mu.Lock()
	s.routes = routes
	s.mu.Unlock()

	log.Printf("synced routes: %d", len(c.Routes))
	return nil
}

func syncCaddyRoutes(routes []routeCfg) error {
	if _, err := exec.LookPath("caddy"); err != nil {
		return nil
	}
	const (
		caddyMain = "/etc/caddy/Caddyfile"
		caddyDir  = "/etc/caddy/emby-relay"
		caddyFile = "/etc/caddy/emby-relay/routes.caddy"
	)
	if err := os.MkdirAll(caddyDir, 0o755); err != nil {
		return err
	}
	base := "# emby relay managed\nimport " + filepath.ToSlash(filepath.Join(caddyDir, "*.caddy")) + "\n"
	if err := os.WriteFile(caddyMain, []byte(base), 0o644); err != nil {
		return err
	}

	var blocks []string
	for _, rc := range routes {
		if strings.TrimSpace(rc.Domain) == "" {
			continue
		}
		block, err := caddySiteBlock(rc)
		if err != nil {
			log.Printf("skip caddy route %s: %v", rc.RouteID, err)
			continue
		}
		blocks = append(blocks, block)
	}
	content := strings.Join(blocks, "\n\n")
	if content == "" {
		content = "# no domain routes\n"
	}
	if err := os.WriteFile(caddyFile, []byte(content), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("caddy", "reload", "--config", caddyMain).CombinedOutput(); err != nil {
		return fmt.Errorf("caddy reload: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func caddySiteBlock(rc routeCfg) (string, error) {
	up, err := url.Parse(strings.TrimSpace(rc.UpstreamURL))
	if err != nil || up.Scheme == "" || up.Host == "" {
		return "", fmt.Errorf("invalid upstream url")
	}
	basePath := strings.TrimRight(strings.TrimSpace(up.Path), "/")
	up.Path = ""
	up.RawPath = ""
	target := up.String()

	var sb strings.Builder
	sb.WriteString(rc.Domain)
	sb.WriteString(" {\n")
	sb.WriteString("    encode gzip\n")
	sb.WriteString("    header Access-Control-Allow-Origin *\n")
	if redirect := caddyRootRedirect(basePath); redirect != "" {
		sb.WriteString("    @root path /\n")
		sb.WriteString("    redir @root ")
		sb.WriteString(redirect)
		sb.WriteString(" 302\n")
	}
	sb.WriteString("    reverse_proxy ")
	sb.WriteString(target)
	sb.WriteString(" {\n")
	sb.WriteString("        header_up X-Real-IP {remote_host}\n")
	sb.WriteString("        header_up X-Forwarded-For {remote_host}\n")
	sb.WriteString("        header_up X-Forwarded-Proto {scheme}\n")
	sb.WriteString("        header_up X-Forwarded-Host {host}\n")
	sb.WriteString("        header_up Host {upstream_host}\n")
	if transport := caddyTransportBlock(rc); transport != "" {
		sb.WriteString(transport)
	}
	sb.WriteString("    }\n")
	sb.WriteString("}")
	return sb.String(), nil
}

func caddyRootRedirect(basePath string) string {
	switch strings.TrimRight(strings.TrimSpace(basePath), "/") {
	case "/web":
		return "/web/index.html"
	case "/emby":
		return "/emby/web/index.html"
	default:
		return ""
	}
}

func caddyTransportBlock(rc routeCfg) string {
	mode := strings.ToLower(strings.TrimSpace(rc.TLSMode))
	if mode == "" && rc.InsecureTLS {
		mode = "insecure"
	}
	up, err := url.Parse(strings.TrimSpace(rc.UpstreamURL))
	if err != nil || up.Hostname() == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("        transport http {\n")
	sb.WriteString("            tls_server_name ")
	sb.WriteString(up.Hostname())
	sb.WriteString("\n")
	if mode == "insecure" {
		sb.WriteString("            tls_insecure_skip_verify\n")
	}
	sb.WriteString("            versions h1_1 h2\n")
	sb.WriteString("        }\n")
	return sb.String()
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

func shouldPrefixDomainPath(basePath, reqPath string) bool {
	basePath = strings.TrimRight(strings.TrimSpace(basePath), "/")
	reqPath = strings.TrimSpace(reqPath)
	if basePath == "" || basePath == "/" {
		return false
	}
	if reqPath == basePath || strings.HasPrefix(reqPath, basePath+"/") {
		return false
	}
	if reqPath == "" || reqPath == "/" {
		return true
	}
	if basePath != "/web" {
		return true
	}

	webPrefixes := []string{
		"/web/",
		"/apploader.js",
		"/manifest.json",
		"/favicon.ico",
		"/robots.txt",
		"/serviceworker.js",
		"/service-worker.js",
		"/sw.js",
		"/images/",
		"/modules/",
		"/components/",
		"/assets/",
		"/node_modules/",
		"/libraries/",
		"/css/",
		"/scripts/",
		"/themes/",
	}
	for _, prefix := range webPrefixes {
		if strings.HasPrefix(reqPath, prefix) {
			return true
		}
	}

	webExts := []string{".js", ".css", ".png", ".jpg", ".jpeg", ".svg", ".gif", ".ico", ".webp", ".woff", ".woff2", ".ttf", ".map", ".json", ".html"}
	for _, ext := range webExts {
		if strings.HasSuffix(reqPath, ext) {
			return true
		}
	}

	return false
}

func domainRootRewrite(basePath, reqPath string) string {
	basePath = strings.TrimRight(strings.TrimSpace(basePath), "/")
	reqPath = strings.TrimSpace(reqPath)
	if reqPath != "" && reqPath != "/" {
		return ""
	}
	switch basePath {
	case "/web":
		return "/web/index.html"
	default:
		return ""
	}
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

func normalizeHost(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if h, _, err := net.SplitHostPort(v); err == nil {
		return h
	}
	if i := strings.Index(v, ":"); i > -1 && strings.Count(v, ":") == 1 {
		host := v[:i]
		if host != "" {
			return host
		}
	}
	return v
}

func sendHeartbeat(panelURL, agentID, token string) error {
	endpoint := panelURL + "/api/agent/heartbeat?agent_id=" + url.QueryEscape(agentID) + "&token=" + url.QueryEscape(token)
	req, _ := http.NewRequest(http.MethodPost, endpoint, nil)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &statusErr{StatusCode: resp.StatusCode, Body: string(b)}
	}
	return nil
}

