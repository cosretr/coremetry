package api

// chat_problem_scope.go — v0.10.2480 (operatör-bildirimi, prod v0.10.2478):
// Problem sayfasından açılan sohbetin Problem'e bağlı iki kararı.
//
// E — pencere: sohbetin zaman penceresi varsayılan olarak Problem'in KENDİ
// penceresidir (başlangıç − problemWindowMargin → bitiş, açıksa → şimdi);
// sayfada kalmış eski aralık ("son 15 dakika", 19:37'de biten) değil. İstemci
// Problem penceresi çipi açıkken `problemWindow:true` gönderir (çip ✕ ile
// kalkar → sayfanın aralığı geri gelir); sunucu pencereyi İSTEK ANINDA
// Problem satırından kurar (açık Problem'de "şimdi" kaymaz). Kullanıcı soruda
// pencere yazdıysa (durum çözücüsü WindowSet) ya da konuşmada açık bir
// pencere seçtiyse o kazanır.
//
// C — ilk bakılacak araç: Problem'in metriği / kural kimliği / açıklamasından
// (varlık modeli alanları; servis adı eşlemesi YOK) deterministik tür → sıralı
// kısa ipucu satırı ("önce messaging_flow"). Ön-getirme yerine ipucu seçildi:
// küçük yerel model için ön-getirme argümanı (topic / tüketici rolü) Problem
// satırından güvenle kurulamıyor, yanlış argümanlı bir okuma küçük bağlamı
// boşa doldurur; tek satır ipucu ≈30 token, son eklenen istem satırı olarak
// küçük modelin en çok uyduğu yerde durur ve bütçe artışı (A) + kayıttan
// karşılanan çağrılar (B) ile sıradaki araç artık tavana takılmaz.

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	// problemWindowMargin — pencerenin Problem başlangıcından önceki payı
	// (başlangıç anı kovaya düşsün, öncesi taban olarak görünsün).
	problemWindowMargin = 15 * time.Minute
	// problemWindowMaxS — durum penceresinin üst sınırıyla aynı (30 gün).
	problemWindowMaxS = 30 * 86400
	problemScopeRead  = 2 * time.Second
)

// chatProblemScope — sohbetin Problem'i (sayfa bağlamındaki problemId).
type chatProblemScope struct {
	ID          string
	Ref         string // sayfadaki kimlik (görünen ya da iç)
	Service     string
	Metric      string
	RuleID      string
	Description string
	StartedAt   int64 // unix ns
	ResolvedAt  int64 // unix ns; 0 = açık
}

// chatProblemScopeFor — G/Ç: sayfa Problem'i → satır (okunamazsa nil; tur aynen sürer).
func (s *Server) chatProblemScopeFor(ctx context.Context, page *agentctx.PageContext) *chatProblemScope {
	if s == nil || s.store == nil || page == nil || strings.TrimSpace(page.ProblemID) == "" {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, problemScopeRead)
	defer cancel()
	var p *chstore.Problem
	var err error
	if chstore.IsProblemDisplayID(page.ProblemID) {
		p, err = s.store.GetProblemByDisplayID(c, page.ProblemID)
	} else {
		p, err = s.store.GetProblem(c, page.ProblemID)
	}
	if err != nil || p == nil || p.ID == "" {
		return nil
	}
	return problemScopeOf(*p, page.ProblemID)
}

// problemScopeOf — SAF: satır → kapsam.
func problemScopeOf(p chstore.Problem, ref string) *chatProblemScope {
	ps := &chatProblemScope{ID: p.ID, Ref: ref, Service: p.Service, Metric: p.Metric, RuleID: p.RuleID,
		Description: p.Description, StartedAt: p.StartedAt}
	if p.ResolvedAt != nil && *p.ResolvedAt > 0 && p.Status != "open" {
		ps.ResolvedAt = *p.ResolvedAt
	}
	return ps
}

// problemChatWindow — SAF (E): Problem penceresi → (range_s, toMs). Açık
// Problem'de toMs = 0 (pencere şimdiye çapalı, istek anında kurulur);
// kapanmışta toMs = bitiş. Başlangıç bilinmiyorsa ok=false.
func problemChatWindow(startNs, resolvedNs int64, now time.Time) (rangeS, toMs int64, ok bool) {
	if startNs <= 0 {
		return 0, 0, false
	}
	from := time.Unix(0, startNs).Add(-problemWindowMargin)
	to := now
	if resolvedNs > 0 && time.Unix(0, resolvedNs).Before(now) {
		to = time.Unix(0, resolvedNs)
		toMs = to.UnixMilli()
	}
	rangeS = int64(to.Sub(from) / time.Second)
	rangeS = min(max(rangeS, 60), problemWindowMaxS)
	return rangeS, toMs, true
}

// problemWindowLabelTR — SAF: "Problem penceresi: 12:35 → şimdi" / "… → 14:10"
// (başka günse tarih de). loc nil → UTC.
func problemWindowLabelTR(startNs, resolvedNs int64, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	from := time.Unix(0, startNs).Add(-problemWindowMargin).In(loc)
	n := now.In(loc)
	stamp := func(t time.Time) string {
		if t.Year() != n.Year() || t.YearDay() != n.YearDay() {
			return t.Format("02.01 15:04")
		}
		return t.Format("15:04")
	}
	end := "şimdi"
	if resolvedNs > 0 && time.Unix(0, resolvedNs).Before(now) {
		end = stamp(time.Unix(0, resolvedNs).In(loc))
	}
	return "Problem penceresi: " + stamp(from) + " → " + end
}

// pastWindowLabelTR — SAF (E): bitişi geçmişte olan pencere ASLA "son N
// dakika" diye yazılmaz → "19:22 → 19:37" (başka günse tarihli). Bitiş
// şimdiye yakınsa (≤ 2 dk) ya da yoksa göreli etiket.
func pastWindowLabelTR(rangeS, toMs int64, now time.Time, loc *time.Location) string {
	if toMs <= 0 || now.Sub(time.UnixMilli(toMs)) <= 2*time.Minute {
		return fmtRangeTR(rangeS)
	}
	if loc == nil {
		loc = time.UTC
	}
	to := time.UnixMilli(toMs).In(loc)
	from := to.Add(-time.Duration(rangeS) * time.Second)
	n := now.In(loc)
	stamp := func(t time.Time) string {
		if t.Year() != n.Year() || t.YearDay() != n.YearDay() {
			return t.Format("02.01 15:04")
		}
		return t.Format("15:04")
	}
	return stamp(from) + " → " + stamp(to)
}

// ── C: Problem türü → ilk araçlar ────────────────────────────────────────

// problemKind — deterministik Problem türü (C).
type problemKind string

const (
	problemKindNone    problemKind = ""
	problemKindLag     problemKind = "lag"
	problemKindError   problemKind = "error"
	problemKindLatency problemKind = "latency"
	problemKindOOM     problemKind = "oom"
)

// problemKindOf — SAF: Problem alanlarından tür. Sıra bilinçli: OOM ve lag
// metrik adında "error" / "_ms" taşısa da önce tanınır.
func problemKindOf(metric, ruleID, description string) problemKind {
	m := strings.ToLower(metric)
	r := strings.ToLower(ruleID)
	switch {
	case strings.Contains(m, "oom") || strings.Contains(r, "oom") || strings.Contains(description, "(OOMKilled)"):
		return problemKindOOM
	case strings.Contains(m, "lag"):
		return problemKindLag
	case strings.Contains(m, "error") || strings.Contains(m, "exception") || strings.Contains(m, "5xx"):
		return problemKindError
	case strings.Contains(m, "p99") || strings.Contains(m, "p95") || strings.Contains(m, "latency") ||
		strings.HasSuffix(m, "_ms") || strings.Contains(m, "slow") || strings.Contains(r, "slowdown"):
		return problemKindLatency
	}
	return problemKindNone
}

// problemKindTools — tür → aday araçlar (sıralı); katalogda olmayan düşer.
var problemKindTools = map[problemKind][]string{
	problemKindLag:     {mcptools.MessagingFlowName, "get_messaging_health"},
	problemKindError:   {"list_exception_groups", "get_exception_samples"},
	problemKindLatency: {mcptools.EndpointMetricsName, mcptools.FindTracesName, "query_traces"},
	problemKindOOM:     {"get_pod_health", "list_pods"},
}

// problemKindLabelTR — ipucu satırındaki tür adı.
var problemKindLabelTR = map[problemKind]string{
	problemKindLag:     "tüketici gecikmesi",
	problemKindError:   "hata oranı",
	problemKindLatency: "yanıt süresi",
	problemKindOOM:     "bellek sınırı (OOMKilled)",
}

// problemFirstToolsHintTR — SAF (C): sıralı kısa ipucu (en çok 2 araç; tür
// bilinmiyor ya da araç katalogda yoksa ""). service: Problem satırının öznesi.
func problemFirstToolsHintTR(kind problemKind, service string, specs []copilot.ToolSpec) string {
	cands := problemKindTools[kind]
	if len(cands) == 0 {
		return ""
	}
	have := make(map[string]bool, len(specs))
	for _, sp := range specs {
		have[sp.Name] = true
	}
	var picked []string
	for _, c := range cands {
		if have[c] && len(picked) < 2 {
			picked = append(picked, c)
		}
	}
	if len(picked) == 0 {
		return ""
	}
	subj := ""
	if s := strings.TrimSpace(service); s != "" {
		subj = fmt.Sprintf(" (service=%s)", s)
	}
	return fmt.Sprintf("\nPROBLEM TÜRÜ: %s — önce %s%s çağır; kayıtta olmayanı bununla tamamla.\n",
		problemKindLabelTR[kind], strings.Join(picked, ", sonra "), subj)
}

// problemPromptLine — döngü istemine eklenen satır (kapsam yoksa "").
func (p *chatProblemScope) promptLine(specs []copilot.ToolSpec) string {
	if p == nil {
		return ""
	}
	return problemFirstToolsHintTR(problemKindOf(p.Metric, p.RuleID, p.Description), p.Service, specs)
}

// applyProblemWindow — E: istek (ve konuşma durumu) Problem penceresiyle
// güncellenir; uygulanan pencerenin etiketi döner ("" = uygulanmadı). Kullanıcı
// bu turda pencere yazdıysa ya da konuşmada açık pencere seçtiyse dokunulmaz.
func applyProblemWindow(req *chatRequest, stt *chatStateTurn, ps *chatProblemScope, now time.Time) string {
	if req == nil || ps == nil || !req.Context.ProblemWindow {
		return ""
	}
	if stt != nil && stt.st != nil && (stt.res.WindowSet || stt.st.ctx.RangeExplicit) {
		return ""
	}
	rs, toMs, ok := problemChatWindow(ps.StartedAt, ps.ResolvedAt, now)
	if !ok {
		return ""
	}
	label := problemWindowLabelTR(ps.StartedAt, ps.ResolvedAt, now, chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin))
	req.Context.RangeS, req.Context.ToMs = rs, toMs
	if stt != nil && stt.st != nil {
		c := &stt.st.ctx
		if c.RangeS != rs || c.WinToMs != toMs || c.WinLabel != label {
			c.RangeS, c.WinToMs, c.WinLabel = rs, toMs, label
			stt.st.dirty = true
		}
	}
	return label
}
