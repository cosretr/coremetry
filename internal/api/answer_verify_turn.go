package api

// answer_verify_turn.go — v0.10.2327 (conversation-audit §6.1 E1): doğrulayıcının
// GÖLGE bağlantısı. Tur boyunca kanıt toplanır, cevap ve akış HİÇ değişmez;
// doğrulama turun kayıt defer'inde (recordChatExchange) koşar — "done"
// yayınlandıktan sonra, SSE yolunun dışında — ve yalnız sayar:
//
//   - cosre_interactions.verify kolonu: "v1 tier=… tür=denetlenen/doğrulanan/türetilen/uyuşmayan";
//   - en az bir uyuşmazlıkta TEK sistem sinyali verify_mismatch (aktör
//     system:cosre; ayrıntı yalnız kademe, türler ve uyuşmayan jetonlar —
//     cevap metni yok, ≤512 B);
//   - değerlendirme koşusunda sonuç koşucuya (evalAgentOutcome.Verify).
//
// Kapı: v2 (cosreV2Marked) VE cosre_conversation.verification ≠ off. "off"
// kill switch'tir; "enforce" bu dilimde PUT'ta reddedilir, okunursa da gölge
// gibi davranır (E2'ye kadar hiçbir cevap değişmez).
//
// Kanıt (kademe başına):
//   - döngü: yürüyen her aracın argümanı + TAM çıktısı (modele giden 2.048
//     baytlık kırpık kopya DEĞİL; read_source_code'dan yalnız referans satırı),
//     konuşmadaki tohum araç sonuçları (ön-getirme), ekran / sayfa / kural /
//     trace takibi önsözleri, v2 kapsam eki;
//   - guided: sunucunun yazdığı kanıt bloğu + kural bloğu + çekmece bağlamı +
//     rotanın özet olguları (guidedSummaryFacts) + rota penceresi;
//   - genel cevap: yalnız operatörün kendi metni (canlı veri beklenmez);
//   - hepsinde: operatör turlarının metni, pencere sınırları ve turun anı,
//     konuşma durumunun adları ve önceki turun defteri. İstemcinin
//     yankıladığı durum (fromClient) ASLA kanıt değildir.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

// verifyEvidenceMaxBytes — tur başına toplanan kanıt metni tavanı (bellek).
// Aşılırsa kalan kanıt düşer ve sonuç "truncated" işaretlenir.
const verifyEvidenceMaxBytes = 2 << 20

// verifySignalMaxTokens / verifySignalTokenMax — sinyal ayrıntısındaki jeton tavanları.
const (
	verifySignalMaxTokens = 8
	verifySignalTokenMax  = 48
)

// answerVerifyTurn — bir turun doğrulayıcı toplayıcısı; nil = kapalı (yöntemler nil-güvenli).
type answerVerifyTurn struct {
	mu        sync.Mutex
	loc       *time.Location
	catalog   []string
	texts     []string
	size      int
	truncated bool
	numbers   []float64
	names     []string
	times     []time.Time
	tier      string
	answer    string
}

// verifyShadowOn — kapı: v2 ve doğrulama kapalı değil.
func verifyShadowOn(ctx context.Context) bool {
	return cosreV2Marked(ctx) && chstore.CurrentCosreConversation().Verification != chstore.CosreVerifyOff
}

// newAnswerVerifyTurn — kapalıyken nil. Saat dilimi çözücüyle aynı kural.
// v0.10.2328: değerlendirme konuşma kipinde ayardan bağımsız açık (yalnız
// sayaç; koşu çevrimdışıdır, "doğrulayıcıdan geçen pay" her koşuda ölçülür).
func (s *Server) newAnswerVerifyTurn(ctx context.Context, req *chatRequest) *answerVerifyTurn {
	if !verifyShadowOn(ctx) && (evalConvFrom(ctx) == nil || !cosreV2Marked(ctx)) {
		return nil
	}
	v := &answerVerifyTurn{loc: chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin), catalog: s.verifyCatalog(ctx)}
	v.times = append(v.times, time.Now())
	v.addText(traceFollowUpEvidenceSeed(nil, req.Messages)) // yalnız operatör turları
	return v
}

// verifyCatalog — ad tespiti için servis kataloğu (guided ile aynı önbellek
// anahtarı); store'suz sunucuda yalnız önbellek okunur.
func (s *Server) verifyCatalog(ctx context.Context) []string {
	if names := evalConvCatalog(ctx); names != nil {
		return names
	}
	if s.store != nil {
		return s.guidedServiceNames(ctx)
	}
	if s.cache == nil {
		return nil
	}
	if b, ok, _ := s.cache.Get(ctx, "copilot:guided:svcnames"); ok {
		var names []string
		if json.Unmarshal(b, &names) == nil {
			return names
		}
	}
	return nil
}

// verifyTurnFrom — ctx'teki turun toplayıcısı (sohbet dışında / kapalıyken nil).
func verifyTurnFrom(ctx context.Context) *answerVerifyTurn {
	if x := chatExchangeFrom(ctx); x != nil {
		return x.verify
	}
	return nil
}

func (v *answerVerifyTurn) addText(texts ...string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range texts {
		if strings.TrimSpace(t) == "" {
			continue
		}
		if v.size+len(t) > verifyEvidenceMaxBytes {
			v.truncated = true
			continue
		}
		v.size += len(t)
		v.texts = append(v.texts, t)
	}
}

// addFacts — defter olguları: değer sayı kanıtı, kapsam ad kanıtı.
func (v *answerVerifyTurn) addFacts(facts []chatFact) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, f := range facts {
		v.numbers = append(v.numbers, f.Value)
		if f.Scope != "" {
			v.names = append(v.names, f.Scope)
		}
	}
}

// addWindow — pencere sınırları (saat/tarih) ve uzunluğu (saniye; "son 15 dakika").
func (v *answerVerifyTurn) addWindow(from, to time.Time, rangeS int64) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range []time.Time{from, to} {
		if !t.IsZero() && t.Unix() > 0 {
			v.times = append(v.times, t)
		}
	}
	if rangeS > 0 {
		v.numbers = append(v.numbers, float64(rangeS))
	}
}

// noteState — konuşma durumu: adlar + önceki turun defteri. İstemci yankısı hiç alınmaz.
func (v *answerVerifyTurn) noteState(st *chatContextState) {
	if v == nil || st == nil || st.fromClient {
		return
	}
	c := st.ctx
	v.mu.Lock()
	for _, n := range append([]string{c.Cluster, c.Namespace, c.Workload, c.Service, c.Pod, c.Trace, c.Operation, c.Env, c.Problem}, c.Services...) {
		if n != "" {
			v.names = append(v.names, n)
		}
	}
	v.mu.Unlock()
	v.addFacts(c.Ledger)
	if c.RangeS > 0 && c.WinToMs > 0 {
		to := time.UnixMilli(c.WinToMs)
		v.addWindow(to.Add(-time.Duration(c.RangeS)*time.Second), to, c.RangeS)
	}
}

// addTool — döngünün yürüyen çağrısı: argüman + TAM çıktı.
func (v *answerVerifyTurn) addTool(name string, args json.RawMessage, content string) {
	if v == nil {
		return
	}
	v.addText(string(args), chatFollowUpEvidence(name, content))
}

// addConversation — döngü konuşmasındaki araç argümanları ve sonuçları (ön-getirme tohumları dahil).
func (v *answerVerifyTurn) addConversation(conv []copilot.ChatMessage) {
	if v == nil {
		return
	}
	for _, m := range conv {
		for _, tc := range m.ToolCalls {
			v.addText(string(tc.Input))
		}
		for _, tr := range m.ToolResults {
			if !tr.IsError {
				v.addText(chatFollowUpEvidence(tr.Name, tr.Content))
			}
		}
	}
}

// addLoopContext — döngü önsözleri; durum önsözleri yalnız durum istemci yankısı değilse.
func (v *answerVerifyTurn) addLoopContext(st *chatContextState, fixed []string, stateTexts ...string) {
	if v == nil {
		return
	}
	v.addText(fixed...)
	if st != nil && !st.fromClient {
		v.addText(stateTexts...)
	}
}

// noteLoopAnswer — döngünün son cevabı: konuşmadaki araç izleri + model metni.
func (v *answerVerifyTurn) noteLoopAnswer(text string, conv []copilot.ChatMessage) {
	if v == nil {
		return
	}
	v.addConversation(conv)
	v.setAnswer(verifyTierLoop, text)
}

// setAnswer — anlatım kademesinin MODEL metni (sunucu ekleri hariç); son yazan kazanır.
func (v *answerVerifyTurn) setAnswer(tier, text string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	v.tier, v.answer = tier, text
	v.mu.Unlock()
}

// run — turun doğrulaması (anlatım yoksa ok=false: rapor, şablon, LLM'siz cevap).
func (v *answerVerifyTurn) run() (verifyResult, bool) {
	if v == nil {
		return verifyResult{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.tier == "" || strings.TrimSpace(v.answer) == "" {
		return verifyResult{}, false
	}
	ev := verifyEvidence{Texts: v.texts, Numbers: v.numbers, Names: v.names, Times: v.times, Loc: v.loc}
	return verifyAnswer(v.tier, v.answer, ev, v.catalog), true
}

// guidedLedgerFacts — SAF: guided rotasının özet sayıları → defter satırları.
func guidedLedgerFacts(intent string, f guidedSummaryFacts, window string) []chatFact {
	tool := "guided:" + intent
	var out []chatFact
	add := func(scope, name string, v float64, unit string) {
		if len(out) < chatLedgerMax {
			out = append(out, chatFact{Tool: tool, Scope: scope, Name: name, Value: v, Unit: unit, Window: window})
		}
	}
	if f.REDKnown {
		add(f.Scope, "error_rate_pct", f.ErrorRatePct, "%")
		add(f.Scope, "p99_ms", f.P99Ms, "ms")
	}
	if f.ProblemsKnown {
		add(f.Scope, "open_problems", float64(f.Problems), "")
	}
	if f.TeamREDKnown {
		add(f.Scope, "services", float64(f.Services), "")
		add(f.Scope, "error_services", float64(f.ErrServices), "")
		if f.Worst != "" {
			add(f.Worst, "error_rate_pct", f.WorstErrPct, "%")
		}
	}
	return out
}

// noteGuidedNarration — guided anlatımı: kanıt bloğu, olgular, pencere ve model metni.
func noteGuidedNarration(ctx context.Context, intent, raw, evidence, explain string, facts guidedSummaryFacts, from, to time.Time, rangeS int64) {
	gf := guidedLedgerFacts(intent, facts, "")
	stateTurnFrom(ctx).noteFacts(gf) // defter: guided turu da sonraki tura olgu taşır
	v := verifyTurnFrom(ctx)
	if v == nil {
		return
	}
	v.addText(evidence, explain)
	v.addFacts(gf)
	v.addWindow(from, to, rangeS)
	v.setAnswer(verifyTierGuided, raw)
}

// verifySignalDetail — SAF: sinyal ayrıntısı "tier=… kinds=number:1,name:1 tokens=a|b" (≤512 B).
func verifySignalDetail(r verifyResult, truncated bool) string {
	mm := r.mismatches()
	kinds := map[string]int{}
	var toks []string
	for _, c := range mm {
		kinds[c.Kind]++
		if len(toks) < verifySignalMaxTokens {
			toks = append(toks, strings.ReplaceAll(clipBytes(c.Text, verifySignalTokenMax), "|", "/"))
		}
	}
	ks := make([]string, 0, len(kinds))
	for _, k := range verifyKinds {
		if n := kinds[k]; n > 0 {
			ks = append(ks, k+":"+strconv.Itoa(n))
		}
	}
	d := "tier=" + r.Tier + " kinds=" + strings.Join(ks, ",") + " tokens=" + strings.Join(toks, "|")
	if truncated {
		d += " truncated=1"
	}
	return clipBytes(d, cosreSignalDetailMax)
}

// verifySignals — SAF: uyuşmazlık varsa TEK sistem sinyali.
func verifySignals(xid string, r verifyResult, truncated bool) []chstore.CosreSignal {
	if len(r.mismatches()) == 0 {
		return nil
	}
	return []chstore.CosreSignal{{ExchangeID: xid, Kind: cosrelearn.SigVerifyMismatch,
		UserEmail: cosrelearn.SystemSignalActor, Detail: verifySignalDetail(r, truncated)}}
}

// verifyOutcome — turun kayıt sonucu (kolon + değerlendirme).
type verifyOutcome struct {
	Compact   string
	Result    verifyResult
	Truncated bool
}

// finish — doğrulamayı koşar (kayıt defer'i); ok=false → anlatım yok ya da kapalı.
func (v *answerVerifyTurn) finish() (verifyOutcome, bool) {
	r, ok := v.run()
	if !ok {
		return verifyOutcome{}, false
	}
	v.mu.Lock()
	tr := v.truncated
	v.mu.Unlock()
	c := r.compact()
	if tr {
		c += " truncated"
	}
	return verifyOutcome{Compact: c, Result: r, Truncated: tr}, true
}
