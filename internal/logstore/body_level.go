package logstore

import (
	"encoding/json"
	"strings"
)

// v0.10.1794 — Operator-reported (prod ES, Service › Logs): container
// stdout JSON satırlarında `"level":"INFO"` yalnız `message` gövdesinin
// İÇİNDE; ES belgesinde üst alan yok. Şerit histogramı (severity bantları),
// satır rozeti ve `level:x` kısayolu hep üst alanlara baktığından bu
// satırlar OTHER/DEBUG'a düşüyordu. Üç okuma yolu da aynı gövde-seviyesi
// tanımını buradan alır (düzeltmenin ikinci yarıları tek yerde).

// bodyLevelKey — gövde JSON'unda aranan seviye anahtarı. Tek anahtar:
// her ek anahtar histogramda bant × önek kadar phrase yan tümcesi demek
// (ES maliyeti); imza çıkarımındaki sigLevelKeys'in ilki.
const bodyLevelKey = "level"

// jsonBodyLevel — gövde bir JSON nesnesiyse ve seviye anahtarı (büyük-küçük
// harf duyarsız, sigLevelKeys sırasıyla) dizge değer taşıyorsa onu döner;
// değilse "". Satır okuma (mapHit) üst alan boşken buna düşer. SAF.
func jsonBodyLevel(body string) string {
	b := strings.TrimSpace(body)
	if len(b) < 2 || b[0] != '{' {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(b), &m); err != nil {
		return ""
	}
	for _, want := range sigLevelKeys {
		for k, raw := range m {
			if !strings.EqualFold(k, want) {
				continue
			}
			var v string
			if json.Unmarshal(raw, &v) == nil && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// bodyLevelPhraseClause — histogram bandı / takım hata yüklemi için gövde
// yan tümcesi: standart analizörde `"level":"ERROR"` → [level, error]
// belirteçleri; `match_phrase_prefix` "level err*" onu yakalar. Keyword
// ya da eşlenmemiş gövde alanında hiçbir şey eşlemez, hata vermez.
// max_expansions küçük: önek bant kelimesinin kendisi (error/errors…).
func bodyLevelPhraseClause(bodyField, prefix string) map[string]any {
	return map[string]any{
		"match_phrase_prefix": map[string]any{
			bodyField: map[string]any{"query": bodyLevelKey + " " + prefix, "max_expansions": 10},
		},
	}
}

// bodyLevelQueryStringPart — `level:error` kısayolunun gövde kolu
// (query_string sözdizimi): `message:"level error"`. Tırnaklı değer
// soyulur; joker/boşluk içeren değer phrase'e uymaz, "" döner.
func bodyLevelQueryStringPart(bodyField, val string) string {
	v := strings.Trim(val, `"`)
	if v == "" || strings.ContainsAny(v, ` *?"\`) {
		return ""
	}
	return bodyField + `:"` + bodyLevelKey + " " + v + `"`
}
