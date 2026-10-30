package logstore

// es_services_filter.go — v0.10.1684 (CoSRE v2 araç seti dilim 3): Filter.Services'in ES yan tümcesi.

import "github.com/cilcenk/coremetry/internal/chstore"

// logServicesSet — Filter.Services sözleşmesi (iki arka uç tek kaynak: chstore.LogServicesSet).
func logServicesSet(f Filter) []string { return chstore.LogServicesSet(f.Service, f.Services) }

// esServicesShould — SAF: aday alanlar × servis kümesi (tam ad + gerçekten
// soyulmuşsa env-eksiz ad) için alan başına İKİ biçim (exactTermsBothShapes'in
// `terms` ikizi): `<alan>.keyword` terms + `.keyword` yokken çıplak alan terms.
// Boş/tekrarlı alan atlanır; değer sırası girdinin (sıralı) sırası.
func esServicesShould(fields, services []string) []any {
	vals := make([]any, 0, len(services)*2)
	seenV := map[string]bool{}
	for _, sv := range services {
		for _, v := range []string{sv, stripLogEnvSuffix(sv)} {
			if v == "" || seenV[v] {
				continue
			}
			seenV[v] = true
			vals = append(vals, v)
		}
	}
	seen := map[string]bool{}
	out := make([]any, 0, len(fields)*2)
	for _, fld := range fields {
		if fld == "" || seen[fld] {
			continue
		}
		seen[fld] = true
		out = append(out,
			map[string]any{"terms": map[string]any{fld + ".keyword": vals}},
			map[string]any{"bool": map[string]any{
				"must":     []any{map[string]any{"terms": map[string]any{fld: vals}}},
				"must_not": []any{map[string]any{"exists": map[string]any{"field": fld + ".keyword"}}},
			}},
		)
	}
	return out
}
