package main

import "testing"

func TestFilter(t *testing.T) {
	db.BadWords = []string{}
	bad := []string{"du negger", "N3gger", "n e g g e r", "Nigga", "niggaz", "Hurensohn!!", "du spast", "fotze", "neeeeger", "Du Missgeburt", "nigger123", "HURE", "Niggar", "NIGGAR", "nigor", "nygga", "neggar", "Nіggar", "n1gg@r", "niggers", "Niger", "Ardolf hitler", "Adolf Hitler 😂", "H1tl3r", "Sieg Heil", "s i e g  h e i l", "du nazi", "卐"}
	good := []string{"Hallo wie gehts", "Das ist negativ", "Nigeria ist ein Land", "Frage", "Mongolei", "Spasstikk", "ich mag Schnee", "Nacht", "night", "negativ", "Nigeria", "Zigarre", "Negation", "ich gehe nach Hause", "Ich bin heil angekommen", "Sieg!", "Wir haben gesiegt", "Er heißt Adolf", "Nazare Portugal"}
	for _, b := range bad {
		if !isBad(b) {
			t.Errorf("sollte blocken: %q", b)
		}
	}
	for _, g := range good {
		if isBad(g) {
			t.Errorf("sollte NICHT blocken: %q", g)
		}
	}
}
