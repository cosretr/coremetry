package chstore

// problem_standard_origin.go — v0.10.2473: Problem standardının "Köken"
// alanı. Bağlı kümenin (entity grafı, evaluator computeOrigin) seçtiği köken
// manşetin hemen altında "Köken: <varlık>" + düz gerekçe olarak gösterilir
// (liste satırı ve detay başlığı aynı alanı okur).
//
// Kural — TAHMİN YOK:
//   - küme < 2 varlık, köken hesaplanmadı ya da güven `unclear` (hiçbir kural
//     ayırmadı) → alan boş, etiket çizilmez;
//   - kazanan kural tanınan dört kuraldan biri değilse → alan boş;
//   - köken varlığının kimliği / adı yoksa → alan boş.
//
// Alanı YALNIZ API okuma yolu doldurur (liste + detay ucu, görünüm kipi `on`
// iken). StandardizeProblem doldurmaz: bildirim, politika gölgesi, özet ve
// replay bu alanı görmez (bildirim metni değişmez).
//
// Bağlantı entity kimliğinden kurulur (Entity = `svc:…` / `db:…`); ad yalnız
// metindir, ad eşlemesi yapılmaz.

// StdOrigin — kümenin kökeni (görünüm).
type StdOrigin struct {
	Entity string `json:"entity"` // entity kimliği
	Type   string `json:"type"`   // service | database | queue | external
	Name   string `json:"name"`
	// Rule — kazanan (ilk ayıran) köken kuralı: K-ROLLOUT | K-SHARED | K-DEEP |
	// K-FIRST. Arayüz gerekçe cümlesini bundan kurar.
	Rule string `json:"rule"`
	// Confidence — strong (kazanan ≥ 2 kuralda önde) | weak (tek kural).
	Confidence string `json:"confidence"`
	// Entities — kümedeki varlık sayısı (köken dahil; kökenin kendi Problem'i
	// yoksa o sayılmaz).
	Entities int `json:"entities"`
	// Self — bu Problem'in öznesi kökenin kendisi.
	Self bool `json:"self,omitempty"`
}

// stdOriginRule — SAF: gösterilebilir köken kuralı mı.
func stdOriginRule(r string) bool {
	switch r {
	case OriginRuleRollout, OriginRuleShared, OriginRuleDeep, OriginRuleFirst:
		return true
	}
	return false
}

// StdOriginOf — SAF: küme → Problem'in köken alanı; gösterilmeyecekse nil.
func StdOriginOf(g ProblemGraphGroup, problemID string) *StdOrigin {
	o := g.Origin
	if o == nil || g.Entities < 2 || o.Entity == "" || o.Name == "" || !stdOriginRule(o.Rule) {
		return nil
	}
	if o.Confidence != OriginStrong && o.Confidence != OriginWeak {
		return nil
	}
	out := &StdOrigin{Entity: o.Entity, Type: o.Type, Name: o.Name, Rule: o.Rule, Confidence: o.Confidence, Entities: g.Entities}
	for _, m := range g.Members {
		if m.ProblemID == problemID {
			out.Self = m.Entity == o.Entity
			break
		}
	}
	return out
}
