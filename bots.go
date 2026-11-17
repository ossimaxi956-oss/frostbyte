package main

// Eingebaute Bots: Giveaway-Bot und Galaxy-Bot (pro Server einstellbar)

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type BotCfg struct {
	Giveaway   bool   `json:"giveaway"`
	Galaxy     bool   `json:"galaxy"`
	WelcomeOn  bool   `json:"welcomeOn"`
	WelcomeCh  string `json:"welcomeCh"`
	WelcomeMsg string `json:"welcomeMsg"`
	LeaveOn    bool   `json:"leaveOn"`
	LeaveMsg   string `json:"leaveMsg"`
	LevelsOn   bool   `json:"levelsOn"`
	LevelCh    string `json:"levelCh"`
	AntiLink   bool   `json:"antiLink"`
	AntiCaps   bool   `json:"antiCaps"`
	AntiSpam   bool   `json:"antiSpam"`
}

type Giveaway struct {
	ID        string   `json:"id"`
	ServerID  string   `json:"serverId"`
	Channel   string   `json:"channel"`
	MsgID     string   `json:"msgId"`
	Prize     string   `json:"prize"`
	Winners   int      `json:"winners"`
	EndsAt    int64    `json:"endsAt"`
	HostID    string   `json:"hostId"`
	Entrants  []string `json:"entrants"`
	Ended     bool     `json:"ended"`
	WinnerIDs []string `json:"winnerIds"`
}

func botsOf(s *Server) *BotCfg {
	if s.Bots == nil {
		return &BotCfg{}
	}
	return s.Bots
}

func firstText(s *Server) string {
	for _, c := range s.Channels {
		if c.Type != "voice" {
			return c.ID
		}
	}
	return ""
}

func hasText(s *Server, ch string) bool {
	for _, c := range s.Channels {
		if c.ID == ch && c.Type != "voice" {
			return true
		}
	}
	return false
}

// Bot schreibt eine Nachricht (mu muss gehalten werden)
func botPost(s *Server, ch, bot, text, gw string) *Message {
	if !hasText(s, ch) {
		ch = firstText(s)
	}
	if ch == "" {
		return nil
	}
	m := &Message{ID: newID(), Channel: ch, UserID: "bot:" + bot, Text: trunc(text, 2000), Ts: now(), Reactions: map[string][]string{}, Giveaway: gw}
	list := append(db.Messages[ch], m)
	if len(list) > 2000 {
		list = list[len(list)-2000:]
	}
	db.Messages[ch] = list
	save()
	send(s.Members, "message", M{"message": m})
	return m
}

func fillVars(t string, u *User, s *Server) string {
	t = strings.ReplaceAll(t, "{user}", "@"+u.Name)
	t = strings.ReplaceAll(t, "{name}", u.Name)
	t = strings.ReplaceAll(t, "{server}", s.Name)
	t = strings.ReplaceAll(t, "{count}", strconv.Itoa(len(s.Members)))
	return t
}

func botWelcome(s *Server, u *User) {
	b := botsOf(s)
	if !b.Galaxy || !b.WelcomeOn {
		return
	}
	msg := b.WelcomeMsg
	if strings.TrimSpace(msg) == "" {
		msg = "👋 Willkommen auf **{server}**, {user}! Du bist Mitglied Nr. {count} ❄️"
	}
	botPost(s, b.WelcomeCh, "galaxy", fillVars(msg, u, s), "")
}

func botLeave(s *Server, u *User) {
	b := botsOf(s)
	if !b.Galaxy || !b.LeaveOn {
		return
	}
	msg := b.LeaveMsg
	if strings.TrimSpace(msg) == "" {
		msg = "😢 {name} hat **{server}** verlassen."
	}
	botPost(s, b.WelcomeCh, "galaxy", fillVars(msg, u, s), "")
}

// ---- Level-System ----
var xpCooldown = map[string]int64{}

func levelOf(xp int) int  { return int(math.Floor(math.Sqrt(float64(xp) / 50))) }
func xpFor(level int) int { return level * level * 50 }

func botLevels(s *Server, ch string, u *User) {
	b := botsOf(s)
	if !b.Galaxy || !b.LevelsOn {
		return
	}
	k := s.ID + u.ID
	if now()-xpCooldown[k] < 15000 {
		return
	}
	xpCooldown[k] = now()
	if s.XP == nil {
		s.XP = map[string]int{}
	}
	before := levelOf(s.XP[u.ID])
	s.XP[u.ID] += 10 + randInt(11)
	after := levelOf(s.XP[u.ID])
	if after > before {
		target := ch
		if b.LevelCh != "" {
			target = b.LevelCh
		}
		botPost(s, target, "galaxy", fmt.Sprintf("🎉 GG @%s – du bist jetzt **Level %d**! ⭐", u.Name, after), "")
	}
}

// ---- Auto-Mod ----
var lastMsgs = map[string][]string{}
var lastMsgTs = map[string][]int64{}

func botAutomod(s *Server, u *User, text string) error {
	b := botsOf(s)
	if !b.Galaxy || canManage(u, s) {
		return nil
	}
	low := strings.ToLower(text)
	if b.AntiLink && (strings.Contains(low, "http://") || strings.Contains(low, "https://") || strings.Contains(low, "www.") || strings.Contains(low, "discord.gg")) {
		return fail(400, "🔗 Links sind auf diesem Server nicht erlaubt (Galaxy-Bot)")
	}
	if b.AntiCaps {
		letters, upper := 0, 0
		for _, r := range text {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters >= 10 && upper*100/letters > 70 {
			return fail(400, "🔠 Bitte nicht so viel GROSS schreiben (Galaxy-Bot)")
		}
	}
	if b.AntiSpam {
		k := s.ID + u.ID
		t := now()
		keepT, keepM := []int64{}, []string{}
		for i, x := range lastMsgTs[k] {
			if t-x < 30000 {
				keepT = append(keepT, x)
				keepM = append(keepM, lastMsgs[k][i])
			}
		}
		same := 0
		for _, m := range keepM {
			if m == low {
				same++
			}
		}
		if same >= 2 {
			return fail(400, "🚫 Nicht spammen! Gleiche Nachricht zu oft (Galaxy-Bot)")
		}
		lastMsgTs[k] = append(keepT, t)
		lastMsgs[k] = append(keepM, low)
	}
	return nil
}

// ---- Befehle (/rank, /top ...) ----
var eightBall = []string{"Ja, auf jeden Fall! ✅", "Nein. ❌", "Vielleicht … 🤔", "Frag später nochmal 🔮", "Sieht gut aus! 🌟", "Eher nicht 😬",
	"Ganz sicher! 💯", "Darauf würde ich nicht wetten 🙅", "Die Sterne sagen ja ✨", "Konzentrier dich und frag nochmal 🧘"}

// Gibt true zurueck, wenn der Text ein Befehl war
func botCommand(s *Server, ch string, u *User, text string) bool {
	b := botsOf(s)
	if !b.Galaxy || !strings.HasPrefix(text, "/") {
		return false
	}
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])
	arg := strings.TrimSpace(strings.TrimPrefix(text, parts[0]))
	reply := ""
	switch cmd {
	case "/hilfe", "/help":
		reply = "🌌 **Galaxy-Bot – Befehle**\n/rank – dein Level\n/top – Rangliste\n/münze – Kopf oder Zahl\n/würfel [zahl] – würfeln\n/8ball [frage] – die magische Kugel\n/server – Infos zum Server"
		if b.Giveaway {
			reply += "\n/giveaway – Gewinnspiel starten (Admins)"
		}
	case "/rank", "/level":
		xp := 0
		if s.XP != nil {
			xp = s.XP[u.ID]
		}
		lv := levelOf(xp)
		pos := 1
		for _, v := range s.XP {
			if v > xp {
				pos++
			}
		}
		reply = fmt.Sprintf("⭐ @%s – **Level %d** · %d XP · Platz #%d\nNoch %d XP bis Level %d", u.Name, lv, xp, pos, xpFor(lv+1)-xp, lv+1)
		if !b.LevelsOn {
			reply += "\n(Das Level-System ist auf diesem Server aus.)"
		}
	case "/top", "/rangliste", "/leaderboard":
		type e struct {
			id string
			xp int
		}
		list := []e{}
		for id, v := range s.XP {
			list = append(list, e{id, v})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].xp > list[j].xp })
		reply = "🏆 **Rangliste von " + s.Name + "**"
		medals := []string{"🥇", "🥈", "🥉"}
		for i, x := range list {
			if i >= 10 {
				break
			}
			nm := "?"
			if uu := db.Users[x.id]; uu != nil {
				nm = uu.Name
			}
			m := fmt.Sprintf("%d.", i+1)
			if i < 3 {
				m = medals[i]
			}
			reply += fmt.Sprintf("\n%s %s – Level %d (%d XP)", m, nm, levelOf(x.xp), x.xp)
		}
		if len(list) == 0 {
			reply += "\nNoch keiner hat XP. Schreibt was! ✍️"
		}
	case "/münze", "/muenze", "/coin":
		r := "**Kopf** 🪙"
		if randInt(2) == 1 {
			r = "**Zahl** 🪙"
		}
		reply = "@" + u.Name + " wirft eine Münze … " + r
	case "/würfel", "/wuerfel", "/dice":
		n := 6
		if v, err := strconv.Atoi(arg); err == nil && v >= 2 && v <= 1000 {
			n = v
		}
		reply = fmt.Sprintf("🎲 @%s würfelt (1–%d) … **%d**!", u.Name, n, randInt(n)+1)
	case "/8ball":
		if arg == "" {
			reply = "🎱 Stell eine Frage, z. B. `/8ball Gewinne ich heute?`"
		} else {
			reply = "🎱 „" + trunc(arg, 200) + "“\n→ " + eightBall[randInt(len(eightBall))]
		}
	case "/server":
		online := 0
		for _, id := range s.Members {
			if isOnline(id) {
				online++
			}
		}
		owner := "?"
		if o := db.Users[s.OwnerID]; o != nil {
			owner = o.Name
		}
		reply = fmt.Sprintf("🌌 **%s**\n👑 Besitzer: %s\n👥 Mitglieder: %d (%d online)\n💬 Kanäle: %d", s.Name, owner, len(s.Members), online, len(s.Channels))
	default:
		return false
	}
	botPost(s, ch, "galaxy", reply, "")
	return true
}

// ---- Giveaways ----
func pickWinners(g *Giveaway) {
	pool := append([]string{}, g.Entrants...)
	g.WinnerIDs = []string{}
	for i := 0; i < g.Winners && len(pool) > 0; i++ {
		k := randInt(len(pool))
		g.WinnerIDs = append(g.WinnerIDs, pool[k])
		pool = append(pool[:k], pool[k+1:]...)
	}
}

func announceGW(g *Giveaway, reroll bool) {
	s := db.Servers[g.ServerID]
	if s == nil {
		return
	}
	if len(g.WinnerIDs) == 0 {
		botPost(s, g.Channel, "giveaway", "😢 Das Giveaway **"+g.Prize+"** ist vorbei – leider hat niemand teilgenommen.", "")
		return
	}
	names := []string{}
	for _, id := range g.WinnerIDs {
		if u := db.Users[id]; u != nil {
			names = append(names, "@"+u.Name)
		}
	}
	head := "🎉 Herzlichen Glückwunsch "
	if reroll {
		head = "🔄 Neu ausgelost! Glückwunsch "
	}
	botPost(s, g.Channel, "giveaway", head+strings.Join(names, ", ")+"! Ihr habt **"+g.Prize+"** gewonnen! 🏆", "")
}

func endGW(g *Giveaway) {
	g.Ended = true
	g.EndsAt = now()
	pickWinners(g)
	if s := db.Servers[g.ServerID]; s != nil {
		send(s.Members, "giveaway", g)
	}
	announceGW(g, false)
	save()
}

func giveawayLoop() {
	for {
		time.Sleep(time.Second)
		mu.Lock()
		for _, g := range db.Giveaways {
			if !g.Ended && now() >= g.EndsAt {
				endGW(g)
			}
		}
		mu.Unlock()
	}
}

func gwsFor(uid string) []*Giveaway {
	out := []*Giveaway{}
	for _, g := range db.Giveaways {
		if s := db.Servers[g.ServerID]; s != nil && contains(s.Members, uid) {
			out = append(out, g)
		}
	}
	return out
}

func getGW(c *Ctx, manage bool) (*Giveaway, *Server, error) {
	g := db.Giveaways[c.b.ID]
	if g == nil {
		return nil, nil, fail(404, "Giveaway nicht gefunden")
	}
	s, err := getServer(c.user, g.ServerID, manage)
	if err != nil {
		return nil, nil, err
	}
	return g, s, nil
}

func setupBotRoutes() {
	R("POST /api/bots/config", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, true)
		if err != nil {
			return nil, err
		}
		var cfg BotCfg
		if json.Unmarshal(c.b.Cfg, &cfg) != nil {
			return nil, fail(400, "Ungültige Einstellungen")
		}
		cfg.WelcomeMsg = trunc(strings.TrimSpace(cfg.WelcomeMsg), 300)
		cfg.LeaveMsg = trunc(strings.TrimSpace(cfg.LeaveMsg), 300)
		if err := checkText(c.user, cfg.WelcomeMsg+" "+cfg.LeaveMsg, "Bot-Nachricht"); err != nil {
			return nil, err
		}
		if cfg.WelcomeCh != "" && !hasText(s, cfg.WelcomeCh) {
			cfg.WelcomeCh = ""
		}
		if cfg.LevelCh != "" && !hasText(s, cfg.LevelCh) {
			cfg.LevelCh = ""
		}
		s.Bots = &cfg
		return serverChanged(s)
	})
	R("POST /api/gw/create", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, true)
		if err != nil {
			return nil, err
		}
		if !botsOf(s).Giveaway {
			return nil, fail(400, "Der Giveaway-Bot ist auf diesem Server aus. Schalte ihn in den Server-Einstellungen → Bots ein.")
		}
		prize := trunc(strings.TrimSpace(c.b.Name), 100)
		if prize == "" {
			return nil, fail(400, "Was gibt es zu gewinnen?")
		}
		if err := checkText(c.user, prize, "Giveaway"); err != nil {
			return nil, err
		}
		if c.b.Minutes < 1 || c.b.Minutes > 60*24*14 {
			return nil, fail(400, "Dauer: 1 Minute bis 14 Tage")
		}
		if c.b.Count < 1 || c.b.Count > 20 {
			return nil, fail(400, "1 bis 20 Gewinner")
		}
		ch := c.b.Channel
		if !hasText(s, ch) {
			ch = firstText(s)
		}
		g := &Giveaway{ID: newID(), ServerID: s.ID, Channel: ch, Prize: prize, Winners: c.b.Count,
			EndsAt: now() + int64(c.b.Minutes)*60000, HostID: c.user.ID, Entrants: []string{}, WinnerIDs: []string{}}
		db.Giveaways[g.ID] = g
		send(s.Members, "giveaway", g)
		m := botPost(s, ch, "giveaway", "🎉 GIVEAWAY: "+prize, g.ID)
		if m != nil {
			g.MsgID = m.ID
		}
		save()
		return M{"giveaway": g}, nil
	})
	R("POST /api/gw/join", func(c *Ctx) (any, error) {
		g, s, err := getGW(c, false)
		if err != nil {
			return nil, err
		}
		if g.Ended {
			return nil, fail(400, "Das Giveaway ist schon vorbei")
		}
		if contains(g.Entrants, c.user.ID) {
			g.Entrants = without(g.Entrants, c.user.ID)
		} else {
			g.Entrants = append(g.Entrants, c.user.ID)
		}
		save()
		send(s.Members, "giveaway", g)
		return M{"giveaway": g}, nil
	})
	R("POST /api/gw/end", func(c *Ctx) (any, error) {
		g, _, err := getGW(c, true)
		if err != nil {
			return nil, err
		}
		if !g.Ended {
			endGW(g)
		}
		return M{"giveaway": g}, nil
	})
	R("POST /api/gw/reroll", func(c *Ctx) (any, error) {
		g, s, err := getGW(c, true)
		if err != nil {
			return nil, err
		}
		if !g.Ended {
			return nil, fail(400, "Das Giveaway läuft noch")
		}
		pickWinners(g)
		send(s.Members, "giveaway", g)
		announceGW(g, true)
		save()
		return M{"giveaway": g}, nil
	})
}
