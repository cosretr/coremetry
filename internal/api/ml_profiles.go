package api

// ml_profiles.go — v0.10.1816, öğrenme döngüsü Faz 1 dilim 1.5 (audit
// docs/anomaly/learning-loop-audit.md §4.4, §6.7): servis profilleri.
//
//   GET    /api/ml/profiles?q=&profile=&offset=&limit= — her rol (viewer salt-okur)
//   PUT    /api/ml/profiles/{name}/pin {profile, reason}   — admin, audit ml.profile.pin
//   DELETE /api/ml/profiles/{name}/pin                     — admin, audit ml.profile.unpin
//
// Yol `/pin` sonekli: `PUT /api/ml/profiles/{name}` mevcut `PUT
// /api/ml/{id}/verdict` ile ServeMux'ta çakışır ("/api/ml/profiles/verdict"
// ikisine de uyar; TestMuxRoutePatterns yakaladı).
//
// Binlerce servis: liste SUNUCU-sayfalı + sunucuda ad araması + profil
// süzgeci (eager liste yok); cache anahtarı tüm girdileri taşır. Sabitleme
// ml_learned'a yeni revizyon yazar (author = kullanıcı); sınıflandırıcı
// sabitlenmiş profili ezmez. Profil bu sürümde HİÇBİR dedektöre girmez.
// api.go BÜYÜMEZ: kayıt init()'te registerRoutesExtra ile.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml-profiles", (*Server).registerMLProfileRoutes) }

func (s *Server) registerMLProfileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/profiles", auth.RequireAnyRole(mlReadRoles, s.getMLProfiles))
	mux.HandleFunc("PUT /api/ml/profiles/{name}/pin", auth.RequireRole(auth.RoleAdmin, s.putMLProfilePin))
	mux.HandleFunc("DELETE /api/ml/profiles/{name}/pin", auth.RequireRole(auth.RoleAdmin, s.deleteMLProfilePin))
}

const (
	mlProfilesTTL       = 30 * time.Second
	mlProfilesMaxLimit  = 200
	mlProfilesMaxOffset = 100000
	mlProfileReasonMax  = 500
	mlProfileSearchMax  = 200
)

// mlProfilesKey — SAF: tüm girdiler anahtarda (arama metni %q ile, ayırıcı çakışmasız).
func mlProfilesKey(q, profile string, offset, limit int) string {
	return fmt.Sprintf("ml:profiles:v1:q=%q:p=%q:o=%d:l=%d", q, profile, offset, limit)
}

// parseMLProfilesQuery — SAF: kelepçeli girdiler.
func parseMLProfilesQuery(q, profile, offset, limit string) (string, string, int, int) {
	q = strings.TrimSpace(q)
	if len([]rune(q)) > mlProfileSearchMax {
		q = string([]rune(q)[:mlProfileSearchMax])
	}
	profile = strings.TrimSpace(profile)
	if !ml.ValidProfile(profile) && profile != ml.ProfileUnknown {
		profile = ""
	}
	o, _ := strconv.Atoi(offset)
	if o < 0 || o > mlProfilesMaxOffset {
		o = 0
	}
	l, _ := strconv.Atoi(limit)
	if l <= 0 || l > mlProfilesMaxLimit {
		l = 50
	}
	return q, profile, o, l
}

func (s *Server) getMLProfiles(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q, profile, offset, limit := parseMLProfilesQuery(v.Get("q"), v.Get("profile"), v.Get("offset"), v.Get("limit"))
	s.serveCached(w, r, mlProfilesKey(q, profile, offset, limit), mlProfilesTTL, func(ctx context.Context) (any, error) {
		rows, total, err := s.store.MLLearnedLatest(ctx, chstore.MLLearnedQuery{Kind: chstore.MLLearnedKindProfile, Search: q, Profile: profile, Offset: offset, Limit: limit})
		if err != nil {
			return nil, err
		}
		items := make([]ml.ServiceProfile, 0, len(rows))
		for _, row := range rows {
			items = append(items, ml.ServiceProfileFromRow(row))
		}
		return map[string]any{"items": items, "total": total, "offset": offset, "limit": limit}, nil
	})
}

func (s *Server) putMLProfilePin(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	var body struct {
		Profile string `json:"profile"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if name == "" || !ml.ValidProfile(body.Profile) {
		writeJSONError(w, http.StatusBadRequest, "servis ve profil (batch | low_traffic | business_hours | always_on) zorunlu")
		return
	}
	if body.Reason == "" || len([]rune(body.Reason)) > mlProfileReasonMax {
		writeJSONError(w, http.StatusBadRequest, "gerekçe zorunlu (en çok 500 karakter)")
		return
	}
	ctx := r.Context()
	prev, ok, err := s.store.MLLearnedOne(ctx, chstore.MLLearnedKindProfile, name)
	if err != nil {
		writeErr(w, err)
		return
	}
	who := ""
	if c := auth.FromContext(ctx); c != nil {
		who = c.Email
	}
	row := ml.PinProfileRow(name, prev, ok, body.Profile, body.Reason, who, time.Now())
	if err := s.store.InsertMLLearned(ctx, []chstore.MLLearnedRow{row}); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(ctx, "ml:")
	before := ml.ServiceProfileFromRow(prev)
	details, _ := json.Marshal(map[string]any{"profile": body.Profile, "reason": body.Reason, "before": before.Profile, "rev": row.Rev})
	s.audit(r, "ml.profile.pin", "ml_profile", name, string(details))
	writeJSON(w, ml.ServiceProfileFromRow(row))
}

func (s *Server) deleteMLProfilePin(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	ctx := r.Context()
	prev, ok, err := s.store.MLLearnedOne(ctx, chstore.MLLearnedKindProfile, name)
	if err != nil {
		writeErr(w, err)
		return
	}
	cur := ml.ServiceProfileFromRow(prev)
	if !ok || !cur.Pinned {
		writeJSONError(w, http.StatusConflict, "servisin sabitlenmiş profili yok")
		return
	}
	who := ""
	if c := auth.FromContext(ctx); c != nil {
		who = c.Email
	}
	row := ml.UnpinProfileRow(name, prev, who, time.Now())
	if err := s.store.InsertMLLearned(ctx, []chstore.MLLearnedRow{row}); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(ctx, "ml:")
	after := ml.ServiceProfileFromRow(row)
	details, _ := json.Marshal(map[string]any{"before": cur.Profile, "after": after.Profile, "rev": row.Rev})
	s.audit(r, "ml.profile.unpin", "ml_profile", name, string(details))
	writeJSON(w, after)
}
