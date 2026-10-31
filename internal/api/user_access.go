package api

// user_access.go — v0.10.1734 (kullanıcı detay sayfası S4, operatör onayı
// 2026-10-31): Yetkiler + Bildirimler sekmeleri.
//
//   GET /api/users/{id}/access → {permissions, notifications} (admin)
//
// Yetkiler: rolün çözülmüş hâli — admin/editor "tüm izinler", özel rolsüz
// viewer "tüm sayfalar, salt-okunur", özel rollü viewer o rolün sayfa listesi
// (auth.Service katalogu; etiketleri istemci /api/admin/pages ile çözer).
//
// Bildirimler: Coremetry'de kişi başına abonelik YOK. Bu kullanıcıya ulaşan
// iki yol gösterilir: (1) alıcı listesinde bu e-postayı taşıyan e-posta
// kanalları, (2) Settings › Team routing: kullanıcının birincil takımının
// ekip adresi (takım düzeyinde — ekip adresinin kullanıcıya gidip gitmediği
// Coremetry'nin bilgisi dışında) ve adres listesinde bu e-posta DOĞRUDAN
// geçen ekipler. Okuma notification_channels FINAL (küçük durum tablosu) +
// team_contacts blob'u; sıcak yol değil, sekme açılınca çağrılır.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("user-access", (*Server).registerUserAccessRoutes) }

func (s *Server) registerUserAccessRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/users/{id}/access",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getUserAccess)))
}

type userPermissions struct {
	Role       string `json:"role"`
	CustomRole string `json:"customRole,omitempty"`
	// Scope: all = tüm izinler (admin/editor) · viewer = tüm sayfalar salt
	// okunur · pages = yalnız Pages (özel rol) · missing = özel rol katalogda
	// yok (silinmiş; kullanıcı kısıtsız viewer gibi davranır).
	Scope string   `json:"scope"`
	Pages []string `json:"pages"`
}

type userNotifyChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Enabled     bool   `json:"enabled"`
	MinSeverity string `json:"minSeverity,omitempty"`
}

type userTeamRoute struct {
	Team      string   `json:"team"`
	Addresses []string `json:"addresses"`
	// Direct: ekibin adres listesinde kullanıcının e-postası var. false =
	// kullanıcının birincil takımı (takım düzeyinde ulaşım).
	Direct bool `json:"direct"`
}

type userNotifications struct {
	Channels           []userNotifyChannel `json:"channels"`
	TeamRoutes         []userTeamRoute     `json:"teamRoutes"`
	TeamRoutingEnabled bool                `json:"teamRoutingEnabled"`
	TeamMinSeverity    string              `json:"teamMinSeverity,omitempty"`
}

type userAccessResp struct {
	Permissions   userPermissions   `json:"permissions"`
	Notifications userNotifications `json:"notifications"`
}

// resolveUserPermissions — SAF. pages = özel rolün sayfaları (nil = katalogda yok).
func resolveUserPermissions(role, customRole string, pages []string) userPermissions {
	p := userPermissions{Role: role, Pages: []string{}}
	switch {
	case role == auth.RoleAdmin || role == auth.RoleEditor:
		p.Scope = "all"
	case customRole == "":
		p.Scope = "viewer"
	case pages == nil:
		p.CustomRole, p.Scope = customRole, "missing"
	default:
		p.CustomRole, p.Scope = customRole, "pages"
		p.Pages = append(p.Pages, pages...)
		sort.Strings(p.Pages)
	}
	return p
}

func splitAddresses(list []string) []string {
	var out []string
	for _, v := range list {
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				out = append(out, a)
			}
		}
	}
	return out
}

func hasAddress(list []string, email string) bool {
	for _, a := range list {
		if strings.EqualFold(a, email) {
			return true
		}
	}
	return false
}

// userNotifyReach — SAF: bu kullanıcıya ulaşan kanallar ve ekip yolları.
func userNotifyReach(email, team string, channels []chstore.NotificationChannel, tc chstore.TeamContacts) userNotifications {
	out := userNotifications{Channels: []userNotifyChannel{}, TeamRoutes: []userTeamRoute{},
		TeamRoutingEnabled: tc.Enabled, TeamMinSeverity: tc.MinSeverity}
	email = strings.TrimSpace(email)
	for _, c := range channels {
		if c.Type != "email" || email == "" {
			continue
		}
		var cfg struct {
			Recipients []string `json:"recipients"`
		}
		if json.Unmarshal(c.Config, &cfg) != nil {
			continue
		}
		if hasAddress(splitAddresses(cfg.Recipients), email) {
			out.Channels = append(out.Channels, userNotifyChannel{ID: c.ID, Name: c.Name, Type: c.Type,
				Enabled: c.Enabled, MinSeverity: c.MinSeverity})
		}
	}
	sort.Slice(out.Channels, func(i, j int) bool {
		return strings.ToLower(out.Channels[i].Name) < strings.ToLower(out.Channels[j].Name)
	})
	team = strings.TrimSpace(team)
	for name := range tc.Contacts {
		addrs := tc.EmailsForTeam(name)
		direct := email != "" && hasAddress(addrs, email)
		primary := team != "" && strings.EqualFold(strings.TrimSpace(name), team)
		if !direct && !primary {
			continue
		}
		out.TeamRoutes = append(out.TeamRoutes, userTeamRoute{Team: strings.TrimSpace(name), Addresses: append([]string{}, addrs...), Direct: direct})
	}
	sort.Slice(out.TeamRoutes, func(i, j int) bool {
		return strings.ToLower(out.TeamRoutes[i].Team) < strings.ToLower(out.TeamRoutes[j].Team)
	})
	return out
}

func (s *Server) getUserAccess(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUserByIDAnyState(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if u == nil || (u.Disabled && !u.Blocked) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	var pages []string
	if u.Role == auth.RoleViewer && u.CustomRole != "" {
		pages = s.auth.CustomRolePages(u.CustomRole)
	}
	channels, err := s.store.ListChannels(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	tc, err := s.store.GetTeamContacts(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, userAccessResp{
		Permissions:   resolveUserPermissions(u.Role, u.CustomRole, pages),
		Notifications: userNotifyReach(u.Email, u.Team, channels, tc),
	})
}
