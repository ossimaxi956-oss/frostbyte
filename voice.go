package main

// Sprach-Relay: Der Ton laeuft ueber den Frostbyte-Server (WebSocket),
// damit Anrufe auch hinter Routern/Firewalls immer funktionieren.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type wsConn struct {
	c  net.Conn
	r  *bufio.Reader
	wm sync.Mutex
}

func (w *wsConn) write(op byte, data []byte) error {
	w.wm.Lock()
	defer w.wm.Unlock()
	h := []byte{0x80 | op}
	n := len(data)
	switch {
	case n < 126:
		h = append(h, byte(n))
	case n < 65536:
		h = append(h, 126, byte(n>>8), byte(n))
	default:
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(n))
		h = append(append(h, 127), b...)
	}
	w.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := w.c.Write(h); err != nil {
		return err
	}
	_, err := w.c.Write(data)
	return err
}

func (w *wsConn) read() (byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(w.r, hdr[:]); err != nil {
		return 0, nil, err
	}
	op := hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	n := uint64(hdr[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		if _, err := io.ReadFull(w.r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		if _, err := io.ReadFull(w.r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > 1<<16 {
		return 0, nil, errors.New("frame zu gross")
	}
	var key [4]byte
	if masked {
		if _, err := io.ReadFull(w.r, key[:]); err != nil {
			return 0, nil, err
		}
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(w.r, data); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range data {
			data[i] ^= key[i%4]
		}
	}
	return op, data, nil
}

type voiceRoom struct {
	a, b  string
	conns map[string]*wsConn
}

var (
	vmu   sync.Mutex
	rooms = map[string]*voiceRoom{}
)

// wird aufgerufen, wenn ein Anruf angenommen / beendet wird (unter mu)
func voiceAllow(id, caller, callee string) {
	vmu.Lock()
	if old := rooms[id]; old == nil || old.a != caller || old.b != callee {
		rooms[id] = &voiceRoom{a: caller, b: callee, conns: map[string]*wsConn{}}
	}
	// alte, nie benutzte Raeume aufraeumen
	if len(rooms) > 200 {
		for k, r := range rooms {
			if len(r.conns) == 0 && k != id {
				delete(rooms, k)
			}
		}
	}
	vmu.Unlock()
}
func voiceEnd(id string) {
	vmu.Lock()
	r := rooms[id]
	delete(rooms, id)
	vmu.Unlock()
	if r != nil {
		for _, c := range r.conns {
			c.write(1, []byte("end"))
			c.c.Close()
		}
	}
}

func voiceWS(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	u, _ := authUser(r)
	mu.Unlock()
	id := r.URL.Query().Get("call")
	vmu.Lock()
	room := rooms[id]
	ok := u != nil && room != nil && (room.a == u.ID || room.b == u.ID)
	vmu.Unlock()
	if !ok || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "Kein Zugriff", 403)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	hj, ok := w.(http.Hijacker)
	if !ok || key == "" {
		http.Error(w, "geht nicht", 500)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
	brw.Flush()
	ws := &wsConn{c: conn, r: brw.Reader}
	me := u.ID
	vmu.Lock()
	if old := room.conns[me]; old != nil {
		old.c.Close()
	}
	room.conns[me] = ws
	both := len(room.conns) == 2
	peers := []*wsConn{}
	for _, c := range room.conns {
		peers = append(peers, c)
	}
	vmu.Unlock()
	if both {
		for _, c := range peers {
			c.write(1, []byte("ready"))
		}
	}
	defer func() {
		conn.Close()
		vmu.Lock()
		if room.conns[me] == ws {
			delete(room.conns, me)
			for _, c := range room.conns {
				go c.write(1, []byte("left"))
			}
		}
		vmu.Unlock()
	}()
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		op, data, err := ws.read()
		if err != nil {
			return
		}
		switch op {
		case 8:
			ws.write(8, nil)
			return
		case 9:
			ws.write(10, data)
		case 1:
			if string(data) == "ping" {
				ws.write(1, []byte("pong"))
			}
		case 2:
			vmu.Lock()
			var other *wsConn
			for uid, c := range room.conns {
				if uid != me {
					other = c
				}
			}
			vmu.Unlock()
			if other != nil {
				if other.write(2, data) != nil {
					other.c.Close()
				}
			}
		}
	}
}

// ===================== Sprachkanaele (Gruppen) =====================
// Jeder schickt seinen Ton an den Server, der Server verteilt ihn an alle anderen im Kanal.
// Format an die Clients: 16 Zeichen Benutzer-ID + Ton-Daten.

var vcs = map[string]map[string]*wsConn{} // Kanal -> Benutzer -> Verbindung (unter vmu)

func vcUsers(ch string) []string {
	out := []string{}
	for uid := range vcs[ch] {
		out = append(out, uid)
	}
	return out
}

// Sprachkanal-Belegung aller Kanaele eines Servers (mu muss gehalten werden)
func vcStateFor(s *Server) M {
	vmu.Lock()
	defer vmu.Unlock()
	st := M{}
	for _, c := range s.Channels {
		if c.Type == "voice" {
			st[c.ID] = vcUsers(c.ID)
		}
	}
	return st
}

// Allen auf dem Server mitteilen, wer im Kanal ist (mu muss gehalten werden)
func vcBroadcast(s *Server, ch string) {
	vmu.Lock()
	users := vcUsers(ch)
	vmu.Unlock()
	send(s.Members, "voice_state", M{"serverId": s.ID, "channel": ch, "users": users})
}

// Wirft jemanden aus Sprachkanaelen (ganzer Server oder alle, wenn sid leer)
func vcKick(uid, sid string) {
	vmu.Lock()
	for ch, m := range vcs {
		if c := m[uid]; c != nil {
			if sid == "" || channelServer(ch) == sid {
				c.write(1, []byte("kicked"))
				c.c.Close()
			}
		}
	}
	vmu.Unlock()
}

// Alle aus einem Kanal werfen (z. B. wenn er geloescht wird)
func vcCloseChannel(ch string) {
	vmu.Lock()
	for _, c := range vcs[ch] {
		c.write(1, []byte("deleted"))
		c.c.Close()
	}
	vmu.Unlock()
}

var chServer = map[string]string{} // Kanal -> Server (Zwischenspeicher, unter vmu)

func channelServer(ch string) string { return chServer[ch] }

func vcWS(w http.ResponseWriter, r *http.Request) {
	ch := r.URL.Query().Get("channel")
	mu.Lock()
	u, _ := authUser(r)
	var srv *Server
	if u != nil {
		if cc := ctxOf(ch); cc != nil && !cc.isDM && contains(cc.server.Members, u.ID) {
			for _, c := range cc.server.Channels {
				if c.ID == ch && c.Type == "voice" {
					srv = cc.server
				}
			}
		}
	}
	mu.Unlock()
	if srv == nil || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "Kein Zugriff", 403)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	hj, ok := w.(http.Hijacker)
	if !ok || key == "" {
		http.Error(w, "geht nicht", 500)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
	brw.Flush()
	ws := &wsConn{c: conn, r: brw.Reader}
	me := u.ID
	prefix := []byte(me)
	if len(prefix) != 16 {
		conn.Close()
		return
	}
	vmu.Lock()
	// in einem anderen Sprachkanal? -> dort raus
	for och, m := range vcs {
		if old := m[me]; old != nil {
			old.write(1, []byte("moved"))
			old.c.Close()
			delete(m, me)
			if och != ch {
				go func(och string) {
					mu.Lock()
					if s := db.Servers[channelServer(och)]; s != nil {
						vcBroadcast(s, och)
					}
					mu.Unlock()
				}(och)
			}
		}
	}
	if vcs[ch] == nil {
		vcs[ch] = map[string]*wsConn{}
	}
	vcs[ch][me] = ws
	chServer[ch] = srv.ID
	vmu.Unlock()
	mu.Lock()
	vcBroadcast(srv, ch)
	mu.Unlock()
	ws.write(1, []byte("joined"))

	defer func() {
		conn.Close()
		vmu.Lock()
		removed := false
		if vcs[ch][me] == ws {
			delete(vcs[ch], me)
			removed = true
			if len(vcs[ch]) == 0 {
				delete(vcs, ch)
			}
		}
		vmu.Unlock()
		if removed {
			mu.Lock()
			if s := db.Servers[srv.ID]; s != nil {
				vcBroadcast(s, ch)
			}
			mu.Unlock()
		}
	}()
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		op, data, err := ws.read()
		if err != nil {
			return
		}
		switch op {
		case 8:
			ws.write(8, nil)
			return
		case 9:
			ws.write(10, data)
		case 1:
			if string(data) == "ping" {
				ws.write(1, []byte("pong"))
			}
		case 2:
			if len(data) > 8000 {
				continue
			}
			pkt := append(append([]byte{}, prefix...), data...)
			vmu.Lock()
			others := []*wsConn{}
			for uid, c := range vcs[ch] {
				if uid != me {
					others = append(others, c)
				}
			}
			vmu.Unlock()
			for _, c := range others {
				if c.write(2, pkt) != nil {
					c.c.Close()
				}
			}
		}
	}
}
