package notify

// sre_summary.go — CoSRE Faz C1 (operatör onayı 2026-11-21): Problem
// bildirimine AI SRE araştırmasının kısa özeti.
//
// Araştırma bildirim anında BİTMİŞSE özet bloğu mesajın içine girer (bütün
// kanallar; webhook hariç — gövde operatörün şablonu, sözleşme). Bitmemişse
// bildirim bugünkü gibi gider, gönderilen mesajların künyesi saklanır
// (chstore.NotifySREFollowUp) ve araştırma bitince sre_followup.go Zoom
// mesajını düzenler / diğer kanallara konuya tek kısa yanıt yollar.
//
// Kanıt kapısı: arayüzde gölgede olsa da bildirimde kararına UYULUR —
// "yetersiz" ise neden ASLA söylenmez, "Kanıt yetersiz" + eksikler yazılır
// (sohbet kanalında yanlış ama kendinden emin bir iddia geri alınamaz).
//
// Metin yalnız kod adlarından çizilir (hipotez türü, kontrol adı, kaynak
// adı, erişim durumu): ham log satırı / öznitelik değeri / kontrol gerekçesi
// GİRMEZ. Yeni LLM çağrısı yok; saklı kayıt okunur.
//
// notify sre'yi import edemez (sre → mcptools → … → notify); kayıt okuması
// main.go'da sre.NotifyLookup ile enjekte edilir.

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// InvestigationDigest — bildirime giren araştırma özeti (sre.Digest üretir).
type InvestigationDigest struct {
	InvestigationID string // saved_views satır kimliği ("sre-<problemId>"); rapor bağlantısı
	ProblemID       string
	State           string // queued | running | done | partial | error | skipped | shared
	Finished        bool   // done | partial
	Sufficient      bool   // kanıt kapısı kararı
	TopKind         string // yalnız Sufficient
	TopLabel        string // türün Türkçe adı (kayıttan)
	TeamConfirmed   bool   // top'u ekip doğruladı → "(ekip doğruladı)"
	Evidence        []DigestEvidence
	Missing         []DigestGap
	Blind           []DigestReach
}

// DigestEvidence — en olası hipotezi doğrulayan bir kontrol.
type DigestEvidence struct {
	Check  string // kontrol adı (hypothesisTable)
	Source string // adım adı
}

// DigestGap — kapının eksik kalemi (sre.GateGap aynası).
type DigestGap struct {
	Code, Source, Status string
}

// DigestReach — kör nokta (bakılamayan kaynak).
type DigestReach struct {
	Source, Status string
}

// InvestigationInfo — kayıt okumasının sonucu.
type InvestigationInfo struct {
	// Enabled — sre_agent açık VE notifySummary açık.
	Enabled bool
	// AutoPriorities — otomatik kuyruğa alınan öncelikler (kayıt henüz
	// yokken "araştırma gelecek mi" kararı).
	AutoPriorities []string
	Digest         *InvestigationDigest // kayıt yoksa nil
}

// InvestigationLookup — problemin araştırma özetini okur.
type InvestigationLookup func(ctx context.Context, problemID string) (InvestigationInfo, error)

// sreWriteTimeout — künye okuma/yazma tavanı (gönderimi bekletmez).
const sreWriteTimeout = 5 * time.Second

func firstNonEmptyStr(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// sreSummary — Notifier'ın C1 durumu.
type sreSummary struct {
	mu     sync.RWMutex
	lookup InvestigationLookup
}

// SetInvestigationLookup — main.go bağlar (nil = özellik yok).
func (n *Notifier) SetInvestigationLookup(f InvestigationLookup) {
	n.sreSum.mu.Lock()
	n.sreSum.lookup = f
	n.sreSum.mu.Unlock()
}

func (n *Notifier) investigationLookup() InvestigationLookup {
	n.sreSum.mu.RLock()
	defer n.sreSum.mu.RUnlock()
	return n.sreSum.lookup
}

// ── ctx taşıyıcıları ────────────────────────────────────────────────────

type sreDigestKey struct{}
type sreSinkKey struct{}

// sreSink — bu turda başarıyla giden kanal mesajları (künye kaydı için).
type sreSink struct {
	mu    sync.Mutex
	items []chstore.NotifySREFollowUpItem
}

func (s *sreSink) upsert(it chstore.NotifySREFollowUpItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ChannelID == it.ChannelID {
			if it.MessageID != "" {
				s.items[i].MessageID, s.items[i].Text = it.MessageID, it.Text
			}
			return
		}
	}
	s.items = append(s.items, it)
}

func (s *sreSink) snapshot() []chstore.NotifySREFollowUpItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]chstore.NotifySREFollowUpItem(nil), s.items...)
}

func sreDigestOf(ctx context.Context) *InvestigationDigest {
	d, _ := ctx.Value(sreDigestKey{}).(*InvestigationDigest)
	return d
}

func sreSinkOf(ctx context.Context) *sreSink {
	s, _ := ctx.Value(sreSinkKey{}).(*sreSink)
	return s
}

// followUpChannel — araştırma sonradan bitince güncellenebilen kanal türü.
// webhook (operatör şablonu) ve whatsapp (sayfa kanalı, ek mesaj = yeni
// bildirim) yalnız gönderim anında bitmiş özeti taşır.
func followUpChannel(typ string) bool {
	switch typ {
	case "zoomchat", "slack", "mattermost", "teams", "email":
		return true
	}
	return false
}

// awaitInvestigation — SAF: kayıt henüz bitmemişken araştırmanın gelmesi
// bekleniyor mu. Kayıt varsa durumundan; yoksa önceliği otomatik listede mi.
func awaitInvestigation(info InvestigationInfo, p chstore.Problem) bool {
	if !info.Enabled || p.Status != "open" {
		return false
	}
	if d := info.Digest; d != nil {
		return d.State == "queued" || d.State == "running"
	}
	pr := strings.ToUpper(strings.TrimSpace(p.Priority))
	for _, a := range info.AutoPriorities {
		if pr != "" && a == pr {
			return true
		}
	}
	return false
}

// attachInvestigation — SendProblemAlert'te kanal döngüsünden önce BİR kez:
// bitmiş özet ctx'e (kanallar bloğu çizer); bekleniyorsa künye toplayıcı.
func (n *Notifier) attachInvestigation(ctx context.Context, p chstore.Problem) context.Context {
	lookup := n.investigationLookup()
	if lookup == nil || p.ID == "" {
		return ctx
	}
	info, err := lookup(ctx, p.ID)
	if err != nil || !info.Enabled {
		return ctx
	}
	if d := info.Digest; d != nil && d.Finished {
		return context.WithValue(ctx, sreDigestKey{}, d)
	}
	if awaitInvestigation(info, p) {
		return context.WithValue(ctx, sreSinkKey{}, &sreSink{})
	}
	return ctx
}

// noteSent — sendOne başarılı gönderimden sonra çağırır.
func noteSent(ctx context.Context, c chstore.NotificationChannel, err error) {
	s := sreSinkOf(ctx)
	if s == nil || err != nil || !followUpChannel(c.Type) {
		return
	}
	s.upsert(chstore.NotifySREFollowUpItem{ChannelID: c.ID, Type: c.Type})
}

// noteZoomMessage — sendZoomChat mesaj kimliğini bildirir.
func noteZoomMessage(ctx context.Context, c chstore.NotificationChannel, msgID, text string) {
	if s := sreSinkOf(ctx); s != nil && msgID != "" {
		s.upsert(chstore.NotifySREFollowUpItem{ChannelID: c.ID, Type: c.Type, MessageID: msgID, Text: text})
	}
}

// queueInvestigationFollowUp — kanal döngüsünden sonra: toplanan künyeleri
// yazar (aynı problemin önceki bekleyen kaydıyla birleşir).
func (n *Notifier) queueInvestigationFollowUp(ctx context.Context, p chstore.Problem) {
	s := sreSinkOf(ctx)
	if s == nil || n.store == nil {
		return
	}
	items := s.snapshot()
	if len(items) == 0 {
		return
	}
	rec := chstore.NotifySREFollowUp{ProblemID: p.ID, Lang: "en", Title: alertTitleCtx(ctx, p), Subject: alertTitle(p)}
	if v := alertViewOf(ctx); v != nil {
		rec.Lang, rec.Subject, rec.ThreadRoot = "tr", trEmailSubject(v), threadMessageRoot(v.Thread)
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sreWriteTimeout)
	defer cancel()
	if prev, err := n.store.GetNotifySREFollowUp(wctx, p.ID); err == nil && prev != nil {
		rec.CreatedAt = prev.CreatedAt
		rec.Items = mergeFollowUpItems(prev.Items, items)
	} else {
		rec.Items = items
	}
	if err := n.store.PutNotifySREFollowUp(wctx, rec); err != nil {
		log.Printf("[notify] CoSRE özet künyesi yazılamadı (%s): %v", p.ID, err)
	}
}

// mergeFollowUpItems — SAF: kanal başına en yeni künye kalır.
func mergeFollowUpItems(prev, next []chstore.NotifySREFollowUpItem) []chstore.NotifySREFollowUpItem {
	out := append([]chstore.NotifySREFollowUpItem(nil), prev...)
	for _, it := range next {
		found := false
		for i := range out {
			if out[i].ChannelID == it.ChannelID {
				out[i], found = it, true
				break
			}
		}
		if !found {
			out = append(out, it)
		}
	}
	return out
}

// ── metin ───────────────────────────────────────────────────────────────

// reportURL — rapor sayfası (public URL yoksa boş).
func (n *Notifier) reportURL(d *InvestigationDigest) string {
	base := n.PublicURL()
	if base == "" || d == nil || d.InvestigationID == "" {
		return ""
	}
	return base + "/sre/report/" + url.PathEscape(d.InvestigationID)
}

var hypKindEN = map[string]string{
	"deploy": "Deploy", "config": "Configuration change", "saturation": "Saturation / OOM",
	"dependency": "Dependency failure", "external": "External service outage", "db_slow": "Database slowness",
	"traffic_surge": "Traffic surge", "code_regression": "Code regression / new exception",
}

// checkNames — kontrol adı → [TR, EN] (FE hyp.chk.* aynası).
var checkNames = map[string][2]string{
	"deploy_near":             {"Başlangıca yakın deploy", "Deploy near the start"},
	"deploy_before_error":     {"İlk hatadan önce deploy", "Deploy before the first error"},
	"recurring_before_deploy": {"Deploy öncesinde de tekrarlıyor", "Recurred before the deploy"},
	"rollout_failed":          {"Rollout başarısız", "Rollout failed"},
	"change_without_image":    {"İmaj değişmeden değişiklik", "Change without a new image"},
	"change_before_error":     {"İlk hatadan önce değişiklik", "Change before the first error"},
	"argo_sync":               {"Argo CD senkronu", "Argo CD sync"},
	"pod_limits":              {"Pod limitleri / OOMKilled", "Pod limits / OOMKilled"},
	"jvm_post_gc":             {"JVM post-GC heap", "JVM post-GC heap"},
	"runtime_signals":         {"Runtime sinyalleri (thread pool / GC / havuzlar)", "Runtime signals (thread pool / GC / pools)"},
	"node_pressure":           {"Node baskısı", "Node pressure"},
	"latency_symptom":         {"Gecikme belirtisi", "Latency symptom"},
	"downstream_worse":        {"Aşağı akış kötüleşti", "Downstream got worse"},
	"synth_agrees":            {"Sentezleyici aynı yönde", "Synthesizer agrees"},
	"downstream_errors":       {"Aşağı akış hataları", "Downstream errors"},
	"external_problem_open":   {"Dış kaynak problemi açık", "External problem open"},
	"external_edge_errors":    {"Dış çağrı hataları", "External call errors"},
	"db_health_open":          {"Veritabanı problemi açık", "Database problem open"},
	"pivot_db_path":           {"Kritik yolda DB", "DB on the critical path"},
	"db_edge_slow":            {"Yavaş veritabanı çağrıları", "Slow database calls"},
	"rate_up":                 {"İstek hızı arttı", "Request rate up"},
	"p99_up":                  {"p99 arttı", "p99 up"},
	"upstream_surge":          {"Çağıran trafiği arttı", "Caller traffic surge"},
	"new_error_pattern":       {"Yeni hata deseni", "New error pattern"},
	"after_deploy":            {"Hatalar deploy sonrasında", "After a deploy"},
	"pivot_error_spans":       {"Trace'te hatalı span", "Error spans in the trace"},
	"error_symptom":           {"Hata belirtisi", "Error symptom"},
	// v0.10.2477 — Problem grafı kökeni (sre known.go).
	"graph_origin_rollout":   {"Problem grafı: deploy sonrası başladı", "Problem graph: started after a deploy"},
	"graph_origin_elsewhere": {"Problem grafı: köken başka varlık", "Problem graph: origin is another entity"},
	"graph_origin_external":  {"Problem grafı: köken dış servis", "Problem graph: origin is an external service"},
	"graph_origin_db":        {"Problem grafı: köken veritabanı", "Problem graph: origin is a database"},
}

// sourceNames — adım adı → [TR, EN] (FE inv.step.* aynası).
var sourceNames = map[string][2]string{
	"sweep": {"Servis taraması", "Service sweep"}, "deploys": {"Deploy", "Deploy"},
	"deps": {"Bağımlılık", "Dependency"}, "pivot": {"Trace pivotu", "Trace pivot"},
	"log_spike": {"Log artışı", "Log spike"}, "events": {"Olaylar", "Events"},
	"node": {"Node baskısı", "Node pressure"}, "errors": {"Hata özeti", "Error digest"},
	"baseline": {"Taban çizgisi", "Baseline"}, "anomalies": {"Anomaliler", "Anomalies"},
	"graph_origin": {"Problem grafı kökeni", "Problem graph origin"}, "impact": {"Etki olayları", "Impact events"}, // v0.10.2477
	"similar": {"Benzer onaylı vakalar", "Similar confirmed cases"},
}

// reachNames — erişim durumu → [TR, EN] (FE iq.reach.* aynası).
var reachNames = map[string][2]string{
	"partial": {"kısmi", "partial"}, "no_data": {"veri yok", "no data"},
	"not_configured": {"yapılandırılmamış", "not configured"}, "timed_out": {"zaman aşımı", "timed out"},
	"error": {"okunamadı", "read failed"}, "not_run": {"planlanmadı", "not planned"},
}

func pick(m map[string][2]string, k string, tr bool) string {
	v, ok := m[k]
	if !ok {
		return k
	}
	if tr {
		return v[0]
	}
	return v[1]
}

func trEN(tr bool, a, b string) string {
	if tr {
		return a
	}
	return b
}

// gapLine — SAF: eksik kalemin metni.
func gapLine(g DigestGap, tr bool) string {
	switch g.Code {
	case "no_supported":
		return trEN(tr, "desteklenen hipotez yok", "no supported hypothesis")
	case "single_source":
		s := trEN(tr, "künyesiz", "no reference")
		if g.Source != "" {
			s = pick(sourceNames, g.Source, tr)
		}
		return trEN(tr, "en olası hipotez tek kaynağa dayanıyor (", "the most likely hypothesis rests on a single source (") + s + ")"
	case "blind_source":
		return pick(sourceNames, g.Source, tr) + trEN(tr, " bakılamadı (", " not checked (") + pick(reachNames, g.Status, tr) + ")"
	}
	return g.Code
}

// sreBlockLines — SAF: özet bloğunun satırları (ilk satır başlık).
func sreBlockLines(d *InvestigationDigest, tr bool, report string) []string {
	if d == nil || !d.Finished {
		return nil
	}
	out := []string{trEN(tr, "CoSRE araştırması", "CoSRE investigation")}
	if d.Sufficient && d.TopKind != "" {
		name := firstNonEmptyStr(d.TopLabel, d.TopKind)
		if en, ok := hypKindEN[d.TopKind]; ok && !tr {
			name = en
		}
		if d.TeamConfirmed {
			out = append(out, trEN(tr, "Neden: ", "Cause: ")+name+trEN(tr, " (ekip doğruladı)", " (confirmed by the team)"))
		} else {
			out = append(out, trEN(tr, "En olası neden: ", "Most likely cause: ")+name)
		}
		for _, e := range d.Evidence {
			line := trEN(tr, "Kanıt: ", "Evidence: ") + pick(checkNames, e.Check, tr)
			if e.Source != "" {
				line += " (" + pick(sourceNames, e.Source, tr) + ")"
			}
			out = append(out, line)
		}
	} else {
		out = append(out, trEN(tr, "Kanıt yetersiz — kök neden belirtilmedi", "Insufficient evidence — no root cause stated"))
		var gs []string
		for _, g := range d.Missing {
			if g.Code == "blind_source" {
				continue // kör noktalar satırında
			}
			gs = append(gs, gapLine(g, tr))
		}
		if len(gs) > 0 {
			out = append(out, trEN(tr, "Eksik: ", "Missing: ")+strings.Join(gs, "; "))
		}
	}
	if len(d.Blind) > 0 {
		bs := make([]string, 0, len(d.Blind))
		for _, b := range d.Blind {
			bs = append(bs, pick(sourceNames, b.Source, tr)+" ("+pick(reachNames, b.Status, tr)+")")
		}
		out = append(out, trEN(tr, "Kör noktalar: ", "Blind spots: ")+strings.Join(bs, ", "))
	}
	if report != "" {
		out = append(out, trEN(tr, "Rapor: ", "Report: ")+report)
	}
	return out
}

// sreBlockText — SAF: düz metin blok ("\n\n" ile başlar; yoksa boş).
func sreBlockText(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return "\n\n" + lines[0] + "\n• " + strings.Join(lines[1:], "\n• ")
}

// sreBlockHTML — SAF: e-posta HTML bloğu.
func sreBlockHTML(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div style="margin:16px;padding:12px;border:1px solid #d1d5db;border-radius:6px;font-family:Arial,sans-serif;font-size:13px;color:#111827">`)
	fmt.Fprintf(&b, "<b>%s</b>", html.EscapeString(lines[0]))
	for _, l := range lines[1:] {
		fmt.Fprintf(&b, "<br>• %s", html.EscapeString(l))
	}
	b.WriteString("</div>")
	return b.String()
}

// ctxBlock — ctx'teki bitmiş özetin satırları (dil: Türkçe görünüm varsa TR).
func (n *Notifier) ctxBlock(ctx context.Context) []string {
	d := sreDigestOf(ctx)
	if d == nil {
		return nil
	}
	return sreBlockLines(d, alertViewOf(ctx) != nil, n.reportURL(d))
}

// withSREEmail — e-posta gövdelerine bloğu ekler.
func (n *Notifier) withSREEmail(ctx context.Context, plain, htm string) (string, string) {
	lines := n.ctxBlock(ctx)
	if len(lines) == 0 {
		return plain, htm
	}
	plain += sreBlockText(lines)
	block := sreBlockHTML(lines)
	if i := strings.LastIndex(htm, "</body>"); i >= 0 {
		htm = htm[:i] + block + htm[i:]
	} else {
		htm += block
	}
	return plain, htm
}

// withSRESlack — Slack / Mattermost gövdesine ek attachment.
func (n *Notifier) withSRESlack(ctx context.Context, body map[string]any) map[string]any {
	lines := n.ctxBlock(ctx)
	if len(lines) == 0 {
		return body
	}
	att, _ := body["attachments"].([]map[string]any)
	body["attachments"] = append(att, map[string]any{
		"color": "#6366f1", "title": lines[0], "text": "• " + strings.Join(lines[1:], "\n• "),
	})
	return body
}

// withSRETeams — Teams MessageCard'a ek bölüm.
func (n *Notifier) withSRETeams(ctx context.Context, body map[string]any) map[string]any {
	lines := n.ctxBlock(ctx)
	if len(lines) == 0 {
		return body
	}
	secs, _ := body["sections"].([]map[string]any)
	body["sections"] = append(secs, map[string]any{
		"activityTitle": lines[0], "text": "- " + strings.Join(lines[1:], "\n\n- "),
	})
	return body
}

// withSREText — Zoom / WhatsApp düz metnine blok.
func (n *Notifier) withSREText(ctx context.Context, text string) string {
	return text + sreBlockText(n.ctxBlock(ctx))
}
