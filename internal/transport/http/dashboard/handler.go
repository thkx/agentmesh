package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	sessionCookie = "agentmesh_dashboard"
	sessionTTL    = 8 * time.Hour
)

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · AgentMesh</title><link rel="stylesheet" href="/dashboard/assets/app.css">
</head>
<body>
<aside class="sidebar">
  <a class="brand" href="/dashboard">AgentMesh</a>
  <nav>
    <a class="{{if eq .Active "overview"}}active{{end}}" href="/dashboard">Overview</a>
    <a class="muted" href="#" aria-disabled="true">Providers &amp; Models <small>M50</small></a>
    <a class="muted" href="#" aria-disabled="true">Agents <small>M51</small></a>
    <a class="muted" href="#" aria-disabled="true">Workflows <small>M51</small></a>
    <a class="muted" href="#" aria-disabled="true">Executions <small>M52</small></a>
  </nav>
  {{if .Authenticated}}<form method="post" action="/dashboard/logout"><button class="logout" type="submit">Sign out</button></form>{{end}}
</aside>
<main class="main">
  <header><div><p class="eyebrow">LOCAL CONTROL PLANE</p><h1>{{.Title}}</h1><p class="sub">{{.Subtitle}}</p></div><span class="local-pill">Local-first</span></header>
  {{if .Flash}}<div class="notice">{{.Flash}}</div>{{end}}
  {{if .Login}}<section class="panel login"><h2>Dashboard sign in</h2><p>Enter the AgentMesh API token configured for this local instance.</p>
    {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
    <form method="post" action="/dashboard/login"><label for="token">API token</label><input id="token" name="token" type="password" autocomplete="current-password" required minlength="32"><button type="submit">Sign in</button></form>
  </section>{{else}}
  <section class="cards">
    <article class="metric"><span>Service</span><strong>{{.ServiceStatus}}</strong><small>{{.ServiceDetail}}</small></article>
    <article class="metric"><span>Database</span><strong>{{.DatabaseStatus}}</strong><small>{{.DatabaseDetail}}</small></article>
    <article class="metric"><span>Executions</span><strong>{{.ExecutionCount}}</strong><small>Canonical execution records</small></article>
  </section>
  <section class="panel"><div class="panel-heading"><div><h2>Control Center</h2><p>Manage the existing AgentMesh capabilities from one place.</p></div><span class="tag">M49 foundation</span></div>
    <div class="feature-grid">
      <div><b>Provider &amp; Model Console</b><p>Configure providers, model catalog and credential status.</p><span>Coming in M50</span></div>
      <div><b>Agent &amp; Workflow Console</b><p>Manage definitions and launch supported work.</p><span>Coming in M51</span></div>
      <div><b>Unified Execution Center</b><p>Inspect attempts, lifecycle timeline and provenance.</p><span>Coming in M52</span></div>
    </div>
  </section>
  {{end}}
  <footer>AgentMesh · Dashboard foundation · No external frontend assets</footer>
</main>
</body></html>`

const stylesheet = `:root{color-scheme:dark;--bg:#0b1020;--panel:#121a2b;--line:#263249;--text:#e8edf7;--muted:#9aa8bd;--accent:#8ba8ff}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}.sidebar{position:fixed;inset:0 auto 0 0;width:230px;padding:28px 18px;background:#0e1627;border-right:1px solid var(--line);display:flex;flex-direction:column}.brand{font-size:21px;font-weight:750;color:var(--text);text-decoration:none;margin:0 10px 42px}.sidebar nav{display:grid;gap:7px}.sidebar nav a{padding:11px 12px;border-radius:8px;color:var(--muted);text-decoration:none}.sidebar nav a.active{background:#1b2a49;color:#fff}.sidebar nav a.muted{opacity:.55;cursor:default}.sidebar small{float:right}.logout{margin: auto 10px 0;background:transparent;border:1px solid var(--line);color:var(--muted);padding:9px 12px;border-radius:8px;cursor:pointer}.main{margin-left:230px;padding:44px clamp(22px,5vw,72px);max-width:1500px}header{display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:34px}.eyebrow{color:var(--accent);font-size:11px;letter-spacing:.15em;font-weight:700}h1{font-size:34px;line-height:1.2;margin:7px 0}.sub, .panel p,.metric small{color:var(--muted)}.local-pill,.tag{border:1px solid var(--line);border-radius:99px;padding:6px 11px;color:#b8c8ef;font-size:12px}.cards{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;margin-bottom:20px}.metric,.panel{background:var(--panel);border:1px solid var(--line);border-radius:13px;padding:22px}.metric span{display:block;color:var(--muted);font-size:13px}.metric strong{display:block;font-size:27px;margin:9px 0 3px}.metric small{font-size:12px}.panel-heading{display:flex;justify-content:space-between;align-items:flex-start}.panel h2{margin:0;font-size:19px}.feature-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;margin-top:22px}.feature-grid>div{border:1px solid var(--line);border-radius:10px;padding:17px}.feature-grid p{min-height:48px;font-size:13px}.feature-grid span{color:var(--accent);font-size:12px}.notice{padding:12px 16px;background:#172744;border:1px solid #314d7d;border-radius:8px;margin-bottom:18px}.login{max-width:480px;margin:60px auto}.login form{display:grid;gap:12px;margin-top:22px}.login input{background:#0b1020;color:var(--text);border:1px solid var(--line);padding:12px;border-radius:8px}.login button{background:#496fe0;color:white;border:0;border-radius:8px;padding:12px;font-weight:650;cursor:pointer}.error{color:#ff9e9e}footer{color:#68778f;font-size:12px;margin-top:38px}@media(max-width:850px){.sidebar{position:static;width:auto;padding:15px 18px;border-right:0;border-bottom:1px solid var(--line)}.brand{margin:0 0 12px}.sidebar nav{display:flex;overflow:auto}.sidebar nav a{white-space:nowrap}.logout{position:absolute;right:16px;top:13px;margin:0}.main{margin:0;padding:26px 18px}.cards,.feature-grid{grid-template-columns:1fr}header{gap:14px;flex-direction:column}h1{font-size:28px}}`

type pageData struct {
	Title, Subtitle, Active string
	ServiceStatus, ServiceDetail string
	DatabaseStatus, DatabaseDetail string
	ExecutionCount string
	Authenticated, Login bool
	Error, Flash string
}

type Handler struct {
	db *sql.DB
	apiToken string
	signingKey []byte
	now func() time.Time
	tpl *template.Template
}

func New(db *sql.DB, apiToken string) (*Handler, error) {
	tpl, err := template.New("dashboard").Parse(pageTemplate)
	if err != nil { return nil, fmt.Errorf("parse dashboard template: %w", err) }
	h := &Handler{db: db, apiToken: strings.TrimSpace(apiToken), now: time.Now, tpl: tpl}
	if h.apiToken != "" {
		mac := hmac.New(sha256.New, []byte(h.apiToken))
		_, _ = mac.Write([]byte("agentmesh-dashboard-session-v1"))
		h.signingKey = mac.Sum(nil)
	}
	return h, nil
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/dashboard")
	g.GET("/login", h.loginPage)
	g.POST("/login", h.login)
	g.POST("/logout", h.logout)
	g.GET("/assets/app.css", func(c *gin.Context) { c.Data(http.StatusOK, "text/css; charset=utf-8", []byte(stylesheet)) })
	g.GET("", h.requireSession(), h.overview)
	g.GET("/", h.requireSession(), h.overview)
}

func (h *Handler) render(c *gin.Context, data pageData) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	c.Header("Referrer-Policy", "same-origin")
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := h.tpl.Execute(c.Writer, data); err != nil { c.AbortWithStatus(http.StatusInternalServerError) }
}

func (h *Handler) loginPage(c *gin.Context) {
	if h.apiToken == "" { c.Redirect(http.StatusSeeOther, "/dashboard"); return }
	h.render(c, pageData{Title:"Sign in", Login:true, ServiceStatus:"Protected", DatabaseStatus:"—"})
}

func (h *Handler) login(c *gin.Context) {
	if !h.sameOrigin(c) || !h.safeLoginTransport(c) {
		c.AbortWithStatus(http.StatusForbidden); return
	}
	got := c.PostForm("token")
	if h.apiToken == "" || len(got) != len(h.apiToken) || subtle.ConstantTimeCompare([]byte(got), []byte(h.apiToken)) != 1 {
		h.render(c, pageData{Title:"Sign in", Login:true, Error:"Sign-in failed. Check the token and try again."})
		return
	}
	expires := h.now().Add(sessionTTL).Unix()
	value := strconv.FormatInt(expires, 10) + "." + h.signature(expires)
	c.SetCookie(sessionCookie, value, int(sessionTTL.Seconds()), "/dashboard", "", c.Request.TLS != nil, true)
	c.SetSameSite(http.SameSiteStrictMode)
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

func (h *Handler) logout(c *gin.Context) {
	if !h.sameOrigin(c) { c.AbortWithStatus(http.StatusForbidden); return }
	c.SetCookie(sessionCookie, "", -1, "/dashboard", "", c.Request.TLS != nil, true)
	c.SetSameSite(http.SameSiteStrictMode)
	c.Redirect(http.StatusSeeOther, "/dashboard/login")
}

func (h *Handler) requireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.apiToken == "" || h.validSession(c) { c.Next(); return }
		if strings.Contains(c.GetHeader("Accept"), "text/html") { c.Redirect(http.StatusSeeOther, "/dashboard/login") } else { c.AbortWithStatus(http.StatusUnauthorized) }
		c.Abort()
	}
}

func (h *Handler) validSession(c *gin.Context) bool {
	cookie, err := c.Cookie(sessionCookie); if err != nil { return false }
	parts := strings.Split(cookie, "."); if len(parts) != 2 { return false }
	expires, err := strconv.ParseInt(parts[0], 10, 64); if err != nil { return false }
	if expires <= h.now().Unix() || expires > h.now().Add(sessionTTL+time.Minute).Unix() { return false }
	expected := h.signature(expires)
	return len(parts[1]) == len(expected) && subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expected)) == 1
}

func (h *Handler) signature(expires int64) string {
	mac := hmac.New(sha256.New, h.signingKey)
	_, _ = mac.Write([]byte("dashboard-session:" + strconv.FormatInt(expires, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *Handler) sameOrigin(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" { return false }
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, c.Request.Host) && (u.Scheme == "https" || (u.Scheme == "http" && isLoopbackRequest(c)))
}

func (h *Handler) safeLoginTransport(c *gin.Context) bool {
	return c.Request.TLS != nil || isLoopbackRequest(c)
}

func isLoopbackRequest(c *gin.Context) bool {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil { host = c.Request.RemoteAddr }
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (h *Handler) overview(c *gin.Context) {
	data := pageData{Title:"Overview", Subtitle:"A single control surface for your local AgentMesh runtime.", Active:"overview", ServiceStatus:"Online", ServiceDetail:"HTTP server is responding", DatabaseStatus:"Unknown", DatabaseDetail:"Database check unavailable", ExecutionCount:"—", Authenticated:h.apiToken != ""}
	if h.db != nil {
		ctx, cancel := timeBoundContext(c, 2*time.Second); defer cancel()
		if err := h.db.PingContext(ctx); err == nil {
			data.DatabaseStatus, data.DatabaseDetail = "Ready", "SQLite connection healthy"
			var count int64
			if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM executions").Scan(&count); err == nil { data.ExecutionCount = strconv.FormatInt(count, 10) } else { data.ExecutionCount = "Unavailable" }
		} else { data.DatabaseStatus, data.DatabaseDetail = "Unavailable", "Database health check failed" }
	} else { data.DatabaseStatus, data.DatabaseDetail = "Not configured", "No database dependency was supplied" }
	h.render(c, data)
}

func timeBoundContext(c *gin.Context, timeout time.Duration) (interface{ Done() <-chan struct{}; Err() error; Deadline() (time.Time, bool); Value(any) any }, func()) {
	// Kept as a small helper to bound dashboard reads to the request lifetime.
	ctx, cancel := contextWithTimeout(c, timeout)
	return ctx, cancel
}
