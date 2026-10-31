package api

// presence_online.go — v0.10.1736 "Kim çevrimiçi" HER role.
//
// Varlık (presence.go, v0.8.403 + v0.10.1735 yerel geri-düşme) bugüne dek
// yalnız admin'e özel GET /api/users satırlarında görünüyordu. Bu uç aynı
// damgaları her oturum açmış role (viewer/editor/admin) DAR bir biçimde
// açar: yalnız {id, name, team, lastSeenAt}. E-posta, rol, dizin alanları
// YOK — `name` = fullName, yoksa e-postanın yerel kısmı (asla tam adres).
//
// Önbellek: yanıt çağırandan bağımsızdır (admin'e özgü /users/:id bağı
// istemcide kurulur), bu yüzden TEK paylaşılan girdi; anahtar yalnız
// çevrimiçi eşiğini taşır. 15s TTL — Topbar menüsü açılışta okur.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("presence-online", (*Server).registerPresenceOnlineRoutes) }

const (
	// presenceOnlineCap — liste tavanı (sıralamadan SONRA uygulanır).
	presenceOnlineCap = 200
	// presenceOnlineTTL — paylaşılan önbellek girdisinin tazeliği.
	presenceOnlineTTL = 15 * time.Second
)

// presenceOnlineRoles — oturum açmış her rol. Özel rollü kullanıcılar
// rol olarak viewer taşır, onlar da dahildir.
var presenceOnlineRoles = []string{auth.RoleAdmin, auth.RoleEditor, auth.RoleViewer}

// presenceOnlineCacheKey — çağırana özgü hiçbir şey yok; yalnız eşik.
var presenceOnlineCacheKey = fmt.Sprintf("presence:online:v1:w=%d", presenceOnlineWindow.Milliseconds())

func (s *Server) registerPresenceOnlineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/presence/online", auth.RequireAnyRole(presenceOnlineRoles, s.getPresenceOnline))
}

// onlineUser — dar satır. Alan eklemek = her role yeni veri açmak;
// presence_online_test.go alan kümesini çiviler.
type onlineUser struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Team       string `json:"team,omitempty"`
	LastSeenAt int64  `json:"lastSeenAt"` // unix ns
}

func (s *Server) getPresenceOnline(w http.ResponseWriter, r *http.Request) {
	s.servePresenceOnline(w, r, s.store.ListUsers)
}

// servePresenceOnline — handler gövdesi; kullanıcı kaynağı enjekte edilir
// (test CH'siz koşar).
func (s *Server) servePresenceOnline(w http.ResponseWriter, r *http.Request, load func(ctx context.Context) ([]chstore.User, error)) {
	s.serveCached(w, r, presenceOnlineCacheKey, presenceOnlineTTL, func(ctx context.Context) (any, error) {
		users, err := load(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(users))
		for _, u := range users {
			if !u.Disabled {
				ids = append(ids, u.ID)
			}
		}
		seen := s.presence.lastSeen(ctx, ids)
		return shapeOnlineUsers(users, seen, time.Now().UnixNano(), presenceOnlineCap), nil
	})
}

// shapeOnlineUsers — SAF: kullanıcılar + damgalar → çevrimiçi satırlar.
// Devre dışı/engelli hesap ve eşik dışı damga elenir; ada göre (büyük/küçük
// harf duyarsız, eşitlikte id) sıralanır, sonra cap uygulanır. Boş sonuç
// nil değil boş dilim (JSON `[]`).
func shapeOnlineUsers(users []chstore.User, seen map[string]int64, nowNs int64, limit int) []onlineUser {
	out := make([]onlineUser, 0)
	for _, u := range users {
		if u.Disabled {
			continue
		}
		stamp, ok := seen[u.ID]
		if !ok || !presenceOnline(stamp, nowNs) {
			continue
		}
		out = append(out, onlineUser{ID: u.ID, Name: onlineDisplayName(u.FullName, u.Email), Team: u.Team, LastSeenAt: stamp})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// onlineDisplayName — fullName; yoksa e-postanın @ öncesi. Tam adres asla.
func onlineDisplayName(fullName, email string) string {
	if n := strings.TrimSpace(fullName); n != "" {
		return n
	}
	local, _, _ := strings.Cut(strings.TrimSpace(email), "@")
	return local
}
