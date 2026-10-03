package api

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// ── Anomaly autocomplete (Cmd-K palette) ──────────────────────────

// listActiveAnomalies — autocomplete-shaped read for the v0.5.459
// Cmd-K "silence anomaly" action. Returns the most recent (last
// 24h) AnomalyEvents matching a typed substring on service or
// pattern. Reuses ListAnomalyEvents (200-row cap) and filters
// in-memory because the underlying CH query is already bounded;
// adding a separate WHERE-with-LIKE would just complicate the
// path with a marginal saving at this scale.
//
// Payload is slim: each row gives the palette enough to render
// (label) and the silence-create handler enough to act
// (id/fingerprint, kind, service, pattern).
//
// v0.9.1136 (A7) — viewer-readable. Eskiden createAnomalySilence'ın
// kapısını AYNALIYORDU (editor), ama silence YAZMASI zaten kendi
// başına editor-kapılı ve aynı satırlar /api/anomalies/events +
// MCP list_anomalies'ten kapısız görünüyordu: okuma kapısı hiçbir
// şey korumuyor, yalnız viewer'ın Cmd-K paletini bozuyordu
// (invariant #7: viewer state'i GÖRÜR). Gerekçe api.go'daki route
// satırında.
func (s *Server) listActiveAnomalies(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := parseInt(r.URL.Query().Get("limit"), 20)
	if limit > 50 {
		limit = 50
	}
	// Short cache — the palette polls only when the operator
	// types, so the working-set is small. 5s catches the burst
	// of keystrokes without serving stale "active" status to a
	// long-running session.
	key := fmt.Sprintf("anomalies-active:%s:%d", q, limit)
	s.serveCached(w, r, key, 5*time.Second, func(ctx context.Context) (any, error) {
		evts, err := s.store.ListAnomalyEvents(ctx,
			chstore.ListAnomalyEventsFilter{Limit: 200})
		if err != nil {
			return nil, err
		}
		type slim struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Pattern string `json:"pattern"`
			Service string `json:"service"`
			Status  string `json:"status"`
			Label   string `json:"label"`
		}
		out := []slim{}
		for _, e := range evts {
			if q != "" {
				hay := strings.ToLower(e.Service + " " + e.Pattern)
				if !strings.Contains(hay, q) {
					continue
				}
			}
			out = append(out, slim{
				ID:      e.ID,
				Kind:    e.Kind,
				Pattern: e.Pattern,
				Service: e.Service,
				Status:  e.Status,
				Label:   e.Service + " · " + e.Pattern,
			})
			if len(out) >= limit {
				break
			}
		}
		return out, nil
	})
}

// ── Anomaly silences ─────────────────────────────────────────────

func (s *Server) listAnomalySilences(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListActiveSilences(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, out)
}

func (s *Server) createAnomalySilence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fingerprint string `json:"fingerprint"`
		Kind        string `json:"kind"`
		Pattern     string `json:"pattern"`
		Service     string `json:"service"`
		Reason      string `json:"reason"`
		DurationSec int    `json:"durationSec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Fingerprint == "" || body.Kind == "" {
		http.Error(w, "fingerprint and kind required", http.StatusBadRequest)
		return
	}
	if body.DurationSec <= 0 {
		body.DurationSec = 60 * 60 // default 1h
	}
	now := time.Now()
	until := now.Add(time.Duration(body.DurationSec) * time.Second)

	claims := auth.FromContext(r.Context())
	id := newRandID(8)
	sil := chstore.AnomalySilence{
		ID:          id,
		Fingerprint: silenceFingerprint(body.Fingerprint, body.Kind, body.Pattern, body.Service),
		Kind:        body.Kind,
		Pattern:     body.Pattern,
		Service:     body.Service,
		CreatedBy:   claimEmail(claims),
		CreatedAt:   now.UnixNano(),
		UntilAt:     until.UnixNano(),
		Reason:      body.Reason,
	}
	if err := s.store.UpsertAnomalySilence(r.Context(), sil); err != nil {
		writeErr(w, err)
		return
	}
	// v0.9.234 — the silence set is an INPUT to the cached anomaly reads
	// (both closures call ActiveSilencedFingerprints and filter on it) but
	// rides neither the key nor an invalidation, so a just-muted anomaly
	// kept showing — or a just-unmuted one stayed hidden — for the TTL
	// window. Topology solves the same shape by hashing its hidden-pattern
	// set into the key; here a prefix drop is cheaper and mutations are rare.
	// v0.10.1042 — inbox listesi + rozeti de artık bu kümeyi okuyor:
	// invalidateSilenceReaders ikisini birden düşürür.
	s.invalidateSilenceReaders(r.Context())
	s.audit(r, "anomaly_silence.create", "anomaly_silence", id,
		fmt.Sprintf(`{"fp":%q,"kind":%q,"service":%q,"durationSec":%d,"reason":%q}`,
			body.Fingerprint, body.Kind, body.Service, body.DurationSec, body.Reason))
	sil.Active = true
	writeJSON(w, sil)
}

func (s *Server) deleteAnomalySilence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteAnomalySilence(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.invalidateSilenceReaders(r.Context()) // v0.9.234 + v0.10.1042 (inbox)
	s.audit(r, "anomaly_silence.delete", "anomaly_silence", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// bulkDeleteAnomalySilences mirrors the v0.5.83 problem bulk-ack
// shape: POST a body of ids, get back a count. Lets an operator
// clean up an overnight storm of silences in one click instead of
// hitting × on each chip. One audit entry per call with the id
// list in details.
func (s *Server) bulkDeleteAnomalySilences(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body.IDs) == 0 {
		http.Error(w, "ids list required", http.StatusBadRequest)
		return
	}
	if len(body.IDs) > 200 {
		http.Error(w, "max 200 ids per bulk delete", http.StatusBadRequest)
		return
	}
	n, err := s.store.DeleteAnomalySilences(r.Context(), body.IDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"ids": body.IDs, "deleted": n})
	s.invalidateSilenceReaders(r.Context()) // v0.9.234 + v0.10.1042 (inbox)
	s.audit(r, "anomaly_silence.bulk_delete", "anomaly_silence", "", string(details))
	writeJSON(w, map[string]any{"deleted": n})
}

// invalidateSilenceReaders — susturma kümesini OKUYAN her önbellekli yüzeyi
// düşürür; üç yazım ucu (oluştur / sil / toplu sil) yazım commit olduktan
// hemen sonra çağırır.
//
// v0.10.1042 (operatör: "Anomalide 'Mute' sonrası satır listeden düşsün") —
// inbox listesi (inbox:v10:…) ve rozeti (inbox:count:…) artık susturmaları
// okuyor ama 15 sn önbellekli (+SWR 45 sn). Ack / exception durumu /
// incident yazımlarının emsali AYNEN: açık önek düşürme (inboxListCachePrefix,
// sürümsüz — v0.9.321), anahtara susturma özeti DEĞİL. Özet her cache
// isabetinde bir CH okuması demek olurdu (anahtar serveCached'in dışında
// kuruluyor; inboxListKey'in alias gerekçesiyle aynı); yazım seyrek, düşürme
// ucuz. Ön yüz `['inbox']` sorgularını mute sonrası tazeliyor — bir sonraki
// istek mute öncesi gövdeyi önbellekten alamaz.
func (s *Server) invalidateSilenceReaders(ctx context.Context) {
	s.cacheInvalidatePrefix(ctx, "anomaly:")
	s.cacheInvalidatePrefix(ctx, inboxListCachePrefix)
}

// activeSilencedAnomalies — aktif susturmaların parmak izi kümesi; API
// tarafındaki TEK okuma kapısı (v0.10.1042): inbox listesi ve rozeti
// derleme başına bir kez, /anomalies canlı uçları (trace-ops, log-patterns)
// istek başına bir kez çağırır — satır başına asla. ActiveSilencedFingerprints
// `until_at > now64()` ile süzer, yani süresi dolan susturma bir sonraki
// derlemede satırı geri getirir — ek iş yok.
//
// Yumuşak-hata yönü BİLİNÇLİ: okunamayan süzgeç süzmez. Hata → nil → hiçbir
// satır gizlenmez, rozet süzgeçsiz sayar (evaluator.go "promoting unfiltered"
// ile aynı yön: en kötü durum operatörün zaten bildiği gürültü, kaybolan bir
// satır değil). Çağrı başına tek log satırı — canlı uçlar eskiden hatayı
// `muted, _ :=` ile sessizce yutuyordu.
func (s *Server) activeSilencedAnomalies(ctx context.Context, where string) map[string]bool {
	muted, err := s.store.ActiveSilencedFingerprints(ctx)
	if err != nil {
		log.Printf("[anomaly-silence] %s: silence list unreadable (nothing hidden): %v", where, err)
		return nil
	}
	return muted
}

// ── Audit log ─────────────────────────────────────────────────────

func (s *Server) listAuditLog(w http.ResponseWriter, r *http.Request) {
	claims := auth.FromContext(r.Context())
	if claims == nil || claims.Role != auth.RoleAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	since := parseDuration(q.Get("since"), 24*time.Hour)
	rows, err := s.store.ListAuditLog(r.Context(), chstore.AuditFilter{
		SinceNs:    time.Now().Add(-since).UnixNano(),
		Actor:      strings.TrimSpace(q.Get("actor")),
		Action:     strings.TrimSpace(q.Get("action")),
		TargetKind: strings.TrimSpace(q.Get("target")),
		TargetID:   strings.TrimSpace(q.Get("targetId")),
		Limit:      parseInt(q.Get("limit"), 200),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rows)
}

// exportAuditLog streams the audit log as CSV using the same
// filter shape as the JSON list endpoint. Limit is bumped to 50k
// so a quarterly compliance pull (~SOC2 audit window) lands in
// one shot. Filename embeds the since param + UTC date so the
// downloaded file is self-describing on a reviewer's disk.
func (s *Server) exportAuditLog(w http.ResponseWriter, r *http.Request) {
	claims := auth.FromContext(r.Context())
	if claims == nil || claims.Role != auth.RoleAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	since := parseDuration(q.Get("since"), 24*time.Hour)
	rows, err := s.store.ListAuditLog(r.Context(), chstore.AuditFilter{
		SinceNs:    time.Now().Add(-since).UnixNano(),
		Actor:      strings.TrimSpace(q.Get("actor")),
		Action:     strings.TrimSpace(q.Get("action")),
		TargetKind: strings.TrimSpace(q.Get("target")),
		Limit:      parseInt(q.Get("limit"), 50000),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	filename := fmt.Sprintf("coremetry-audit-%s-%s.csv",
		strings.ReplaceAll(time.Now().UTC().Format(time.DateOnly), "-", ""),
		strings.ReplaceAll(q.Get("since"), " ", ""))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"time", "actor_email", "actor_role", "action", "target_kind", "target_id", "ip", "details"})
	for _, e := range rows {
		_ = cw.Write([]string{
			time.Unix(0, e.Time).UTC().Format(time.RFC3339),
			e.ActorEmail, e.ActorRole, e.Action,
			e.TargetKind, e.TargetID, e.IP, e.Details,
		})
	}
}

// audit is a thin helper called from mutation handlers. v0.5.339
// (async writer): pushes the entry onto a buffered channel; the
// background drainer batches them into CH every 200ms or when
// the channel hits 64 entries — whichever fires first. Removes
// the per-mutation goroutine + single-row INSERT cost that
// bottlenecked high-rate admin scripts (e.g. bulk alert-rule
// import). Channel-full = log+drop; audit is best-effort by
// design (CLAUDE.md "Admin write = audit entry" promises a row
// per mutation, but never at the cost of blocking the response).
func (s *Server) audit(r *http.Request, action, kind, targetID, details string) {
	claims := auth.FromContext(r.Context())
	if claims == nil {
		return
	}
	s.auditAs(claims, clientIP(r), action, kind, targetID, details)
}

// auditAs — v0.10.979 — audit'in *http.Request'siz gövdesi: aktör ve IP
// istekten ÖNCEDEN alınmış (Rollouts v2 sorgu paketi 202 döndükten sonra
// arka plan goroutine'inde biten koşunun satırını yazar; r.Context() o
// sırada ölmüştür). Davranış aynı: kanal → drainer; kanal yoksa senkron
// yedek; doluysa log+drop sayacı.
func (s *Server) auditAs(claims *auth.Claims, ip, action, kind, targetID, details string) {
	if claims == nil {
		return
	}
	entry := chstore.AuditEntry{
		Time:       time.Now().UnixNano(),
		ActorID:    claims.UserID,
		ActorEmail: claims.Email,
		ActorRole:  claims.Role,
		Action:     action,
		TargetKind: kind,
		TargetID:   targetID,
		IP:         ip,
		Details:    details,
	}
	if s.auditQ == nil {
		// Drainer not started yet (very early boot path) — fall
		// back to the synchronous goroutine so we never silently
		// drop an audit row that fired before main wired the
		// drainer.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.store.AppendAudit(ctx, entry)
		}()
		return
	}
	select {
	case s.auditQ <- entry:
	default:
		// Channel saturated — the drainer is behind on CH. Log
		// once and drop; the operator sees the drop counter
		// climb on /admin/stats.
		s.auditDropCount.Add(1)
	}
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// Trust the first hop only — anything past that is
		// caller-controlled and useful for forensics but not
		// authoritative.
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}

func claimEmail(c *auth.Claims) string {
	if c == nil {
		return ""
	}
	return c.Email
}

// ── Saved views ──────────────────────────────────────────────────

// listSavedViews — v0.10.1024 notu: okuma bilerek değişmedi. FE her çağrıda
// sabit bir page gönderir (SavedViewsBar sayfaları, alert-template,
// dashboard-star), sistem sayfası satırı Topbar listesine düşmez; page'siz
// doğrudan çağrının döndürdüğü "pv:" satırları da GET /api/problem-verdicts'te
// zaten her role açık. Yazma / silme sistem sayfalarına kapalı (aşağıda).
func (s *Server) listSavedViews(w http.ResponseWriter, r *http.Request) {
	claims := auth.FromContext(r.Context())
	page := strings.TrimSpace(r.URL.Query().Get("page"))
	owner := ""
	if claims != nil {
		owner = claims.UserID
	}
	out, err := s.store.ListSavedViews(r.Context(), owner, page)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, out)
}

func (s *Server) createSavedView(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Page        string `json:"page"`
		QueryString string `json:"queryString"`
		Pinned      bool   `json:"pinned"`
		Shared      bool   `json:"shared"` // admin-only knob
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Page = strings.TrimSpace(body.Page)
	if body.Name == "" || body.Page == "" {
		http.Error(w, "name and page required", http.StatusBadRequest)
		return
	}
	// v0.10.1024 — sistem sayfası depoya ve denetime DOKUNMADAN 400 (her rol).
	if msg := savedViewCreateRejection(body.Page); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	claims := auth.FromContext(r.Context())
	owner := ""
	if claims != nil {
		owner = claims.UserID
	}
	// Team-shared views are admin-gated to keep accidental
	// "pin everyone's quirky filter" out of the topbar.
	if body.Shared {
		if claims == nil || claims.Role != auth.RoleAdmin {
			http.Error(w, "admin only for shared views", http.StatusForbidden)
			return
		}
		owner = ""
	}

	v := chstore.SavedView{
		ID:          newRandID(6),
		OwnerID:     owner,
		Name:        body.Name,
		Page:        body.Page,
		QueryString: body.QueryString,
		Pinned:      body.Pinned,
		CreatedAt:   time.Now().UnixNano(),
	}
	if err := s.store.UpsertSavedView(r.Context(), v); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "saved_view.create", "saved_view", v.ID,
		fmt.Sprintf(`{"page":%q,"name":%q,"shared":%t}`, v.Page, v.Name, body.Shared))
	writeJSON(w, v)
}

func (s *Server) deleteSavedView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	// Authorisation: you can only delete your own non-shared
	// views unless you're admin.
	cur, err := s.store.GetSavedView(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cur == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if code, msg := savedViewDeleteRejection(*cur, auth.FromContext(r.Context())); code != 0 {
		http.Error(w, msg, code)
		return
	}
	if err := s.store.DeleteSavedView(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "saved_view.delete", "saved_view", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// savedViewCreateRejection — v0.10.1024 — SAF: POST /api/views'in yazamayacağı
// page değeri için hata metni ("" = yazılabilir). Kusur (kod incelemesi, prod'da
// gözlenmedi): uç rol kapısız ve her page'i kabul ediyordu; viewer
// page="problem-verdict", name="noise" ve elle kurulmuş bir gövdeyle ekip
// kararı taklit edebiliyordu — PUT /api/problem-verdicts'in editor kapısı ve
// "problem.verdict" denetimi atlanıyor, susturma politikası açıksa (v0.10.1016)
// o imzanın bildirimi de susuyordu. Sistem sayfaları (chstore.
// IsSystemSavedViewPage) yalnız kendi uçlarından yazılır; ret depoya ve
// denetime dokunmadan, admin dahil her rol için.
func savedViewCreateRejection(page string) string {
	if chstore.IsSystemSavedViewPage(page) {
		return fmt.Sprintf("reserved page %q: sistem durumudur, genel kayıtlı-görünüm ucundan yazılamaz (kendi ucunu kullanın)", page)
	}
	return ""
}

// savedViewDeleteRejection — v0.10.1024 — SAF: DELETE /api/views/{id} için
// yetki kararı (code 0 = silinebilir). Sahiplik kuralı aynen korunur: admin her
// görünümü siler; diğerleri yalnız kendi paylaşımsız görünümünü (paylaşımlı,
// yani owner_id boş satır → 403).
//
// Sistem sayfası satırı ise ADMİN DAHİL 403: "pv:" karar satırlarında owner_id
// boş olduğundan viewer/editor zaten 403 alıyordu, ama admin bu uçtan
// gerçek bir ekip kararını mezar taşıyla silebiliyordu — "problem.verdict"
// denetimi yerine "saved_view.delete" düşüyor, karar okuma önbelleği
// düşürülmüyordu. Karar PUT /api/problem-verdicts ile boş verdict göndererek
// kaldırılır (editor+, denetimli) — tek denetimli yol orası.
func savedViewDeleteRejection(cur chstore.SavedView, claims *auth.Claims) (int, string) {
	if chstore.IsSystemSavedViewPage(cur.Page) {
		return http.StatusForbidden, fmt.Sprintf("reserved page %q: sistem durumudur, genel kayıtlı-görünüm ucundan silinemez (kendi ucunu kullanın)", cur.Page)
	}
	switch {
	case claims != nil && claims.Role == auth.RoleAdmin:
		// admins can delete anything (except system pages, above)
	case cur.OwnerID == "" || (claims != nil && claims.UserID != cur.OwnerID):
		return http.StatusForbidden, "not your view"
	}
	return 0, ""
}

func newRandID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// silenceFingerprint — v0.10.162 (inceleme, ön-var kusur): UI susturmayı
// `kind|pattern|service` düz metniyle gönderiyordu, sunucu olduğu gibi
// saklıyordu; ama HER tüketici (getTraceOpAnomalies / getLogPatternAnomalies
// / evaluator promotion gate) sha1 parmak izini (chstore.FingerprintAnomaly)
// karşılaştırır — düz ≠ sha1, yani susturma listeyi gizliyor ama akışı ve
// Problem terfisini HİÇ susturmuyordu. Desen+servis varsa kanonik sha1
// yazılır; desensiz eski çağıranlar (Cmd-K vb.) gönderdiklerini korur.
// Frontend eşleyicisi (lib/anomalyRegions.ts isSilenced) iki yazımı da kabul
// eder — eski düz satırlar için köprü.
//
// v0.10.1042 (operatör: "Anomalide 'Mute' sonrası satır listeden düşsün") —
// KURAL: bir OLAY için yazılan susturma O OLAYLA eşleşir. Yeniden hesaplama
// yalnız olay kimliği desenden türeyen türlerde kimliği verir (log_pattern,
// trace_op, trace_op_latency, elastic_ml). log_template_new kimliği şablon
// kimliğinden (recorder.go), behavior_change ham metrik adından (behavior.go
// behaviorEventID) türer; desen ise görüntü metnidir — yeniden hesaplanan
// sha1 o olayın kimliği DEĞİLDİR ve susturma hiçbir okuyucuyla (inbox,
// evaluator terfisi, /anomalies) eşleşmiyordu. Sıra:
//  1. raw, FingerprintAnomaly ŞEKLİNDE bir olay kimliğiyse (detay sayfası,
//     inbox çekmecesi, servis sayfası, Cmd-K hepsi olay kimliğini gönderir)
//     olduğu gibi saklanır;
//  2. değilse desen+servis varsa kanonik sha1 (/anomalies canlı akış satırı:
//     olay değil, `kind|pattern|service` gönderir — o türlerde sha1 = kimlik);
//  3. değilse raw (v0.10.162'nin desensiz eski çağıran köprüsü).
//
// Biçimi bozuk değer asla kimlik sayılmaz (chstore.IsAnomalyFingerprint).
// SAF; tablo testi anomaly_silence_fp_test.go.
func silenceFingerprint(raw, kind, pattern, service string) string {
	if chstore.IsAnomalyFingerprint(raw) {
		return raw
	}
	if pattern != "" && service != "" {
		return chstore.FingerprintAnomaly(kind, pattern, service)
	}
	return raw
}
