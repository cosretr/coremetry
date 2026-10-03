package logstore

// es_pattern_verify.go — v0.10.1080: VerifyPatterns'ın ES yolu (gerekçe
// pattern_verify.go başlığında). Maliyet: dedektör yalnız tetiklemek üzere
// olan ≤N deseni (tik başına üst sınır çağıranda) TEK _msearch'le sorar;
// tetiklemeyen desen için hiç istek yok. Her alt sorgu: size ≤50, _source
// yalnız gövde, shard başına terminate_after, _doc sırası (skor / sıralama
// maliyeti yok), ≤5 s yumuşak timeout, request_cache.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// patternVerifyTimeout — örnek alt sorgusunun yumuşak timeout tavanı.
// esSoftTimeout yalnız kısaltır: operatörün daha düşük env değeri kalır.
const patternVerifyTimeout = 5 * time.Second

// patternSampleBody — SAF: bir desenin örnek alt sorgusu. Yüklem
// patternCountBody'ninkiyle AYNI (zaman aralığı + patternMatchClause) —
// sayılan dokümanlardan örnek çekilir, başka bir kümeden değil.
//
//   - size + terminate_after = size: shard başına en çok `size` doküman
//     toplanır, koordinatör ilk `size`'ı döndürür;
//   - sort _doc: dizin sırası, skor/sıralama yok (en ucuz okuma);
//   - _source yalnız gövde alanı: 50 tam doküman değil 50 metin;
//   - track_total_hits:false: sayıyı CountPatterns zaten verdi.
func patternSampleBody(patClause map[string]any, bodyField, tsField, from, to string, size int, timeout string) map[string]any {
	return map[string]any{
		"size":             size,
		"terminate_after":  size,
		"track_total_hits": false,
		"timeout":          timeout,
		"_source":          []string{bodyField},
		"sort":             []any{"_doc"},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"range": map[string]any{tsField: map[string]any{"gte": from, "lt": to}}},
					patClause,
				},
			},
		},
	}
}

// patternVerifyNDJSON — SAF: _msearch gövdesi (başlık + gövde satır çifti,
// desen başına). Token'sız desen match_none ile yer tutar (CountPatterns
// emsali) — cevap dizisi girdiyle hizalı kalsın.
func patternVerifyNDJSON(pats []PatternSpec, indices []string, bodyField, tsField, from, to, timeout string) string {
	var nd strings.Builder
	tru := true
	for _, pat := range pats {
		hb, _ := json.Marshal(map[string]any{
			"index":              indices,
			"allow_no_indices":   tru,
			"ignore_unavailable": tru,
			// Mutlak pencere; size>0 isteği ES ancak açık istekle önbelleğe alır.
			"request_cache": tru,
		})
		nd.Write(hb)
		nd.WriteByte('\n')
		if len(pat.Tokens) == 0 {
			nd.WriteString(`{"size":0,"query":{"match_none":{}}}` + "\n")
			continue
		}
		bb, _ := json.Marshal(patternSampleBody(patternMatchClause(pat, bodyField),
			bodyField, tsField, from, to, patternSampleSize, timeout))
		nd.Write(bb)
		nd.WriteByte('\n')
	}
	return nd.String()
}

// sampleBodyText — örnek dokümandan gövde metni: düz anahtar ("message")
// ya da noktalı yol ("log.message" iç içe).
func sampleBodyText(src map[string]any, field string) string {
	if v, ok := src[field].(string); ok {
		return v
	}
	return readPath(src, field)
}

func (s *ESStore) VerifyPatterns(ctx context.Context, pats []PatternSpec, from, to time.Time) ([]PatternVerification, error) {
	out := make([]PatternVerification, len(pats))
	if len(pats) == 0 {
		return out, nil
	}
	idx := s.queryIndices(ctx, Filter{From: from, To: to})
	nd := patternVerifyNDJSON(pats, idx, s.fields.Body, s.fields.Timestamp,
		from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano),
		esSoftTimeout(patternVerifyTimeout))
	req := esapi.MsearchRequest{Body: strings.NewReader(nd)}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return out, s.recordQueryError("msearch verify-patterns", idx, []byte(nd), 0,
			fmt.Errorf("ES msearch verify-patterns: %w", err))
	}
	defer res.Body.Close()
	if res.IsError() {
		return out, s.recordQueryError("msearch verify-patterns", idx, []byte(nd), res.StatusCode,
			parseESError("msearch verify-patterns", res, s.cfg.Index))
	}
	var raw struct {
		Responses []struct {
			esSearchEnvelope
			Hits struct {
				Hits []struct {
					Source map[string]any `json:"_source"`
				} `json:"hits"`
			} `json:"hits"`
			Error any `json:"error,omitempty"`
		} `json:"responses"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return out, fmt.Errorf("decode ES msearch verify-patterns: %w", err)
	}
	for i, r := range raw.Responses {
		if i >= len(out) {
			break
		}
		// Alt sorgu hatası → Sampled 0 (bilinmiyor): dedektör sayımı aynen
		// bırakır, bastırmaz. Kısmi cevap yine örnektir (oran tahmini).
		if r.Error != nil || len(pats[i].Tokens) == 0 {
			continue
		}
		if d := r.describe(); d != "" {
			log.Printf("[logstore-es] PARTIAL pattern sample (%s) — slot %d örneklemi küçük", d, i)
		}
		bodies := make([]string, 0, len(r.Hits.Hits))
		for _, h := range r.Hits.Hits {
			// Gövdesi okunamayan doküman örneğe GİRMEZ: alan eşlemesi
			// beklenenden farklıysa boş metin "regex'e uymadı" sayılıp
			// gerçek olayları bastırırdı. Hepsi boşsa Sampled 0 = bilinmiyor.
			if b := sampleBodyText(h.Source, s.fields.Body); b != "" {
				bodies = append(bodies, b)
			}
		}
		out[i] = verifyPatternBodies(pats[i].Regex, bodies)
	}
	return out, nil
}
