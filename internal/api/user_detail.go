package api

// user_detail.go — v0.10.1731 (operatör onayı 2026-10-31: Settings › Users
// kullanıcı detay sayfası).
//
//   GET    /api/users/{id}            → tek kullanıcı + LDAP grup üyelikleri
//   DELETE /api/users/{id}/role-lock  → "Rolü AD'ye geri ver" (kilit kalkar)
//
// İkisi de admin (liste GET /api/users gibi: kullanıcı kayıtları yalnız
// admin'e açık). Detay okuması bir FINAL tek satır + bellekteki grup
// anlık görüntüsü; sıcak yol değil, mutasyondan hemen sonra tazelik gerektiği
// için önbelleğe alınmaz.
//
// ROL KİLİDİ (Q1): loginViaLDAP, AÇIK bir GroupRoleMap eşleşmesi varsa rolü
// her girişte AD'den yeniler (resolveLdapLoginRole, v0.8.528). Admin'in elle
// verdiği rol bu yüzden bir sonraki girişte eziliyordu; setUserRole artık
// LDAP hesabında role_locked=1 yazar ve giriş kilitli rolü korur.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

func init() { registerRoutesExtra("user-detail", (*Server).registerUserDetailRoutes) }

func (s *Server) registerUserDetailRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/users/{id}",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getUserDetail)))
	mux.Handle("DELETE /api/users/{id}/role-lock",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.clearUserRoleLock)))
}

// userLdapGroup — kullanıcının üyesi olduğu bir dizin grubu (Takımlar sekmesi).
type userLdapGroup struct {
	CN string `json:"cn"`
	DN string `json:"dn"`
}

type userDetailResp struct {
	User             chstore.User    `json:"user"`
	Groups           []userLdapGroup `json:"groups"`
	GroupSyncEnabled bool            `json:"groupSyncEnabled"`
	GroupsSyncedAt   int64           `json:"groupsSyncedAt,omitempty"` // unix ns
}

func (s *Server) getUserDetail(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUserByIDAnyState(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	// Silinmiş hesap (disabled=1, engelli değil) listede yok — detayda da yok.
	if u == nil || (u.Disabled && !u.Blocked) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	if u.AuthProvider == "" {
		u.AuthProvider = "local"
	}
	resp := userDetailResp{User: *u, Groups: []userLdapGroup{}}
	if s.ldapGroupSync != nil {
		resp.GroupSyncEnabled = s.ldapGroupSync.Enabled()
		if snap := s.ldapGroupSync.Snapshot(); snap != nil {
			resp.Groups = userLdapGroups(snap, userGroupAliases(u))
			if !snap.SyncedAt.IsZero() {
				resp.GroupsSyncedAt = snap.SyncedAt.UnixNano()
			}
		}
	}
	writeJSON(w, resp)
}

func (s *Server) clearUserRoleLock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, err := s.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if target == nil {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	if !target.RoleLocked { // no-op: audit/cache çalkalanmasın
		writeJSON(w, map[string]any{"id": target.ID, "roleLocked": false})
		return
	}
	target.RoleLocked = false
	if err := s.store.UpsertUser(r.Context(), *target); err != nil {
		writeErr(w, err)
		return
	}
	s.meUsers.clear()
	details, _ := json.Marshal(map[string]any{"email": target.Email, "role": target.Role})
	s.audit(r, "user.role_unlock", "user", target.ID, string(details))
	writeJSON(w, map[string]any{"id": target.ID, "roleLocked": false})
}

// userGroupAliases — SAF: grup anlık görüntüsünün UserGroups anahtarlarıyla
// eşleşen kimlik takma adları (küçük harf sAMAccountName, e-posta, e-posta
// yerel kısmı). Hydrate sonrası yalnız sAMAccountName yeniden kurulabildiği
// için ldap_username birincil; diğerleri canlı-eşitleme yolunun zengin
// takma adları.
func userGroupAliases(u *chstore.User) []string {
	out := []string{}
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			return
		}
		for _, x := range out {
			if x == v {
				return
			}
		}
		out = append(out, v)
	}
	add(u.LdapUsername)
	add(u.Email)
	if at := strings.IndexByte(u.Email, '@'); at > 0 {
		add(u.Email[:at])
	}
	return out
}

// userLdapGroups — SAF: takma adların üyesi olduğu gruplar, UID'ye göre
// tekilleştirilmiş, CN'ye göre sıralı (aynı CN'de DN). Anlık görüntüde
// olmayan UID atlanır (tombstone/yarış).
func userLdapGroups(snap *ldap.Snapshot, aliases []string) []userLdapGroup {
	out := []userLdapGroup{}
	if snap == nil {
		return out
	}
	seen := map[string]bool{}
	for _, a := range aliases {
		for _, uid := range snap.UserGroups[a] {
			if seen[uid] {
				continue
			}
			g, ok := snap.Groups[uid]
			if !ok {
				continue
			}
			seen[uid] = true
			out = append(out, userLdapGroup{CN: g.CN, DN: g.DN})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !strings.EqualFold(out[i].CN, out[j].CN) {
			return strings.ToLower(out[i].CN) < strings.ToLower(out[j].CN)
		}
		return out[i].DN < out[j].DN
	})
	return out
}

// carryLdapLoginRow — SAF: LDAP girişinin taze satırına (u) mevcut satırdan
// taşınması gereken her alanı aktarır. ReplacingMergeTree TÜM satırı
// değiştirir; burada unutulan alan her girişte sessizce silinir (v0.8.238
// takım/özel rol, v0.8.450 son giriş, v0.10.1731 rol kilidi). Dizin değerleri
// (ad, kuruluş, kullanıcı adı) girişte yenilenir ama dizinde YOKSA saklı
// değer silinmez. Foto bilinçli olarak taşınmaz: dizinden kaldırılan foto
// burada da kalkar (v0.8.238).
func carryLdapLoginRow(existing *chstore.User, u chstore.User) chstore.User {
	if existing == nil {
		return u
	}
	u.ID = existing.ID
	u.CreatedAt = existing.CreatedAt
	u.Team = existing.Team
	u.CustomRole = existing.CustomRole
	u.PasswordHash = existing.PasswordHash
	u.LastLoginAt = existing.LastLoginAt
	u.RoleLocked = existing.RoleLocked
	if u.FullName == "" {
		u.FullName = existing.FullName
	}
	if u.Org == "" {
		u.Org = existing.Org
	}
	if u.LdapUsername == "" {
		u.LdapUsername = existing.LdapUsername
	}
	return u
}
