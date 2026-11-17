package chstore

// cosre_conversation_settings.go — v0.10.2324 (docs/cosre/conversation-audit.md
// §7 karar 5, seçenek b): CoSRE konuşma kalitesi programının TEK ayar blobu,
// system_settings `cosre_conversation`:
//
//	{"state":true,"style":false,"verification":"shadow","updatedBy":"…","updatedAt":"…"}
//
//   - state — A/G dilimleri (konuşma durumu: çözücü, döngünün kendi yazdığı
//     bağlam, data-state). Varsayılan AÇIK (operatör davranışı istedi; anahtar
//     geri alma anahtarıdır). Blob yok ya da alan yok = açık; yalnız açık false
//     kapatır.
//   - style — B/C/D/F1 için AYRILMIŞ, kapalı; bu sürümde hiçbir yol okumaz.
//   - verification — E dilimi: off | shadow | enforce. v0.10.2327 (E1) gölge
//     kipi bağlı ve VARSAYILAN shadow: cevap doğrulayıcısı yalnız sayar,
//     kullanıcıya görünen hiçbir şey değişmez; off kill switch'tir. enforce
//     E2'ye kadar PUT'ta reddedilir (okunursa gölge gibi davranır).
//
// Her şey ayrıca `cosre_v2` bayrağının arkasındadır (genel kapı). Şablon
// ai_general_questions.go: boot'ta yükle + 30 s cfgRefresh, süreç-içi atomic;
// yazma yolu api/cosre_conversation_settings.go (admin PUT + audit).

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// CosreConversationKey — system_settings anahtarı.
const CosreConversationKey = "cosre_conversation"

// Doğrulama kipleri (karar 5; E dilimi bağlayacak).
const (
	CosreVerifyOff     = "off"
	CosreVerifyShadow  = "shadow"
	CosreVerifyEnforce = "enforce"
)

// CosreConversation — blob.
type CosreConversation struct {
	State        bool      `json:"state"`
	Style        bool      `json:"style"`
	Verification string    `json:"verification"`
	UpdatedBy    string    `json:"updatedBy,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitzero"`
}

// DefaultCosreConversation — blob yokken geçerli ayar: durum AÇIK, üslup
// kapalı, doğrulama gölge (v0.10.2327).
func DefaultCosreConversation() CosreConversation {
	return CosreConversation{State: true, Verification: CosreVerifyShadow}
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
		c.Verification = CosreVerifyShadow
	}
	cosreConversationCur.Store(&c)
}

// parseCosreConversation — SAF: boş blob varsayılan; "state" alanı olmayan
// blob da açık. Bozuk blob ok=false; bilinmeyen ya da boş doğrulama kipi
// varsayılan (shadow; v0.10.2327).
func parseCosreConversation(raw []byte) (CosreConversation, bool) {
	if len(raw) == 0 {
		return DefaultCosreConversation(), true
	}
	var in struct {
		State        *bool     `json:"state"`
		Style        bool      `json:"style"`
		Verification string    `json:"verification"`
		UpdatedBy    string    `json:"updatedBy"`
		UpdatedAt    time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return CosreConversation{}, false
	}
	out := DefaultCosreConversation()
	if in.State != nil {
		out.State = *in.State
	}
	out.Style = in.Style
	if ValidCosreVerification(in.Verification) {
		out.Verification = in.Verification
	}
	out.UpdatedBy = strings.TrimSpace(in.UpdatedBy)
	out.UpdatedAt = in.UpdatedAt
	return out, true
}

// SaveCosreConversation — yazar ve bu pod'a yayınlar.
func (s *Store) SaveCosreConversation(ctx context.Context, c CosreConversation) (CosreConversation, error) {
	c.UpdatedBy = strings.TrimSpace(c.UpdatedBy)
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
