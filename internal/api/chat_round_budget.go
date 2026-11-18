package api

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_round_budget.go — v0.10.2340: serbest döngünün PROAKTİF tur bütçesi.
//
// Bugüne dek bağlam taşması yalnız TEPKİSEL ele alınıyordu: sağlayıcı 400
// verdikten sonra konuşma yarıya indirilip tur bir kez yeniden deneniyordu
// (chat_overflow.go). Uzun incelemeler ya "bağlam taştı" ile ölüyor ya da
// yarıya indirme en eski talimatları sessizce siliyordu. Pencere modeli
// çalıştıran sunucudan zaten okunuyordu (copilot.ContextWindowInfoFor) ama
// döngü onu kullanmıyordu.
//
// Her model çağrısından ÖNCE (loopTurn — döngünün tek çağrı kapısı):
//
//	tahmin = önceki çağrının GERÇEK giriş token'ı
//	         + (bu çağrının baytı − o çağrının baytı) / bpt
//	ilk çağrıda (gerçek sayı yokken)  tahmin = bayt / bpt
//	bpt  = 3 bayt/token (muhafazakâr); alışverişin ilk gerçek sayısıyla
//	       kalibre edilir: bpt = gönderilen bayt / gerçek token, [1.5, 6]
//	sınır = %80 × (pencere − turun max_tokens'ı − pay), pay = max(pencere/20, 256)
//
// Bayt = sistem prompt'u + araç kataloğu (alışveriş başına bir kez
// serileştirilir, önbellekte) + mesaj içeriklerinin uzunluk toplamı —
// konuşma her turda yeniden serileştirilmez.
//
// Tahmin sınırı aşarsa KONUŞMANIN KOPYASI deterministik daraltılır (model
// çağrısı yok): önce EN ESKİ araç sonuçları tek satırlık bir saplamaya
// iner (araç adı, argümanın kapsamı, ilk satırın başı, "kısaltıldı"
// işareti; çağrı/sonuç çifti yapısal olarak durur), sonra hâlâ sığmıyorsa
// eski asistan/araç turları BÜTÜN çift olarak çıkar. Sistem prompt'u, araç
// kataloğu, kullanıcı mesajları, son turun sonuçları ve her araç ailesinin
// en son sonucu ASLA değişmez. Trace takibinde log ve trace aileleri en son
// kısaltılır (öncelikli kanıt).
//
// Döngünün kendi `conv`'u değişmez: doğrulayıcı (noteLoopAnswer) ve durum
// defteri yürütmede yakalanan tam çıktıyı okur.
//
// Pencere bilinmiyorsa (yoklama başarısız ya da ad tahmini, Source != "server")
// ya da tam daraltma bile sığmıyorsa hiçbir şey yapılmaz: tepkisel yol
// bugünkü gibi güvenlik ağıdır. Taşma sonrası yeniden deneme çağrısında
// proaktif adım UYGULANMAZ (aynı turda iki küçültme yok).
//
// Ayar yok; v1 ve v2 ortak (tepkisel yol da ortak). Sınırın altında
// konuşma dilimi AYNEN döner: sağlayıcıya giden bayt bugünküyle aynı.

const (
	// roundBudgetBytesPerToken — kalibrasyon öncesi oran. Araç sonuçları
	// sayı/kimlik ağırlıklı JSON, metin Türkçe (çok baytlı harf): ikisi de
	// İngilizce düzyazının ~4 bayt/token'ından yoğun. 3, trace drill'in
	// 4 karakter/token kabulünden muhafazakâr (token'ı fazla sayar).
	roundBudgetBytesPerToken = 3.0
	roundBudgetMinBPT        = 1.5
	roundBudgetMaxBPT        = 6.0
	// roundBudgetFillPct — kullanılabilir alanın en çok bu yüzdesi.
	roundBudgetFillPct = 80
	// roundBudgetMarginMin — güvenlik payının tabanı (token).
	roundBudgetMarginMin = 256
	// roundBudgetHeadRunes — saplamada kalan ilk satır başı.
	roundBudgetHeadRunes = 100
	// roundBudgetScopeRunes — saplamadaki tek kapsam değerinin tavanı.
	roundBudgetScopeRunes = 40
	// roundBudgetMsgOverhead / roundBudgetPartOverhead — rol, kimlik, şablon payı (bayt).
	roundBudgetMsgOverhead  = 16
	roundBudgetPartOverhead = 24
)

// roundBudgetStubFmt — kısaltılan sonucun modelin gördüğü hâli (chatToolResultTruncNote üslubu).
// %s: araç adı, kapsam, ilk satır başı; %d: atlanan karakter.
const roundBudgetStubFmt = "[kısaltıldı: bağlam penceresine sığması için bu eski araç sonucu çıkarıldı — %s%s; ilk satırı: %s (%d karakter atlandı). " +
	"Bu sonuca dayanarak sayım yapma; gerekiyorsa aracı daha dar bir kapsamla yeniden çağır.]"

// roundBudgetDropNoteFmt — bütün çift çıkarıldığında kalan ilk sonucun başına.
const roundBudgetDropNoteFmt = "[kısaltıldı: bağlam penceresine sığması için önceki %d araç turu (çağrılar ve sonuçları) çıkarıldı.]\n\n"

// roundBudgetChipFmt — operatörün gördüğü adım satırı ("bağlam taştı — …" emsali).
const roundBudgetChipFmt = "bağlam daraltıldı: %d eski araç sonucu kısaltıldı"

// roundBudgetScopeKeys — saplamada kalan argümanlar (kapsam: servis / pencere).
var roundBudgetScopeKeys = []string{"service", "env", "operation", "range_s", "window", "from", "to", "trace_id"}

// roundBudgetPriorityFamilies — trace takibinde en son kısaltılan aileler.
var roundBudgetPriorityFamilies = map[string]bool{"Log": true, "Trace": true}

// roundBudgetWindowSeam — test dikişi: nil = copilot.ContextWindowInfoFor.
var roundBudgetWindowSeam func(context.Context) copilot.ContextWindowInfo

// roundBudget — alışverişin bütçe durumu (döngü tek goroutine; roundRetry emsali).
type roundBudget struct {
	traceFollowUp bool

	resolved bool
	window   int // 0 = bilinmiyor → proaktif adım yok

	prevIn, prevBytes int     // son başarılı çağrının gerçek token'ı ve gönderilen baytı
	bpt               float64 // 0 = kalibre edilmedi
	sentBytes         int     // bu çağrıda gönderilen bayt (observe kalibre eder)
	lastOverflow      bool    // önceki çağrı taştı → bu çağrı tepkisel yeniden deneme

	specKey   uint64
	specBytes int

	peakTrimmed           uint16 // tek çağrıda tam görülmeyen sonuç sayısının en büyüğü
	chipStubbed, chipDrop int    // son yayınlanan adım satırı
}

type roundBudgetKey struct{}

func withRoundBudget(ctx context.Context, b *roundBudget) context.Context {
	return context.WithValue(ctx, roundBudgetKey{}, b)
}

func roundBudgetFrom(ctx context.Context) *roundBudget {
	b, _ := ctx.Value(roundBudgetKey{}).(*roundBudget)
	return b
}

// fitRoundBudget — loopTurn: bu çağrıda gönderilecek konuşma (sığıyorsa aynı dilim).
func (s *Server) fitRoundBudget(ctx context.Context, emit func(string, any), sys string, conv []copilot.ChatMessage, specs []copilot.ToolSpec) []copilot.ChatMessage {
	b := roundBudgetFrom(ctx)
	if b == nil {
		return conv
	}
	if !b.resolved {
		b.resolved = true
		b.window = s.roundBudgetWindow(ctx)
	}
	if b.window <= 0 {
		return conv
	}
	fixed := len(sys) + b.catalogBytes(specs)
	if b.lastOverflow { // tepkisel yeniden deneme: küçültme zaten yapıldı
		b.lastOverflow = false
		b.sentBytes = fixed + chatConvBytes(conv)
		return conv
	}
	plan := planRoundCompaction(roundCompactIn{
		Conv: conv, FixedBytes: fixed, Window: b.window, MaxTokens: s.roundBudgetMaxTokens(ctx),
		PrevIn: b.prevIn, PrevBytes: b.prevBytes, BPT: b.bpt, TraceFollowUp: b.traceFollowUp,
	})
	b.sentBytes = plan.Bytes
	if plan.Stubbed+plan.DroppedPairs == 0 {
		return conv
	}
	if t := uint16(min(plan.Trimmed, math.MaxUint16)); t > b.peakTrimmed {
		b.peakTrimmed = t
	}
	if plan.Stubbed > b.chipStubbed || plan.DroppedPairs > b.chipDrop {
		b.chipStubbed, b.chipDrop = plan.Stubbed, plan.DroppedPairs
		emit("step", map[string]string{"label": roundBudgetChipLabel(plan.Stubbed, plan.DroppedPairs)})
	}
	return plan.Conv
}

// observe — çağrının sonucu: ilk gerçek sayı kalibre eder; taşma bir sonraki
// çağrıyı tepkisel yeniden deneme olarak işaretler.
func (b *roundBudget) observe(turn copilot.ChatTurn, err error) {
	if b == nil || b.window <= 0 {
		return
	}
	if err != nil {
		b.lastOverflow = isContextOverflowErr(err)
		return
	}
	if turn.InputTokens == 0 || b.sentBytes <= 0 {
		return
	}
	if b.bpt == 0 {
		b.bpt = clampBPT(float64(b.sentBytes) / float64(turn.InputTokens))
	}
	b.prevIn, b.prevBytes = int(turn.InputTokens), b.sentBytes
}

// roundBudgetWindow — çözülen profilin penceresi; yalnız sunucudan okunduysa.
func (s *Server) roundBudgetWindow(ctx context.Context) int {
	var info copilot.ContextWindowInfo
	switch {
	case roundBudgetWindowSeam != nil:
		info = roundBudgetWindowSeam(ctx)
	case s != nil && s.copilot != nil:
		info = s.copilot.ContextWindowInfoFor(ctx)
	}
	if info.Source != "server" || info.Tokens <= 0 {
		return 0 // ad tahmini düşük güven: proaktif adım yok
	}
	return info.Tokens
}

// roundBudgetMaxTokens — turun cevap payı: çözülen profilin max_tokens'ı
// (Derin kipte ölçekli), ctx tavanı varsa küçüğü.
func (s *Server) roundBudgetMaxTokens(ctx context.Context) int {
	if s == nil || s.copilot == nil {
		return 0
	}
	n := s.copilot.MaxTokensFor(ctx)
	if c := copilot.MaxTokensCapOf(ctx); c > 0 && c < n {
		n = c
	}
	return n
}

// catalogBytes — araç kataloğunun baytı; liste değişmedikçe yeniden serileştirilmez.
func (b *roundBudget) catalogBytes(specs []copilot.ToolSpec) int {
	h := fnv.New64a()
	for _, sp := range specs {
		_, _ = h.Write([]byte(sp.Name))
		_, _ = h.Write([]byte{0})
	}
	k := h.Sum64()
	if k == b.specKey && b.specBytes > 0 {
		return b.specBytes
	}
	n := 0
	for _, sp := range specs {
		raw, _ := json.Marshal(sp.InputSchema)
		n += len(sp.Name) + len(sp.Description) + len(raw) + roundBudgetPartOverhead
	}
	b.specKey, b.specBytes = k, n
	return n
}

// mark — döngü satırının alanı: tek çağrıda tam görülmeyen en çok sonuç.
func (b *roundBudget) mark(lu copilot.LoopUsage) copilot.LoopUsage {
	if b != nil {
		lu.CompactedResults = b.peakTrimmed
	}
	return lu
}

func roundBudgetChipLabel(stubbed, dropped int) string {
	l := fmt.Sprintf(roundBudgetChipFmt, stubbed)
	if dropped > 0 {
		l += fmt.Sprintf(", %d eski tur çıkarıldı", dropped)
	}
	return l
}

func clampBPT(v float64) float64 {
	return math.Max(roundBudgetMinBPT, math.Min(roundBudgetMaxBPT, v))
}

// ── SAF çekirdek ───────────────────────────────────────────────────────────

// roundEstimate — SAF: bu çağrının token tahmini.
func roundEstimate(bytes, prevIn, prevBytes int, bpt float64) int {
	if bpt <= 0 {
		bpt = roundBudgetBytesPerToken
	}
	if prevIn > 0 && prevBytes > 0 {
		return max(0, prevIn+int(math.Ceil(float64(bytes-prevBytes)/bpt)))
	}
	return int(math.Ceil(float64(bytes) / bpt))
}

// roundBudgetLimit — SAF: tahminin aşmaması gereken token (0 = yer yok).
func roundBudgetLimit(window, maxTok int) int {
	margin := max(window/20, roundBudgetMarginMin)
	avail := window - maxTok - margin
	if avail <= 0 {
		return 0
	}
	return avail * roundBudgetFillPct / 100
}

// chatMsgBytes — SAF: mesajın içerik baytı (serileştirmesiz).
func chatMsgBytes(m copilot.ChatMessage) int {
	n := roundBudgetMsgOverhead + len(m.Role) + len(m.Text) + len(m.RawContent)
	for _, tc := range m.ToolCalls {
		n += roundBudgetPartOverhead + len(tc.ID) + len(tc.Name) + len(tc.Input) + len(tc.Raw)
	}
	for _, tr := range m.ToolResults {
		n += roundBudgetPartOverhead + len(tr.CallID) + len(tr.Name) + len(tr.Content)
	}
	return n
}

func chatConvBytes(conv []copilot.ChatMessage) int {
	n := 0
	for _, m := range conv {
		n += chatMsgBytes(m)
	}
	return n
}

type roundCompactIn struct {
	Conv              []copilot.ChatMessage
	FixedBytes        int // sistem prompt'u + katalog
	Window, MaxTokens int
	PrevIn, PrevBytes int
	BPT               float64
	TraceFollowUp     bool
}

type roundCompactOut struct {
	Conv         []copilot.ChatMessage // sığıyorsa ya da sığdırılamıyorsa girdi dilimi AYNEN
	Bytes        int                   // gönderilecek bayt
	Estimate     int                   // daraltmadan önceki tahmin
	Limit        int
	Stubbed      int // saplamaya inen sonuç
	DroppedPairs int // bütün çıkarılan tur
	Trimmed      int // tam görülmeyen sonuç (saplama + çıkarılan turlardaki sonuçlar)
	Fits         bool
}

type budgetCand struct{ msg, res int }

// planRoundCompaction — SAF, deterministik: girdi konuşması DEĞİŞMEZ.
func planRoundCompaction(in roundCompactIn) roundCompactOut {
	cur := in.FixedBytes + chatConvBytes(in.Conv)
	est := roundEstimate(cur, in.PrevIn, in.PrevBytes, in.BPT)
	lim := roundBudgetLimit(in.Window, in.MaxTokens)
	out := roundCompactOut{Conv: in.Conv, Bytes: cur, Estimate: est, Limit: lim, Fits: est <= lim}
	if out.Fits || lim <= 0 {
		return out
	}
	bpt := in.BPT
	if bpt <= 0 {
		bpt = roundBudgetBytesPerToken
	}
	need := int(math.Ceil(float64(est-lim) * bpt)) // kazanılması gereken bayt

	conv := in.Conv
	cur2 := -1 // son turun sonuç mesajı (asla dokunulmaz)
	for i := len(conv) - 1; i >= 0; i-- {
		if len(conv[i].ToolResults) > 0 {
			cur2 = i
			break
		}
	}
	// her ailenin en son sonucu korunur
	protected := map[string]bool{}
	seenFam := map[string]bool{}
	for i := len(conv) - 1; i >= 0; i-- {
		for j := len(conv[i].ToolResults) - 1; j >= 0; j-- {
			tr := conv[i].ToolResults[j]
			f := budgetFamily(tr.Name)
			if !seenFam[f] {
				seenFam[f] = true
				protected[budgetResKey(i, j)] = true
			}
		}
	}
	var cands, late []budgetCand
	for i, m := range conv {
		if i == cur2 {
			continue
		}
		for j, tr := range m.ToolResults {
			if protected[budgetResKey(i, j)] {
				continue
			}
			c := budgetCand{i, j}
			if in.TraceFollowUp && roundBudgetPriorityFamilies[stepPhaseTR(tr.Name)] {
				late = append(late, c)
			} else {
				cands = append(cands, c)
			}
		}
	}
	cands = append(cands, late...)

	work := make([]copilot.ChatMessage, len(conv))
	copy(work, conv)
	copied := map[int]bool{}
	saved := 0
	stubbedAt := map[int]int{}
	for _, c := range cands {
		if saved >= need {
			break
		}
		tr := conv[c.msg].ToolResults[c.res]
		stub := budgetStub(tr, budgetCallArgs(conv, c.msg, tr.CallID))
		if len(stub) >= len(tr.Content) {
			continue
		}
		if !copied[c.msg] {
			work[c.msg].ToolResults = append([]copilot.ToolResult(nil), conv[c.msg].ToolResults...)
			copied[c.msg] = true
		}
		work[c.msg].ToolResults[c.res].Content = stub
		saved += len(tr.Content) - len(stub)
		out.Stubbed++
		stubbedAt[c.msg]++
	}

	// hâlâ sığmıyorsa: eski turlar bütün çift olarak (korunan sonucu taşımayanlar)
	drop := map[int]bool{}
	dropRes := 0
	for i := 0; i+1 < len(work) && saved < need; i++ {
		if len(work[i].ToolCalls) == 0 || len(work[i+1].ToolResults) == 0 || i+1 == cur2 {
			continue
		}
		hasProt := false
		for j := range work[i+1].ToolResults {
			if protected[budgetResKey(i+1, j)] {
				hasProt = true
				break
			}
		}
		if hasProt {
			continue
		}
		drop[i], drop[i+1] = true, true
		saved += chatMsgBytes(work[i]) + chatMsgBytes(work[i+1])
		out.DroppedPairs++
		dropRes += len(work[i+1].ToolResults)
		out.Stubbed -= stubbedAt[i+1] // çıkan çiftin saplamaları ayrıca sayılmaz
		i++
	}
	if out.DroppedPairs > 0 {
		kept := make([]copilot.ChatMessage, 0, len(work)-2*out.DroppedPairs)
		noted := false
		for i, m := range work {
			if drop[i] {
				continue
			}
			if !noted && len(m.ToolResults) > 0 {
				note := fmt.Sprintf(roundBudgetDropNoteFmt, out.DroppedPairs)
				if !copied[i] {
					m.ToolResults = append([]copilot.ToolResult(nil), m.ToolResults...)
				}
				m.ToolResults[0].Content = note + m.ToolResults[0].Content
				saved -= len(note)
				noted = true
			}
			kept = append(kept, m)
		}
		work = kept
	}
	if saved < need || out.Stubbed+out.DroppedPairs == 0 {
		// tam daraltma da sığmıyor: bugünkü istek, tepkisel yol güvenlik ağı
		return roundCompactOut{Conv: in.Conv, Bytes: cur, Estimate: est, Limit: lim}
	}
	out.Conv, out.Bytes, out.Fits = work, cur-saved, true
	out.Trimmed = out.Stubbed + dropRes
	return out
}

// budgetCallArgs — sonucun çağrısının argümanı: HEMEN önceki asistan mesajında
// aranır (bazı yerel sunucular kimliği her turda yeniden kullanır: "call_1").
func budgetCallArgs(conv []copilot.ChatMessage, msg int, id string) json.RawMessage {
	if msg == 0 {
		return nil
	}
	for _, tc := range conv[msg-1].ToolCalls {
		if tc.ID == id {
			return tc.Input
		}
	}
	return nil
}

func budgetResKey(i, j int) string { return fmt.Sprintf("%d/%d", i, j) }

// budgetFamily — araç ailesi: evre (chat_step_phase.go), bilinmiyorsa araç adı.
func budgetFamily(name string) string {
	if p := stepPhaseTR(name); p != "" {
		return p
	}
	return "tool:" + name
}

// budgetStub — SAF: sonucun tek satırlık saplaması.
func budgetStub(tr copilot.ToolResult, input json.RawMessage) string {
	head := tr.Content
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	head = clipRunes(strings.TrimSpace(head), roundBudgetHeadRunes)
	dropped := utf8.RuneCountInString(tr.Content) - utf8.RuneCountInString(head)
	return fmt.Sprintf(roundBudgetStubFmt, tr.Name, budgetScope(input), head, max(dropped, 0))
}

// budgetScope — SAF: argümanın kapsamı " (service=checkout, range_s=3600)".
func budgetScope(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	var parts []string
	for _, k := range roundBudgetScopeKeys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				parts = append(parts, k+"="+clipRunes(x, roundBudgetScopeRunes))
			}
		case float64:
			parts = append(parts, fmt.Sprintf("%s=%g", k, x))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
