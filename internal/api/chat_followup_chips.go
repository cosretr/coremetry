package api

// chat_followup_chips.go — v0.10.1291 (CoSRE follow-up D): serbest tool
// döngüsünün cevabına KURAL-türevli takip çipleri. Guided cevabın rotadan
// türeyen önerileri (guidedSuggestions, copilot_followup.go) yıllardır vardı;
// modelin araç çağırdığı yol çipsizdi ve FE statik FOLLOWUPS listesine
// düşüyordu. Artık çipler ARAÇ SONUÇLARINDAN, deterministik kurallarla
// üretilir — modelin metninden ya da modele sorarak ASLA:
//
//	query_metrics latency|red: p99 last ≥1.5×avg ya da max ≥2×avg → "{svc} en yavaş trace'ler?"
//	query_metrics errors (sıfırdan büyük seri) | query_traces errorRatePct ≥5 → "{svc} hata logları?"
//	query_metrics satırı limitPct ≥85 | pivot/tarama pod·jvm bulgusu | log artışı pod katkısı
//	                                   → `{svc} logları pod:"{pod}"`
//	query_traces ilk satır | pivot adayları → "{traceId} trace'ini açıkla"
//	tarama deploy bulgusu              → "{svc} son deploy etkisi?"
//	get_problem araştırma hipotezi     → "{hipotez} hipotezini doğrula" (AKSİYON çipi:
//	                                     mevcut sre_hypothesis / verify, tık-only, editor+)
//
// Değişmezler (testte pinli): her METİN şablonu guided router'da kendi başına
// yönlenir (TestFollowupTemplatesRouteGuided — guidedSuggestions'ın v0.9.411
// sözleşmesi; yönlenmeyen şablon gönderilmez); sorulmuş mesajlarla aynı /
// ≥0.8 benzer çip düşer; en çok 3. Tel: answer.followups (yeni) +
// answer.suggestions (yalnız metin çipleri — eski istemci); olgu yoksa ikisi
// de YOK (FE statik çiplerini korur).

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sre"
)

// Olgu türleri — çip önceliği bu sırada (özgül bulgu önce, servis geneli sonra).
const (
	fuKindHypothesis = "hypothesis"
	fuKindPod        = "pod"
	fuKindTrace      = "trace"
	fuKindSlow       = "slow"
	fuKindErrors     = "errors"
	fuKindDeploy     = "deploy"
	// fuKindExceptionGroup — v0.10.1331: operasyon ön-getirmesinin en yeni
	// Exceptions grubu → "<KOD> grubunu açıkla" LİNK çipi (mesaj göndermez;
	// grubun sayfası + AI açıklaması, /problems?exc=<fp>&ai=exception).
	fuKindExceptionGroup = "exception_group"
)

// v0.10.1388 — fuKindOpTopology (operasyon cevabının "<op> örnek trace
// topolojisi" çipi) exception grubundan hemen sonra.
var fuKindOrder = map[string]int{fuKindHypothesis: 0, fuKindExceptionGroup: 1, fuKindOpTopology: 2, fuKindPod: 3, fuKindTrace: 4, fuKindSlow: 5, fuKindErrors: 6, fuKindDeploy: 7}

const (
	followupMax            = 3
	followupSlowLastFactor = 1.5
	followupSlowMaxFactor  = 2.0
	followupErrRatePct     = 5.0
	followupPodLimitPct    = 85.0
	followupDedupJaccard   = 0.8
)

// followupFact — bir araç sonucundan çıkan, çipe dönüşebilir olgu.
type followupFact struct {
	Kind       string
	Service    string
	Env        string
	Pod        string
	TraceID    string
	ProblemID  string // hypothesis: get_problem id
	DisplayID  string // hypothesis: get_problem displayId
	Hypothesis string // hypothesis: sre hipotez türü (kapalı küme)
	// v0.10.1331 — exception_group: hata kodu + grup parmak izi.
	Code        string
	Fingerprint string
	// Operation — v0.10.1388: op_topology çipinin operasyonu ("PAYMENT_CREATE",
	// "POST /api/payments").
	Operation string
}

// followupAction — aksiyon çipinin yükü (chat_actions.go sre_hypothesis bloğuyla aynı alanlar).
type followupAction struct {
	Kind       string `json:"kind"` // sre_hypothesis
	ProblemID  string `json:"problemId"`
	Hypothesis string `json:"hypothesis"`
	Label      string `json:"label"`
}

// chatFollowup — answer.followups öğesi. Metin çipi tıklanınca Text yeni mesaj
// olarak gider; Scope/Command yapısal kapsam olarak (chat_scope.go) eşlik eder.
// Action doluysa çip mesaj GÖNDERMEZ, aksiyonu çalıştırır.
type chatFollowup struct {
	Text    string          `json:"text"`
	Scope   *chatScope      `json:"scope,omitempty"`
	Command string          `json:"command,omitempty"`
	Kind    string          `json:"kind"`
	Action  *followupAction `json:"action,omitempty"`
	// Href — v0.10.1331: LİNK çipi (uygulama-içi yol); doluysa çip mesaj
	// GÖNDERMEZ, o sayfayı açar (FE isSafeAppPath kapısından geçirir).
	Href string `json:"href,omitempty"`
	// Label — v0.10.1383: çipte GÖRÜNEN kısa metin ("Operasyonları göster");
	// boşsa Text görünür. Tık yine Text'i (router'a yönlenen tam cümle) gönderir.
	Label string `json:"label,omitempty"`
	// ExplainAsk — v0.10.1400 (Tek CoSRE): AÇIKLAMA çipi; tık Text'i kullanıcı
	// turu olarak + bu yapısal niyeti context.explainAsk olarak gönderir
	// (chat_explain_turn.go) — açıklama sohbet içinde akar, çekmece açılmaz.
	ExplainAsk *chatExplainAsk `json:"explainAsk,omitempty"`
	// Pick — v0.10.1515 (operatör: "Alttaki chipler çok çıkıyor, gerek yok
	// servis servis yazmasına"): SEÇİCİ çipi. Tık mesaj GÖNDERMEZ, satır içi
	// sunucu-aramalı servis seçici açar (adaylar önce); seçilen servisle
	// Template doldurulur ve o cümle Scope + seçilen servisle gönderilir.
	Pick *followupPick `json:"pick,omitempty"`
}

// followupPick — v0.10.1515: seçici çipinin yükü. Template "{service}" yer
// tutucusunu taşır (fillPickTemplate); Services takım cevabının servisleri
// (hata oranı azalan), seçicide ilk sırada.
type followupPick struct {
	Kind     string   `json:"kind"` // "service"
	Template string   `json:"template"`
	Services []string `json:"services,omitempty"`
}

// pickServicePlaceholder — Template'teki servis yer tutucusu (FE
// followupPick.ts PICK_SERVICE_PLACEHOLDER ile aynı).
const pickServicePlaceholder = "{service}"

// fillPickTemplate — SAF: seçici çipinin seçimle gönderdiği cümle.
func fillPickTemplate(tpl, svc string) string {
	return strings.ReplaceAll(tpl, pickServicePlaceholder, strings.TrimSpace(svc))
}

var (
	fuPodRe     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	fuServiceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,127}$`)
	fuTraceRe   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	// v0.10.1331 — exception_group çipi: hata kodu ve parmak izi biçimi.
	fuExcCodeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,63}$`)
	fuExcFPRe   = regexp.MustCompile(`^(ora:)?[0-9a-f]{8,64}$`)
)

// fuEnvelope — atomik cosre araçlarının zarfı (query_*).
type fuEnvelope struct {
	OK      bool            `json:"ok"`
	Request map[string]any  `json:"request"`
	Data    json.RawMessage `json:"data"`
}

func fuReq(req map[string]any, k string) string {
	if v, ok := req[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// followupFactsFromTool — SAF: BAŞARILI bir araç sonucundan çip olguları.
// args = yürüyen argüman (kapsam yedeği); content = aracın TAM çıktısı.
// Tanınmayan araç / bozuk JSON / ok=false → nil.
func followupFactsFromTool(tool string, args json.RawMessage, content string) []followupFact {
	var argm map[string]any
	_ = json.Unmarshal(args, &argm)
	switch tool {
	case "query_metrics":
		return fuQueryMetrics(content, argm)
	case "query_traces":
		return fuQueryTraces(content, argm)
	case mcptools.PivotFromTraceName:
		return fuPivot(content)
	case mcptools.ServiceHealthSweepName:
		return fuSweep(content, argm)
	case mcptools.ExplainLogSpikeName:
		return fuLogSpike(content, argm)
	case "get_problem":
		return fuGetProblem(content)
	}
	return nil
}

func fuScope(env map[string]any, args map[string]any) (svc, envName string) {
	svc, envName = fuReq(env, "service"), fuReq(env, "env")
	if svc == "" {
		svc = fuReq(args, "service")
	}
	if envName == "" {
		envName = fuReq(args, "env")
	}
	return svc, envName
}

func fuQueryMetrics(content string, args map[string]any) []followupFact {
	var e fuEnvelope
	if json.Unmarshal([]byte(content), &e) != nil || !e.OK {
		return nil
	}
	var d struct {
		Series []struct {
			Name  string `json:"name"`
			Stats struct {
				Avg  float64 `json:"avg"`
				Max  float64 `json:"max"`
				Last float64 `json:"last"`
			} `json:"stats"`
		} `json:"series"`
		Rows []struct {
			Pod      string  `json:"pod"`
			LimitPct float64 `json:"limitPct"`
		} `json:"rows"`
	}
	if json.Unmarshal(e.Data, &d) != nil {
		return nil
	}
	svc, env := fuScope(e.Request, args)
	family := fuReq(e.Request, "family")
	var out []followupFact
	switch family {
	case "latency", "red":
		for _, s := range d.Series {
			if !strings.HasPrefix(s.Name, "p99") || s.Stats.Avg <= 0 {
				continue
			}
			if s.Stats.Last >= followupSlowLastFactor*s.Stats.Avg || s.Stats.Max >= followupSlowMaxFactor*s.Stats.Avg {
				out = append(out, followupFact{Kind: fuKindSlow, Service: svc, Env: env})
				break
			}
		}
	case "errors":
		for _, s := range d.Series {
			if s.Stats.Max > 0 {
				out = append(out, followupFact{Kind: fuKindErrors, Service: svc, Env: env})
				break
			}
		}
	}
	for _, r := range d.Rows {
		if r.LimitPct >= followupPodLimitPct {
			out = append(out, followupFact{Kind: fuKindPod, Service: svc, Env: env, Pod: r.Pod})
		}
	}
	return out
}

func fuQueryTraces(content string, args map[string]any) []followupFact {
	var e fuEnvelope
	if json.Unmarshal([]byte(content), &e) != nil || !e.OK {
		return nil
	}
	var d struct {
		Traces []struct {
			TraceID string `json:"traceId"`
		} `json:"traces"`
		ServiceTotals *struct {
			ErrorRatePct float64 `json:"errorRatePct"`
		} `json:"serviceTotals"`
	}
	if json.Unmarshal(e.Data, &d) != nil {
		return nil
	}
	svc, env := fuScope(e.Request, args)
	var out []followupFact
	if d.ServiceTotals != nil && d.ServiceTotals.ErrorRatePct >= followupErrRatePct {
		out = append(out, followupFact{Kind: fuKindErrors, Service: svc, Env: env})
	}
	if len(d.Traces) > 0 {
		out = append(out, followupFact{Kind: fuKindTrace, Service: svc, Env: env, TraceID: d.Traces[0].TraceID})
	}
	return out
}

func fuPivot(content string) []followupFact {
	var r struct {
		OK      bool           `json:"ok"`
		Request map[string]any `json:"request"`
		Trace   *struct {
			Service string `json:"service"`
		} `json:"trace"`
		Findings []struct {
			Hop     string `json:"hop"`
			Subject string `json:"subject"`
			Level   string `json:"level"`
		} `json:"findings"`
		Data *struct {
			Pods []struct {
				Pod     string `json:"pod"`
				Service string `json:"service"`
			} `json:"pods"`
			JVM []struct {
				Pod     string `json:"pod"`
				Service string `json:"service"`
			} `json:"jvm"`
			Candidates []struct {
				TraceID string `json:"traceId"`
			} `json:"candidates"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(content), &r) != nil {
		return nil
	}
	env := fuReq(r.Request, "env")
	podSvc := map[string]string{}
	if r.Data != nil {
		for _, p := range r.Data.Pods {
			podSvc[p.Pod] = p.Service
		}
		for _, p := range r.Data.JVM {
			if podSvc[p.Pod] == "" {
				podSvc[p.Pod] = p.Service
			}
		}
	}
	traceSvc := ""
	if r.Trace != nil {
		traceSvc = r.Trace.Service
	}
	var out []followupFact
	for _, f := range r.Findings {
		if (f.Hop != "pods" && f.Hop != "jvm") || (f.Level != "crit" && f.Level != "warn") {
			continue
		}
		svc := podSvc[f.Subject]
		if svc == "" {
			svc = traceSvc
		}
		out = append(out, followupFact{Kind: fuKindPod, Service: svc, Env: env, Pod: f.Subject})
	}
	if r.Data != nil && len(r.Data.Candidates) > 0 {
		out = append(out, followupFact{Kind: fuKindTrace, Env: env, TraceID: r.Data.Candidates[0].TraceID})
	}
	return out
}

func fuSweep(content string, args map[string]any) []followupFact {
	var r struct {
		Request  map[string]any `json:"request"`
		Findings []struct {
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			Level string `json:"level"`
		} `json:"findings"`
	}
	if json.Unmarshal([]byte(content), &r) != nil {
		return nil
	}
	svc, env := fuScope(r.Request, args)
	var out []followupFact
	for _, f := range r.Findings {
		if f.Level != "crit" && f.Level != "warn" {
			continue
		}
		switch f.Kind {
		case "pod", "jvm":
			out = append(out, followupFact{Kind: fuKindPod, Service: svc, Env: env, Pod: f.Name})
		case "deploy":
			out = append(out, followupFact{Kind: fuKindDeploy, Service: svc, Env: env})
		}
	}
	return out
}

func fuLogSpike(content string, args map[string]any) []followupFact {
	var r struct {
		OK           bool           `json:"ok"`
		Request      map[string]any `json:"request"`
		Contributors []struct {
			Dim   string `json:"dim"`
			Value string `json:"value"`
		} `json:"contributors"`
	}
	if json.Unmarshal([]byte(content), &r) != nil || !r.OK {
		return nil
	}
	svc, env := fuScope(r.Request, args)
	var out []followupFact
	for _, c := range r.Contributors {
		if c.Dim == "pod" {
			out = append(out, followupFact{Kind: fuKindPod, Service: svc, Env: env, Pod: c.Value})
		}
	}
	return out
}

// fuGetProblem — get_problem çıktısındaki Faz 2 araştırma hipotezi
// (v0.10.1288). Kural sreHypothesisPick'in aynısı: ilk hipotez kapalı
// kümeden, düşük bantta değil, operatörce işaretlenmemiş.
func fuGetProblem(content string) []followupFact {
	var out struct {
		ID            string `json:"id"`
		DisplayID     string `json:"displayId"`
		Investigation *struct {
			Hypotheses []struct {
				Kind     string `json:"kind"`
				Band     string `json:"band"`
				Operator string `json:"operator"`
			} `json:"hypotheses"`
		} `json:"investigation"`
	}
	if json.Unmarshal([]byte(content), &out) != nil || out.Investigation == nil || len(out.Investigation.Hypotheses) == 0 || out.ID == "" {
		return nil
	}
	h := out.Investigation.Hypotheses[0]
	if !sre.IsHypothesisKind(h.Kind) || h.Band == sre.BandLow || h.Operator != "" {
		return nil
	}
	return []followupFact{{Kind: fuKindHypothesis, ProblemID: out.ID, DisplayID: out.DisplayID, Hypothesis: h.Kind}}
}

// gateFollowupFacts — SAF: aksiyon olgusu yalnız editor+ ve sayfanın
// problemi için kalır (sreHypothesisAction kapısının aynısı: viewer'a
// yazma önerilmez; hedef problem sayfa bağlamından doğrulanır).
func gateFollowupFacts(facts []followupFact, canWrite bool, pageProblemID string) []followupFact {
	pageProblemID = strings.TrimSpace(pageProblemID)
	out := facts[:0:0]
	for _, f := range facts {
		if f.Kind == fuKindHypothesis {
			if !canWrite || pageProblemID == "" || (f.ProblemID != pageProblemID && f.DisplayID != pageProblemID) {
				continue
			}
			f.ProblemID = pageProblemID
		}
		out = append(out, f)
	}
	return out
}

// renderFollowup — SAF: olgu → çip; şablonun kimliği geçersizse ok=false.
func renderFollowup(f followupFact) (chatFollowup, bool) {
	svcScope := func() *chatScope {
		return &chatScope{Services: []string{f.Service}, Env: f.Env}
	}
	switch f.Kind {
	case fuKindSlow, fuKindErrors, fuKindDeploy:
		if !fuServiceRe.MatchString(f.Service) {
			return chatFollowup{}, false
		}
		suffix := map[string]string{fuKindSlow: " en yavaş trace'ler?", fuKindErrors: " hata logları?", fuKindDeploy: " son deploy etkisi?"}[f.Kind]
		return chatFollowup{Text: f.Service + suffix, Scope: svcScope(), Kind: f.Kind}, true
	case fuKindPod:
		if !fuServiceRe.MatchString(f.Service) || !fuPodRe.MatchString(f.Pod) {
			return chatFollowup{}, false
		}
		return chatFollowup{Text: f.Service + ` logları pod:"` + f.Pod + `"`, Scope: svcScope(), Kind: f.Kind}, true
	case fuKindTrace:
		id := strings.ToLower(strings.TrimSpace(f.TraceID))
		if !fuTraceRe.MatchString(id) {
			return chatFollowup{}, false
		}
		return chatFollowup{Text: id + " trace'ini açıkla", Scope: &chatScope{Trace: id, Env: f.Env}, Kind: f.Kind}, true
	case fuKindExceptionGroup: // v0.10.1331 — router'a gitmez
		code := strings.TrimSpace(f.Code)
		if !fuExcCodeRe.MatchString(code) || !fuExcFPRe.MatchString(f.Fingerprint) {
			return chatFollowup{}, false
		}
		// v0.10.1400 — link çipi (/problems?exc=…&ai=exception → çekmecenin
		// özne kipi) yerine AÇIKLAMA çipi: grup açıklaması sohbetin turu olur.
		return chatFollowup{Text: code + " grubunu açıkla", Kind: f.Kind,
			ExplainAsk: &chatExplainAsk{Kind: explainAskException, ID: f.Fingerprint, Src: "chip"}}, true
	case fuKindOpTopology: // v0.10.1388 — router trace_topology'ye (operasyonla) yönlenir
		op := strings.TrimSpace(f.Operation)
		if !fuOpTopoRe.MatchString(op) {
			return chatFollowup{}, false
		}
		return chatFollowup{Text: topoOpChoiceChip(op), Label: "Örnek trace topolojisi", Kind: f.Kind}, true
	case fuKindHypothesis:
		if !sre.IsHypothesisKind(f.Hypothesis) || f.ProblemID == "" {
			return chatFollowup{}, false
		}
		label := sre.HypothesisLabel(f.Hypothesis) + " hipotezini doğrula"
		return chatFollowup{Text: label, Kind: f.Kind,
			Action: &followupAction{Kind: "sre_hypothesis", ProblemID: f.ProblemID, Hypothesis: f.Hypothesis, Label: label}}, true
	}
	return chatFollowup{}, false
}

// followupTokens — benzerlik için kelime kümesi (guided normalize + token).
func followupTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range guidedTokens(normalizeGuidedMsg(s)) {
		out[t] = true
	}
	return out
}

func followupJaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// followupChips — SAF: olgular → en çok max çip, tür başına bir. Öncelik
// fuKindOrder (aynı türde olgu sırası); sorulmuş bir mesajla normalize-eşit
// ya da token Jaccard ≥0.8 çip ve kendi aralarında tekrar düşer (düşen çip
// türünü harcamaz — aynı türün sonraki olgusu denenir).
func followupChips(facts []followupFact, asked []string, max int) []chatFollowup {
	if max <= 0 {
		max = followupMax
	}
	ordered := append([]followupFact(nil), facts...)
	sort.SliceStable(ordered, func(i, j int) bool { return fuKindOrder[ordered[i].Kind] < fuKindOrder[ordered[j].Kind] })
	askedNorm := make([]string, 0, len(asked))
	askedToks := make([]map[string]bool, 0, len(asked))
	for _, q := range asked {
		if strings.TrimSpace(q) == "" {
			continue
		}
		askedNorm = append(askedNorm, normalizeGuidedMsg(q))
		askedToks = append(askedToks, followupTokens(q))
	}
	seen := map[string]bool{}
	kindUsed := map[string]bool{}
	var out []chatFollowup
	for _, f := range ordered {
		if len(out) >= max {
			break
		}
		if kindUsed[f.Kind] {
			continue // tür başına bir çip: üç çip üç AYRI drill-down olsun
		}
		c, ok := renderFollowup(f)
		if !ok {
			continue
		}
		n := normalizeGuidedMsg(c.Text)
		if seen[n] {
			continue
		}
		dup := false
		toks := followupTokens(c.Text)
		for i := range askedNorm {
			if askedNorm[i] == n || followupJaccard(toks, askedToks[i]) >= followupDedupJaccard {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		seen[n] = true
		kindUsed[f.Kind] = true
		out = append(out, c)
	}
	return out
}

// withAnswerFollowups — answer olayına followups + (eski istemci için)
// suggestions; çip yoksa olay AYNEN (iki alan da yok).
func withAnswerFollowups(ans map[string]any, chips []chatFollowup) map[string]any {
	if len(chips) == 0 {
		return ans
	}
	ans["followups"] = chips
	var texts []string
	for _, c := range chips {
		if c.Action == nil && c.Href == "" && c.ExplainAsk == nil && c.Pick == nil { // v0.10.1331 — link çipi eski istemcide metin olarak gönderilmez; v0.10.1400 — açıklama çipi de (niyetsiz metin yönlenmez); v0.10.1515 — seçici çipi de (etiketi soru değil)
			texts = append(texts, c.Text)
		}
	}
	if len(texts) > 0 {
		ans["suggestions"] = texts
	}
	return ans
}

// chatUserTexts — SAF: konuşmadaki bütün kullanıcı metinleri (dedupe tabanı;
// tool-result turları Text taşımaz, atlanır).
func chatUserTexts(msgs []copilot.ChatMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Text) != "" {
			out = append(out, m.Text)
		}
	}
	return out
}

// loopFollowups — döngü cevabının çipleri: kapı (rol + sayfa problemi) → kurallar.
func loopFollowups(c *auth.Claims, facts []followupFact, asked []string, pageProblemID string) []chatFollowup {
	if len(facts) == 0 {
		return nil
	}
	canWrite := c != nil && (c.Role == auth.RoleAdmin || c.Role == auth.RoleEditor)
	return followupChips(gateFollowupFacts(facts, canWrite, pageProblemID), asked, followupMax)
}

// chatLoopFollowups — döngünün iki cevap çıkışının ortak çağrısı (normal tur +
// tur tavanı). Sayfa problemi pageCtx'ten (sanitize edilmiş istemci bağlamı).
func (s *Server) chatLoopFollowups(ctx context.Context, facts []followupFact, msgs []copilot.ChatMessage, page *agentctx.PageContext) []chatFollowup {
	if suggestTurnOf(ctx).noteLoop(facts) {
		return nil // v0.10.2364 — I3: interaction açıkken olgular data-suggestions'a (chat_suggestions.go)
	}
	problemID := ""
	if page != nil {
		problemID = page.ProblemID
	}
	return loopFollowups(auth.FromContext(ctx), facts, chatUserTexts(msgs), problemID)
}
