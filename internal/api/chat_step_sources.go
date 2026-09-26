package api

// chat_step_sources.go — v0.10.944 (CoSRE araştırma asistanı): araç
// çıktısının TAMAMINDAN kaynak durumlarını (internal/sourcestate.Status)
// okuyup `step-result` olayına `sources` olarak ekler.
//
// Neden sunucuda: çipin önizlemesi 4 KB'ta kırpılır (clipStepPreview) ve
// eski araçların map tabanlı çıktısında anahtar sırası alfabetiktir — büyük
// veri dizisi `source`tan önce gelir, rozet önizlemeden okunamaz. Yeni
// araçlar durumu öne koyuyor, ama durumu önizlemenin şansına bırakmak
// "kısmi / gecikmeli / limitli" sonucun arayüzde düz "ok" görünmesi
// demekti. Neden yalnız rozet alt kümesi: çip bir tabela; ayrıntı
// (notlar, pencere) araç çıktısında ve modelde zaten var.

import (
	"encoding/json"
	"strings"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// stepSourceState — ön yüzün ChatStepSourceState aynası.
type stepSourceState struct {
	Source string   `json:"source"`
	State  string   `json:"state"`
	Flags  []string `json:"flags,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

// stepSourceMax — tek çipte en çok bu kadar kaynak rozeti (compare_periods
// iki pencere × birkaç kaynak döndürür; daha fazlası çipi kalabalıklaştırır).
const stepSourceMax = 8

// stepSourceStatuses — SAF: üst düzey `source` (tek nesne) ve `sources`
// (dizi) okunur; ikisi de varsa ikisi de eklenir. Bozuk/eksik alanlı
// girdiler atlanır.
//
// v0.10.944 — nil = denetlenemedi (JSON değil / çözülemedi); boş dilim =
// çıktının TAMAMI okundu, durum YOK. Ön yüz eksik `sources`'u "eski sunucu,
// durum bilinmiyor" diye okur (toolSteps.ts stateUnknown); denetlenmiş ama
// durumsuz eski araç sonucuna `sources: []` gitmezse 4 KB üstü her başarılı
// çağrı sarı "durum okunamadı" uyarısı alıyordu.
func stepSourceStatuses(content string) []stepSourceState {
	c := strings.TrimSpace(content)
	if !strings.HasPrefix(c, "{") {
		return nil
	}
	var top struct {
		Source  json.RawMessage   `json:"source"`
		Sources []json.RawMessage `json:"sources"`
	}
	if json.Unmarshal([]byte(c), &top) != nil {
		return nil
	}
	out := []stepSourceState{}
	add := func(raw json.RawMessage) {
		if len(raw) == 0 || len(out) >= stepSourceMax {
			return
		}
		var st stepSourceState
		if json.Unmarshal(raw, &st) != nil || st.State == "" {
			return
		}
		if r := []rune(st.Detail); len(r) > 160 {
			st.Detail = string(r[:160]) + "…"
		}
		out = append(out, st)
	}
	add(top.Source)
	for _, r := range top.Sources {
		add(r)
	}
	return out
}

// stepSourcesAllFailed — v0.10.944: en az bir kaynak durumu var ve HİÇBİRİ
// kanıt olarak kullanılabilir değil (unreachable/unauthorized/timeout/
// not_configured/error). Künye (chatSourceNoteTR) bu çağrıyı "veri döndürdü"
// saymaz — v0.10.53 kuralı. Kaynak hatası artık Go hatası değil, source.state
// taşıyan başarılı sonuç; !IsError tek başına yetmiyor. Durum listesi
// sourcestate.Usable'dan — ikinci bir sabit liste yok.
//
// Bilinen sınır: stepSourceStatuses en çok stepSourceMax durum okur; sekizinci
// sonrasındaki kullanılabilir bir kaynak görülmez — bu yalnız künyeyi daha
// temkinli yapar, ters yönde yanıltmaz.
func stepSourcesAllFailed(srcs []stepSourceState) bool {
	if len(srcs) == 0 {
		return false
	}
	for _, s := range srcs {
		if (sourcestate.Status{State: sourcestate.State(s.State)}).Usable() {
			return false
		}
	}
	return true
}
