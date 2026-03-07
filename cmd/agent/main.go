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

	if err := syncNginxRoutes(c.Routes); err != nil {
		log.Printf("sync nginx failed: %v", err)
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

func syncNginxRoutes(routes []routeCfg) error {
	if _, err := exec.LookPath("nginx"); err != nil {
		return nil
	}
	const (
		webrootDir = "/var/www/emby-relay"
		nginxConf  = "/etc/nginx/conf.d/emby-relay.conf"
	)
	if err := os.MkdirAll(filepathJoin(webrootDir, ".well-known", "acme-challenge"), 0o755); err != nil {
		return err
	}

	domainRoutes := make([]routeCfg, 0, len(routes))
	for _, rc := range routes {
		if strings.TrimSpace(rc.Domain) != "" {
			domainRoutes = append(domainRoutes, rc)
		}
	}

	content, missing, err := nginxConfig(domainRoutes)
	if err != nil {
		return err
	}
	if err := os.WriteFile(nginxConf, []byte(content), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx -t: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx reload: %v: %s", err, strings.TrimSpace(string(out)))
	}

	if ensureACMECerts(missing, webrootDir) {
		content, _, err = nginxConfig(domainRoutes)
		if err != nil {
			return err
		}
		if err := os.WriteFile(nginxConf, []byte(content), 0o644); err != nil {
			return err
		}
		if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
			return fmt.Errorf("nginx -t after certbot: %v: %s", err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
			return fmt.Errorf("nginx reload after certbot: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func nginxConfig(routes []routeCfg) (string, []routeCfg, error) {
	preamble := "map $http_upgrade $connection_upgrade {\n    default upgrade;\n    '' close;\n}\n\nmap $request_method $emby_proxy_method {\n    default $request_method;\n    HEAD GET;\n}\n\n"
	if len(routes) == 0 {
		return preamble + "# no domain routes\n", nil, nil
	}
	var blocks []string
	var missing []routeCfg
	for _, rc := range routes {
		block, hasCert, err := nginxServerBlock(rc)
		if err != nil {
			log.Printf("skip nginx route %s: %v", rc.RouteID, err)
			continue
		}
		blocks = append(blocks, block)
		if !hasCert {
			missing = append(missing, rc)
		}
	}
	if len(blocks) == 0 {
		return preamble + "# no domain routes\n", missing, nil
	}
	return preamble + strings.Join(blocks, "\n\n"), missing, nil
}

func nginxServerBlock(rc routeCfg) (string, bool, error) {
	up, err := url.Parse(strings.TrimSpace(rc.UpstreamURL))
	if err != nil || up.Scheme == "" || up.Host == "" {
		return "", false, fmt.Errorf("invalid upstream url")
	}
	basePath := strings.TrimRight(strings.TrimSpace(up.Path), "/")
	target := up.Scheme + "://" + up.Host
	domain := strings.TrimSpace(rc.Domain)
	fullchain, privkey, hasCert := nginxCertFiles(domain)

	var sb strings.Builder
	sb.WriteString("server {\n")
	sb.WriteString("    listen 80;\n")
	sb.WriteString("    server_name ")
	sb.WriteString(domain)
	sb.WriteString(";\n")
	sb.WriteString("    location ^~ /.well-known/acme-challenge/ {\n")
	sb.WriteString("        root /var/www/emby-relay;\n")
	sb.WriteString("    }\n")
	if redirect := nginxRootRedirect(basePath); redirect != "" {
		sb.WriteString("    location = / {\n")
		sb.WriteString("        return 302 ")
		sb.WriteString(redirect)
		sb.WriteString(";\n")
		sb.WriteString("    }\n")
	}
	sb.WriteString(nginxProxyLocation(target, up, rc, "    "))
	sb.WriteString("}\n")

	if hasCert {
		sb.WriteString("\nserver {\n")
		sb.WriteString("    listen 443 ssl;\n")
		sb.WriteString("    server_name ")
		sb.WriteString(domain)
		sb.WriteString(";\n")
		sb.WriteString("    ssl_certificate ")
		sb.WriteString(fullchain)
		sb.WriteString(";\n")
		sb.WriteString("    ssl_certificate_key ")
		sb.WriteString(privkey)
		sb.WriteString(";\n")
		sb.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n")
		sb.WriteString("    location ^~ /.well-known/acme-challenge/ {\n")
		sb.WriteString("        root /var/www/emby-relay;\n")
		sb.WriteString("    }\n")
		if redirect := nginxRootRedirect(basePath); redirect != "" {
			sb.WriteString("    location = / {\n")
			sb.WriteString("        return 302 ")
			sb.WriteString(redirect)
			sb.WriteString(";\n")
			sb.WriteString("    }\n")
		}
		sb.WriteString(nginxProxyLocation(target, up, rc, "    "))
		sb.WriteString("}")
	}

	return sb.String(), hasCert, nil
}

func nginxRootRedirect(basePath string) string {
	switch strings.TrimRight(strings.TrimSpace(basePath), "/") {
	case "/web":
		return "/web/index.html"
	case "/emby":
		return "/emby/web/index.html"
	default:
		return ""
	}
}

func nginxProxyLocation(target string, up *url.URL, rc routeCfg, indent string) string {
	mode := strings.ToLower(strings.TrimSpace(rc.TLSMode))
	if mode == "" && rc.InsecureTLS {
		mode = "insecure"
	}
	var sb strings.Builder
	sb.WriteString(indent)
	sb.WriteString("location / {\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_pass ")
	sb.WriteString(target)
	sb.WriteString(";\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_method $emby_proxy_method;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_http_version 1.1;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_set_header Host ")
	sb.WriteString(up.Hostname())
	sb.WriteString(";\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_set_header X-Real-IP $remote_addr;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_set_header X-Forwarded-Proto $scheme;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_set_header Upgrade $http_upgrade;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_set_header Connection $connection_upgrade;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_ssl_server_name on;\n")
	sb.WriteString(indent)
	sb.WriteString("    proxy_ssl_name ")
	sb.WriteString(up.Hostname())
	sb.WriteString(";\n")
	if mode == "insecure" {
		sb.WriteString(indent)
		sb.WriteString("    proxy_ssl_verify off;\n")
	}
	sb.WriteString(indent)
	sb.WriteString("}\n")
	return sb.String()
}

func nginxCertFiles(domain string) (string, string, bool) {
	base := "/etc/letsencrypt/live/" + domain
	fullchain := filepathJoin(base, "fullchain.pem")
	privkey := filepathJoin(base, "privkey.pem")
	if fileExists(fullchain) && fileExists(privkey) {
		return fullchain, privkey, true
	}
	return fullchain, privkey, false
}

func ensureACMECerts(routes []routeCfg, webroot string) bool {
	if len(routes) == 0 {
		return false
	}
	if _, err := exec.LookPath("certbot"); err != nil {
		return false
	}
	changed := false
	for _, rc := range routes {
		domain := strings.TrimSpace(rc.Domain)
		if domain == "" {
			continue
		}
		_, _, hasCert := nginxCertFiles(domain)
		if hasCert {
			continue
		}
		args := []string{
			"certonly",
			"--webroot",
			"-w", webroot,
			"-d", domain,
			"--non-interactive",
			"--agree-tos",
			"--register-unsafely-without-email",
			"--keep-until-expiring",
		}
		if out, err := exec.Command("certbot", args...).CombinedOutput(); err != nil {
			log.Printf("certbot failed for %s: %v: %s", domain, err, strings.TrimSpace(string(out)))
			continue
		}
		changed = true
	}
	return changed
}

func filepathJoin(parts ...string) string {
	return strings.ReplaceAll(strings.Join(parts, "/"), "//", "/")
}

func fileExists(p string) bool {
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return true
	}
	return false
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

