package chstore

// ai_general_questions.go — v0.10.1458 (operatör kararı 2026-10-23: "Genel
// soruları açık bırakalım, anahtar da olabilir."): CoSRE'nin konu dışı
// (off_topic) sorulara MODEL cevabı verip vermeyeceği — system_settings
// `ai.generalQuestions` blobu:
//
//	{"allow":true,"updatedBy":"…","updatedAt":"…"}
//
// Varsayılan (blob yok ya da "allow" alanı yok): allow=TRUE — operatör kararı
// v0.10.819'un deterministik sınırını tersine çevirir. false = v0.10.819
// davranışı bayt bayt (kibar sınır + yol tarifi, model koşmaz).
//
// Şablon ai_toolarg_settings.go: boot'ta yükle + 30 s cfgRefresh, süreç-içi
// atomic. Yazma yolu api/ai_general_questions.go (admin PUT + audit).
//
// Geri alma: `{"allow":false}` yaz (kod değişmeden v0.10.819 davranışı) ya da
// revert — blob okuyansız kalırsa zararsız.

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// AIGeneralQuestionsKey — system_settings anahtarı.
const AIGeneralQuestionsKey = "ai.generalQuestions"

// AIGeneralQuestions — blob.
type AIGeneralQuestions struct {
	Allow bool `json:"allow"`
	// SourceDiscovery — v0.10.1553 (operatör onayı "Önerin"): CoSRE kaynak
	// keşfi (api/chat_source_discovery.go). nil = AÇIK (varsayılan), false =
	// v0.10.1552 kademe zinciri bayt bayt.
	SourceDiscovery *bool     `json:"sourceDiscovery,omitempty"`
	UpdatedBy       string    `json:"updatedBy,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt,omitzero"`
}

// SourceDiscoveryOn — SAF: nil = açık.
func (a AIGeneralQuestions) SourceDiscoveryOn() bool {
	return a.SourceDiscovery == nil || *a.SourceDiscovery
}

// DefaultAIGeneralQuestions — blob yokken geçerli ayar: AÇIK.
func DefaultAIGeneralQuestions() AIGeneralQuestions {
	return AIGeneralQuestions{Allow: true}
}

var aiGeneralQuestionsCur atomic.Pointer[AIGeneralQuestions]

// CurrentAIGeneralQuestions — süreç-içi son yüklenen ayar (CH okuması değil).
func CurrentAIGeneralQuestions() AIGeneralQuestions {
	if a := aiGeneralQuestionsCur.Load(); a != nil {
		return *a
	}
	return DefaultAIGeneralQuestions()
}

// SetAIGeneralQuestions — yazımdan sonra bu pod'u hemen günceller.
func SetAIGeneralQuestions(a AIGeneralQuestions) {
	a.UpdatedBy = strings.TrimSpace(a.UpdatedBy)
	aiGeneralQuestionsCur.Store(&a)
}

// parseAIGeneralQuestions — SAF: boş blob varsayılan; "allow" alanı olmayan
// blob da varsayılan (true) — yalnız AÇIK false kapatır. Bozuk blob ok=false.
func parseAIGeneralQuestions(raw []byte) (AIGeneralQuestions, bool) {
	if len(raw) == 0 {
		return DefaultAIGeneralQuestions(), true
	}
	var in struct {
		Allow           *bool     `json:"allow"`
		SourceDiscovery *bool     `json:"sourceDiscovery"`
		UpdatedBy       string    `json:"updatedBy"`
		UpdatedAt       time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return AIGeneralQuestions{}, false
	}
	out := DefaultAIGeneralQuestions()
	if in.Allow != nil {
		out.Allow = *in.Allow
	}
	out.SourceDiscovery = in.SourceDiscovery
	out.UpdatedBy = strings.TrimSpace(in.UpdatedBy)
	out.UpdatedAt = in.UpdatedAt
	return out, true
}

// SaveAIGeneralQuestions — yazar ve bu pod'a yayınlar.
func (s *Store) SaveAIGeneralQuestions(ctx context.Context, a AIGeneralQuestions) (AIGeneralQuestions, error) {
	a.UpdatedBy = strings.TrimSpace(a.UpdatedBy)
	raw, err := json.Marshal(a)
	if err != nil {
		return AIGeneralQuestions{}, err
	}
	if err := s.PutSetting(ctx, AIGeneralQuestionsKey, raw); err != nil {
		return AIGeneralQuestions{}, err
	}
	SetAIGeneralQuestions(a)
	return a, nil
}

// LoadAIGeneralQuestions — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadAIGeneralQuestions(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, AIGeneralQuestionsKey)
	if err != nil {
		return err
	}
	a, ok := parseAIGeneralQuestions(raw)
	if !ok {
		log.Printf("[general-questions] ai.generalQuestions ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetAIGeneralQuestions(a)
	return nil
}
