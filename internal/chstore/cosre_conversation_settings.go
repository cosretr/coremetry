package chstore

// cosre_conversation_settings.go — v0.10.2324 (docs/cosre/conversation-audit.md
// §7 karar 5, seçenek b): CoSRE konuşma kalitesi programının TEK ayar blobu,
// system_settings `cosre_conversation`:
//
//	{"state":true,"style":false,"verification":"enforce","verificationRev":1,"bannedPhrases":["…"],"updatedBy":"…","updatedAt":"…"}
//
//   - state — A/G dilimleri (konuşma durumu: çözücü, döngünün kendi yazdığı
//     bağlam, data-state). Varsayılan AÇIK (operatör davranışı istedi; anahtar
//     geri alma anahtarıdır). Blob yok ya da alan yok = açık; yalnız açık false
//     kapatır.
//   - style — B/C/D/F1 üslup anahtarı, varsayılan KAPALI; v0.10.2331'den beri
//     B (cevap sınıfı ölçüsü) okur (api/chat_answer_class.go). Tek aç/kapa.
//   - verification — E dilimi: off | shadow | enforce. v0.10.2327 (E1) gölge
//     kipi bağlı ve VARSAYILAN shadow: cevap doğrulayıcısı yalnız sayar,
//     kullanıcıya görünen hiçbir şey değişmez; off kill switch'tir. enforce
//     v0.10.2339 (E2) ile bağlı: uyuşmayan değer cevapta "(doğrulanmamış
//     değer)" notuyla işaretlenir (api/answer_seam.go). v0.10.2439'tan beri
//     VARSAYILAN enforce (operatör kararı 2026-11-21, "kanıt kapısı açık").
//     verificationRev (yazımda cosreVerifyRev) olmayan eski blobun "shadow"u
//     o günden önceki varsayılanın taşınmış hâlidir → enforce okunur; "off"
//     ve rev'li her değer aynen kalır.
//   - bannedPhrases — v0.10.2338 (D): cevap dikişinin kalıp açılış/kapanış
//     listesini GENİŞLETEN operatör ifadeleri (varsayılan liste
//     evalrubric.BoilerplateOpenings/Closings, tek kaynak). En çok 40 ifade,
//     her biri ≤80 bayt; yalnız style açıkken okunur.
//   - interaction — v0.10.2353 (interaction-audit §6, I5): off | on, varsayılan
//     off. Açıkken v2 sohbeti sunucu kuralıyla onaylı aksiyon kartı
//     (data-action) önerir ve POST /api/cosre/actions/confirm yazar
//     (api/cosre_actions.go). Kapalıyken akış bayt aynı, uç 404.
//
//   - traceExplainStyle — v0.10.2480 (operatör-bildirimli): trace açıklamasının
//     biçimi, anlatim (TÜR + KARAR + Sonraki adım; kanıtı UI çizer) | bolumlu
//     (v0.10.2456 bölümlü şablonu, geri dönüş). Varsayılan anlatim; bilinmeyen
//     ya da boş değer varsayılan. `cosre_v2` kapısına BAĞLI DEĞİL (✨ düğmesi
//     ve sohbet turu aynı ayarı okur).
//
// Her şey ayrıca `cosre_v2` bayrağının arkasındadır (genel kapı; traceExplainStyle hariç). Şablon
// ai_general_questions.go: boot'ta yükle + 30 s cfgRefresh, süreç-içi atomic;
// yazma yolu api/cosre_conversation_settings.go (admin PUT + audit).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// CosreConversationKey — system_settings anahtarı.
const CosreConversationKey = "cosre_conversation"

// Doğrulama kipleri (karar 5): off | shadow (E1) | enforce (E2, v0.10.2339).
const (
	CosreVerifyOff     = "off"
	CosreVerifyShadow  = "shadow"
	CosreVerifyEnforce = "enforce"
)

// Etkileşim kipleri (v0.10.2353, interaction-audit §6 / açık soru 11): tek alan.
const (
	CosreInteractionOff = "off"
	CosreInteractionOn  = "on"
)

// ValidCosreInteraction — SAF: kapalı küme.
func ValidCosreInteraction(v string) bool {
	return v == CosreInteractionOff || v == CosreInteractionOn
}

// Trace açıklaması biçimleri (v0.10.2480): kapalı küme.
const (
	TraceExplainNarrative = "anlatim"
	TraceExplainSections  = "bolumlu"
)

// Sohbet akışı kipleri (v0.10.2491): inline = araç grupları cevabın içinde
// (sunucu anlatım satırı + "N araç çalıştı"); block = bugünkü tek adım bloğu.
const (
	CosreFlowInline = "inline"
	CosreFlowBlock  = "block"
)

// ValidCosreFlow — SAF: kapalı küme.
func ValidCosreFlow(v string) bool { return v == CosreFlowInline || v == CosreFlowBlock }

// ValidTraceExplainStyle — SAF: kapalı küme.
func ValidTraceExplainStyle(v string) bool {
	return v == TraceExplainNarrative || v == TraceExplainSections
}

// Kanıt kipleri (v0.10.2355, karar 25): off | shadow | on.
const (
	CosreEvidenceOff    = "off"
	CosreEvidenceShadow = "shadow"
	CosreEvidenceOn     = "on"
)

// ValidCosreEvidence — SAF: kapalı küme.
func ValidCosreEvidence(v string) bool {
	return v == CosreEvidenceOff || v == CosreEvidenceShadow || v == CosreEvidenceOn
}

// CosreConversation — blob.
type CosreConversation struct {
	State         bool     `json:"state"`
	Style         bool     `json:"style"`
	Verification  string   `json:"verification"`
	BannedPhrases []string `json:"bannedPhrases,omitempty"` // v0.10.2338 — D: varsayılan kalıp listesine ek
	Interaction   string   `json:"interaction"`             // v0.10.2353 — I5: off | on
	Evidence      string   `json:"evidence"`                // v0.10.2355 — S1: off | shadow | on
	// StatusLine — v0.10.2473: araç turu başında deterministik ara durum
	// cümlesi (api/chat_status_line.go). `style`tan ayrıldı; varsayılan AÇIK,
	// alanı taşımayan eski blob da açık (kayıtlı açık değer kazanır).
	StatusLine bool `json:"statusLine"`
	// TraceExplainStyle — v0.10.2480: anlatim | bolumlu (varsayılan anlatim).
	TraceExplainStyle string `json:"traceExplainStyle"`
	// Flow — v0.10.2491: inline | block (varsayılan inline; api/chat_narration.go).
	Flow string `json:"flow"`
	// VerificationRev — v0.10.2439: doğrulama kipinin yazıldığı varsayılan
	// kuşağı. SaveCosreConversation her yazımda cosreVerifyRev yazar.
	VerificationRev int       `json:"verificationRev,omitempty"`
	UpdatedBy       string    `json:"updatedBy,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt,omitzero"`
}

// Kalıp ifadesi tavanları (v0.10.2338, D).
const (
	CosreBannedPhrasesMax     = 40
	CosreBannedPhraseMaxBytes = 80
)

// NormalizeCosreBannedPhrases — SAF: kırpılmış, boş satırsız, büyük/küçük
// harfe bakmadan tekil liste (ilk yazım korunur). Tavan aşılırsa hata; boş
// liste nil.
func NormalizeCosreBannedPhrases(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.Join(strings.Fields(p), " ")
		if p == "" {
			continue
		}
		if len(p) > CosreBannedPhraseMaxBytes {
			return nil, fmt.Errorf("ifade %d bayttan uzun: %q", CosreBannedPhraseMaxBytes, p)
		}
		k := strings.ToLower(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	if len(out) > CosreBannedPhrasesMax {
		return nil, fmt.Errorf("en çok %d ifade (%d verildi)", CosreBannedPhrasesMax, len(out))
	}
	return out, nil
}

// clipCosreBannedPhrases — okuma yolu: geçersiz girdi blobu bozmaz; uzun
// ifade atılır, liste tavanda kesilir.
func clipCosreBannedPhrases(in []string) []string {
	var keep []string
	for _, p := range in {
		if p = strings.Join(strings.Fields(p), " "); p != "" && len(p) <= CosreBannedPhraseMaxBytes {
			keep = append(keep, p)
		}
	}
	out, err := NormalizeCosreBannedPhrases(keep)
	if err != nil {
		out, _ = NormalizeCosreBannedPhrases(keep[:CosreBannedPhrasesMax])
	}
	return out
}

// cosreVerifyRev — doğrulama varsayılanının kuşağı: 0 = shadow varsayılanı
// (v0.10.2327–2439), 1 = enforce varsayılanı (v0.10.2439).
const cosreVerifyRev = 1

// DefaultCosreConversation — blob yokken geçerli ayar: durum AÇIK, üslup
// kapalı, doğrulama ZORLA (v0.10.2439; önce gölge), kanıt kapalı (v0.10.2355),
// ara durum cümlesi AÇIK (v0.10.2473).
func DefaultCosreConversation() CosreConversation {
	return CosreConversation{State: true, Verification: CosreVerifyEnforce, Interaction: CosreInteractionOff, Evidence: CosreEvidenceOff, StatusLine: true, TraceExplainStyle: TraceExplainNarrative, Flow: CosreFlowInline}
}

// ValidCosreVerification — SAF: kapalı küme.
func ValidCosreVerification(v string) bool {
	return v == CosreVerifyOff || v == CosreVerifyShadow || v == CosreVerifyEnforce
}

var cosreConversationCur atomic.Pointer[CosreConversation]

// CurrentCosreConversation — süreç-içi son yüklenen ayar (CH okuması değil).
func CurrentCosreConversation() CosreConversation {
	if c := cosreConversationCur.Load(); c != nil {
		return *c
	}
	return DefaultCosreConversation()
}

// SetCosreConversation — yazımdan sonra bu pod'u hemen günceller (testler de).
func SetCosreConversation(c CosreConversation) {
	c.UpdatedBy = strings.TrimSpace(c.UpdatedBy)
	if !ValidCosreVerification(c.Verification) {
		c.Verification = CosreVerifyEnforce
	}
	if !ValidCosreInteraction(c.Interaction) {
		c.Interaction = CosreInteractionOff
	}
	if !ValidCosreEvidence(c.Evidence) {
		c.Evidence = CosreEvidenceOff
	}
	if !ValidTraceExplainStyle(c.TraceExplainStyle) {
		c.TraceExplainStyle = TraceExplainNarrative // v0.10.2480
	}
	if !ValidCosreFlow(c.Flow) {
		c.Flow = CosreFlowInline // v0.10.2491
	}
	cosreConversationCur.Store(&c)
}

// parseCosreConversation — SAF: boş blob varsayılan; "state" alanı olmayan
// blob da açık. Bozuk blob ok=false; bilinmeyen ya da boş doğrulama kipi
// varsayılan (enforce; v0.10.2439). Rev'siz eski blobun "shadow"u enforce.
func parseCosreConversation(raw []byte) (CosreConversation, bool) {
	if len(raw) == 0 {
		return DefaultCosreConversation(), true
	}
	var in struct {
		State         *bool     `json:"state"`
		Style         bool      `json:"style"`
		Verification  string    `json:"verification"`
		BannedPhrases []string  `json:"bannedPhrases"`
		Interaction   string    `json:"interaction"`
		Evidence      string    `json:"evidence"`
		StatusLine    *bool     `json:"statusLine"`        // v0.10.2473 — yoksa varsayılan (açık)
		TraceStyle    string    `json:"traceExplainStyle"` // v0.10.2480 — yoksa varsayılan (anlatim)
		Flow          string    `json:"flow"`              // v0.10.2491 — yoksa varsayılan (inline)
		VerifyRev     int       `json:"verificationRev"`
		UpdatedBy     string    `json:"updatedBy"`
		UpdatedAt     time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return CosreConversation{}, false
	}
	out := DefaultCosreConversation()
	if in.State != nil {
		out.State = *in.State
	}
	out.Style = in.Style
	if in.StatusLine != nil {
		out.StatusLine = *in.StatusLine
	}
	if ValidCosreVerification(in.Verification) {
		out.Verification = in.Verification
	}
	if in.VerifyRev < cosreVerifyRev && out.Verification == CosreVerifyShadow {
		out.Verification = CosreVerifyEnforce // v0.10.2439 — taşınmış eski varsayılan
	}
	out.VerificationRev = cosreVerifyRev
	out.BannedPhrases = clipCosreBannedPhrases(in.BannedPhrases)
	if ValidCosreInteraction(in.Interaction) {
		out.Interaction = in.Interaction
	}
	if ValidCosreEvidence(in.Evidence) { // v0.10.2355 — bilinmeyen / boş kip varsayılan (off)
		out.Evidence = in.Evidence
	}
	if ValidTraceExplainStyle(in.TraceStyle) {
		out.TraceExplainStyle = in.TraceStyle
	}
	if ValidCosreFlow(in.Flow) {
		out.Flow = in.Flow
	}
	out.UpdatedBy = strings.TrimSpace(in.UpdatedBy)
	out.UpdatedAt = in.UpdatedAt
	return out, true
}

// SaveCosreConversation — yazar ve bu pod'a yayınlar.
func (s *Store) SaveCosreConversation(ctx context.Context, c CosreConversation) (CosreConversation, error) {
	c.UpdatedBy = strings.TrimSpace(c.UpdatedBy)
	c.VerificationRev = cosreVerifyRev // v0.10.2439 — yazılan kip operatörün seçimi
	raw, err := json.Marshal(c)
	if err != nil {
		return CosreConversation{}, err
	}
	if err := s.PutSetting(ctx, CosreConversationKey, raw); err != nil {
		return CosreConversation{}, err
	}
	SetCosreConversation(c)
	return c, nil
}

// LoadCosreConversation — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadCosreConversation(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, CosreConversationKey)
	if err != nil {
		return err
	}
	c, ok := parseCosreConversation(raw)
	if !ok {
		log.Printf("[cosre-conversation] cosre_conversation ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetCosreConversation(c)
	return nil
}
