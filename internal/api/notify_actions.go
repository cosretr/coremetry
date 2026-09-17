package api

// notify_actions.go — v0.10.749: bildirimdeki "Sustur" bağlantısının
// ucu (operatör: "alarm geldiğinde bu ignore edilebilsin; audit'e kimin
// ignore ettiği yazsın").
//
//	GET /api/public/notify/ignore/{token}
//
// PUBLIC (auth.SkipPath "/api/public/notify/"): e-posta alıcısının
// oturumu yok; sınır jeton sahipliği + 7 gün (auth.VerifyAction). Etki
// idempotent ve sınırlı: problem/incident ACK + notification_log'a
// "ignore" işareti (SendProblemAlert o kimlik için bir daha fan-out
// yapmaz) + audit "notify.ignore".
//
// KİM: s.audit kullanılmaz (claims nil → sessizce yazmaz); AppendAudit
// doğrudan. Kimlik merdiveni: tıklayanın Coremetry oturum çerezi (en
// güçlü, jetonun üstüne yazar) > jetondaki kimlik (e-posta alıcısı ya
// da "channel:<tür>/<ad>") — ikisi de audit details'te. api.go BÜYÜMEZ:
// route_registry defteri.

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("notify-actions", (*Server).registerNotifyActionRoutes) }

func (s *Server) registerNotifyActionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/public/notify/ignore/{token}", s.notifyIgnore)
}

func (s *Server) notifyIgnore(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeIgnorePage(w, http.StatusServiceUnavailable, "Kimlik servisi hazır değil", "", "", "")
		return
	}
	claims, err := s.auth.VerifyAction(r.PathValue("token"), time.Now())
	if err != nil {
		msg := "Bağlantı geçersiz"
		if err == auth.ErrActionExpired {
			msg = "Bağlantının süresi dolmuş (7 gün)"
		}
		writeIgnorePage(w, http.StatusGone, msg, "", "", "")
		return
	}
	// Kimlik: oturum çerezi > jeton.
	actorEmail, actorID, actorRole, session := claims.Who, "", "", ""
	if ck, err := r.Cookie(auth.CookieName); err == nil && ck.Value != "" {
		if cl, err := s.auth.Parse(ck.Value); err == nil && cl != nil && cl.Email != "" {
			actorEmail, actorID, actorRole, session = cl.Email, cl.UserID, cl.Role, cl.Email
		}
	}
	ctx := r.Context()
	title, openPath := "", ""
	switch claims.Kind {
	case chstore.NotifyKindProblem:
		if _, err := s.store.AcknowledgeProblems(ctx, []string{claims.ID}, actorEmail); err != nil {
			writeErr(w, err)
			return
		}
		if p, err := s.store.GetProblem(ctx, claims.ID); err == nil && p != nil {
			title = strings.TrimSpace(p.Service + " · " + p.RuleName)
		}
		openPath = "/problems?problem=" + claims.ID
	case chstore.NotifyKindIncident:
		inc, err := s.store.GetIncident(ctx, claims.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		if inc == nil {
			writeIgnorePage(w, http.StatusNotFound, "Incident bulunamadı", "", "", "")
			return
		}
		title = inc.Title
		openPath = "/incident?id=" + claims.ID
		if inc.Status == "open" {
			now := time.Now().UnixNano()
			inc.Status = "acknowledged"
			inc.AckAt = &now
			if err := s.store.UpsertIncident(ctx, inc); err != nil {
				writeErr(w, err)
				return
			}
			_ = s.store.AppendIncidentEvent(ctx, chstore.IncidentEvent{
				IncidentID: inc.ID, Kind: "ack", Actor: actorEmail, Body: "Ignored via notification link",
			})
		}
	default:
		writeIgnorePage(w, http.StatusBadRequest, "Bilinmeyen eylem", "", "", "")
		return
	}
	ip := clientIP(r)
	if err := s.store.MarkNotificationIgnored(ctx, claims.Kind, claims.ID, actorEmail, ip); err != nil {
		log.Printf("[notify] ignore marker %s/%s: %v", claims.Kind, claims.ID, err)
	}
	details, _ := json.Marshal(map[string]any{
		"via": "notification-link", "tokenWho": claims.Who, "session": session, "kind": claims.Kind,
	})
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.store.AppendAudit(actx, chstore.AuditEntry{
		Time: time.Now().UnixNano(), ActorID: actorID, ActorEmail: actorEmail, ActorRole: actorRole,
		Action: "notify.ignore", TargetKind: claims.Kind, TargetID: claims.ID, IP: ip, Details: string(details),
	}); err != nil {
		log.Printf("[notify] ignore audit %s/%s: %v", claims.Kind, claims.ID, err)
	}
	s.invalidateInboxCaches(r)
	writeIgnorePage(w, http.StatusOK, "Susturuldu", title, actorEmail, openPath)
}

// writeIgnorePage — sunucu-çizimli küçük onay sayfası (SPA'sız; alıcı
// giriş yapmamış olabilir). Her değer kaçışlı.
func writeIgnorePage(w http.ResponseWriter, status int, headline, title, who, openPath string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="tr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Coremetry</title>` +
		`<style>body{font-family:system-ui,-apple-system,sans-serif;background:#0f172a;color:#e2e8f0;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}` +
		`main{max-width:480px;padding:32px;background:#1e293b;border-radius:12px}h1{font-size:20px;margin:0 0 8px}p{margin:6px 0;color:#94a3b8}a{color:#7dd3fc}</style></head><body><main>`)
	fmt.Fprintf(&b, "<h1>%s</h1>", html.EscapeString(headline))
	if title != "" {
		fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(title))
	}
	if who != "" {
		fmt.Fprintf(&b, "<p>Susturan: %s</p>", html.EscapeString(who))
	}
	if status == http.StatusOK {
		b.WriteString("<p>Bu problem/incident için bir daha bildirim gönderilmeyecek; Coremetry'de <b>acknowledged</b> görünür.</p>")
	}
	if openPath != "" {
		fmt.Fprintf(&b, `<p><a href="%s">Coremetry'de aç →</a></p>`, html.EscapeString(openPath))
	}
	b.WriteString(`</main></body></html>`)
	_, _ = io.WriteString(w, b.String())
}
