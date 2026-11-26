package cosrelearn

// failed_turns.go — v0.10.2468 (operatör onayı 2026-11-22: "CoSRE istemlerini
// körlemesine değiştirmeyi bırak"). Başarısız tur yakalama: ÖRTÜK sinyallerden
// bir İNCELEME KUYRUĞU — etiket DEĞİL. Bayrak bir turun neden şüpheli olduğunu
// söyler (gerekçe kodları); cevabın doğru/yanlış olduğuna karar vermez, gece
// puanına, aday seçimine ya da çalışma zamanı kararına girmez. 👍/👎 GİRDİ
// DEĞİLDİR (kalıcı kural: oylar zayıf sinyal).
//
// Gerekçeler (tur başına, tekil, sıralı):
//
//	reask              aynı kullanıcı ≤60 sn içinde aynı soruyu yeniden sordu
//	                   (normalleştirilmiş kelime kümesi Jaccard ≥0.5) ya da
//	                   istemci retry / regenerate beyan etti
//	complaint_followup sonraki mesaj şikâyet: "noldu", "tekrar dene", "cevap
//	                   vermedin", "yanlış", "o değil" … (≤10 dk)
//	tool_error         turda başarısız araç (atlanan hariç)
//	timeout            tur ya da araç zaman aşımı
//	round_cap          serbest döngü tur / çağrı / token tavanına dayandı
//	                   (loop_stop sistem sinyali; cevap tavan turunda zorla)
//	context_overflow   model bağlam penceresi taştı (hata metni) ya da geçmiş
//	                   küçültülüp yeniden denendi = KIRPILMIŞ bağlam (loop_stop)
//	verify_mismatch    gölge doğrulayıcı uyuşmazlık saydı (sistem sinyali)
//	provider_error     sağlayıcı HTTP 4xx/5xx
//	error              başka tur hatası
//	empty_answer       boş cevap
//	clarify_only       tur yalnız netleştirme sorusu sordu (router boşluğu)
//
// Kümeler: haftalık (ISO hafta), normalleştirilmiş kelime kümesi üzerinde
// açgözlü, DETERMİNİSTİK tek geçiş (sıra: zaman, exchange_id).
//
// Saklama: gerekçeler cosre_signals'a sistem sinyali olarak yazılır
// (kind failed_turn, ayrıntı "reasons=a,b"; yeni şema yok). Saatlik süpürme
// (RunFailedTurnSweep) kalite işinin lider kilidi altında koşar; admin listesi
// aynı saf dedektörü canlı da koşturur ve saklı bayraklarla birleştirir.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// SigFailedTurn — tur başına bir sistem sinyali (ayrıntı "reasons=…").
const SigFailedTurn = "failed_turn"

// Gerekçe kodları.
const (
	FailReask          = "reask"
	FailComplaint      = "complaint_followup"
	FailToolError      = "tool_error"
	FailTimeout        = "timeout"
	FailRoundCap       = "round_cap"
	FailCtxOverflow    = "context_overflow"
	FailVerifyMismatch = "verify_mismatch"
	FailProviderError  = "provider_error"
	FailError          = "error"
	FailEmpty          = "empty_answer"
	FailClarifyOnly    = "clarify_only"
)

// FailReasons — kapalı küme (API süzgeci ve FE etiketi bu sırayla).
var FailReasons = []string{FailReask, FailComplaint, FailToolError, FailTimeout, FailRoundCap, FailCtxOverflow, FailVerifyMismatch,
	FailProviderError, FailError, FailEmpty, FailClarifyOnly}

const (
	// ReaskWindow — önceki turun bitişinden sonraki soruya kadar.
	ReaskWindow = 60 * time.Second
	// ReaskSimilarity — normalleştirilmiş kelime kümesi Jaccard eşiği.
	ReaskSimilarity = 0.5
	// ComplaintWindow — şikâyet mesajının önceki turu işaretleyebildiği süre.
	ComplaintWindow = 10 * time.Minute
	// ClusterSimilarity — kümeye katılma eşiği (tohum soruyla Jaccard).
	ClusterSimilarity = 0.5
	// FailedTurnSettle — süpürme bu kadar yeni turu yazmaz (sonraki mesaj gelsin).
	FailedTurnSettle = 11 * time.Minute
	// FailedTurnSweepWindow — saatlik süpürmenin geriye baktığı pencere.
	FailedTurnSweepWindow = 3 * time.Hour

	clusterSamples   = 3
	clusterExchanges = 20
	failErrMax       = 200
)

// FailedTurn — bayraklı tur (admin listesinin satırı).
type FailedTurn struct {
	ExchangeID     string    `json:"exchangeId"`
	ConversationID string    `json:"conversationId,omitempty"`
	UserEmail      string    `json:"userEmail,omitempty"`
	At             time.Time `json:"at"`
	Tier           string    `json:"tier,omitempty"`
	Question       string    `json:"question"`
	Reasons        []string  `json:"reasons"`
	// NextQuestion — reask / complaint gerekçesini doğuran sonraki mesaj.
	NextQuestion string `json:"nextQuestion,omitempty"`
	ErrorMsg     string `json:"errorMsg,omitempty"`
}

// FailedCluster — haftalık benzer soru kümesi.
type FailedCluster struct {
	Week        string        `json:"week"`
	Label       string        `json:"label"`
	Count       int           `json:"count"`
	Users       int           `json:"users"`
	Samples     []string      `json:"samples"`
	Reasons     []ReasonCount `json:"reasons"`
	ExchangeIDs []string      `json:"exchangeIds"`
}

// ReasonCount — gerekçe sayacı.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// ── Normalleştirme ─────────────────────────────────────────────────────

var trFold = strings.NewReplacer("ı", "i", "ş", "s", "ğ", "g", "ü", "u", "ö", "o", "ç", "c", "â", "a", "î", "i", "û", "u", "̇", "")

// NormalizeQuestion — SAF: küçük harf, Türkçe harfler ASCII'ye katlanır,
// harf/rakam dışı ayırıcı, tek boşluk. "Noldu?" ve "noldu" aynı metindir.
func NormalizeQuestion(s string) string {
	s = trFold.Replace(strings.ToLower(s))
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		space = true
	}
	return b.String()
}

// clusterStop — küme etiketini taşımayan sık kelimeler (katlanmış).
var clusterStop = map[string]bool{
	"ve": true, "ile": true, "mi": true, "mu": true, "var": true, "ne": true, "bu": true, "su": true,
	"bir": true, "son": true, "da": true, "de": true, "icin": true, "nasil": true, "neden": true,
	"the": true, "is": true, "what": true, "bana": true, "goster": true, "nedir": true, "neler": true,
	"hangi": true, "olan": true, "kac": true, "saat": true, "dakika": true, "dk": true, "gun": true,
}

// QuestionTokens — SAF: normalleştirilmiş, ≥2 karakter, yalnız-rakam ve
// durak kelimeleri hariç kelime kümesi (küme ve yeniden sorma için).
func QuestionTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(NormalizeQuestion(s)) {
		if len([]rune(w)) < 2 || clusterStop[w] || strings.IndexFunc(w, unicode.IsLetter) < 0 {
			continue
		}
		out[w] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// ReaskSimilar — SAF: iki soru yeniden sorma sayılacak kadar benzer mi.
func ReaskSimilar(a, b string) bool {
	na, nb := NormalizeQuestion(a), NormalizeQuestion(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	return jaccard(QuestionTokens(a), QuestionTokens(b)) >= ReaskSimilarity
}

// complaintStrong — mesajın herhangi bir yerinde geçince şikâyet sayılır.
var complaintStrong = []string{
	"noldu", "ne oldu", "tekrar dene", "yeniden dene", "cevap vermedin", "cevap yok", "yanit vermedin",
	"o degil", "bu degil", "anlamadin", "olmadi", "calismadi", "try again", "not what i asked", "no answer",
}

// complaintWeak — yalnız kısa mesajda (≤4 kelime) şikâyet: "yanlış" bir
// sorunun parçası da olabilir ("yanlış giriş hataları").
var complaintWeak = []string{"yanlis", "hatali cevap", "alakasiz", "wrong", "eksik", "bos"}

// IsComplaint — SAF: mesaj önceki cevaba şikâyet mi.
func IsComplaint(q string) bool {
	n := " " + NormalizeQuestion(q) + " "
	if strings.TrimSpace(n) == "" {
		return false
	}
	for _, p := range complaintStrong {
		if strings.Contains(n, " "+p+" ") {
			return true
		}
	}
	if len(strings.Fields(n)) > 4 {
		return false
	}
	for _, p := range complaintWeak {
		if strings.Contains(n, " "+p+" ") {
			return true
		}
	}
	return false
}

var providerStatusRe = regexp.MustCompile(`(?i)(status|http|code|gateway|provider|openai|anthropic|compat)[^0-9]{0,12}\b([45]\d\d)\b`)

// ProviderError — SAF: hata metni sağlayıcının 4xx/5xx cevabı mı.
func ProviderError(msg string) bool { return providerStatusRe.MatchString(msg) }

func isTimeout(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "deadline exceeded") || strings.Contains(m, "timeout") || strings.Contains(m, "timed out") || strings.Contains(m, "zaman aşımı")
}

// TurnFailReasons — SAF: tek turun kendinden okunan gerekçeler (tekil, sıralı).
// İptal (kullanıcı ayrıldı) başarısızlık sayılmaz.
func TurnFailReasons(r chstore.CosreInteraction) []string {
	set := map[string]bool{}
	cancelled := r.Status == "aborted" || strings.Contains(strings.ToLower(r.ErrorMsg), "context canceled")
	switch {
	case cancelled:
	case r.Status == "error":
		switch {
		case ContextOverflowText(r.ErrorMsg):
			set[FailCtxOverflow] = true
		case isTimeout(r.ErrorMsg):
			set[FailTimeout] = true
		case ProviderError(r.ErrorMsg):
			set[FailProviderError] = true
		default:
			set[FailError] = true
		}
	case r.EmptyAnswer:
		set[FailEmpty] = true
	}
	for _, t := range r.Tools {
		if t.OK || t.Error == "skipped" {
			continue
		}
		if isTimeout(t.Error) {
			set[FailTimeout] = true
		} else {
			set[FailToolError] = true
		}
	}
	if r.Tier == "clarify" {
		set[FailClarifyOnly] = true
	}
	return sortedReasons(set)
}

func sortedReasons(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, k := range FailReasons {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

func turnEnd(r chstore.CosreInteraction) time.Time {
	return r.CreatedAt.Add(time.Duration(r.TotalMs) * time.Millisecond)
}

// followGap — önceki turun bitişinden sonraki soruya süre (bitiş sonrakini
// geçiyorsa başlangıçtan).
func followGap(prev, next chstore.CosreInteraction) time.Duration {
	gap := next.CreatedAt.Sub(turnEnd(prev))
	if gap < 0 {
		gap = next.CreatedAt.Sub(prev.CreatedAt)
	}
	return gap
}

// userKey — yeniden sorma "aynı kullanıcı" ister; konuşma değişse de.
func userKey(r chstore.CosreInteraction) string {
	if r.UserEmail != "" {
		return "u:" + strings.ToLower(r.UserEmail)
	}
	return "c:" + r.ConversationID
}

// DetectFailedTurns — SAF, deterministik: turlar + sinyaller → bayraklı
// turlar (yeniden eskiye; eşitlikte exchange_id). Saklı failed_turn
// sinyallerinin gerekçeleri birleşir (satırı penceredeyse). Oylar okunmaz.
func DetectFailedTurns(rows []chstore.CosreInteraction, sigs []chstore.CosreSignal) []FailedTurn {
	reasons := map[string]map[string]bool{}
	next := map[string]string{}
	add := func(id, reason string) {
		if id == "" {
			return
		}
		if reasons[id] == nil {
			reasons[id] = map[string]bool{}
		}
		reasons[id][reason] = true
	}
	byID := map[string]chstore.CosreInteraction{}
	for _, r := range rows {
		byID[r.ExchangeID] = r
		for _, k := range TurnFailReasons(r) {
			add(r.ExchangeID, k)
		}
	}
	chipped := map[string]bool{}
	for _, g := range sigs {
		if _, ok := byID[g.ExchangeID]; !ok {
			continue
		}
		switch g.Kind {
		case SigVerifyMismatch:
			add(g.ExchangeID, FailVerifyMismatch)
		case SigFollowupClick, SigSuggestionClick:
			chipped[g.ExchangeID] = true
		case SigFailedTurn:
			for _, k := range ParseFailedTurnDetail(g.Detail) {
				add(g.ExchangeID, k)
			}
		case SigLoopStop:
			for _, k := range LoopStopReasons(g.Detail) {
				add(g.ExchangeID, k)
			}
		}
	}
	groups := map[string][]chstore.CosreInteraction{}
	for _, r := range rows {
		k := userKey(r)
		groups[k] = append(groups[k], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool {
			if !g[i].CreatedAt.Equal(g[j].CreatedAt) {
				return g[i].CreatedAt.Before(g[j].CreatedAt)
			}
			return g[i].ExchangeID < g[j].ExchangeID
		})
		for i := 0; i+1 < len(g); i++ {
			prev, nx := g[i], g[i+1]
			gap := followGap(prev, nx)
			hit := false
			if nx.Attempt == "retry" || nx.Attempt == "regenerate" {
				add(prev.ExchangeID, FailReask)
				hit = true
			} else if gap <= ReaskWindow && !chipped[prev.ExchangeID] && ReaskSimilar(prev.Question, nx.Question) {
				add(prev.ExchangeID, FailReask)
				hit = true
			}
			if gap <= ComplaintWindow && IsComplaint(nx.Question) {
				add(prev.ExchangeID, FailComplaint)
				hit = true
			}
			if hit && next[prev.ExchangeID] == "" {
				next[prev.ExchangeID] = nx.Question
			}
		}
	}
	out := make([]FailedTurn, 0, len(reasons))
	for id, set := range reasons {
		r, ok := byID[id]
		if !ok || len(set) == 0 {
			continue
		}
		msg := r.ErrorMsg
		if len(msg) > failErrMax {
			msg = msg[:failErrMax]
		}
		out = append(out, FailedTurn{
			ExchangeID: id, ConversationID: r.ConversationID, UserEmail: r.UserEmail, At: r.CreatedAt.UTC(),
			Tier: r.Tier, Question: r.Question, Reasons: sortedReasons(set), NextQuestion: next[id], ErrorMsg: msg,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].ExchangeID < out[j].ExchangeID
	})
	return out
}

// FailedTurnDetail — SAF: saklı sinyalin ayrıntısı.
func FailedTurnDetail(reasons []string) string { return "reasons=" + strings.Join(reasons, ",") }

// ParseFailedTurnDetail — SAF: "reasons=a,b" → bilinen gerekçeler.
func ParseFailedTurnDetail(d string) []string {
	known := map[string]bool{}
	for _, k := range FailReasons {
		known[k] = true
	}
	var out []string
	for _, f := range strings.Fields(d) {
		v, ok := strings.CutPrefix(f, "reasons=")
		if !ok {
			continue
		}
		for _, k := range strings.Split(v, ",") {
			if known[k] {
				out = append(out, k)
			}
		}
	}
	return out
}

// ISOWeek — SAF: "2026-W47".
func ISOWeek(t time.Time) string {
	y, w := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// ClusterFailedTurns — SAF, deterministik: haftalık benzer soru kümeleri.
// Girdi sırası sonucu değiştirmez (önce zaman + exchange_id'ye göre dizilir).
// Sıra: hafta yeniden eskiye, sayı çoktan aza, etiket.
func ClusterFailedTurns(turns []FailedTurn) []FailedCluster {
	ts := append([]FailedTurn(nil), turns...)
	sort.Slice(ts, func(i, j int) bool {
		if !ts[i].At.Equal(ts[j].At) {
			return ts[i].At.Before(ts[j].At)
		}
		return ts[i].ExchangeID < ts[j].ExchangeID
	})
	type acc struct {
		week    string
		key     string // kelimesiz soruda normalleştirilmiş metin
		seed    map[string]bool
		freq    map[string]int
		users   map[string]bool
		reasons map[string]int
		samples []string
		ids     []string
		n       int
	}
	var cs []*acc
	for _, t := range ts {
		tok := QuestionTokens(t.Question)
		week, key := ISOWeek(t.At), ""
		if len(tok) == 0 {
			key = NormalizeQuestion(t.Question)
		}
		var hit *acc
		for _, c := range cs {
			if c.week != week {
				continue
			}
			if (len(tok) == 0 && len(c.seed) == 0 && c.key == key) || jaccard(tok, c.seed) >= ClusterSimilarity {
				hit = c
				break
			}
		}
		if hit == nil {
			hit = &acc{week: week, key: key, seed: tok, freq: map[string]int{}, users: map[string]bool{}, reasons: map[string]int{}}
			cs = append(cs, hit)
		}
		hit.n++
		for w := range tok {
			hit.freq[w]++
		}
		if t.UserEmail != "" {
			hit.users[strings.ToLower(t.UserEmail)] = true
		}
		for _, r := range t.Reasons {
			hit.reasons[r]++
		}
		q := strings.TrimSpace(t.Question)
		if len(hit.samples) < clusterSamples && q != "" && !containsStr(hit.samples, q) {
			hit.samples = append(hit.samples, q)
		}
		if len(hit.ids) < clusterExchanges {
			hit.ids = append(hit.ids, t.ExchangeID)
		}
	}
	out := make([]FailedCluster, 0, len(cs))
	for _, c := range cs {
		words := make([]string, 0, len(c.freq))
		for w := range c.freq {
			words = append(words, w)
		}
		sort.Slice(words, func(i, j int) bool {
			if c.freq[words[i]] != c.freq[words[j]] {
				return c.freq[words[i]] > c.freq[words[j]]
			}
			return words[i] < words[j]
		})
		if len(words) > 4 {
			words = words[:4]
		}
		label := strings.Join(words, " ")
		if label == "" {
			label = c.key
		}
		if label == "" && len(c.samples) > 0 {
			label = c.samples[0] // yalnız noktalama: ham metin
		}
		var rc []ReasonCount
		for _, k := range FailReasons {
			if n := c.reasons[k]; n > 0 {
				rc = append(rc, ReasonCount{Reason: k, Count: n})
			}
		}
		out = append(out, FailedCluster{
			Week: c.week, Label: label, Count: c.n, Users: len(c.users),
			Samples: append([]string{}, c.samples...), Reasons: rc, ExchangeIDs: c.ids,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Week != out[j].Week {
			return out[i].Week > out[j].Week
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ── Saklama (saatlik süpürme) ──────────────────────────────────────────

// FailedTurnStore — süpürmenin G/Ç yüzeyi (*chstore.Store karşılar).
type FailedTurnStore interface {
	ListCosreInteractionsForScoring(ctx context.Context, from, to time.Time) ([]chstore.CosreInteraction, error)
	ListCosreSignals(ctx context.Context, from, to time.Time) ([]chstore.CosreSignal, error)
	InsertCosreSignal(ctx context.Context, sig chstore.CosreSignal) error
}

// FailedTurnSignals — SAF: yazılacak sinyaller. Yalnız [from, settled)
// aralığındaki ve henüz saklı bayrağı olmayan turlar; sinyalin zamanı turun
// zamanı (pencere okuması tura denk düşsün).
func FailedTurnSignals(turns []FailedTurn, stored map[string]bool, from, settled time.Time) []chstore.CosreSignal {
	var out []chstore.CosreSignal
	for i := len(turns) - 1; i >= 0; i-- { // eskiden yeniye
		t := turns[i]
		if stored[t.ExchangeID] || t.At.Before(from) || !t.At.Before(settled) {
			continue
		}
		out = append(out, chstore.CosreSignal{
			ExchangeID: t.ExchangeID, CreatedAt: t.At, UserEmail: SystemSignalActor,
			Kind: SigFailedTurn, Detail: FailedTurnDetail(t.Reasons),
		})
	}
	return out
}

// RunFailedTurnSweep — son FailedTurnSweepWindow'un oturmuş turlarını
// bayraklar ve yazar (tekrar koşmak güvenli: saklı bayrak atlanır).
func RunFailedTurnSweep(ctx context.Context, st FailedTurnStore, now time.Time) (int, error) {
	from := now.Add(-FailedTurnSweepWindow)
	rows, err := st.ListCosreInteractionsForScoring(ctx, from.Add(-ComplaintWindow), now)
	if err != nil {
		return 0, err
	}
	sigs, err := st.ListCosreSignals(ctx, from.Add(-ComplaintWindow), now.Add(time.Minute))
	if err != nil {
		return 0, err
	}
	stored := map[string]bool{}
	for _, g := range sigs {
		if g.Kind == SigFailedTurn {
			stored[g.ExchangeID] = true
		}
	}
	n := 0
	for _, g := range FailedTurnSignals(DetectFailedTurns(rows, sigs), stored, from, now.Add(-FailedTurnSettle)) {
		if err := st.InsertCosreSignal(ctx, g); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
