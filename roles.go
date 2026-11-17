package main

// Rollen, Rechte, Server-Bans und Server-Bild (wie bei Discord)

import (
	"encoding/json"
	"strings"
)

type Role struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Color string   `json:"color"`
	Perms []string `json:"perms"`
	Hoist bool     `json:"hoist"`
}

var allPerms = []string{"admin", "manageServer", "manageChannels", "manageRoles", "manageMessages", "kick", "ban", "giveaway"}

var permNames = map[string]string{
	"admin": "Administrator", "manageServer": "Server verwalten", "manageChannels": "Kanäle verwalten",
	"manageRoles": "Rollen verwalten", "manageMessages": "Nachrichten verwalten", "kick": "Mitglieder kicken",
	"ban": "Mitglieder bannen", "giveaway": "Giveaways starten",
}

func roleByID(s *Server, id string) *Role {
	for _, r := range s.Roles {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func hasPerm(u *User, s *Server, p string) bool {
	if u == nil || s == nil {
		return false
	}
	if s.OwnerID == u.ID || isMod(u) {
		return true
	}
	for _, rid := range s.MemberRoles[u.ID] {
		if r := roleByID(s, rid); r != nil && (contains(r.Perms, "admin") || contains(r.Perms, p)) {
			return true
		}
	}
	return false
}

// Hoechste Rolle (kleinster Index); -1 = keine, -2 = Besitzer/App-Admin
func topRole(u *User, s *Server) int {
	if s.OwnerID == u.ID || isMod(u) {
		return -2
	}
	best := 1 << 30
	for _, rid := range s.MemberRoles[u.ID] {
		for i, r := range s.Roles {
			if r.ID == rid && i < best {
				best = i
			}
		}
	}
	if best == 1<<30 {
		return 1 << 30
	}
	return best
}

// Darf actor etwas mit target machen (kicken, bannen, Rollen aendern)?
func outranks(actor, target *User, s *Server) bool {
	if target.ID == s.OwnerID || target.Role == "owner" {
		return false
	}
	if s.OwnerID == actor.ID || isMod(actor) {
		return true
	}
	return topRole(actor, s) < topRole(target, s)
}

func removeMember(s *Server, uid string) {
	s.Members = without(s.Members, uid)
	delete(s.MemberRoles, uid)
	vcKick(uid, s.ID)
}

func setupRoleRoutes() {
	R("POST /api/roles/save", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageRoles")
		if err != nil {
			return nil, err
		}
		var in Role
		if json.Unmarshal(c.b.Cfg, &in) != nil {
			return nil, fail(400, "Ungültige Rolle")
		}
		in.Name = trunc(strings.TrimSpace(in.Name), 30)
		if in.Name == "" {
			return nil, fail(400, "Die Rolle braucht einen Namen")
		}
		if err := checkText(c.user, in.Name, "Rollenname"); err != nil {
			return nil, err
		}
		if in.Color != "" && !colorRe.MatchString(in.Color) {
			in.Color = ""
		}
		perms := []string{}
		for _, p := range in.Perms {
			if contains(allPerms, p) && !contains(perms, p) {
				perms = append(perms, p)
			}
		}
		if contains(perms, "admin") && !hasPerm(c.user, s, "admin") {
			return nil, fail(403, "Nur Administratoren dürfen die Administrator-Rolle vergeben")
		}
		in.Perms = perms
		if in.ID == "" {
			if len(s.Roles) >= 50 {
				return nil, fail(400, "Maximal 50 Rollen")
			}
			in.ID = newID()
			r := in
			s.Roles = append([]*Role{&r}, s.Roles...) // neue Rolle ganz oben
		} else {
			r := roleByID(s, in.ID)
			if r == nil {
				return nil, fail(404, "Rolle nicht gefunden")
			}
			if contains(r.Perms, "admin") && !hasPerm(c.user, s, "admin") {
				return nil, fail(403, "Nur Administratoren dürfen diese Rolle ändern")
			}
			r.Name, r.Color, r.Perms, r.Hoist = in.Name, in.Color, in.Perms, in.Hoist
		}
		return serverChanged(s)
	})
	R("POST /api/roles/delete", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageRoles")
		if err != nil {
			return nil, err
		}
		keep := []*Role{}
		for _, r := range s.Roles {
			if r.ID != c.b.ID {
				keep = append(keep, r)
			} else if contains(r.Perms, "admin") && !hasPerm(c.user, s, "admin") {
				return nil, fail(403, "Nur Administratoren dürfen diese Rolle löschen")
			}
		}
		s.Roles = keep
		for uid, list := range s.MemberRoles {
			s.MemberRoles[uid] = without(list, c.b.ID)
		}
		return serverChanged(s)
	})
	R("POST /api/roles/move", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageRoles")
		if err != nil {
			return nil, err
		}
		for i, r := range s.Roles {
			if r.ID == c.b.ID {
				j := i + c.b.Count
				if j >= 0 && j < len(s.Roles) {
					s.Roles[i], s.Roles[j] = s.Roles[j], s.Roles[i]
				}
				break
			}
		}
		return serverChanged(s)
	})
	R("POST /api/roles/assign", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageRoles")
		if err != nil {
			return nil, err
		}
		t := db.Users[c.b.UserID]
		r := roleByID(s, c.b.ID)
		if t == nil || r == nil || !contains(s.Members, t.ID) {
			return nil, fail(404, "Nicht gefunden")
		}
		if contains(r.Perms, "admin") && !hasPerm(c.user, s, "admin") {
			return nil, fail(403, "Nur Administratoren dürfen diese Rolle vergeben")
		}
		if s.MemberRoles == nil {
			s.MemberRoles = map[string][]string{}
		}
		if c.b.Banned { // Banned = entfernen
			s.MemberRoles[t.ID] = without(s.MemberRoles[t.ID], r.ID)
		} else if !contains(s.MemberRoles[t.ID], r.ID) {
			s.MemberRoles[t.ID] = append(s.MemberRoles[t.ID], r.ID)
		}
		return serverChanged(s)
	})
	R("POST /api/servers/icon", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "manageServer")
		if err != nil {
			return nil, err
		}
		if c.b.Image == "" {
			s.Icon = ""
		} else {
			p, err := saveImage(c.b.Image)
			if err != nil {
				return nil, err
			}
			s.Icon = p
		}
		return serverChanged(s)
	})
	R("POST /api/servers/ban", func(c *Ctx) (any, error) {
		s, err := getServer(c.user, c.b.ServerID, "ban")
		if err != nil {
			return nil, err
		}
		t := db.Users[c.b.UserID]
		if t == nil || t.ID == c.user.ID {
			return nil, fail(404, "Benutzer nicht gefunden")
		}
		if c.b.Banned {
			if !outranks(c.user, t, s) {
				return nil, fail(403, "Diese Person kannst du nicht bannen")
			}
			if !contains(s.Bans, t.ID) {
				s.Bans = append(s.Bans, t.ID)
			}
			if contains(s.Members, t.ID) {
				removeMember(s, t.ID)
				send([]string{t.ID}, "server_removed", M{"serverId": s.ID})
				botLeave(s, t)
			}
		} else {
			s.Bans = without(s.Bans, t.ID)
		}
		return serverChanged(s)
	})
}
