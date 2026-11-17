// Frostbyte - dein eigener Winter-Chat als eine einzige .exe
// Doppelklick -> Server startet -> App-Fenster oeffnet sich.
package main

import (
	"bufio"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// ======== EINSTELLUNGEN ========
const (
	AppName       = "Frostbyte"
	OldAppName    = "He Discord"
	OwnerName     = "Owner"
	OwnerPassword = "Owner#2026" // nur beim allerersten Start benutzt
	DefaultPort   = "3000"
	Version       = "17"
)

//go:embed index.html
var indexHTML string

//go:embed logo.svg
var logoSVG []byte

//go:embed icon-192.png
var icon192 []byte

//go:embed icon-512.png
var icon512 []byte
var page []byte

var EMOJIS = []string{"❄️", "☃️", "⛄", "🎄", "🎅", "🎁", "☕", "🧣", "👍", "👎", "❤️", "😂", "🤣", "😮", "😢", "😭",
	"😡", "🔥", "🎉", "👀", "💯", "✅", "❌", "😎", "🤔", "🙏", "👋", "💀", "🥳", "😍", "😅", "🙂",
	"😉", "😜", "🤯", "🥶", "💪", "🚀", "⭐", "🍕", "🎮", "🏆", "💎", "👑", "🤝", "✨", "⚡", "💬"}
var COLORS = []string{"#4aa8ff", "#5865f2", "#3ba55c", "#faa61a", "#ed4245", "#eb459e", "#9b59b6", "#1abc9c", "#e67e22", "#3498db", "#f1c40f", "#e91e63", "#607d8b"}

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9_.\-äöüÄÖÜß]{3,20}$`)
	colorRe  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	uploadRe = regexp.MustCompile(`^[a-f0-9]{16}\.(png|jpg|gif|webp)$`)
	spaceRe  = regexp.MustCompile(`\s+`)
	badChRe  = regexp.MustCompile(`[^a-z0-9äöüß_\-]`)
	dashRe   = regexp.MustCompile(`-+`)
)

// ---------------- Datenmodell ----------------
type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NameLower string `json:"nameLower"`
	Pass      string `json:"pass"`
	Role      string `json:"role"`
	Color     string `json:"color"`
	About     string `json:"about"`
	Created   int64  `json:"created"`
	Banned    bool   `json:"banned"`
	Avatar    string `json:"avatar,omitempty"`
}
type Session struct {
	UserID  string `json:"userId"`
	Created int64  `json:"created"`
}
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"` // "" = Text, "voice" = Sprache
}
type Server struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	OwnerID     string              `json:"ownerId"`
	Invite      string              `json:"invite"`
	Members     []string            `json:"members"`
	Channels    []Channel           `json:"channels"`
	Created     int64               `json:"created"`
	Bots        *BotCfg             `json:"bots,omitempty"`
	XP          map[string]int      `json:"xp,omitempty"`
	Roles       []*Role             `json:"roles,omitempty"`
	MemberRoles map[string][]string `json:"memberRoles,omitempty"`
	Bans        []string            `json:"bans,omitempty"`
	Icon        string              `json:"icon,omitempty"`
}
type Message struct {
	ID        string              `json:"id"`
	Channel   string              `json:"channel"`
	UserID    string              `json:"userId"`
	Text      string              `json:"text"`
	Image     *string             `json:"image"`
	Ts        int64               `json:"ts"`
	Edited    *int64              `json:"edited"`
	Reactions map[string][]string `json:"reactions"`
	Giveaway  string              `json:"giveaway,omitempty"`
	ReplyTo   string              `json:"replyTo,omitempty"`
}
type DM struct {
	ID    string   `json:"id"`
	Users []string `json:"users"`
	Last  int64    `json:"last"`
}
type FriendReq struct {
	From string `json:"from"`
	To   string `json:"to"`
	Ts   int64  `json:"ts"`
}
type ModEntry struct {
	Ts       int64  `json:"ts"`
	Kind     string `json:"kind"` // blocked | report
	UserID   string `json:"userId"`
	Where    string `json:"where"`
	Text     string `json:"text"`
	Reporter string `json:"reporter,omitempty"`
}
type DB struct {
	Users      map[string]*User      `json:"users"`
	Sessions   map[string]*Session   `json:"sessions"`
	Servers    map[string]*Server    `json:"servers"`
	Messages   map[string][]*Message `json:"messages"`
	Dms        map[string]*DM        `json:"dms"`
	MainServer string                `json:"mainServer"`
	Friends    map[string][]string   `json:"friends"`
	Requests   []*FriendReq          `json:"requests"`
	BadWords   []string              `json:"badWords"`
	ModLog     []*ModEntry           `json:"modLog"`
	Giveaways  map[string]*Giveaway  `json:"giveaways"`
}

func modLog(e *ModEntry) {
	e.Ts = now()
	e.Text = trunc(e.Text, 500)
	db.ModLog = append(db.ModLog, e)
	if len(db.ModLog) > 1000 {
		db.ModLog = db.ModLog[len(db.ModLog)-1000:]
	}
	save()
}
func modIDs(except string) []string {
	out := []string{}
	for _, x := range db.Users {
		if isMod(x) && x.ID != except {
			out = append(out, x.ID)
		}
	}
	return out
}
func whereOf(ch string) string {
	cc := ctxOf(ch)
	if cc == nil {
		return "?"
	}
	if cc.isDM {
		names := []string{}
		for _, id := range cc.dm.Users {
			if u := db.Users[id]; u != nil {
				names = append(names, u.Name)
			}
		}
		return "Privatchat " + strings.Join(names, " & ")
	}
	for _, c := range cc.server.Channels {
		if c.ID == ch {
			return cc.server.Name + " #" + c.Name
		}
	}
	return cc.server.Name
}

var (
	mu        sync.Mutex
	db        DB
	dataDir   string
	uploadDir string
	dataFile  string
	saveCh    = make(chan struct{}, 1)
)

type M = map[string]any

// ---------------- Hilfsfunktionen ----------------
func newID() string { b := make([]byte, 8); rand.Read(b); return hex.EncodeToString(b) }
func now() int64    { return time.Now().UnixMilli() }
func randInt(n int) int {
	x, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	return int(x.Int64())
}
func inviteCode() string {
	const c = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = c[randInt(len(c))]
	}
	return string(b)
}
func hashPw(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	k, _ := pbkdf2.Key(sha256.New, pw, salt, 120000, 32)
	return "pbkdf2$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(k)
}
func checkPw(pw, stored string) bool {
	p := strings.Split(stored, "$")
	if len(p) != 3 || p[0] != "pbkdf2" {
		return false
	}
	salt, e1 := hex.DecodeString(p[1])
	want, e2 := hex.DecodeString(p[2])
	if e1 != nil || e2 != nil {
		return false
	}
	k, _ := pbkdf2.Key(sha256.New, pw, salt, 120000, 32)
	return subtle.ConstantTimeCompare(k, want) == 1
}
func runes(s string) int { return utf8.RuneCountInString(s) }
func trunc(s string, n int) string {
	if runes(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
func contains(list []string, x string) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}
func without(list []string, x string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
func byName(n string) *User {
	l := strings.ToLower(n)
	for _, u := range db.Users {
		if u.NameLower == l {
			return u
		}
	}
	return nil
}
func createUser(name, pw, role string) *User {
	u := &User{ID: newID(), Name: name, NameLower: strings.ToLower(name), Pass: hashPw(pw), Role: role, Color: COLORS[randInt(len(COLORS))], Created: now()}
	db.Users[u.ID] = u
	return u
}
func makeServer(name, ownerID string, defaults bool) *Server {
	ch := []Channel{{newID(), "allgemein", ""}, {newID(), "Sprachchat", "voice"}}
	if defaults {
		ch = []Channel{{newID(), "allgemein", ""}, {newID(), "winter-chat", ""}, {newID(), "memes", ""}, {newID(), "spiele", ""}, {newID(), "bilder", ""}, {newID(), "Sprachchat", "voice"}}
	}
	s := &Server{ID: newID(), Name: name, OwnerID: ownerID, Invite: inviteCode(), Members: []string{ownerID}, Channels: ch, Created: now()}
	db.Servers[s.ID] = s
	return s
}
func chanName(n string) string {
	s := strings.ToLower(strings.TrimSpace(n))
	s = spaceRe.ReplaceAllString(s, "-")
	s = badChRe.ReplaceAllString(s, "")
	s = dashRe.ReplaceAllString(s, "-")
	return trunc(s, 30)
}
func isMod(u *User) bool                { return u.Role == "owner" || u.Role == "admin" }
func canManage(u *User, s *Server) bool { return s.OwnerID == u.ID || isMod(u) }

// ---------------- Speichern ----------------
func writeDB() {
	mu.Lock()
	data, err := json.Marshal(&db)
	mu.Unlock()
	if err != nil {
		return
	}
	tmp := dataFile + ".tmp"
	if os.WriteFile(tmp, data, 0644) == nil {
		os.Rename(tmp, dataFile)
	}
}
func save() {
	select {
	case saveCh <- struct{}{}:
	default:
	}
}
func saver() {
	for range saveCh {
		time.Sleep(400 * time.Millisecond)
		writeDB()
	}
}

// ---------------- Live-Verbindungen ----------------
type Client struct {
	userID, token string
	ch            chan []byte
	done          chan struct{}
	closed        bool
}

var clients = map[*Client]bool{}

func isOnline(uid string) bool {
	for c := range clients {
		if c.userID == uid {
			return true
		}
	}
	return false
}
func event(typ string, data any) []byte {
	b, _ := json.Marshal(M{"type": typ, "data": data})
	return []byte("data: " + string(b) + "\n\n")
}
func push(c *Client, msg []byte) {
	if c.closed {
		return
	}
	select {
	case c.ch <- msg:
	default:
	}
}
func send(ids []string, typ string, data any) {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	msg := event(typ, data)
	for c := range clients {
		if set[c.userID] {
			push(c, msg)
		}
	}
}
func sendAll(typ string, data any) {
	msg := event(typ, data)
	for c := range clients {
		push(c, msg)
	}
}
func closeClient(c *Client) {
	if !c.closed {
		c.closed = true
		close(c.done)
	}
	delete(clients, c)
}
func kickSessions(uid, except, evt string) {
	for t, s := range db.Sessions {
		if s.UserID == uid && t != except {
			delete(db.Sessions, t)
		}
	}
	for c := range clients {
		if c.userID == uid && c.token != except {
			push(c, event(evt, M{}))
			closeClient(c)
		}
	}
	if !isOnline(uid) {
		sendAll("presence", M{"userId": uid, "online": false})
	}
	save()
}

type PubUser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	Role    string `json:"role"`
	About   string `json:"about"`
	Banned  bool   `json:"banned"`
	Created int64  `json:"created"`
	Online  bool   `json:"online"`
	Avatar  string `json:"avatar,omitempty"`
}
type PubServer struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	OwnerID     string              `json:"ownerId"`
	Invite      string              `json:"invite"`
	Members     []string            `json:"members"`
	Channels    []Channel           `json:"channels"`
	Main        bool                `json:"main"`
	Bots        *BotCfg             `json:"bots"`
	Roles       []*Role             `json:"roles"`
	MemberRoles map[string][]string `json:"memberRoles"`
	Bans        []string            `json:"bans"`
	Icon        string              `json:"icon,omitempty"`
}

func pubUser(u *User) PubUser {
	return PubUser{u.ID, u.Name, u.Color, u.Role, u.About, u.Banned, u.Created, isOnline(u.ID), u.Avatar}
}
func pubServer(s *Server) PubServer {
	roles := s.Roles
	if roles == nil {
		roles = []*Role{}
	}
	mr := s.MemberRoles
	if mr == nil {
		mr = map[string][]string{}
	}
	bans := s.Bans
	if bans == nil {
		bans = []string{}
	}
	return PubServer{s.ID, s.Name, s.OwnerID, s.Invite, s.Members, s.Channels, s.ID == db.MainServer, botsOf(s), roles, mr, bans, s.Icon}
}

type ChCtx struct {
	isDM     bool
	server   *Server
	dm       *DM
	audience []string
}

func ctxOf(ch string) *ChCtx {
	if ch == "" {
		return nil
	}
	if strings.HasPrefix(ch, "dm_") {
		if dm := db.Dms[ch]; dm != nil {
			return &ChCtx{isDM: true, dm: dm, audience: dm.Users}
		}
		return nil
	}
	for _, s := range db.Servers {
		for _, c := range s.Channels {
			if c.ID == ch {
				return &ChCtx{server: s, audience: s.Members}
			}
		}
	}
	return nil
}
func canSee(u *User, c *ChCtx) bool { return c != nil && contains(c.audience, u.ID) }

// ---------------- HTTP ----------------
type HErr struct {
	Status int
	Msg    string
}

func (e *HErr) Error() string    { return e.Msg }
func fail(s int, m string) error { return &HErr{s, m} }

type Body struct {
	Name      string          `json:"name"`
	Password  string          `json:"password"`
	Old       string          `json:"old"`
	Channel   string          `json:"channel"`
	Text      string          `json:"text"`
	Image     string          `json:"image"`
	ID        string          `json:"id"`
	Emoji     string          `json:"emoji"`
	Invite    string          `json:"invite"`
	ServerID  string          `json:"serverId"`
	ChannelID string          `json:"channelId"`
	UserID    string          `json:"userId"`
	Role      string          `json:"role"`
	Color     *string         `json:"color"`
	About     *string         `json:"about"`
	Banned    bool            `json:"banned"`
	Data      json.RawMessage `json:"data"`
	Cfg       json.RawMessage `json:"cfg"`
	Minutes   int             `json:"minutes"`
	Count     int             `json:"count"`
	ReplyTo   string          `json:"replyTo"`
}

func sortedPair(a, b string) []string {
	if a < b {
		return []string{a, b}
	}
	return []string{b, a}
}

type Ctx struct {
	r     *http.Request
	b     Body
	user  *User
	token string
}
type Route struct {
	fn    func(*Ctx) (any, error)
	auth  bool
	limit int64
}

var routes = map[string]Route{}

func R(key string, fn func(*Ctx) (any, error)) { routes[key] = Route{fn, true, 100000} }

func authUser(r *http.Request) (*User, string) {
	t := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		t = h[7:]
	} else {
		t = r.URL.Query().Get("token")
	}
	s := db.Sessions[t]
	if t == "" || s == nil {
		return nil, ""
	}
	u := db.Users[s.UserID]
	if u == nil || u.Banned {
		return nil, ""
	}
	return u, t
}
func newSession(u *User) string {
	b := make([]byte, 32)
	rand.Read(b)
	t := hex.EncodeToString(b)
	db.Sessions[t] = &Session{u.ID, now()}
	save()
	return t
}
func saveImage(d string) (string, error) {
	i := strings.Index(d, ";base64,")
	if i < 0 || !strings.HasPrefix(d, "data:image/") {
		return "", fail(400, "Nur Bilder (PNG, JPG, GIF, WEBP) erlaubt")
	}
	ext := map[string]string{"png": "png", "jpeg": "jpg", "gif": "gif", "webp": "webp"}[d[11:i]]
	if ext == "" {
		return "", fail(400, "Nur Bilder (PNG, JPG, GIF, WEBP) erlaubt")
	}
	buf, err := base64.StdEncoding.DecodeString(d[i+8:])
	if err != nil {
		return "", fail(400, "Bild ist kaputt")
	}
	if len(buf) > 5*1024*1024 {
		return "", fail(413, "Bild zu groß (max. 5 MB)")
	}
	name := newID() + "." + ext
	if err := os.WriteFile(filepath.Join(uploadDir, name), buf, 0644); err != nil {
		return "", fail(500, "Bild konnte nicht gespeichert werden")
	}
	return "/uploads/" + name, nil
}
func ipOf(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	// Ueber den Cloudflare-Tunnel kommt alles von 127.0.0.1 - dann die echte IP nehmen
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
			return cf
		}
	}
	return h
}

type limiter struct {
	count int
	until int64
}

var (
	loginFails = map[string]*limiter{}
	regCount   = map[string]*limiter{}
	spam       = map[string][]int64{}
)

func getServer(u *User, id string, perm string) (*Server, error) {
	s := db.Servers[id]
	if s == nil || !contains(s.Members, u.ID) {
		return nil, fail(404, "Server nicht gefunden")
	}
	if perm != "" && !hasPerm(u, s, perm) {
		return nil, fail(403, "Dafür fehlt dir das Recht „"+permNames[perm]+"“")
	}
	return s, nil
}
func serverChanged(s *Server) (any, error) {
	save()
	send(s.Members, "server_update", pubServer(s))
	return M{"server": pubServer(s)}, nil
}
func findMsg(u *User, ch, id string) (*ChCtx, int, error) {
	c := ctxOf(ch)
	if !canSee(u, c) {
		return nil, 0, fail(403, "Kein Zugriff")
	}
	for i, m := range db.Messages[ch] {
		if m.ID == id {
			return c, i, nil
		}
	}
	return nil, 0, fail(404, "Nachricht nicht gefunden")
}
func target(u *User, id string) (*User, error) {
	t := db.Users[id]
	if t == nil {
		return nil, fail(404, "Benutzer nicht gefunden")
	}
	if t.ID == u.ID {
		return nil, fail(400, "Das geht nicht bei dir selbst")
	}
	if t.Role == "owner" {
		return nil, fail(403, "Der Owner kann nicht verändert werden")
	}
	return t, nil
}

// ---------------- Freunde ----------------
func friendState(uid string) M {
	in, out := []string{}, []string{}
	for _, r := range db.Requests {
		if r.To == uid {
			in = append(in, r.From)
		} else if r.From == uid {
			out = append(out, r.To)
		}
	}
	fr := db.Friends[uid]
	if fr == nil {
		fr = []string{}
	}
	return M{"friends": fr, "incoming": in, "outgoing": out}
}
func findReq(from, to string) int {
	for i, r := range db.Requests {
		if r.From == from && r.To == to {
			return i
		}
	}
	return -1
}
func dropReq(from, to string) bool {
	if i := findReq(from, to); i >= 0 {
		db.Requests = append(db.Requests[:i:i], db.Requests[i+1:]...)
		return true
	}
	return false
}
func makeFriends(a, b string) {
	dropReq(a, b)
	dropReq(b, a)
	if !contains(db.Friends[a], b) {
		db.Friends[a] = append(db.Friends[a], b)
	}
	if !contains(db.Friends[b], a) {
		db.Friends[b] = append(db.Friends[b], a)
	}
}
func friendsChanged(a, b string) {
	save()
	send([]string{a}, "friends", friendState(a))
	send([]string{b}, "friends", friendState(b))
}

func setupRoutes() {
	setupBotRoutes()
	setupRoleRoutes()
	routes["GET /api/info"] = Route{func(c *Ctx) (any, error) { return M{"app": AppName, "version": Version}, nil }, false, 0}
	R("POST /api/friends/request", func(c *Ctx) (any, error) {
		var t *User
		if c.b.UserID != "" {
			t = db.Users[c.b.UserID]
		} else {
			t = byName(strings.TrimSpace(c.b.Name))
		}
		if t == nil {
			return nil, fail(404, "Diesen Frostbyte-Benutzer gibt es nicht. Dein Freund muss sich zuerst bei Frostbyte registrieren (echte Discord-Namen gehen nicht).")
		}
		if t.ID == c.user.ID {
			return nil, fail(400, "Du kannst dich nicht selbst adden 😄")
		}
		if contains(db.Friends[c.user.ID], t.ID) {
			return nil, fail(400, "Ihr seid schon befreundet!")
		}
		if findReq(c.user.ID, t.ID) >= 0 {
			return nil, fail(400, "Du hast "+t.Name+" schon eine Anfrage geschickt")
		}
		accepted := false
		if findReq(t.ID, c.user.ID) >= 0 {
			makeFriends(c.user.ID, t.ID)
			accepted = true
		} else {
			n := 0
			for _, r := range db.Requests {
				if r.From == c.user.ID {
					n++
				}
			}
			if n >= 100 {
				return nil, fail(429, "Zu viele offene Anfragen")
			}
			db.Requests = append(db.Requests, &FriendReq{c.user.ID, t.ID, now()})
		}
		friendsChanged(c.user.ID, t.ID)
		return M{"accepted": accepted, "user": pubUser(t), "state": friendState(c.user.ID)}, nil
	})
	R("POST /api/friends/accept", func(c *Ctx) (any, error) {
		if findReq(c.b.UserID, c.user.ID) < 0 {
			return nil, fail(404, "Keine Anfrage gefunden")
		}
		makeFriends(c.user.ID, c.b.UserID)
		friendsChanged(c.user.ID, c.b.UserID)
		return M{"state": friendState(c.user.ID)}, nil
	})
	R("POST /api/friends/decline", func(c *Ctx) (any, error) {
		a := dropReq(c.b.UserID, c.user.ID)
		b := dropReq(c.user.ID, c.b.UserID)
		if !a && !b {
			return nil, fail(404, "Keine Anfrage gefunden")
		}
		friendsChanged(c.user.ID, c.b.UserID)
		return M{"state": friendState(c.user.ID)}, nil
	})
	R("POST /api/friends/remove", func(c *Ctx) (any, error) {
		if !contains(db.Friends[c.user.ID], c.b.UserID) {
			return nil, fail(404, "Ihr seid nicht befreundet")
		}
		db.Friends[c.user.ID] = without(db.Friends[c.user.ID], c.b.UserID)
		db.Friends[c.b.UserID] = without(db.Friends[c.b.UserID], c.user.ID)
		friendsChanged(c.user.ID, c.b.UserID)
		return M{"state": friendState(c.user.ID)}, nil
	})

	routes["POST /api/register"] = Route{func(c *Ctx) (any, error) {
		name, pw := strings.TrimSpace(c.b.Name), c.b.Password
		if !nameRe.MatchString(name) {
			return nil, fail(400, "Name: 3–20 Zeichen, nur Buchstaben, Zahlen und _ . -")
		}
		if runes(pw) < 6 {
			return nil, fail(400, "Das Passwort muss mindestens 6 Zeichen haben")
		}
		if runes(pw) > 200 {
			return nil, fail(400, "Passwort zu lang")
		}
		if byName(name) != nil {
			return nil, fail(409, "Diesen Namen gibt es schon")
		}
		if isBad(name) {
			return nil, fail(400, "🚫 Dieser Name ist nicht erlaubt. Such dir einen netten Namen aus ❄️")
		}
		ip := ipOf(c.r)
		r := regCount[ip]
		if r == nil || now() > r.until {
			r = &limiter{0, now() + 3600000}
		}
		if r.count >= 10 {
			return nil, fail(429, "Zu viele neue Konten. Versuch es später nochmal.")
		}
		r.count++
		regCount[ip] = r
		u := createUser(name, pw, "user")
		t := newSession(u)
		sendAll("user_update", pubUser(u))
		return M{"token": t}, nil
	}, false, 100000}

	routes["POST /api/login"] = Route{func(c *Ctx) (any, error) {
		ip := ipOf(c.r)
		e := loginFails[ip]
		if e == nil || now() > e.until {
			e = &limiter{0, 0}
		}
		if e.count >= 8 {
			return nil, fail(429, "Zu viele falsche Versuche. Warte 5 Minuten.")
		}
		u := byName(strings.TrimSpace(c.b.Name))
		if u == nil || !checkPw(c.b.Password, u.Pass) {
			e.count++
			e.until = now() + 5*60000
			loginFails[ip] = e
			return nil, fail(401, "Name oder Passwort falsch")
		}
		if u.Banned {
			return nil, fail(403, "Du wurdest gebannt.")
		}
		delete(loginFails, ip)
		return M{"token": newSession(u)}, nil
	}, false, 100000}

	R("POST /api/logout", func(c *Ctx) (any, error) {
		delete(db.Sessions, c.token)
		save()
		for cl := range clients {
			if cl.token == c.token {
				closeClient(cl)
			}
		}
		if !isOnline(c.user.ID) {
			sendAll("presence", M{"userId": c.user.ID, "online": false})
		}
		return nil, nil
	})

	R("GET /api/state", func(c *Ctx) (any, error) {
		users := make([]PubUser, 0, len(db.Users))
		for _, u := range db.Users {
			users = append(users, pubUser(u))
		}
		servers := make([]PubServer, 0)
		voice := M{}
		for _, s := range db.Servers {
			if contains(s.Members, c.user.ID) {
				servers = append(servers, pubServer(s))
				for k, v := range vcStateFor(s) {
					voice[k] = v
				}
			}
		}
		dms := make([]*DM, 0)
		for _, d := range db.Dms {
			if contains(d.Users, c.user.ID) {
				dms = append(dms, d)
			}
		}
		return M{"app": AppName, "version": Version, "me": pubUser(c.user), "mainServer": db.MainServer, "emojis": EMOJIS, "colors": COLORS,
			"users": users, "servers": servers, "dms": dms, "friends": friendState(c.user.ID), "voice": voice, "giveaways": gwsFor(c.user.ID), "publicUrl": publicURL, "lan": func() []string {
				if c.user.Role != "owner" {
					return []string{}
				}
				out := []string{}
				for _, ip := range lanIPs() {
					out = append(out, "http://"+ip+":"+listenPort)
				}
				return out
			}(), "badWords": func() []string {
				if isMod(c.user) {
					return db.BadWords
				}
				return []string{}
			}()}, nil
	})

	R("GET /api/messages", func(c *Ctx) (any, error) {
		ch := c.r.URL.Query().Get("channel")
		if !canSee(c.user, ctxOf(ch)) {
			return nil, fail(403, "Kein Zugriff")
		}
		list := db.Messages[ch]
		if len(list) > 150 {
			list = list[len(list)-150:]
		}
		if list == nil {
			list = []*Message{}
		}
		return M{"messages": list}, nil
	})

	routes["POST /api/messages"] = Route{func(c *Ctx) (any, error) {
		ch := c.b.Channel
		cc := ctxOf(ch)
		if !canSee(c.user, cc) {
			return nil, fail(403, "Kein Zugriff")
		}
		t := now()
		arr := []int64{}
		for _, x := range spam[c.user.ID] {
			if t-x < 5000 {
				arr = append(arr, x)
			}
		}
		if len(arr) >= 5 {
			return nil, fail(429, "Langsam! Du schreibst zu schnell.")
		}
		text := trunc(strings.TrimSpace(c.b.Text), 2000)
		if text == "" && c.b.Image == "" {
			return nil, fail(400, "Leere Nachricht")
		}
		if err := checkTextAt(c.user, text, "Nachricht", ch); err != nil {
			return nil, err
		}
		if !cc.isDM && c.b.Image == "" && botCommand(cc.server, ch, c.user, text) {
			return M{"command": true}, nil
		}
		if !cc.isDM {
			if err := botAutomod(cc.server, c.user, text); err != nil {
				return nil, err
			}
		}
		var img *string
		if c.b.Image != "" {
			p, err := saveImage(c.b.Image)
			if err != nil {
				return nil, err
			}
			img = &p
		}
		spam[c.user.ID] = append(arr, t)
		m := &Message{ID: newID(), Channel: ch, UserID: c.user.ID, Text: text, Image: img, Ts: t, Reactions: map[string][]string{}}
		if c.b.ReplyTo != "" && len(c.b.ReplyTo) < 40 {
			for _, x := range db.Messages[ch] {
				if x.ID == c.b.ReplyTo {
					m.ReplyTo = x.ID
				}
			}
		}
		list := append(db.Messages[ch], m)
		if len(list) > 2000 {
			list = list[len(list)-2000:]
		}
		db.Messages[ch] = list
		ev := M{"message": m}
		if cc.isDM {
			cc.dm.Last = t
			ev["dm"] = cc.dm
		}
		save()
		send(cc.audience, "message", ev)
		if !cc.isDM {
			botLevels(cc.server, ch, c.user)
		}
		return nil, nil
	}, true, 8 * 1024 * 1024}

	R("POST /api/messages/edit", func(c *Ctx) (any, error) {
		cc, i, err := findMsg(c.user, c.b.Channel, c.b.ID)
		if err != nil {
			return nil, err
		}
		m := db.Messages[c.b.Channel][i]
		if m.UserID != c.user.ID {
			return nil, fail(403, "Du kannst nur deine eigenen Nachrichten bearbeiten")
		}
		text := trunc(strings.TrimSpace(c.b.Text), 2000)
		if text == "" && m.Image == nil {
			return nil, fail(400, "Nachricht darf nicht leer sein")
		}
		if err := checkText(c.user, text, "bearbeitete Nachricht"); err != nil {
			return nil, err
		}
		t := now()
		m.Text, m.Edited = text, &t
		save()
		send(cc.audience, "message_update", m)
		return nil, nil
	})

	R("POST /api/messages/delete", func(c *Ctx) (any, error) {
		cc, i, err := findMsg(c.user, c.b.Channel, c.b.ID)
		if err != nil {
			return nil, err
		}
		list := db.Messages[c.b.Channel]
		m := list[i]
		ok := m.UserID == c.user.ID || isMod(c.user) || (!cc.isDM && hasPerm(c.user, cc.server, "manageMessages"))
		if !ok {
			return nil, fail(403, "Das darfst du nicht löschen")
		}
		db.Messages[c.b.Channel] = append(list[:i:i], list[i+1:]...)
		if m.Image != nil {
			os.Remove(filepath.Join(uploadDir, filepath.Base(*m.Image)))
		}
		save()
		send(cc.audience, "message_delete", M{"channel": c.b.Channel, "id": m.ID})
		return nil, nil
	})

	R("POST /api/react", func(c *Ctx) (any, error) {
		cc, i, err := findMsg(c.user, c.b.Channel, c.b.ID)
		if err != nil {
			return nil, err
		}
		if !contains(EMOJIS, c.b.Emoji) {
			return nil, fail(400, "Unbekanntes Emoji")
		}
		m := db.Messages[c.b.Channel][i]
		if m.Reactions == nil {
			m.Reactions = map[string][]string{}
		}
		arr := m.Reactions[c.b.Emoji]
		if contains(arr, c.user.ID) {
			arr = without(arr, c.user.ID)
		} else {
			arr = append(arr, c.user.ID)
		}
		if len(arr) == 0 {
			delete(m.Reactions, c.b.Emoji)
		} else {
			m.Reactions[c.b.Emoji] = arr
		}
		save()
		send(cc.audience, "message_update", m)
		return nil, nil
	})

	R("POST /api/typing", func(c *Ctx) (any, error) {
		cc := ctxOf(c.b.Channel)
		if !canSee(c.user, cc) {
			return nil, fail(403, "Kein Zugriff")
		}
		send(without(cc.audience, c.user.ID), "typing", M{"channel": c.b.Channel, "userId": c.user.ID})
		return nil, nil
	})

	R("POST /api/servers/create", func(c *Ctx) (any, error) {
		name := trunc(strings.TrimSpace(c.b.Name), 40)
		if runes(name) < 2 {
			return nil, fail(400, "Der Name ist zu kurz")
		}
		n := 0
		for _, s := range db.Servers {
			if s.OwnerID == c.user.ID {
				n++
			}
		}
		if !isMod(c.user) && n >= 10 {
			return nil, fail(400, "Du hast schon 10 Server")
		}
		if err := checkText(c.user, name, "Servername"); err != nil {
			return nil, err
		}
		return serverChanged(makeServer(name, c.user.ID, false))
	})
	R("POST /api/servers/join", func(c *Ctx) (any, error) {
		code := strings.ToUpper(strings.TrimSpace(c.b.Invite))
		for _, s := range db.Servers {
			if s.Invite == code {
				if contains(s.Bans, c.user.ID) {
					return nil, fail(403, "Du wurdest von diesem Server gebannt")
				}
				if !contains(s.Members, c.user.ID) {
					s.Members = append(s.Members, c.user.ID)
					out, _ := serverChanged(s)
					botWelcome(s, c.user)
					return out, nil
				}
				return serverChanged(s)
			}
		}
		return nil, fail(404, "Dieser Einladungscode ist ungültig")
	})
	R("POST /api/servers/add", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "")
		if err != nil {
			return nil, err
		}
		var t *User
		if c.b.UserID != "" {
			t = db.Users[c.b.UserID]
		} else {
			t = byName(strings.TrimSpace(c.b.Name))
		}
		if t == nil {
			return nil, fail(404, "Diesen Frostbyte-Benutzer gibt es nicht. Dein Freund muss sich zuerst bei Frostbyte registrieren (echte Discord-Namen gehen nicht).")
		}
		if t.Banned {
			return nil, fail(403, t.Name+" ist gebannt")
		}
		if contains(s.Members, t.ID) {
			return nil, fail(400, t.Name+" ist schon auf dem Server")
		}
		if contains(s.Bans, t.ID) {
			return nil, fail(403, t.Name+" ist von diesem Server gebannt (Server-Einstellungen → Bans)")
		}
		s.Members = append(s.Members, t.ID)
		send([]string{t.ID}, "server_added", M{"name": s.Name, "by": c.user.Name})
		out, _ := serverChanged(s)
		botWelcome(s, t)
		out.(M)["added"] = t.Name
		return out, nil
	})
	R("POST /api/servers/rename", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageServer")
		if err != nil {
			return nil, err
		}
		name := trunc(strings.TrimSpace(c.b.Name), 40)
		if runes(name) < 2 {
			return nil, fail(400, "Der Name ist zu kurz")
		}
		if err := checkText(c.user, name, "Servername"); err != nil {
			return nil, err
		}
		s.Name = name
		return serverChanged(s)
	})
	R("POST /api/servers/leave", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "")
		if err != nil {
			return nil, err
		}
		if s.ID == db.MainServer {
			return nil, fail(400, "Den Hauptserver kannst du nicht verlassen")
		}
		if s.OwnerID == c.user.ID {
			return nil, fail(400, "Du bist der Besitzer – lösche den Server stattdessen")
		}
		removeMember(s, c.user.ID)
		serverChanged(s)
		botLeave(s, c.user)
		send([]string{c.user.ID}, "server_removed", M{"serverId": s.ID})
		return nil, nil
	})
	R("POST /api/servers/delete", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "")
		if err != nil {
			return nil, err
		}
		if s.ID == db.MainServer {
			return nil, fail(400, "Der Hauptserver kann nicht gelöscht werden")
		}
		if s.OwnerID != c.user.ID && c.user.Role != "owner" {
			return nil, fail(403, "Nur der Besitzer darf den Server löschen")
		}
		for _, ch := range s.Channels {
			delete(db.Messages, ch.ID)
			vcCloseChannel(ch.ID)
		}
		delete(db.Servers, s.ID)
		save()
		send(s.Members, "server_removed", M{"serverId": s.ID})
		return nil, nil
	})
	R("POST /api/servers/kick", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "kick")
		if err != nil {
			return nil, err
		}
		if s.ID == db.MainServer {
			return nil, fail(400, "Im Hauptserver bitte \"Bannen\" benutzen")
		}
		t := db.Users[c.b.UserID]
		if t == nil || !contains(s.Members, t.ID) {
			return nil, fail(404, "Nicht auf dem Server")
		}
		if t.ID == c.user.ID || !outranks(c.user, t, s) {
			return nil, fail(403, "Diese Person kannst du nicht kicken")
		}
		removeMember(s, t.ID)
		out, _ := serverChanged(s)
		botLeave(s, t)
		send([]string{t.ID}, "server_removed", M{"serverId": s.ID})
		return out, nil
	})
	R("POST /api/channels/create", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageChannels")
		if err != nil {
			return nil, err
		}
		typ := ""
		name := chanName(c.b.Name)
		if c.b.Role == "voice" {
			typ = "voice"
			name = trunc(strings.TrimSpace(c.b.Name), 30)
		}
		if name == "" {
			return nil, fail(400, "Ungültiger Kanalname")
		}
		if err := checkText(c.user, strings.ReplaceAll(name, "-", " "), "Kanalname"); err != nil {
			return nil, err
		}
		if len(s.Channels) >= 50 {
			return nil, fail(400, "Maximal 50 Kanäle")
		}
		s.Channels = append(s.Channels, Channel{newID(), name, typ})
		return serverChanged(s)
	})
	R("POST /api/channels/delete", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageChannels")
		if err != nil {
			return nil, err
		}
		texts := 0
		for _, ch := range s.Channels {
			if ch.Type != "voice" && ch.ID != c.b.ChannelID {
				texts++
			}
		}
		if texts == 0 {
			return nil, fail(400, "Ein Textkanal muss bleiben")
		}
		keep := []Channel{}
		found := false
		for _, ch := range s.Channels {
			if ch.ID == c.b.ChannelID {
				found = true
			} else {
				keep = append(keep, ch)
			}
		}
		if !found {
			return nil, fail(404, "Kanal nicht gefunden")
		}
		s.Channels = keep
		delete(db.Messages, c.b.ChannelID)
		vcCloseChannel(c.b.ChannelID)
		return serverChanged(s)
	})
	R("POST /api/channels/rename", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageChannels")
		if err != nil {
			return nil, err
		}
		for i, ch := range s.Channels {
			if ch.ID == c.b.ChannelID {
				name := chanName(c.b.Name)
				if ch.Type == "voice" {
					name = trunc(strings.TrimSpace(c.b.Name), 30)
				}
				if name == "" {
					return nil, fail(400, "Ungültiger Kanalname")
				}
				if err := checkText(c.user, strings.ReplaceAll(name, "-", " "), "Kanalname"); err != nil {
					return nil, err
				}
				s.Channels[i].Name = name
				return serverChanged(s)
			}
		}
		return nil, fail(404, "Kanal nicht gefunden")
	})

	R("POST /api/dm/open", func(c *Ctx) (any, error) {
		t := db.Users[c.b.UserID]
		if t == nil || t.ID == c.user.ID {
			return nil, fail(404, "Benutzer nicht gefunden")
		}
		users := []string{c.user.ID, t.ID}
		sort.Strings(users)
		id := "dm_" + strings.Join(users, "_")
		if db.Dms[id] == nil {
			db.Dms[id] = &DM{id, users, now()}
			save()
		}
		return M{"dm": db.Dms[id]}, nil
	})

	R("POST /api/profile", func(c *Ctx) (any, error) {
		if c.b.Color != nil {
			if !colorRe.MatchString(*c.b.Color) {
				return nil, fail(400, "Ungültige Farbe")
			}
			c.user.Color = *c.b.Color
		}
		if c.b.About != nil {
			if err := checkText(c.user, *c.b.About, "Über mich"); err != nil {
				return nil, err
			}
			c.user.About = trunc(*c.b.About, 190)
		}
		save()
		sendAll("user_update", pubUser(c.user))
		return M{"user": pubUser(c.user)}, nil
	})
	R("POST /api/profile/avatar", func(c *Ctx) (any, error) {
		if c.b.Image == "" {
			c.user.Avatar = ""
		} else {
			p, err := saveImage(c.b.Image)
			if err != nil {
				return nil, err
			}
			c.user.Avatar = p
		}
		save()
		sendAll("user_update", pubUser(c.user))
		return M{"user": pubUser(c.user)}, nil
	})
	R("POST /api/password", func(c *Ctx) (any, error) {
		if !checkPw(c.b.Old, c.user.Pass) {
			return nil, fail(400, "Das alte Passwort ist falsch")
		}
		if runes(c.b.Password) < 6 || runes(c.b.Password) > 200 {
			return nil, fail(400, "Das neue Passwort braucht mindestens 6 Zeichen")
		}
		c.user.Pass = hashPw(c.b.Password)
		kickSessions(c.user.ID, c.token, "logout")
		return nil, nil
	})

	R("POST /api/admin/ban", func(c *Ctx) (any, error) {
		if !isMod(c.user) {
			return nil, fail(403, "Nur Admins")
		}
		t, err := target(c.user, c.b.UserID)
		if err != nil {
			return nil, err
		}
		if c.user.Role != "owner" && t.Role == "admin" {
			return nil, fail(403, "Admins können keine Admins bannen")
		}
		t.Banned = c.b.Banned
		save()
		if t.Banned {
			kickSessions(t.ID, "", "banned")
			vcKick(t.ID, "")
		}
		sendAll("user_update", pubUser(t))
		return M{"user": pubUser(t)}, nil
	})
	R("POST /api/report", func(c *Ctx) (any, error) {
		cc, i, err := findMsg(c.user, c.b.Channel, c.b.ID)
		if err != nil {
			return nil, err
		}
		m := db.Messages[c.b.Channel][i]
		if m.UserID == c.user.ID {
			return nil, fail(400, "Du kannst dich nicht selbst melden")
		}
		_ = cc
		modLog(&ModEntry{Kind: "report", UserID: m.UserID, Where: whereOf(c.b.Channel), Text: m.Text, Reporter: c.user.ID})
		au := db.Users[m.UserID]
		name := "?"
		if au != nil {
			name = au.Name
		}
		send(modIDs(""), "reported", M{"name": name, "by": c.user.Name})
		return nil, nil
	})
	R("GET /api/admin/log", func(c *Ctx) (any, error) {
		if !isMod(c.user) {
			return nil, fail(403, "Nur Admins")
		}
		out := []*ModEntry{}
		for i := len(db.ModLog) - 1; i >= 0 && len(out) < 300; i-- {
			out = append(out, db.ModLog[i])
		}
		return M{"log": out}, nil
	})
	R("GET /api/admin/usermsgs", func(c *Ctx) (any, error) {
		if !isMod(c.user) {
			return nil, fail(403, "Nur Admins")
		}
		uid := c.r.URL.Query().Get("userId")
		type row struct {
			Ts    int64   `json:"ts"`
			Where string  `json:"where"`
			Text  string  `json:"text"`
			Image *string `json:"image"`
		}
		rows := []row{}
		for _, s := range db.Servers {
			for _, ch := range s.Channels {
				for _, m := range db.Messages[ch.ID] {
					if m.UserID == uid {
						rows = append(rows, row{m.Ts, s.Name + " #" + ch.Name, m.Text, m.Image})
					}
				}
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Ts > rows[j].Ts })
		if len(rows) > 300 {
			rows = rows[:300]
		}
		log := []*ModEntry{}
		for i := len(db.ModLog) - 1; i >= 0; i-- {
			if db.ModLog[i].UserID == uid {
				log = append(log, db.ModLog[i])
			}
		}
		return M{"messages": rows, "log": log}, nil
	})
	R("POST /api/admin/shutdown", func(c *Ctx) (any, error) {
		if c.user.Role != "owner" {
			return nil, fail(403, "Nur der Owner")
		}
		go func() {
			time.Sleep(400 * time.Millisecond)
			stopTunnel()
			writeDB()
			os.Exit(0)
		}()
		return nil, nil
	})
	R("POST /api/admin/words", func(c *Ctx) (any, error) {
		if !isMod(c.user) {
			return nil, fail(403, "Nur Admins")
		}
		w := strings.ToLower(strings.TrimSpace(c.b.Name))
		if c.b.Role == "remove" {
			db.BadWords = without(db.BadWords, w)
		} else {
			if runes(w) < 2 || runes(w) > 40 || normWord(w) == "" {
				return nil, fail(400, "Ungültiges Wort")
			}
			if !contains(db.BadWords, w) {
				db.BadWords = append(db.BadWords, w)
			}
		}
		save()
		return M{"badWords": db.BadWords}, nil
	})
	R("POST /api/call/signal", func(c *Ctx) (any, error) {
		var d struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(c.b.Data, &d) != nil || d.Type == "" {
			return nil, fail(400, "Ungültig")
		}
		t := db.Users[c.b.UserID]
		if t == nil || t.ID == c.user.ID || t.Banned {
			return nil, fail(404, "Benutzer nicht gefunden")
		}
		allowed := contains(db.Friends[c.user.ID], t.ID) || db.Dms["dm_"+strings.Join(sortedPair(c.user.ID, t.ID), "_")] != nil
		if !allowed {
			for _, s := range db.Servers {
				if contains(s.Members, c.user.ID) && contains(s.Members, t.ID) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return nil, fail(403, "Du kannst nur Freunde anrufen")
		}
		if d.Type == "invite" && !isOnline(t.ID) {
			return nil, fail(400, t.Name+" ist gerade offline 💤")
		}
		switch d.Type {
		case "invite": // Ton-Raum fuer Anrufer + Angerufenen freigeben
			if d.ID != "" && len(d.ID) < 64 {
				voiceAllow(d.ID, c.user.ID, t.ID)
			}
		case "hangup", "decline", "busy":
			voiceEnd(d.ID)
		}
		send([]string{t.ID}, "call", M{"from": c.user.ID, "data": c.b.Data})
		return nil, nil
	})
	R("POST /api/admin/role", func(c *Ctx) (any, error) {
		if c.user.Role != "owner" {
			return nil, fail(403, "Nur der Owner")
		}
		t, err := target(c.user, c.b.UserID)
		if err != nil {
			return nil, err
		}
		if c.b.Role != "admin" && c.b.Role != "user" {
			return nil, fail(400, "Ungültige Rolle")
		}
		t.Role = c.b.Role
		save()
		sendAll("user_update", pubUser(t))
		return M{"user": pubUser(t)}, nil
	})
	R("POST /api/admin/resetpw", func(c *Ctx) (any, error) {
		if c.user.Role != "owner" {
			return nil, fail(403, "Nur der Owner")
		}
		t, err := target(c.user, c.b.UserID)
		if err != nil {
			return nil, err
		}
		if runes(c.b.Password) < 6 || runes(c.b.Password) > 200 {
			return nil, fail(400, "Mindestens 6 Zeichen")
		}
		t.Pass = hashPw(c.b.Password)
		kickSessions(t.ID, "", "logout")
		return M{"user": pubUser(t)}, nil
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(b)
}

func sse(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	mu.Lock()
	u, t := authUser(r)
	if u == nil || !ok {
		mu.Unlock()
		w.WriteHeader(401)
		return
	}
	c := &Client{userID: u.ID, token: t, ch: make(chan []byte, 512), done: make(chan struct{})}
	was := isOnline(u.ID)
	clients[c] = true
	if !was {
		sendAll("presence", M{"userId": u.ID, "online": true})
	}
	mu.Unlock()
	defer func() {
		mu.Lock()
		if clients[c] {
			delete(clients, c)
			if !isOnline(c.userID) {
				sendAll("presence", M{"userId": c.userID, "online": false})
			}
		}
		mu.Unlock()
	}()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	io.WriteString(w, "retry: 3000\n\n")
	fl.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c.ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
			fl.Flush()
		case <-c.done:
			for {
				select {
				case msg := <-c.ch:
					w.Write(msg)
				default:
					fl.Flush()
					return
				}
			}
		case <-tick.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

var mime = map[string]string{"png": "image/png", "jpg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}

func handler(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if r.Method == "GET" && (p == "/" || p == "/index.html") {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'")
		w.Write(page)
		return
	}
	if r.Method == "GET" && (p == "/icon-192.png" || p == "/icon-512.png" || p == "/apple-touch-icon.png") {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		if p == "/icon-512.png" {
			w.Write(icon512)
		} else {
			w.Write(icon192)
		}
		return
	}
	if r.Method == "GET" && p == "/manifest.json" {
		w.Header().Set("Content-Type", "application/manifest+json")
		json.NewEncoder(w).Encode(M{"name": AppName, "short_name": AppName, "start_url": "/", "display": "standalone",
			"background_color": "#0b1322", "theme_color": "#0b1322", "lang": "de",
			"icons": []M{{"src": "/icon-192.png", "sizes": "192x192", "type": "image/png"}, {"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"}}})
		return
	}
	if r.Method == "GET" && (p == "/logo.svg" || p == "/favicon.ico") {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(logoSVG)
		return
	}
	if r.Method == "GET" && strings.HasPrefix(p, "/uploads/") {
		n := p[9:]
		if !uploadRe.MatchString(n) {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(uploadDir, n))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime[n[strings.LastIndex(n, ".")+1:]])
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(data)
		return
	}
	if r.Method == "GET" && p == "/api/vc" {
		vcWS(w, r)
		return
	}
	if r.Method == "GET" && p == "/api/voice" {
		voiceWS(w, r)
		return
	}
	if r.Method == "GET" && p == "/api/events" {
		sse(w, r)
		return
	}
	rt, ok := routes[r.Method+" "+p]
	if !ok {
		http.Error(w, "Nicht gefunden", 404)
		return
	}
	var body Body
	if r.Method == "POST" {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, rt.limit))
		if err != nil {
			writeJSON(w, 413, M{"error": "Zu groß!"})
			return
		}
		if len(data) > 0 && json.Unmarshal(data, &body) != nil {
			writeJSON(w, 400, M{"error": "Ungültige Daten"})
			return
		}
	}
	mu.Lock()
	ctx := &Ctx{r: r, b: body}
	if rt.auth {
		ctx.user, ctx.token = authUser(r)
		if ctx.user == nil {
			mu.Unlock()
			writeJSON(w, 401, M{"error": "Bitte neu einloggen"})
			return
		}
	}
	out, err := rt.fn(ctx)
	var b []byte
	status := 200
	if err != nil {
		var he *HErr
		if errors.As(err, &he) {
			status, out = he.Status, M{"error": he.Msg}
		} else {
			status, out = 500, M{"error": "Serverfehler"}
		}
	} else if out == nil {
		out = M{"ok": true}
	}
	b, _ = json.Marshal(out)
	mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(b)
}

// ---------------- App-Fenster oeffnen ----------------
func openApp(url string) {
	if os.Getenv("HEDISCORD_NO_OPEN") != "" {
		return
	}
	switch runtime.GOOS {
	case "windows":
		cands := []string{}
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			if base := os.Getenv(env); base != "" {
				cands = append(cands,
					filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
					filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
		for _, c := range cands {
			if _, err := os.Stat(c); err == nil {
				if exec.Command(c, "--app="+url, "--start-maximized").Start() == nil {
					return
				}
			}
		}
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

var listenPort = DefaultPort

// Fester Ort fuer die Daten - egal wo die .exe liegt (nicht im Temp-Ordner!)
func stableDataDir() string {
	if runtime.GOOS == "windows" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, AppName)
		}
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, AppName)
	}
	return filepath.Join(".", AppName+" Daten")
}

// Fragt den Server auf dem Port, was dort laeuft.
// Ergebnis: "same" = diese Version, "ours" = alte Frostbyte/He-Discord-Version, "" = etwas anderes
func whoRuns(local string) string {
	cl := http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(local + "/api/info")
	if err != nil {
		return ""
	}
	var v struct {
		App     string `json:"app"`
		Version string `json:"version"`
	}
	ok := resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&v) == nil
	resp.Body.Close()
	if ok && v.App == AppName && v.Version == Version {
		return "same"
	}
	if ok && (v.App == AppName || v.App == OldAppName) {
		return "ours"
	}
	// ganz alte Version ohne /api/info: am Seitentitel erkennen
	if r2, err := cl.Get(local + "/"); err == nil {
		b, _ := io.ReadAll(io.LimitReader(r2.Body, 4096))
		r2.Body.Close()
		if strings.Contains(string(b), "<title>"+OldAppName) || strings.Contains(string(b), "<title>"+AppName) {
			return "ours"
		}
	}
	return ""
}

// Beendet das Programm, das auf dem Port lauscht (nur Windows).
func killPortOwner(port string) {
	out, err := hideCmd(exec.Command("netstat", "-ano", "-p", "tcp")).Output()
	if err != nil {
		return
	}
	self := fmt.Sprint(os.Getpid())
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 && strings.HasSuffix(f[1], ":"+port) && strings.HasSuffix(f[2], ":0") && f[4] != self && f[4] != "0" {
			hideCmd(exec.Command("taskkill", "/F", "/PID", f[4])).Run()
		}
	}
}

func waitEnter() {
	fmt.Println("\n  Druecke Enter zum Schliessen ...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

func lanIPs() []string {
	out := []string{}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = DefaultPort
	}
	local := "http://localhost:" + port

	exe, err := os.Executable()
	base := "."
	if err == nil {
		base = filepath.Dir(exe)
	}
	if d := os.Getenv("HEDISCORD_DATA"); d != "" {
		dataDir = d
	} else {
		dataDir = stableDataDir()
	}
	oldDirs := []string{filepath.Join(base, AppName+" Daten"), filepath.Join(base, OldAppName+" Daten")}
	uploadDir = filepath.Join(dataDir, "uploads")
	dataFile = filepath.Join(dataDir, "data.json")

	fmt.Println()
	fmt.Println("  ==========================================")
	fmt.Println("        *  F R O S T B Y T E  *")
	fmt.Println("  ==========================================")

	// Port holen. Laeuft Frostbyte schon -> nur Fenster oeffnen.
	// Laeuft noch das alte "He Discord" -> beenden und selbst starten.
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		who := whoRuns(local)
		if who == "same" {
			fmt.Println("\n  Frostbyte laeuft schon - ich oeffne das Fenster.")
			openApp(local)
			time.Sleep(2 * time.Second)
			return
		}
		if who == "ours" && runtime.GOOS == "windows" {
			fmt.Println("\n  Eine alte Version laeuft noch - ich beende sie ...")
			killPortOwner(port)
			for i := 0; i < 20 && err != nil; i++ {
				time.Sleep(250 * time.Millisecond)
				ln, err = net.Listen("tcp", ":"+port)
			}
		}
		// Notfalls einen anderen Port nehmen
		for p := 3001; p <= 3020 && err != nil; p++ {
			if ln, err = net.Listen("tcp", fmt.Sprintf(":%d", p)); err == nil {
				port = fmt.Sprint(p)
				local = "http://localhost:" + port
			}
		}
		if err != nil {
			msgBox(AppName, "Frostbyte kann nicht starten: kein freier Port.\n\nStarte den PC neu und versuch es nochmal.\n\n("+err.Error()+")")
			return
		}
	}

	// Alte Daten (neben der .exe) an den festen Ort holen
	if _, e := os.Stat(filepath.Join(dataDir, "data.json")); os.IsNotExist(e) {
		for _, oldDir := range oldDirs {
			if _, e := os.Stat(filepath.Join(oldDir, "data.json")); e == nil && oldDir != dataDir {
				os.MkdirAll(filepath.Dir(dataDir), 0755)
				os.RemoveAll(dataDir)
				for i := 0; i < 10; i++ {
					if os.Rename(oldDir, dataDir) == nil {
						fmt.Println("\n  Alte Daten wurden uebernommen.")
						break
					}
					time.Sleep(300 * time.Millisecond)
				}
				break
			}
		}
	}

	if err := os.MkdirAll(uploadDir, 0755); err == nil && runtime.GOOS == "windows" {
		if f, e := os.Create(filepath.Join(dataDir, "frostbyte-log.txt")); e == nil {
			os.Stdout = f // kein schwarzes Fenster: Ausgaben in eine Log-Datei
		}
	}
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		msgBox(AppName, "Frostbyte kann den Daten-Ordner nicht anlegen:\n"+err.Error())
		return
	}
	db = DB{Users: map[string]*User{}, Sessions: map[string]*Session{}, Servers: map[string]*Server{}, Messages: map[string][]*Message{}, Dms: map[string]*DM{}}
	if data, err := os.ReadFile(dataFile); err == nil {
		if err := json.Unmarshal(data, &db); err != nil {
			msgBox(AppName, "Die Datei data.json ist kaputt:\n"+err.Error()+"\n\nOrdner: "+dataDir)
			return
		}
		if db.Users == nil {
			db.Users = map[string]*User{}
		}
		if db.Sessions == nil {
			db.Sessions = map[string]*Session{}
		}
		if db.Servers == nil {
			db.Servers = map[string]*Server{}
		}
		if db.Messages == nil {
			db.Messages = map[string][]*Message{}
		}
		if db.Dms == nil {
			db.Dms = map[string]*DM{}
		}
	}
	if db.Friends == nil {
		db.Friends = map[string][]string{}
	}
	if db.Requests == nil {
		db.Requests = []*FriendReq{}
	}
	if db.Giveaways == nil {
		db.Giveaways = map[string]*Giveaway{}
	}
	for id, g := range db.Giveaways { // alte, beendete Giveaways nach 30 Tagen wegraeumen
		if g.Ended && now()-g.EndsAt > 30*24*3600*1000 {
			delete(db.Giveaways, id)
		}
	}

	// Owner + Hauptserver
	createdOwner := false
	var owner *User
	for _, u := range db.Users {
		if u.Role == "owner" {
			owner = u
		}
	}
	if owner == nil {
		if owner = byName(OwnerName); owner != nil {
			owner.Role = "owner"
		} else {
			owner = createUser(OwnerName, OwnerPassword, "owner")
			createdOwner = true
		}
	}
	// Kein automatischer Server mehr: den alten Auto-Server entfernen
	if old := db.Servers[db.MainServer]; old != nil {
		for _, ch := range old.Channels {
			delete(db.Messages, ch.ID)
		}
		delete(db.Servers, old.ID)
	}
	db.MainServer = ""
	if n := cleanOldMessages(); n > 0 {
		fmt.Printf("\n  Wortfilter: %d alte Nachricht(en) mit verbotenen Woertern entfernt.\n", n)
	}
	writeDB()
	go saver()

	page = []byte(strings.ReplaceAll(indexHTML, "__APP_NAME__", html.EscapeString(AppName)))
	setupRoutes()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; stopTunnel(); writeDB(); os.Exit(0) }()

	listenPort = port
	srv := &http.Server{Handler: http.HandlerFunc(handler), ReadHeaderTimeout: 15 * time.Second}

	fmt.Println("\n  Frostbyte laeuft!")
	fmt.Println()
	fmt.Println("  Auf diesem PC:      " + local)
	for _, ip := range lanIPs() {
		fmt.Println("  Freunde im WLAN:    http://" + ip + ":" + port)
	}
	fmt.Println()
	if createdOwner {
		fmt.Println("  Owner-Konto:  Name: " + OwnerName + "   Passwort: " + OwnerPassword)
	} else {
		fmt.Println("  Owner-Konto:  Name: " + owner.Name)
	}
	fmt.Println("  Daten-Ordner: " + dataDir)
	fmt.Println()
	fmt.Println("  >> Dieses Fenster offen lassen! Schliessen = Frostbyte aus. <<")
	fmt.Println()

	if os.Getenv("FROSTBYTE_UPDATED") == "" {
		go func() { time.Sleep(300 * time.Millisecond); openApp(local) }()
	}
	go updateLoop(srv)
	go giveawayLoop()
	go startTunnel(port)
	if err := srv.Serve(ln); err != nil {
		msgBox(AppName, "Fehler: "+err.Error())
	}
}
