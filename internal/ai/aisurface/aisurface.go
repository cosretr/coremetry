// Package aisurface — CoSRE'nin ai_calls yüzey etiketlerinin TEK kayıt
// defteri ve alışveriş (exchange) kimliği kodeği (v0.10.1153, operatör: "/ai
// her CoSRE etkileşimini göstermiyor").
//
// Neden ayrı bir yaprak paket: etiketleri hem yazan (internal/api sohbet
// kademeleri) hem okuyan (internal/chstore router-gap / KB süzgeci, /ai
// etkileşim listesi) taraf aynı kümeyi görmeli. Etiket bugüne dek her
// çağrı yerinde düz dize olarak yazılıyordu ve okuyucuların elle yazılmış
// listeleri (evalset eşlemesi, router-gap IN listesi, KB dışlaması) yeni
// yüzeyleri (wiki-chat, wiki-select, rag-chat) hiç görmedi — v0.9.1137'nin
// insight türü whitelist'iyle aynı hata sınıfı. Artık her sohbet yüzeyi
// burada BİR kez, rolüyle birlikte bildirilir; api tarafındaki kaynak testi
// (ai_surface_registry_test.go) sohbet kodunda kayıtsız etiket bırakmaz.
//
// Paket bağımlılıksızdır (yalnız strings): chstore ve api ikisi de içe
// aktarır, döngü riski yok.
package aisurface

import "strings"

// ── Sohbet (CoSRE) yüzeyleri ─────────────────────────────────────────────

const (
	// Chat — serbest araç döngüsü; alışveriş başına tek satır (RecordUsage).
	Chat = "chat"
	// ChatGuided — guided anlatım (deterministik prefetch + tek anlatım).
	ChatGuided = "chat-guided"
	// ChatDrawer — çekmece anlatımı ve exception takibinin kod döngüsü.
	ChatDrawer = "chat-drawer"
	// ChatGeneral — niyet sınıflandırıcısı none → genel bilgi cevabı.
	ChatGeneral = "chat-general"
	// ChatIntent — niyet sınıflandırıcısı (yardımcı JSON çağrısı).
	ChatIntent = "chat-intent"
	// ChatIntentNone — sınıflandırıcı none dedi (model koşmayan işaret satırı).
	ChatIntentNone = "chat-intent-none"
	// ChatOffTopic — konu dışı (model koşmayan işaret satırı).
	ChatOffTopic = "chat-offtopic"
	// WikiChat — wiki anlatımı (açık kademe, takip, yoklama, kurtarma, /wiki).
	WikiChat = "wiki-chat"
	// WikiSelect — iki aşamalı wiki okumasının sayfa seçimi (yardımcı çağrı).
	WikiSelect = "wiki-select"
	// RAGChat — doküman RAG anlatımı.
	RAGChat = "rag-chat"
	// ExplainTraceChat — sohbette trace sorusu (Explain çekirdeği, sohbet içi).
	ExplainTraceChat = "explain-trace:chat"
)

// Role — bir sohbet yüzeyinin alışverişteki yeri.
type Role uint8

const (
	// RoleAnswer — alışverişin cevabını üreten model çağrısı. Satır exchange
	// kimliğini AYNEN taşır: 👍/👎 (ai_feedback) ve KB/evalset JOIN'leri bu
	// satıra bağlanır.
	RoleAnswer Role = iota + 1
	// RoleSubCall — cevaba yardım eden model çağrısı (sınıflandırma, sayfa
	// seçimi). Çocuk kimlik taşır (ChildExchangeID): alışverişin altında
	// gruplanır ama geri bildirim JOIN'ini ikiye katlamaz.
	RoleSubCall
	// RoleMarker — model koşmadan yazılan işaret satırı (0 token; router-gap
	// raporu ve kullanım listesi için). Çocuk kimlik taşır; LLM çağrısı sayılmaz.
	RoleMarker
)

// Spec — bir yüzeyin kaydı. Okuyucuların listeleri buradan TÜRER.
type Spec struct {
	Label string
	Role  Role
	// Eval — 👎 satırının evalset vakasına dönüştüğü yüzey ("" = vaka olamaz:
	// bu yüzeyin sistem prompt'u evalset koşucusunda yok — wiki/RAG bağlamı
	// istek anında kurulur, yeniden üretilemez).
	Eval string
	// RouterGap — guided router'ın yakalayamadığı soruyu sayan satır
	// (chstore.RouterGaps).
	RouterGap bool
	// NoKB — 👍 alsa da KB'ye (rag_chunks curated) terfi edemez: kanıtsız
	// genel bilgi cevabı (v0.10.194).
	NoKB bool
}

// registry — sohbet yüzeylerinin TEK listesi. Yeni yüzey = buraya bir satır;
// api kaynak testi kayıtsız etiketi reddeder.
var registry = []Spec{
	{Label: Chat, Role: RoleAnswer, Eval: "Chat", RouterGap: true},
	{Label: ChatGuided, Role: RoleAnswer, Eval: "Chat"},
	{Label: ChatDrawer, Role: RoleAnswer, Eval: "Chat"},
	{Label: ChatGeneral, Role: RoleAnswer, Eval: "GeneralChat", NoKB: true},
	{Label: ChatIntent, Role: RoleSubCall, Eval: "IntentClassify"},
	{Label: ChatIntentNone, Role: RoleMarker, Eval: "GeneralChat", RouterGap: true},
	{Label: ChatOffTopic, Role: RoleMarker, Eval: "GeneralChat"},
	{Label: WikiChat, Role: RoleAnswer},
	{Label: WikiSelect, Role: RoleSubCall},
	{Label: RAGChat, Role: RoleAnswer},
	{Label: ExplainTraceChat, Role: RoleAnswer, Eval: "Trace"},
}

// Lookup — etiketin kaydı.
func Lookup(label string) (Spec, bool) {
	for _, s := range registry {
		if s.Label == label {
			return s, true
		}
	}
	return Spec{}, false
}

// All — kaydın kopyası (kayıt sırasıyla).
func All() []Spec { return append([]Spec(nil), registry...) }

// RouterGapLabels — chstore.RouterGaps'in IN listesi.
func RouterGapLabels() []string { return labelsWhere(func(s Spec) bool { return s.RouterGap }) }

// NoKBLabels — KB adayı olamayan yüzeyler (chstore.ListKBCandidates).
func NoKBLabels() []string { return labelsWhere(func(s Spec) bool { return s.NoKB }) }

// IsLLMCall — satır gerçek bir model çağrısı mı? İşaret satırları değil;
// kayıtsız etiket (explain-*, insight-* …) model çağrısıdır.
func IsLLMCall(label string) bool {
	s, ok := Lookup(label)
	return !ok || s.Role != RoleMarker
}

func labelsWhere(keep func(Spec) bool) []string {
	var out []string
	for _, s := range registry {
		if keep(s) {
			out = append(out, s.Label)
		}
	}
	return out
}

// ── Etkileşim (turn) satırı ──────────────────────────────────────────────
//
// Her CoSRE kullanıcı turu, kademesi ne olursa olsun (LLM'li ya da LLM'siz),
// ai_calls'a TEK bir etkileşim satırı yazar: yüzey TurnLabel, exchange kimliği
// TurnExchangeID(kök). Kademe / rota / Derin bayrağı yeni kolon açmadan
// etiketin içindedir (iki-boot probe sözleşmesine üçüncü bayrak eklenmez —
// v0.10.940 evalset öneki emsali). Etkileşim satırı model çağrısı DEĞİLDİR:
// token taşımaz ve /ai çağrı agregatları (KPI, seri, bütçe, liste) onu
// TurnPrefix ile dışlar.

// TurnPrefix — etkileşim satırlarının yüzey öneki.
const TurnPrefix = "cosre-turn"

// Kademeler — copilotChat'in cevaplayan basamağı (chat_span tier adlarıyla aynı).
const (
	TierScope  = "scope"
	TierWiki   = "wiki"
	TierGuided = "guided"
	TierDrawer = "drawer"
	TierRAG    = "rag"
	TierIntent = "intent"
	TierLoop   = "loop"
	TierOther  = "other"
)

var tiers = map[string]bool{TierScope: true, TierWiki: true, TierGuided: true, TierDrawer: true,
	TierRAG: true, TierIntent: true, TierLoop: true, TierOther: true}

// KnownTier — kademe adı kayıtlı mı (api kaynak testi copilotChat'in
// cspan.tier(...) adlarını buna karşı denetler).
func KnownTier(t string) bool { return tiers[t] }

// routeMaxLen — rota (guided niyet adı) tavanı; LowCardinality yüzey kolonunun
// kümesi sınırlı kalsın diye rota yalnız [a-z0-9_] ve kısa.
const routeMaxLen = 40

// TurnLabel — SAF: "cosre-turn:<kademe>[/<rota>][+deep]". Bilinmeyen kademe
// "other"a, kurala uymayan rota boşa düşer (kardinalite sınırlı).
func TurnLabel(tier, route string, deep bool) string {
	if !tiers[tier] {
		tier = TierOther
	}
	l := TurnPrefix + ":" + tier
	if r := cleanRoute(route); r != "" {
		l += "/" + r
	}
	if deep {
		l += "+deep"
	}
	return l
}

// ParseTurnLabel — TurnLabel'ın tersi; etkileşim etiketi değilse ok=false.
func ParseTurnLabel(label string) (tier, route string, deep bool, ok bool) {
	rest, found := strings.CutPrefix(label, TurnPrefix+":")
	if !found {
		return "", "", false, false
	}
	rest, deep = strings.CutSuffix(rest, "+deep")
	tier, route, _ = strings.Cut(rest, "/")
	return tier, route, deep, tier != ""
}

// IsTurn — etiket bir etkileşim satırının mı?
func IsTurn(label string) bool { return strings.HasPrefix(label, TurnPrefix) }

func cleanRoute(r string) string {
	r = strings.TrimSpace(r)
	if r == "" || len(r) > routeMaxLen {
		return ""
	}
	for _, c := range r {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return ""
		}
	}
	return r
}

// ── Exchange kimliği kodeği ──────────────────────────────────────────────
//
// Kök kimlik (sohbet handler'ının bastığı 32-hex) cevap satırında AYNEN
// durur — geri bildirim ve evalset JOIN'leri tam eşitlikle bağlanır ve
// değişmez. Yardımcı/işaret satırları ve etkileşim satırı "kök:rol" taşır:
// tam eşitlik JOIN'ine girmez (v0.10.172 #2'nin "ikinci satır JOIN'i ikiye
// katlar" gerekçesi korunur) ama aynı köke gruplanır.

// ExchangeSep — kök ile rol arasındaki ayırıcı (chstore SQL'i de bunu kullanır).
const ExchangeSep = ":"

// TurnRole — etkileşim satırının rolü.
const TurnRole = "turn"

// ChildExchangeID — SAF: kökün altında rol kimliği; kök boşsa boş (alışveriş
// dışı çağrı kimliksiz kalır).
func ChildExchangeID(root, role string) string {
	root = ExchangeRoot(root)
	if root == "" {
		return ""
	}
	return root + ExchangeSep + role
}

// TurnExchangeID — etkileşim satırının exchange kimliği.
func TurnExchangeID(root string) string { return ChildExchangeID(root, TurnRole) }

// ExchangeRoot — SAF: kimliğin kökü (ayırıcıdan önceki parça).
func ExchangeRoot(id string) string {
	root, _, _ := strings.Cut(id, ExchangeSep)
	return root
}
