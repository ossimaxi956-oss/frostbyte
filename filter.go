package main

// Wortfilter: blockiert Beleidigungen, Rassismus usw.
// Erkennt auch Tricks wie N3gger, n e g g e r, neeeeger, n.e.g.e.r

import (
	"regexp"
	"strings"
)

// Alle Varianten vom N-Wort als ganzes Wort: niga, nigar, niger, nigor, nyga, negar, nigaz, nigers ...
var nWordRe = regexp.MustCompile(`^n[iey]g(a|e|o|u|ar|er|or|ur|ah|uh|az|as|es|ers|ars|ors|erz|arz|ahs|i|is)?$`)

// Woerter, die IRGENDWO in einem Wort verboten sind (schon "normalisiert": klein, doppelte Buchstaben zusammengefasst)
var badAnywhere = []string{
	"neger", "hurensohn", "hurenson", "fotze", "schwuchtel", "kanake", "kanacke", "wichser",
	"misgeburt", "fagot", "siegheil", "heilhitler", "nigerlein", "nigar", "negar", "nigor", "nigur", "nygar", "nyger",
	"hitler", "hitla", "hidler", "hytler", "adolfh", "ardolf", "siegheil", "heilhitler", "nsdap", "holocaustwar", "judenraus", "judenvergasen", "vergastdiejuden",
}

// Woerter, die als GANZES Wort verboten sind (auch mit s/z/n/e am Ende)
var badWord = []string{
	"niger", "niga", "nigaz", "nega", "nigah", "nazi", "nazis", "fuhrer", "fuehrer", "gaskamer", "reichsadler", "spast", "spasti", "spasty", "mongo", "hure", "kys", "fag",
}

var leet = map[rune]string{'0': "o", '1': "i", '3': "e", '4': "a", '5': "s", '7': "t", '8': "b", '@': "a", '$': "s", '!': "i", '|': "i",
	'ä': "a", 'ö': "o", 'ü': "u", 'ß': "ss", '€': "e",
	// getarnte Buchstaben aus anderen Alphabeten
	'а': "a", 'е': "e", 'о': "o", 'і': "i", 'ї': "i", 'у': "y", 'с': "c", 'р': "p", 'х': "x", 'к': "k", 'н': "h", 'г': "r", 'ё': "e",
	'ı': "i", 'ğ': "g", 'ģ': "g", 'ĝ': "g", 'ñ': "n", 'ń': "n", 'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'á': "a", 'à': "a", 'â': "a", 'é': "e", 'è': "e", 'ê': "e", 'ó': "o", 'ò': "o", 'ú': "u", 'ý': "y"}

func normWord(s string) string { return normWith(s, true) }

func normWith(s string, useLeet bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if v, ok := leet[r]; ok && (useLeet || r > 127) {
			b.WriteString(v)
		} else if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	// doppelte Buchstaben zusammenfassen: neeeegger -> neger
	out := []byte{}
	w := b.String()
	for i := 0; i < len(w); i++ {
		if i > 0 && w[i] == w[i-1] {
			continue
		}
		out = append(out, w[i])
	}
	return string(out)
}

func tokens(text string) []string {
	raw := strings.Fields(text)
	toks := []string{}
	single := ""
	for _, t := range raw {
		n := normWord(t)
		if len(n) == 1 { // n e g e r -> neger
			single += n
			continue
		}
		if len(single) > 1 {
			toks = append(toks, normWord(single))
		}
		single = ""
		if n != "" {
			toks = append(toks, n)
		}
		if p := normWith(t, false); p != "" && p != n { // auch ohne Zahlen pruefen: nigger123
			toks = append(toks, p)
		}
	}
	if len(single) > 1 {
		toks = append(toks, normWord(single))
	}
	return toks
}

func wordHit(tok, w string) bool {
	if tok == w {
		return true
	}
	for _, suf := range []string{"s", "z", "n", "e", "en", "er"} {
		if tok == w+suf {
			return true
		}
	}
	return false
}

// Saetze, die auch ueber mehrere Woerter verteilt verboten sind
var badPhrases = []string{"siegheil", "heilhitler", "adolfhitler", "ardolfhitler", "judenraus", "whitepower", "hailhitler"}

// isBad prueft einen Text. Muss mit gehaltenem mu aufgerufen werden.
func isBad(text string) bool {
	if strings.ContainsAny(text, "卐卍࿕࿖ᛋ") {
		return true
	}
	all := normWith(strings.ReplaceAll(text, " ", ""), true)
	for _, ph := range badPhrases {
		if strings.Contains(all, normWord(ph)) {
			return true
		}
	}
	for _, tok := range tokens(text) {
		for _, w := range badAnywhere {
			if strings.Contains(tok, w) {
				return true
			}
		}
		for _, w := range badWord {
			if wordHit(tok, w) {
				return true
			}
		}
		if nWordRe.MatchString(tok) {
			return true
		}
		for _, cw := range db.BadWords {
			n := normWord(cw)
			if n == "" {
				continue
			}
			if len(n) >= 5 && strings.Contains(tok, n) || wordHit(tok, n) {
				return true
			}
		}
	}
	return false
}

var (
	strikes    = map[string][]int64{}
	mutedUntil = map[string]int64{}
)

const blockedMsg = "🚫 Blockiert! Dieses Wort ist bei Frostbyte verboten. Bleib nett ❄️"

// checkText blockt boese Texte. where = wo es passiert ist (fuer die Admins).
func checkText(u *User, text, where string) error {
	return checkTextAt(u, text, where, "")
}

func checkTextAt(u *User, text, where, ch string) error {
	if u != nil {
		if until := mutedUntil[u.ID]; until > now() {
			mins := (until-now())/60000 + 1
			return fail(403, "🔇 Du bist wegen Beleidigungen noch "+itoa(mins)+" Min. stummgeschaltet.")
		}
	}
	if !isBad(text) {
		return nil
	}
	if u != nil {
		t := now()
		list := []int64{}
		for _, x := range strikes[u.ID] {
			if t-x < 10*60000 {
				list = append(list, x)
			}
		}
		list = append(list, t)
		strikes[u.ID] = list
		mods := modIDs(u.ID)
		if ch != "" {
			where = where + " in " + whereOf(ch)
		}
		modLog(&ModEntry{Kind: "blocked", UserID: u.ID, Where: where, Text: text})
		if len(list) >= 3 {
			mutedUntil[u.ID] = t + 5*60000
			strikes[u.ID] = nil
			send(mods, "blocked", M{"name": u.Name, "where": where, "muted": true})
			return fail(403, "🔇 3x beleidigt – du bist jetzt 5 Minuten stummgeschaltet.")
		}
		send(mods, "blocked", M{"name": u.Name, "where": where})
	}
	return fail(400, blockedMsg)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// Alte Nachrichten mit verbotenen Woertern beim Start entfernen.
func cleanOldMessages() int {
	removed := 0
	for ch, list := range db.Messages {
		keep := list[:0]
		for _, m := range list {
			if m.Text != "" && isBad(m.Text) {
				removed++
				continue
			}
			keep = append(keep, m)
		}
		db.Messages[ch] = keep
	}
	return removed
}
