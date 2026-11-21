package sre

// narrate.go — v0.10.1287 (AI SRE Faz 1b): araştırmanın TEK anlatım çağrısı.
//
// Yüzey "sre-investigation" (/ai sayfasında ayrı satır), UserID "system".
// Sistem promptu copilot.SystemPromptSREInvestigation (Türkçe-native, dört
// bölüm: Ne oldu / Kanıt / Muhtemel neden / Sonraki adım + tek örnek + veri
// çerçevesi). Girdi deterministik renderInvestigationTR (≤4 KB, serbest metin
// EN SONDA).
//
// Arka plan AI kapıları (ProblemExplainer emsali, anomaly/problem_explainer.go):
// Active (yapılandırılmış + açık), AutoExplainEnabled (Settings → AI otomatik
// açıklama), QuotaBackoffActive (429 sonrası 1 saat), sre_agent.MaxLLMCallsPerHour
// (saatlik sayaç). Kapı kapalı → çağrı YOK, özet "AI özeti yok (otomatik AI
// kapalı)" + nedeni. Bütçe: araştırma başına ≤1 çağrı (resume taze özeti
// yeniden üretmez).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/rca"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// NarrationSurface — ai_calls.surface.
const NarrationSurface = "sre-investigation"

// NarrationGatedText — kapı kapalıyken özetin metni (tasarım sabiti).
const NarrationGatedText = "AI özeti yok (otomatik AI kapalı)"

// narrationMinLeft — bütçede bundan az süre kaldıysa çağrı denenmez.
const narrationMinLeft = 5 * time.Second

// renderMaxBytes — anlatım girdisinin tavanı.
const renderMaxBytes = 4 << 10

// Narrator — *copilot.Service karşılar (test dikişi).
type Narrator interface {
	Active() bool
	AutoExplainEnabled() bool
	QuotaBackoffActive() bool
	ActiveModel() string
	Explain(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// narrationGate — SAF: arka plan AI çağrısı yapılabilir mi; değilse nedeni.
func narrationGate(n Narrator, cfg Settings, usedThisHour int) (bool, string) {
	ok, why, _ := narrationGateMsg(n, cfg, usedThisHour)
	return ok, why
}

// narrationGateMsg — narrationGate + nedenin metin anahtarı (v0.10.2301 S4).
func narrationGateMsg(n Narrator, cfg Settings, usedThisHour int) (bool, string, *chstore.StdMsg) {
	switch {
	case n == nil || !n.Active():
		return false, "AI yapılandırılmamış ya da kapalı", chstore.NewStdMsg("inv.gate.off")
	case !n.AutoExplainEnabled():
		return false, "otomatik AI açıklaması kapalı (Ayarlar → AI)", chstore.NewStdMsg("inv.gate.autoOff")
	case n.QuotaBackoffActive():
		return false, "sağlayıcı kotası doldu — arka plan AI 1 saat bekliyor", chstore.NewStdMsg("inv.gate.quota")
	case usedThisHour >= cfg.MaxLLMCallsPerHour:
		return false, fmt.Sprintf("saatlik AI çağrı tavanı doldu (%d)", cfg.MaxLLMCallsPerHour),
			chstore.NewStdMsg("inv.gate.cap", "n", fmt.Sprintf("%d", cfg.MaxLLMCallsPerHour))
	}
	return true, "", nil
}

// narrate — anlatım adımı. Kayda dokunmaz; dönen adımı koordinatör yazar,
// özeti inv.Summary'ye bu işlev koyar (koordinatör goroutine'inde çağrılır).
func narrate(ctx context.Context, rd readers, inv *Investigation, p chstore.Problem, now func() time.Time) Step {
	st := Step{Name: stepSummary}
	t0 := now()
	finish := func() Step {
		end := now()
		st.DurationMs, st.At = end.Sub(t0).Milliseconds(), end.UnixNano()
		return st
	}
	cfg := CurrentSettings()
	if rd.settings != nil {
		cfg = rd.settings()
	}
	ok, why, whyMsg := narrationGateMsg(rd.narrator, cfg, rd.budget.used(ctx))
	if !ok {
		gate := chstore.NewStdMsg("inv.gated", "whyM", whyMsg)
		inv.Summary = &Narration{Text: NarrationGatedText, At: t0.UnixNano(), Gated: true, Reason: why, GateMsg: gate}
		st.State, st.Summary = StepSkipped, NarrationGatedText+" — "+why
		st.SummaryMsg = gate
		return finish()
	}
	if dl, has := ctx.Deadline(); (has && time.Until(dl) < narrationMinLeft) || ctx.Err() != nil {
		st.State, st.Summary = StepError, "süre bütçesi doldu — AI özeti istenmedi"
		return finish()
	}
	user, _ := narrationInput(*inv, p, tzdefault.Location(), packTokens(rd)) // v0.10.1494 — paket kapalı → renderInvestigationTR bayt bayt
	known := []string{p.Service}
	for _, f := range inv.Findings {
		known = append(known, f.Name)
	}
	nctx, cancel := context.WithTimeout(ctx, capSummary)
	defer cancel()
	// v0.10.1312 — özetin 👍/👎 rayı: ai_calls kimliği (WithCallID → kayıt bu
	// kimlikle yazılır) ve exchange kimliği (ai_calls.exchange_id → oyun yüzey
	// çözümü) çağrıdan ÖNCE basılır, özetle saklanır.
	callID, exchangeID := copilot.NewCallID(), newNarrationExchangeID()
	nctx = copilot.WithCallID(copilot.WithMeta(nctx, copilot.CallMeta{
		Surface: NarrationSurface, UserID: "system", ExchangeID: exchangeID,
		Observe: copilot.MetaFromContext(ctx).Observe, // v0.10.1463 (D1) — sre.narrate token attribute'ları
		Shield: func(prompt, answer string) uint8 {
			return rca.CountUnknownEntities(rca.LowerKnownSet(known...), prompt, answer)
		},
	}), callID)
	// v0.10.1289 — operatör kuralları: bağlama (servis, sahip takım) uyan ≤5
	// kural çitli veri bloğu olarak paketin SONUNA, ek sistem mesajının önüne
	// (DataNotInstruction sonda kalır). Kural yoksa iki metin de bayt bayt eski.
	// Kurallar yalnız ANLATIMA girer: Score / planSteps / kontroller onları
	// görmez (TestRulesNeverReachScoring).
	system := copilot.SystemPromptSREInvestigation()
	if block := narrationRulesBlock(rd, p, tzdefault.Location()); block != "" {
		system = copilot.CosreRulesAddendum() + "\n\n" + system
		user += "\n" + block + "\n"
	}
	// CoSRE Faz E — ekip notları: aynı yer ve aynı çerçeve (blok paketin
	// sonuna, ek sistem mesajının önüne). Replay varyantı noMemory kapatır;
	// replay'de yalnız Problem'den ÖNCE öğrenilmiş notlar ve o Problem'in
	// kendi kök neden notu HARİÇ (cevap sızmaz). Skor / plan / kontrol görmez.
	inv.MemoryNotes = 0
	if block, n := narrationMemoryBlock(rd, *inv, p, now(), tzdefault.Location()); block != "" {
		system = copilot.CosreMemoryAddendum() + "\n\n" + system
		user += "\n" + block + "\n"
		inv.MemoryNotes = n
	}
	rd.budget.inc(ctx)
	text, err := rd.narrator.Explain(nctx, system, user)
	text = strings.TrimSpace(text)
	if err != nil || text == "" {
		msg := "boş cevap"
		if err != nil {
			msg = err.Error()
		}
		st.State, st.Summary = StepError, truncRunes("AI özeti alınamadı: "+msg, 400)
		return finish()
	}
	inv.Summary = &Narration{Text: truncRunes(text, 4000), Model: rd.narrator.ActiveModel(),
		CallID: callID, ExchangeID: exchangeID, At: now().UnixNano()}
	st.State, st.Summary = StepOK, truncRunes(firstLine(text), 300)
	return finish()
}

// narrationRulesBlock — anlatıma girecek kural bloğu ("" = yok). Bağlam:
// problemin servisi + sahip takımı (EnrichProblemsWithTeams doldurduysa);
// arka plan çağrısında kullanıcı takımı yoktur.
func narrationRulesBlock(rd readers, p chstore.Problem, loc *time.Location) string {
	var rules []cosrerules.Rule
	if rd.rules != nil {
		rules = rd.rules()
	} else {
		rules = chstore.CurrentCosreRules().Rules
	}
	if len(rules) == 0 {
		return ""
	}
	picked, omitted := cosrerules.Relevant(rules, cosrerules.Context{Service: p.Service, Team: p.OwnerTeam},
		cosrerules.PromptMaxRules, cosrerules.PromptMaxBytes)
	return cosrerules.BlockTR(picked, omitted, loc)
}

// narrationMemoryBlock — anlatıma girecek ekip notu bloğu ve not sayısı.
// inv.NoMemory (replay varyantı) → yok. Replay / değerlendirme koşusunda
// (inv.AsOf > 0) süzgeç: kaynağı Problem başlangıcından önce olan notlar,
// Problem'in kendi kök neden notu hariç.
func narrationMemoryBlock(rd readers, inv Investigation, p chstore.Problem, at time.Time, loc *time.Location) (string, int) {
	if inv.NoMemory {
		return "", 0
	}
	var items []cosrememory.Item
	if rd.memory != nil {
		items = rd.memory()
	} else {
		items = chstore.CurrentCosreMemory()
	}
	if len(items) == 0 {
		return "", 0
	}
	f := cosrememory.Filter{Now: at.UnixNano(), ExcludeProblem: p.ID}
	if inv.AsOf > 0 {
		f.Now, f.Before = inv.AsOf, inv.ProblemAt
		if f.Before <= 0 {
			f.Before = p.StartedAt
		}
		if f.ExcludeProblem == "" {
			f.ExcludeProblem = inv.ProblemID
		}
	}
	picked, omitted := cosrememory.Relevant(items, cosrememory.Context{Service: p.Service, Team: p.OwnerTeam}, f,
		cosrememory.PromptMaxItems, cosrememory.PromptMaxBytes)
	return cosrememory.BlockTR(picked, omitted, loc), len(picked)
}

// newNarrationExchangeID —anlatım çağrısının exchange kimliği: tıklamalı ✨
// yüzeylerinin biçimi (16 bayt hex, api.newRandID(16) ile aynı; oy ucunun
// kimlik kapısı ≤64). crypto/rand okunamazsa "" — çağrı yine yapılır, yalnız
// oylanamaz (FE kimliksiz rayı çizmez).
func newNarrationExchangeID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// stepLabelTR — adım adının Türkçe etiketi (anlatım girdisi + FE aynası
// sreInvestigationView STEP_LABEL).
func stepLabelTR(n string) string {
	switch n {
	case stepSweep:
		return "servis taraması"
	case stepDeploys:
		return "deploy"
	case stepDeps:
		return "bağımlılık"
	case stepPivot:
		return "trace pivotu"
	case stepLogSpike:
		return "log artışı"
	case stepSummary:
		return "AI özeti"
	case stepEvents:
		return "olaylar"
	case stepNode:
		return "node baskısı"
	case stepBaseline: // v0.10.1494 — Context Pack okuyucuları
		return "baseline"
	case stepAnomalies:
		return "anomaliler"
	case stepErrors:
		return "hata özeti"
	}
	return n
}

func stepStateTR(s string) string {
	switch s {
	case StepOK:
		return "tamam"
	case StepPartial:
		return "kısmi"
	case StepError:
		return "OKUNAMADI"
	case StepSkipped:
		return "atlandı"
	}
	return s
}

// kindTR — bulgu türünün Türkçe adı.
func kindTR(k string) string {
	switch k {
	case "operation":
		return "operasyon"
	case "function":
		return "fonksiyon"
	case "dependency":
		return "bağımlılık"
	case "k8s":
		return "Kubernetes"
	case "jvm":
		return "JVM"
	}
	return k
}

// renderInvestigationTR — SAF: kayıt → anlatım girdisi (≤4 KB, rune sınırında
// kesilir). v0.10.1462 (A1): gövde Context Pack'ten (contextpack.go
// BuildContextPack + RenderTR, narrationBudget) — metin bayt-bayt aynı.
// Sıra: başlık → pencere → adımlar → sentezleyici → bulgular (serbest metin,
// EN SONDA — veri talimat değildir). Damga loc diliminde (nil → UTC).
func renderInvestigationTR(inv Investigation, p chstore.Problem, loc *time.Location) string {
	txt, _ := RenderTR(BuildContextPack(inv, p, loc), narrationBudget)
	return txt
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// capBytes — SAF: UTF-8 rune sınırında bayt tavanı.
func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max - len("…\n")
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…\n"
}

// humanTR — operatör işaretinin kısa etiketi.
func humanTR(h Hypothesis) string {
	if h.HumanState == nil {
		return ""
	}
	switch h.HumanState.State {
	case MarkDismissed:
		return " (operatör eledi)"
	case MarkConfirmed:
		return " (operatör onayladı)"
	}
	return ""
}

// renderHypothesesTR — SAF (v0.10.1288): anlatım girdisinin hipotez satırları.
// Skor KODDAN gelir; model yalnız anabilir (prompt: "skoru değiştirme").
// En olası = operatörün elemediği ilk kart; düşük bantta "belirgin hipotez yok".
func renderHypothesesTR(hs []Hypothesis) string {
	if len(hs) == 0 {
		return ""
	}
	top := -1
	for i, h := range hs {
		if h.HumanState == nil || h.HumanState.State != MarkDismissed {
			top = i
			break
		}
	}
	var b strings.Builder
	switch {
	case top < 0:
		b.WriteString("AJAN HİPOTEZİ: operatör tüm hipotezleri eledi\n")
	case hs[top].Band == BandLow:
		fmt.Fprintf(&b, "AJAN HİPOTEZİ (kod skoru): belirgin hipotez yok — en yüksek %s %s %.2f\n", hs[top].Label, hs[top].Band, hs[top].Score)
	default:
		h := hs[top]
		fmt.Fprintf(&b, "AJAN HİPOTEZİ (kod skoru): %s — %s %.2f%s", h.Label, h.Band, h.Score, humanTR(h))
		for _, c := range h.Checks {
			if c.Outcome > 0 {
				b.WriteString(" — " + truncRunes(oneLine(c.Reason), 160))
				break
			}
		}
		b.WriteString("\n")
	}
	var others []string
	for i, h := range hs {
		if i == top || len(others) >= HypothesesShown-1 {
			continue
		}
		others = append(others, fmt.Sprintf("%s %s %.2f%s", h.Label, h.Band, h.Score, humanTR(h)))
	}
	if len(others) > 0 {
		b.WriteString("DİĞER HİPOTEZLER: " + strings.Join(others, " · ") + "\n")
	}
	return b.String()
}
