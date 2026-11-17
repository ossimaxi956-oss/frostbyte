package main

// Fester Einladungs-Link + "Beitreten"-Modus fuer Freunde
//
// Der Gastgeber veroeffentlicht seinen aktuellen Internet-Link unter einem festen Code
// (ntfy.sh). Die Webseite ?join=CODE schaut dort nach und leitet zum aktuellen Link weiter.
// Freunde, die Frostbyte.exe starten, koennen statt eines eigenen Servers einfach beitreten.

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const SiteURL = "https://ossimaxi956-oss.github.io/frostbyte/"

var lastPublished string

func hostTopic() string { return "frostbyte-" + db.HostCode }
func joinLink() string  { return SiteURL + "?join=" + db.HostCode }

func ensureHostCode() {
	if db.HostCode == "" {
		const c = "abcdefghijkmnpqrstuvwxyz23456789"
		b := make([]byte, 10)
		for i := range b {
			b[i] = c[randInt(len(c))]
		}
		db.HostCode = string(b)
	}
}

// Link unter dem festen Code veroeffentlichen
func publishLink(u string) {
	if os.Getenv("FROSTBYTE_NO_TUNNEL") != "" || db.HostCode == "" {
		return
	}
	lastPublished = u
	body := u
	if body == "" {
		body = "offline"
	}
	cl := http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("POST", "https://ntfy.sh/"+hostTopic(), strings.NewReader(body))
	req.Header.Set("Cache", "yes")
	if resp, err := cl.Do(req); err == nil {
		resp.Body.Close()
	}
}

// Alle 4 Stunden neu veroeffentlichen (ntfy merkt sich Nachrichten nur 12 Stunden)
func republishLoop() {
	for {
		time.Sleep(4 * time.Hour)
		if lastPublished != "" {
			publishLink(lastPublished)
		}
	}
}

func joinFile() string { return filepath.Join(dataDir, "join.txt") }
func joinCode() string {
	b, err := os.ReadFile(joinFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Code aus Eingabe holen: "abc123", ".../?join=abc123" usw.
func parseJoin(in string) string {
	in = strings.TrimSpace(in)
	if i := strings.Index(in, "join="); i >= 0 {
		if v, err := url.ParseQuery(in[i:]); err == nil {
			in = v.Get("join")
		}
	}
	out := []rune{}
	for _, r := range strings.ToLower(in) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		}
	}
	if len(out) < 6 || len(out) > 20 {
		return ""
	}
	return string(out)
}

// Nur direkt am eigenen PC erlaubt (nicht ueber den Tunnel)
func isLocal(r *http.Request) bool {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback() && r.Header.Get("CF-Connecting-IP") == ""
}

const startPage = `<!DOCTYPE html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Frostbyte</title><link rel="icon" href="/logo.svg"><style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;font-family:"Segoe UI",system-ui,sans-serif;color:#e7ecff;
background:radial-gradient(40% 35% at 15% 15%,rgba(46,230,245,.35),transparent 70%),radial-gradient(35% 40% at 85% 20%,rgba(109,93,252,.45),transparent 70%),radial-gradient(45% 40% at 70% 95%,rgba(225,75,214,.3),transparent 70%),#03050c}
.c{background:rgba(12,18,36,.75);border:1px solid rgba(255,255,255,.12);border-radius:22px;padding:34px;max-width:440px;text-align:center;backdrop-filter:blur(10px)}
img{width:90px;filter:drop-shadow(0 10px 30px rgba(109,93,252,.7))}h1{margin:10px 0 4px}p{color:#9aa6cc}
a.b,button{display:block;width:100%;margin-top:14px;padding:14px;border-radius:12px;border:0;font:inherit;font-weight:800;font-size:17px;cursor:pointer;text-decoration:none;color:#fff;box-sizing:border-box}
a.b{background:linear-gradient(135deg,#2ee6f5,#6d5dfc,#e14bd6)}button{background:rgba(255,255,255,.08);font-size:14px;font-weight:600}
</style></head><body><div class="c"><img src="/logo.svg" alt=""><h1>Frostbyte</h1>
<p>Du bist mit dem Frostbyte von deinem Freund verbunden.</p>
<a class="b" id="go" href="__URL__">▶ Weiter zu deinem Freund (<span id="n">3</span>)</a>
<button onclick="fetch('/api/joinmode',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({banned:true})}).then(function(){location.href='/'})">🏠 Lieber eigenen Frostbyte starten (Gastgeber sein)</button>
</div><script>var n=3,t=setInterval(function(){n--;document.getElementById('n').textContent=n;if(n<=0){clearInterval(t);location.href=document.getElementById('go').href}},1000);
document.querySelector('button').addEventListener('click',function(){clearInterval(t)});</script></body></html>`

func setupJoinRoutes() {
	routes["POST /api/joinmode"] = Route{func(c *Ctx) (any, error) {
		if !isLocal(c.r) {
			return nil, fail(403, "Nur am eigenen PC")
		}
		if c.b.Banned { // ausschalten
			os.Remove(joinFile())
			return nil, nil
		}
		code := parseJoin(c.b.Name)
		if code == "" {
			return nil, fail(400, "Das ist kein gültiger Einladungs-Code oder -Link")
		}
		if code == db.HostCode {
			return nil, fail(400, "Das ist dein eigener Code 😄")
		}
		if err := os.WriteFile(joinFile(), []byte(code), 0644); err != nil {
			return nil, fail(500, "Konnte nicht speichern")
		}
		return M{"url": SiteURL + "?join=" + code}, nil
	}, false, 10000}
}

func serveStart(w http.ResponseWriter, r *http.Request) {
	code := joinCode()
	if code == "" || !isLocal(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strings.ReplaceAll(startPage, "__URL__", SiteURL+"?join="+code)))
}
