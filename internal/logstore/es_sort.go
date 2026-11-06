package logstore

// es_sort.go — v0.10.1833 (Log Search Faz 1 dilim 1.10): ES liste
// sıralaması. Kolon kimliği → belge alanı → sıralanabilirlik eşlemeden
// (GetMapping, son 24 saatin indeksleri — ListFieldsBounded ile aynı dar
// kapsam; 5 dk önbellek). Sıralanabilir: keyword / sayısal / date / ip /
// boolean (doc_values kapalı değilse); text alanı keyword alt alanı
// (multi-field) varsa ona sıralanır, yoksa sıralanamaz (400). Eşleme
// okunamazsa alan aynen + unmapped_type keyword ile gönderilir (ES reddi
// yine ErrBadQuery → 400; 500 değil).

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

const esSortMapTTL = 5 * time.Minute

// esSortable — bir alan yolunun sıralama çözümü.
type esSortable struct {
	Field    string // sıralanacak yol (text ise .keyword alt alanı)
	Unmapped string // unmapped_type (eşlemesi olmayan indeks için)
	Reason   string // boş değilse sıralanamaz; operatör cümlesi
}

type esSortCache struct {
	mu sync.Mutex
	at time.Time
	m  map[string]esSortable
}

// walkSortable — SAF: mapping properties → yol → çözüm. İç içe nesneler
// noktalı yola açılır; text alanının keyword alt alanı (fields.*) seçilir.
func walkSortable(prefix string, props map[string]any, out map[string]esSortable) {
	for name, raw := range props {
		if name == "" || name[0] == '_' {
			continue
		}
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if nested, ok := m["properties"].(map[string]any); ok {
			walkSortable(path, nested, out)
			continue
		}
		t, _ := m["type"].(string)
		dv, hasDV := m["doc_values"].(bool)
		// Alt alanlar da doğrudan seçilebilir (kolon "x.keyword" olarak eklenmişse).
		if fs, ok := m["fields"].(map[string]any); ok {
			for sub, sraw := range fs {
				sm, _ := sraw.(map[string]any)
				if st, _ := sm["type"].(string); st == "keyword" {
					out[path+"."+sub] = esSortable{Field: path + "." + sub, Unmapped: "keyword"}
				}
			}
		}
		switch t {
		case "keyword", "constant_keyword", "long", "integer", "short", "byte", "double", "float",
			"half_float", "scaled_float", "unsigned_long", "date", "date_nanos", "boolean", "ip":
			if hasDV && !dv {
				out[path] = esSortable{Reason: "Alanın doc_values'u kapalı."}
				continue
			}
			out[path] = esSortable{Field: path, Unmapped: esUnmappedType(t)}
		case "text", "match_only_text", "wildcard":
			if sub := keywordSubfield(m); sub != "" {
				out[path] = esSortable{Field: path + "." + sub, Unmapped: "keyword"}
				continue
			}
			out[path] = esSortable{Reason: "Metin (text) alanı; keyword alt alanı yok."}
		default:
			if t != "" {
				out[path] = esSortable{Reason: fmt.Sprintf("Alan tipi (%s) sıralanamaz.", t)}
			}
		}
	}
}

// keywordSubfield — text alanının keyword alt alanı (varsa; "keyword" adı önce).
func keywordSubfield(m map[string]any) string {
	fs, ok := m["fields"].(map[string]any)
	if !ok {
		return ""
	}
	if sm, ok := fs["keyword"].(map[string]any); ok {
		if st, _ := sm["type"].(string); st == "keyword" {
			return "keyword"
		}
	}
	best := ""
	for sub, sraw := range fs {
		sm, _ := sraw.(map[string]any)
		if st, _ := sm["type"].(string); st == "keyword" && (best == "" || sub < best) {
			best = sub
		}
	}
	return best
}

func esUnmappedType(t string) string {
	switch t {
	case "constant_keyword":
		return "keyword"
	case "date_nanos":
		return "date"
	case "half_float", "scaled_float":
		return "float"
	case "unsigned_long":
		return "long"
	}
	return t
}

// esSortCandidates — kolon kimliği → denenecek belge alanları (sırayla).
// Yerleşik kolonlar yapılandırılmış alan + bilinen şekiller; diğerleri aynen.
func (s *ESStore) esSortCandidates(id string) []string {
	nonEmpty := func(xs ...string) []string {
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	switch id {
	case SortFieldTime:
		return []string{s.fields.Timestamp}
	case "service":
		return nonEmpty(s.fields.Service)
	case "level":
		return nonEmpty(s.fields.SeverityNo, s.fields.SeverityTx)
	case "cluster":
		return nonEmpty(s.fields.Cluster, "openshift.labels.cluster", "kubernetes.cluster.name", "k8s.cluster.name")
	case "pod":
		return nonEmpty(s.fields.Pod, "kubernetes.pod_name", "kubernetes.pod.name", "k8s.pod.name", "resource.k8s.pod.name", "pod_name", "pod")
	}
	return []string{id}
}

// resolveESSort — SAF: kolon kimliği → (alan, unmapped_type) ya da
// sıralanamaz hatası. m nil = eşleme bilinmiyor (ilk aday aynen).
func resolveESSort(id string, candidates []string, m map[string]esSortable) (string, string, error) {
	if id == SortFieldTime && len(candidates) > 0 {
		return candidates[0], "date", nil
	}
	if len(candidates) == 0 {
		return "", "", unsortable(id, "Bu arka uçta alan yapılandırılmamış.")
	}
	if m == nil {
		return candidates[0], "keyword", nil
	}
	reason := ""
	for _, c := range candidates {
		if r, ok := m[c]; ok {
			if r.Reason == "" {
				return r.Field, r.Unmapped, nil
			}
			if reason == "" {
				reason = r.Reason
			}
		}
	}
	if reason == "" {
		reason = "Alan son 24 saatin indeks eşlemesinde yok."
	}
	return "", "", unsortable(id, reason)
}

// esSortClauses — SAF: anahtarlar → ES `sort` dizisi. Zaman listede yoksa
// eşitlik bozucu olarak (tsDir yönünde) eklenir. Belge kimliği eşitlik
// bozucusunu (_shard_doc/_doc — PIT'e bağlı) çağıran ekler.
func esSortClauses(keys []SortKey, resolve func(id string) (string, string, error), tsField, tsDir string) ([]any, error) {
	out := make([]any, 0, len(keys)+2)
	for _, k := range keys {
		field, unmapped, err := resolve(k.Field)
		if err != nil {
			return nil, err
		}
		dir := "asc"
		if k.Desc {
			dir = "desc"
		}
		out = append(out, map[string]any{field: map[string]any{"order": dir, "unmapped_type": unmapped}})
	}
	if timeTiebreakNeeded(keys) {
		out = append(out, map[string]any{tsField: map[string]any{"order": tsDir, "unmapped_type": "date"}})
	}
	return out, nil
}

// esSortMap — önbellekli eşleme çözümü; hata → nil (çağıran aynen gönderir).
func (s *ESStore) esSortMap(ctx context.Context) map[string]esSortable {
	c := &s.sortCache
	c.mu.Lock()
	if c.m != nil && time.Since(c.at) < esSortMapTTL {
		m := c.m
		c.mu.Unlock()
		return m
	}
	c.mu.Unlock()
	to := time.Now()
	req := esapi.IndicesGetMappingRequest{Index: s.queryIndices(ctx, Filter{From: to.Add(-24 * time.Hour), To: to})}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil
	}
	var body map[string]struct {
		Mappings struct {
			Properties map[string]any `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil
	}
	m := map[string]esSortable{}
	for _, idx := range body {
		walkSortable("", idx.Mappings.Properties, m)
	}
	c.mu.Lock()
	c.m, c.at = m, time.Now()
	c.mu.Unlock()
	return m
}

// userSortClauses — Search'ün çağırdığı kapı: eşleme + saf çözüm.
func (s *ESStore) userSortClauses(ctx context.Context, keys []SortKey, tsDir string) ([]any, error) {
	m := s.esSortMap(ctx)
	return esSortClauses(keys, func(id string) (string, string, error) {
		return resolveESSort(id, s.esSortCandidates(id), m)
	}, s.fields.Timestamp, tsDir)
}
