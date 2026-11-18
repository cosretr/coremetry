package api

// ai_evalset_fluency.go — v0.10.2323 (H1 ölçüm tabanı; DECISIONS
// "CoSRE değerlendirme: akıcılık ölçütleri ve yan yana rapor"): konuşma
// kipi koşusunun otomatik akıcılık ölçütleri ve operatörün puanlayacağı
// yan yana Markdown raporu.
//
//	GET /api/ai/evalset/runs/{id}/fluency-report   admin · text/markdown ek dosya
//
// Rapor yalnız OKUR: koşunun saklı vaka sonuçları (bu sürecin defteri ya da
// ai_eval_runs, nokta okuma). Ölçütler SAF ve deterministiktir
// (evalrubric.FluencyOf — audit §4 S2'nin Go karşılığı, kalıp listesi tek
// yerde). Oy / geri bildirim girdi değildir. Doğrulayıcı payı v0.10.2328'den
// beri turun gölge doğrulama sonucundan (evalVerifierPass); doğrulama
// koşmayan turlar (anlatımsız kademe) paydaya girmez.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func (s *Server) registerAIEvalsetFluencyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/evalset/runs/{id}/fluency-report", auth.RequireRole(auth.RoleAdmin, s.getAIEvalsetFluencyReport))
}

// evalVerifierPassHook — tur → (geçti mi, ölçüldü mü). v0.10.2328 (E1):
// evalVerifierPass. nil ya da hiç ölçülmemiş tur = rapor "ölçülmedi" yazar,
// değer UYDURULMAZ.
var evalVerifierPassHook = evalVerifierPass

// evalVerifierPass — SAF: tur doğrulayıcıdan geçti mi (sıfır uyuşmazlık;
// türetilen değer geçmeyi bozmaz, ayrı sayılır). Doğrulama koşmadıysa ölçülmedi.
func evalVerifierPass(t evalConvTurnResult) (pass, measured bool) {
	if t.Verify == nil {
		return false, false
	}
	return t.Verify.Mismatch == 0, true
}

// evalFluencyAgg — profil başına toplam.
type evalFluencyAgg struct {
	Profile, Model                        string
	Turns, Answered, Clean                int
	Opening, Closing, Bullets, Question   int
	Sentences, Chars                      int
	StatusTurns, StatusSentences          int
	Rounds, ModelCalls, ToolCalls, Reads  int
	InTok, OutTok                         int64
	TTFTSum, TTFTN, ModelMsSum, ToolMsSum int64
	LoopN                                 int
	VerifierPass, VerifierN               int
	DerivedTurns, DerivedClaims           int // v0.10.2328 — türetilen değer (ayrı; geçmeyi bozmaz)
	ModelBoiler, ServerBoiler             int // v0.10.2338 — kalıp: modelin yazdığı (süzgeç öncesi) / sunucunun satırı
}

// evalServerBoilerplate — SAF (v0.10.2338, karar 9): cevap sunucunun kalıp
// satırını taşıyor mu — düz "Kaynak:" künyesi ya da genel cevap notu.
func evalServerBoilerplate(answer string) bool {
	return evalrubric.HasServerSourceLine(answer) || strings.HasPrefix(strings.TrimSpace(answer), intentGeneralNoteTR)
}

// evalFluencyRounds — SAF: cevaba kadar döngü turu: v0.10.2318 telemetrisi
// (loop rounds) varsa o, yoksa tur kancasının sayımı.
func evalFluencyRounds(t evalConvTurnResult) int {
	if t.LoopRounds > 0 {
		return t.LoopRounds
	}
	return t.Rounds
}

// evalFluencyToolCalls — SAF: döngünün araç çağrısı: telemetri (loop_tool_calls) varsa o.
func evalFluencyToolCalls(t evalConvTurnResult) int {
	if t.LoopToolCalls > 0 {
		return t.LoopToolCalls
	}
	return t.ToolCalls
}

func (a *evalFluencyAgg) add(t evalConvTurnResult) {
	a.Turns++
	if strings.TrimSpace(t.Answer) == "" {
		return
	}
	a.Answered++
	f := evalrubric.FluencyOf(t.Answer)
	if len(t.Fails) == 0 {
		a.Clean++
	}
	if f.BoilerplateOpening {
		a.Opening++
	}
	if f.BoilerplateClosing {
		a.Closing++
	}
	if f.BoilerplateOpening || f.BoilerplateClosing || t.SeamDropped > 0 { // v0.10.2338 — dikişin sildiği de modelin yazdığıdır
		a.ModelBoiler++
	}
	if evalServerBoilerplate(t.Answer) {
		a.ServerBoiler++
	}
	if f.Bullets {
		a.Bullets++
	}
	if f.EndsWithQuestion {
		a.Question++
	}
	a.Sentences += f.Sentences
	a.Chars += f.Chars
	if evalTurnClass(t) == "status" { // v0.10.2331 — sunucunun cevap sınıfı (yoksa fikstür sınıfı)
		a.StatusTurns++
		a.StatusSentences += f.Sentences
	}
	a.Rounds += evalFluencyRounds(t)
	a.ModelCalls += t.ModelCalls
	a.ToolCalls += evalFluencyToolCalls(t)
	a.Reads += len(t.Calls)
	a.InTok += int64(t.InputTokens)
	a.OutTok += int64(t.OutputTokens)
	if t.TTFTMs > 0 {
		a.TTFTSum += int64(t.TTFTMs)
		a.TTFTN++
	}
	if t.LoopModelMs > 0 || t.LoopToolMs > 0 {
		a.ModelMsSum += int64(t.LoopModelMs)
		a.ToolMsSum += int64(t.LoopToolMs)
		a.LoopN++
	}
	if evalVerifierPassHook != nil {
		if pass, ok := evalVerifierPassHook(t); ok {
			a.VerifierN++
			if pass {
				a.VerifierPass++
			}
		}
	}
	if t.Verify != nil && t.Verify.Derived > 0 {
		a.DerivedTurns++
		a.DerivedClaims += t.Verify.Derived
	}
}

// evalFluencyProfiles — SAF: sonuçlardaki profiller, ilk görülme sırasıyla.
func evalFluencyProfiles(cases []evalCaseResult) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Mode != evalModeConversation || seen[c.ProfileID] {
			continue
		}
		seen[c.ProfileID] = true
		out = append(out, c.ProfileID)
	}
	return out
}

// evalFluencyAggregate — SAF: profil başına toplam (sıra evalFluencyProfiles).
func evalFluencyAggregate(cases []evalCaseResult) []*evalFluencyAgg {
	profiles := evalFluencyProfiles(cases)
	idx := map[string]*evalFluencyAgg{}
	out := make([]*evalFluencyAgg, 0, len(profiles))
	for _, p := range profiles {
		a := &evalFluencyAgg{Profile: p}
		idx[p] = a
		out = append(out, a)
	}
	for _, c := range cases {
		a := idx[c.ProfileID]
		if c.Mode != evalModeConversation || a == nil || c.Skipped {
			continue
		}
		if a.Model == "" {
			a.Model = c.Model
		}
		for _, t := range c.Turns {
			a.add(t)
		}
	}
	return out
}

// evalMdCell — SAF: Markdown tablo hücresi (| kaçışlı, satır sonu <br>).
func evalMdCell(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", "<br>")
}

func evalPct(n, d int) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprintf("%%%.0f", 100*float64(n)/float64(d))
}

func evalAvg(sum int64, n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", float64(sum)/float64(n))
}

// evalFluencyVerifyCell — SAF: turun gölge doğrulama sayaçları ve uyuşmayan jetonlar.
func evalFluencyVerifyCell(t evalConvTurnResult) string {
	v := t.Verify
	if v == nil {
		return "ölçülmedi"
	}
	s := fmt.Sprintf("%d / %d / %d / %d", v.Checked, v.Verified, v.Derived, v.Mismatch)
	if len(v.Mismatches) > 0 {
		s += " — " + strings.Join(v.Mismatches, ", ")
	}
	return evalMdCell(s)
}

// evalFluencyCallsCell — SAF: turun veri okumaları (ad, servis, pencere).
func evalFluencyCallsCell(t evalConvTurnResult) string {
	if len(t.Calls) == 0 {
		return "okuma yok"
	}
	parts := []string{strings.Join(evalConvCallNames(t.Calls), ", ")}
	if len(t.Services) > 0 {
		parts = append(parts, "servis "+strings.Join(t.Services, ", "))
	}
	if len(t.Windows) > 0 {
		parts = append(parts, "pencere "+strings.Join(t.Windows, ", "))
	}
	return strings.Join(parts, " · ")
}

// evalFluencyReport — SAF: koşu → Markdown (vaka ve tur başına profiller
// yan yana, sonra ölçüt tablosu).
func evalFluencyReport(run evalRunSummary, cases []evalCaseResult) string {
	profiles := evalFluencyProfiles(cases)
	aggs := evalFluencyAggregate(cases)
	model := map[string]string{}
	for _, a := range aggs {
		model[a.Profile] = a.Model
	}
	head := func(p string) string {
		label := p
		if label == "" {
			label = "üretim profili"
		}
		if m := model[p]; m != "" {
			label += " (`" + m + "`)"
		}
		return label
	}
	var b strings.Builder
	date := run.StartedAt
	if len(date) >= 10 {
		date = date[:10]
	}
	fmt.Fprintf(&b, "# CoSRE akıcılık raporu — %s\n\n", date)
	fmt.Fprintf(&b, "- Koşu: `%s` · durum %s · başlatan %s\n", run.ID, run.Status, evalMdCell(run.StartedBy))
	fmt.Fprintf(&b, "- Uygulama %s · prompt %s · yol %s\n", evalMdCell(run.AppVersion), evalMdCell(run.PromptVersion), evalMdCell(run.Path))
	for i, p := range profiles {
		fmt.Fprintf(&b, "- Profil %c: %s\n", 'A'+rune(i), head(p))
	}
	b.WriteString(evalFluencyStyleLine(cases))  // v0.10.2331
	b.WriteString(evalFluencyVerifyLine(cases)) // v0.10.2339
	b.WriteString("- Küme: `internal/copilot/evalset/conversation/fluency.json`; veri sahte, model çağrıları gerçek. Puanlama operatörün; aşağıdaki ölçütler otomatik sayımdır.\n\n")

	// Vakalar: taban kimliğe göre, kümedeki sıra (ilk görülme).
	type pair struct {
		id, why string
		by      map[string]evalCaseResult
	}
	var order []*pair
	idx := map[string]*pair{}
	for _, c := range cases {
		if c.Mode != evalModeConversation {
			continue
		}
		id := evalCaseBaseID(c.ID)
		if len(profiles) < 2 {
			id = c.ID
		}
		p := idx[id]
		if p == nil {
			p = &pair{id: id, why: c.Why, by: map[string]evalCaseResult{}}
			idx[id] = p
			order = append(order, p)
		}
		p.by[c.ProfileID] = c
	}
	b.WriteString("## Konuşmalar\n\n")
	for _, p := range order {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", p.id, evalMdCell(p.why))
		n := 0
		for _, c := range p.by {
			n = max(n, len(c.Turns))
		}
		for i := 0; i < n; i++ {
			user := ""
			for _, pr := range profiles {
				if c, ok := p.by[pr]; ok && i < len(c.Turns) {
					user = c.Turns[i].User
					break
				}
			}
			fmt.Fprintf(&b, "**Tur %d** — %s\n\n", i+1, evalMdCell(user))
			b.WriteString("| |")
			for _, pr := range profiles {
				b.WriteString(" " + head(pr) + " |")
			}
			b.WriteString("\n|---|")
			for range profiles {
				b.WriteString("---|")
			}
			b.WriteString("\n")
			rows := []struct {
				k string
				f func(t evalConvTurnResult) string
			}{
				{"Cevap", func(t evalConvTurnResult) string { return evalMdCell(t.Answer) }},
				{"Kademe", func(t evalConvTurnResult) string { return evalMdCell(strings.Trim(t.Tier+" "+t.Route, " ")) }},
				{"Okunan veri", func(t evalConvTurnResult) string { return evalMdCell(evalFluencyCallsCell(t)) }},
				{"Döngü turu · araç · model çağrısı", func(t evalConvTurnResult) string {
					return fmt.Sprintf("%d · %d · %d", evalFluencyRounds(t), evalFluencyToolCalls(t), t.ModelCalls)
				}},
				{"Token giriş / çıkış", func(t evalConvTurnResult) string { return fmt.Sprintf("%d / %d", t.InputTokens, t.OutputTokens) }},
				{"Doğrulayıcı (denetlenen / doğrulanan / türetilen / uyuşmayan)", evalFluencyVerifyCell},
				{"Yapısal ihlal", func(t evalConvTurnResult) string {
					if len(t.Fails) == 0 {
						return "—"
					}
					return evalMdCell(strings.Join(t.Fails, "\n"))
				}},
			}
			for _, r := range rows {
				b.WriteString("| " + r.k + " |")
				for _, pr := range profiles {
					c, ok := p.by[pr]
					if !ok || i >= len(c.Turns) {
						b.WriteString(" (koşmadı) |")
						continue
					}
					b.WriteString(" " + r.f(c.Turns[i]) + " |")
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Ölçütler\n\n| Ölçüt |")
	for _, a := range aggs {
		b.WriteString(" " + head(a.Profile) + " |")
	}
	b.WriteString("\n|---|")
	for range aggs {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	metric := func(name string, f func(a *evalFluencyAgg) string) {
		b.WriteString("| " + name + " |")
		for _, a := range aggs {
			b.WriteString(" " + f(a) + " |")
		}
		b.WriteString("\n")
	}
	metric("Cevaplanan tur / toplam", func(a *evalFluencyAgg) string { return fmt.Sprintf("%d / %d", a.Answered, a.Turns) })
	metric("Yapısal ihlalsiz tur", func(a *evalFluencyAgg) string { return evalPct(a.Clean, a.Answered) })
	metric("Kalıp açılış", func(a *evalFluencyAgg) string { return evalPct(a.Opening, a.Answered) })
	metric("Kalıp kapanış", func(a *evalFluencyAgg) string { return evalPct(a.Closing, a.Answered) })
	metric("Kalıp — model yazdı (dikiş süzgecinden önce)", func(a *evalFluencyAgg) string { return evalPct(a.ModelBoiler, a.Answered) })
	metric("Kalıp — sunucu satırı (Kaynak / genel cevap notu)", func(a *evalFluencyAgg) string { return evalPct(a.ServerBoiler, a.Answered) })
	metric("Ort. cümle", func(a *evalFluencyAgg) string { return evalAvg(int64(a.Sentences), a.Answered) })
	metric("Ort. cümle (durum turları)", func(a *evalFluencyAgg) string { return evalAvg(int64(a.StatusSentences), a.StatusTurns) })
	metric("Ort. karakter", func(a *evalFluencyAgg) string { return evalAvg(int64(a.Chars), a.Answered) })
	metric("Madde listeli cevap", func(a *evalFluencyAgg) string { return evalPct(a.Bullets, a.Answered) })
	metric("Soruyla biten cevap", func(a *evalFluencyAgg) string { return evalPct(a.Question, a.Answered) })
	metric("Ort. döngü turu (cevaba kadar)", func(a *evalFluencyAgg) string { return evalAvg(int64(a.Rounds), a.Answered) })
	metric("Ort. araç çağrısı", func(a *evalFluencyAgg) string { return evalAvg(int64(a.ToolCalls), a.Answered) })
	metric("Ort. model çağrısı", func(a *evalFluencyAgg) string { return evalAvg(int64(a.ModelCalls), a.Answered) })
	metric("Ort. veri okuması (sahte)", func(a *evalFluencyAgg) string { return evalAvg(int64(a.Reads), a.Answered) })
	metric("Ort. giriş token / tur", func(a *evalFluencyAgg) string { return evalAvg(a.InTok, a.Answered) })
	metric("Ort. çıkış token / tur", func(a *evalFluencyAgg) string { return evalAvg(a.OutTok, a.Answered) })
	metric("Ort. ilk token ms (döngü cevap turu)", func(a *evalFluencyAgg) string { return evalAvg(a.TTFTSum, int(a.TTFTN)) })
	metric("Ort. döngü model ms / araç ms", func(a *evalFluencyAgg) string {
		if a.LoopN == 0 {
			return "—"
		}
		return evalAvg(a.ModelMsSum, a.LoopN) + " / " + evalAvg(a.ToolMsSum, a.LoopN)
	})
	metric("Doğrulayıcıdan geçen", func(a *evalFluencyAgg) string {
		if a.VerifierN == 0 {
			return "ölçülmedi"
		}
		return evalPct(a.VerifierPass, a.VerifierN) + fmt.Sprintf(" (%d / %d)", a.VerifierPass, a.VerifierN)
	})
	metric("Türetilen değer içeren tur (değer)", func(a *evalFluencyAgg) string {
		if a.VerifierN == 0 {
			return "—"
		}
		return fmt.Sprintf("%d (%d)", a.DerivedTurns, a.DerivedClaims)
	})
	b.WriteString("\nTanımlar: kalıp açılış/kapanış, cümle, karakter, madde listesi ve soruyla bitiş audit §4 S2 sorgusunun tanımlarıdır (`internal/ai/evalrubric/fluency.go`; sondaki \"Kaynak:\" satırı gövdeye girmez). Yüzdeler cevaplanan turlara göredir. Döngü turu ve araç çağrısı v0.10.2318 döngü telemetrisinden (yoksa tur kancası); \"—\" = ölçülmedi. Doğrulayıcı (E1, gölge): bir tur cevaptaki sayı/saat/ad iddialarının hiçbiri uyuşmazsa geçer; türetilen değer (kanıttaki iki değerin toplamı, farkı, oranı) geçmeyi bozmaz, ayrı sayılır; payda yalnız model anlatımı olan turlardır. Kalıp (v0.10.2338): \"model yazdı\" = modelin metninde kalıp açılış/kapanış vardı (ekrana ulaşan ya da cevap dikişinin sildiği); \"sunucu satırı\" = cevap metninde sunucunun düz Kaynak satırı ya da genel cevap notu (v2'de üslup açıkken ikisi de adım çipine taşınır).\n")
	return b.String()
}

func (s *Server) getAIEvalsetFluencyReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var run evalRunSummary
	var cases []evalCaseResult
	if sum, cs, ok := evalJobs.snapshot(id); ok {
		run, cases = sum, cs
	} else {
		row, err := s.loadEvalRun(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if row == nil {
			writeJSONError(w, http.StatusNotFound, "koşu bulunamadı")
			return
		}
		run, cases = evalSummaryFromRow(*row, time.Now(), false), evalCasesFromRow(*row)
	}
	if len(evalFluencyProfiles(cases)) == 0 {
		writeJSONError(w, http.StatusNotFound, "bu koşuda konuşma kipi (ChatConversation) vakası yok")
		return
	}
	date := run.StartedAt
	if len(date) >= 10 {
		date = date[:10]
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fluency-%s.md"`, date))
	_, _ = w.Write([]byte(evalFluencyReport(run, cases)))
}

// evalTurnClass — SAF (v0.10.2331): turun sınıfı — sunucunun cevap sınıfı
// (chat_answer_class.go) varsa o, yoksa fikstürün sınıfı (eski koşu).
func evalTurnClass(t evalConvTurnResult) string {
	if t.AnswerClass != "" {
		return t.AnswerClass
	}
	return t.Class
}

// evalStyleLabel — SAF (v0.10.2331): koşu zorlaması → (on|off, forced). nil
// = canlı ayar (koşu anındaki değer).
func evalStyleLabel(override *bool) (string, bool) {
	on, forced := chstore.CurrentCosreConversation().Style, false
	if override != nil {
		on, forced = *override, true
	}
	if on {
		return "on", forced
	}
	return "off", forced
}

// evalVerifyLabel — SAF (v0.10.2339): koşu / vaka zorlaması → (kip, forced).
// "" = canlı ayar (koşu anındaki değer).
func evalVerifyLabel(override string) (string, bool) {
	if override != "" {
		return override, true
	}
	return chstore.CurrentCosreConversation().Verification, false
}

// evalFluencyVerifyLine — SAF (v0.10.2339): künyenin doğrulama kipi satırı
// (turların kaydından; zorla kipinde işaretlenen / yeniden üretilen tur sayısı).
// "Doğrulayıcıdan geçen" payı işaretten ÖNCEKİ model metniyle ölçülür.
func evalFluencyVerifyLine(cases []evalCaseResult) string {
	seen := map[string]bool{}
	var vals []string
	annotated, regenerated := 0, 0
	for _, c := range cases {
		if c.Mode != evalModeConversation {
			continue
		}
		for _, t := range c.Turns {
			if t.Verification == "" {
				continue
			}
			v := t.Verification + " (canlı ayar)"
			if t.VerificationForced {
				v = t.Verification + " (zorlandı)"
			}
			if !seen[v] {
				seen[v] = true
				vals = append(vals, v)
			}
			if strings.Contains(t.Enforce, verifyEnforceAnnotated) {
				annotated++
			}
			if strings.HasPrefix(t.Enforce, verifyEnforceRegenerated) {
				regenerated++
			}
		}
	}
	if len(vals) == 0 {
		return ""
	}
	return fmt.Sprintf("- Doğrulama kipi (`cosre_conversation.verification`): %s · zorla kipinde işaretlenen tur %d, yeniden üretilen tur %d\n",
		strings.Join(vals, ", "), annotated, regenerated)
}

// evalFluencyStyleLine — SAF (v0.10.2331): künyenin üslup satırı; turların
// kaydından (eski koşu: alan yok → satır "bilinmiyor").
func evalFluencyStyleLine(cases []evalCaseResult) string {
	seen := map[string]bool{}
	var vals []string
	for _, c := range cases {
		if c.Mode != evalModeConversation {
			continue
		}
		for _, t := range c.Turns {
			v := "bilinmiyor (bu sürümden eski koşu)"
			switch {
			case t.Style == "on" && t.StyleForced:
				v = "AÇIK (bu koşu için zorlandı)"
			case t.Style == "off" && t.StyleForced:
				v = "KAPALI (bu koşu için zorlandı)"
			case t.Style == "on":
				v = "açık (canlı ayar)"
			case t.Style == "off":
				v = "kapalı (canlı ayar)"
			}
			if !seen[v] {
				seen[v] = true
				vals = append(vals, v)
			}
		}
	}
	if len(vals) == 0 {
		return ""
	}
	return "- Üslup anahtarı (`cosre_conversation.style`): " + strings.Join(vals, ", ") + "\n"
}
