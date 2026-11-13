package api

// ai_evalset_stream_contract.go — v0.10.2107 (CoSRE kalite döngüsü D2,
// docs/cosre/quality-loop-audit.md §3.2): rubriğin stream_contract boyutu.
//
// v2 akış parçaları (ai_evalset_v2sink.go) modelsiz, deterministik olarak
// sözleşmeye karşı denetlenir. Kurallar YENİ YAZILMAZ — sunucunun bugün
// istemciden gelen v2 parçalarını arşivlerken uyguladığı sanitizer'lar
// (ai_conversations_v2parts.go) aynen kullanılır; sanitizer bir parçayı
// düşürür ya da kırparsa sözleşme ihlalidir:
//
//   - bilinen parça türü: çevirici KnownKinds dışındaki olayı data-unknown'a
//     sarar (uistream.Translator) → data-unknown = ihlal;
//   - data-links: sanitizeChatLinksPart her linki korumalı; title varsa kart
//     korunmalı (1–3 kök-göreli link + özet — FE parseDataLinksCard kuralı);
//   - data-question: sanitizeChatQuestion nil dönmemeli (kapalı kind, dolu
//     metin, 1–3 tekil seçenek);
//   - araç çağrısı ↔ sonucu: her tool-input-available'ın aynı toolCallId'li
//     tool-output-available'ı ya da data-approval'ı olmalı. İstisna: akış
//     hatayla bittiyse (finish error) yarıda kalan çağrı model hatası değil
//     akış kesintisidir. Ters yön (girdisiz çıktı) ihlal DEĞİL: onay devam
//     akışı önceki akışın çağrısına sonuç yazar (ui-stream-v1-approval-resume).
//
// Paket evalrubric kural taşımaz; yalnız sonucu (StreamContract *bool + not)
// alır. v1 koşusunda parça yoktur → boyut n/a.

import (
	"encoding/json"
	"fmt"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// evalStreamContract — SAF: parçalar sözleşmeye uyuyor mu; değilse ilk
// ihlalin kısa açıklaması.
func evalStreamContract(parts []uistream.Part) (bool, string) {
	resolved := map[string]bool{}
	var inputs []string
	finishErr := false
	for _, p := range parts {
		typ, _ := p["type"].(string)
		switch typ {
		case "data-unknown":
			kind := ""
			if d, ok := p["data"].(map[string]any); ok {
				kind, _ = d["kind"].(string)
			}
			return false, fmt.Sprintf("bilinmeyen olay türü %q (data-unknown)", kind)
		case "data-links":
			var in aiChatLinksPart
			if !decodePartData(p, &in) {
				return false, "data-links: çözülemedi"
			}
			out := sanitizeChatLinksPart(&in)
			switch {
			case out == nil:
				return false, "data-links: geçerli link yok"
			case len(out.Links) != len(in.Links):
				return false, fmt.Sprintf("data-links: %d linkten %d'i geçersiz", len(in.Links), len(in.Links)-len(out.Links))
			case in.Title != "" && out.Title == "":
				return false, "data-links: title var ama kart kuralı tutmadı (1–3 site-içi link + özet)"
			}
		case "data-question":
			var in aiChatQuestion
			if !decodePartData(p, &in) || sanitizeChatQuestion(&in) == nil {
				return false, "data-question: kart kuralı tutmadı (bilinen kind, metin, 1–3 tekil seçenek)"
			}
		case "tool-input-available":
			if id, _ := p["toolCallId"].(string); id != "" {
				inputs = append(inputs, id)
			}
		case "tool-output-available":
			if id, _ := p["toolCallId"].(string); id != "" {
				resolved[id] = true
			}
		case "data-approval":
			if d, ok := p["data"].(map[string]any); ok {
				if id, _ := d["toolCallId"].(string); id != "" {
					resolved[id] = true
				}
			}
		case "finish":
			finishErr = p["finishReason"] == "error"
		}
	}
	if finishErr {
		return true, ""
	}
	for _, id := range inputs {
		if !resolved[id] {
			return false, fmt.Sprintf("araç çağrısı %s sonuçsuz kaldı", id)
		}
	}
	return true, ""
}

// decodePartData — parçanın data alanını hedef yapıya çevirir (JSON gidiş-
// dönüş: çevirici map[string]any üretir, sanitizer'lar tipli yapı ister).
func decodePartData(p uistream.Part, dst any) bool {
	b, err := json.Marshal(p["data"])
	if err != nil {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

// evalStreamContractInput — SAF: rubrik girdisi. Parça yoksa (v1 / akış hiç
// başlamadı) ya da tavan parça düşürdüyse (dizi eksik — yarıda kalan çağrı
// yanlış ihlal üretirdi) n/a.
func evalStreamContractInput(parts []uistream.Part, dropped int) (*bool, string) {
	if len(parts) == 0 || dropped > 0 {
		return nil, ""
	}
	ok, note := evalStreamContract(parts)
	return &ok, note
}
