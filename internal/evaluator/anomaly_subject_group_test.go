package evaluator

// v0.10.1798 — Davis paritesi FAZ 3 dilim 2: özne terfi kapısı + endpoint
// gruplaması (anomaly_subject_group.go). Hepsi saf, tablo testli.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var subjNow = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func subjEv(route, metric, dir string, ratio float64, startedAgo time.Duration) chstore.AnomalyEvent {
	pattern := metric + " @ " + route
	return chstore.AnomalyEvent{
		ID:           chstore.FingerprintAnomaly(chstore.SubjectAnomalyKind, pattern, "orders-api"),
		Kind:         chstore.SubjectAnomalyKind,
		Pattern:      pattern,
		Service:      "orders-api",
		StartedAt:    subjNow.Add(-startedAgo).UnixNano(),
		LastSeen:     subjNow.UnixNano(),
		PeakRatio:    ratio,
		CurrentRatio: ratio,
		CurrentCount: 500,
		Direction:    dir,
		BaselineKind: chstore.AnomalyBaselineSeasonal,
		SubjectKind:  chstore.AnomalySubjectEndpoint,
		SubjectID:    route,
		Status:       "active",
	}
}

func subjCfgs() (chstore.AnomalySubjectsConfig, chstore.AnomalyPromotionConfig) {
	return chstore.NormalizeAnomalySubjects(chstore.AnomalySubjectsConfig{}),
		chstore.NormalizeAnomalyPromotion(chstore.DefaultAnomalyPromotion())
}

func TestSubjectPromotionGate(t *testing.T) {
	scfg, pcfg := subjCfgs() // 600 sn, 2,0×, countOnlyMinCount 20
	ok := subjEv("GET /cart", "p99_ms", "up", 2.4, 15*time.Minute)
	countOnly := ok
	countOnly.Pattern, countOnly.PeakRatio, countOnly.CurrentRatio, countOnly.BaselineValue = "error_rate @ GET /cart", 0, 0, 0
	db := ok
	db.Pattern, db.SubjectKind, db.SubjectID, db.Service = "error_rate", chstore.AnomalySubjectDB, "db:postgresql@orders-db", "db:postgresql@orders-db"
	cases := []struct {
		name string
		mut  func(*chstore.AnomalyEvent)
		base chstore.AnomalyEvent
		want bool
	}{
		{"p99 artış, 2,4×, 15 dk → geçer", nil, ok, true},
		{"p95 artış → geçer", func(e *chstore.AnomalyEvent) { e.Pattern = "p95_ms @ GET /cart" }, ok, true},
		{"error_rate artış → geçer", func(e *chstore.AnomalyEvent) { e.Pattern = "error_rate @ GET /cart" }, ok, true},
		{"gecikme DÜŞÜŞÜ → geçmez", func(e *chstore.AnomalyEvent) { e.Direction = "down" }, ok, false},
		{"yön bilinmiyor (eski satır) → geçmez", func(e *chstore.AnomalyEvent) { e.Direction = "" }, ok, false},
		{"request_rate düşüş → geçmez", func(e *chstore.AnomalyEvent) { e.Pattern, e.Direction = "request_rate @ GET /cart", "down" }, ok, false},
		{"request_rate artış → geçmez", func(e *chstore.AnomalyEvent) { e.Pattern = "request_rate @ GET /cart" }, ok, false},
		{"sürdürme sınırı tam 600 sn → geçer", func(e *chstore.AnomalyEvent) { e.StartedAt = subjNow.Add(-600 * time.Second).UnixNano() }, ok, true},
		{"sürdürme 599 sn → geçmez", func(e *chstore.AnomalyEvent) { e.StartedAt = subjNow.Add(-599 * time.Second).UnixNano() }, ok, false},
		{"oran sınırı tam 2,0 → geçer", func(e *chstore.AnomalyEvent) { e.PeakRatio = 2.0 }, ok, true},
		{"oran 1,99 → geçmez", func(e *chstore.AnomalyEvent) { e.PeakRatio = 1.99 }, ok, false},
		{"tabansız error_rate, sayım 500 ≥ 20 → geçer", nil, countOnly, true},
		{"tabansız error_rate, sayım 19 → geçmez", func(e *chstore.AnomalyEvent) { e.CurrentCount = 19 }, countOnly, false},
		{"tabansız error_rate, sayım tam 20 → geçer", func(e *chstore.AnomalyEvent) { e.CurrentCount = 20 }, countOnly, true},
		{"tabansız p99 (sayım kolu yok) → geçmez", func(e *chstore.AnomalyEvent) { e.Pattern = "p99_ms @ GET /cart" }, countOnly, false},
		{"db öznesi aynı kapıdan geçer", nil, db, true},
		{"özne olmayan tür → geçmez", func(e *chstore.AnomalyEvent) { e.Kind = "trace_op" }, ok, false},
	}
	for _, c := range cases {
		ev := c.base
		if c.mut != nil {
			c.mut(&ev)
		}
		if got := subjectPromotionGate(ev, scfg, pcfg, subjNow); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// Eşikler ayardan: oran 3,0 istenince 2,4× geçmez.
	scfg.PromotionMinRatio = 3
	if subjectPromotionGate(ok, scfg, pcfg, subjNow) {
		t.Error("PromotionMinRatio=3 iken 2,4× geçti — eşik ayardan okunmuyor")
	}
	// Genel yol (promotionGate) özneleri hâlâ reddeder: özne yolu ayrı.
	if promotionGate(ok, pcfg, subjNow) {
		t.Error("promotionGate özne olayını geçirdi — genel yol özne açmamalı")
	}
}

func TestGroupSubjectEvents(t *testing.T) {
	scfg, pcfg := subjCfgs()
	gate := func(ev chstore.AnomalyEvent) bool { return subjectPromotionGate(ev, scfg, pcfg, subjNow) }
	a := subjEv("GET /cart", "p99_ms", "up", 2.4, 20*time.Minute)
	b := subjEv("POST /pay", "p99_ms", "up", 3.1, 18*time.Minute)
	c := subjEv("GET /item/{id}", "p99_ms", "up", 1.5, 16*time.Minute) // kapı altı, pencere içinde
	late := subjEv("GET /search", "p99_ms", "up", 1.4, 2*time.Minute)  // kapı altı, pencere dışında (18 dk sonra)
	errEv := subjEv("GET /cart", "error_rate", "up", 5, 20*time.Minute)
	down := subjEv("GET /health", "p99_ms", "down", 0.3, 20*time.Minute)
	cleared := subjEv("GET /old", "p99_ms", "up", 9, 30*time.Minute)
	cleared.Status = "cleared"
	mutedEv := subjEv("GET /muted", "p99_ms", "up", 9, 30*time.Minute)
	db := a
	db.Pattern, db.SubjectKind, db.Service = "p99_ms", chstore.AnomalySubjectDB, "db:postgresql@orders-db"
	other := subjEv("GET /a", "p99_ms", "up", 2.5, 20*time.Minute)
	other.Service = "billing-api"
	nonSubj := a
	nonSubj.Kind = "trace_op"

	got := groupSubjectEvents([]chstore.AnomalyEvent{a, b, c, late, errEv, down, cleared, mutedEv, db, other, nonSubj},
		map[string]bool{mutedEv.ID: true}, gate)
	if len(got) != 3 {
		t.Fatalf("aday sayısı %d, want 3 (orders p99, orders error_rate, billing p99): %+v", len(got), got)
	}
	want := []string{
		"anomaly-auto:subject:billing-api:p99_ms:up",
		"anomaly-auto:subject:orders-api:error_rate:up",
		"anomaly-auto:subject:orders-api:p99_ms:up",
	}
	for i, w := range want {
		if got[i].RuleID != w {
			t.Errorf("aday %d rule_id %q, want %q", i, got[i].RuleID, w)
		}
	}
	p99 := got[2]
	routes := func(evs []chstore.AnomalyEvent) string {
		var r []string
		for _, e := range evs {
			r = append(r, subjectEventRoute(e))
		}
		return strings.Join(r, ",")
	}
	// Üyeler: aktif + susturulmamış + kötüleşen, orana göre azalan.
	if r := routes(p99.Members); r != "POST /pay,GET /cart,GET /item/{id},GET /search" {
		t.Errorf("üyeler = %s", r)
	}
	// Açılış: kapıyı geçen ikisi + pencere içindeki kapı altı komşu; geç başlayan dışarıda.
	if r := routes(p99.Opening); r != "POST /pay,GET /cart,GET /item/{id}" {
		t.Errorf("açılış üyeleri = %s", r)
	}

	// Kapıyı geçen rota yoksa açılış adayı değil (yalnız tazelenebilir).
	only := groupSubjectEvents([]chstore.AnomalyEvent{c, late}, nil, gate)
	if len(only) != 1 || len(only[0].Opening) != 0 || len(only[0].Members) != 2 {
		t.Errorf("kapı altı grup: %+v", only)
	}
	// Tek rota da aynı rule_id ailesini alır.
	single := groupSubjectEvents([]chstore.AnomalyEvent{a}, nil, gate)
	if len(single) != 1 || single[0].RuleID != "anomaly-auto:subject:orders-api:p99_ms:up" || len(single[0].Opening) != 1 {
		t.Errorf("tek rota: %+v", single)
	}
}

func TestSubjectGroupTitleAndDescription(t *testing.T) {
	k := subjectGroupKey{Service: "orders-api", Metric: "p99_ms", Direction: "up"}
	a := subjEv("POST /pay", "p99_ms", "up", 3.1, 15*time.Minute)
	b := subjEv("GET /cart", "p99_ms", "up", 2.4, 12*time.Minute)
	c := subjEv("GET /item/{id}", "p99_ms", "up", 2.05, 10*time.Minute)
	ek := subjectGroupKey{Service: "orders-api", Metric: "error_rate", Direction: "up"}
	e1 := subjEv("GET /cart", "error_rate", "up", 2.44, 75*time.Minute)
	e0 := e1
	e0.PeakRatio, e0.CurrentRatio = 0, 0
	cases := []struct {
		name string
		k    subjectGroupKey
		mem  []chstore.AnomalyEvent
		want string
	}{
		{"çok rota gecikme", k, []chstore.AnomalyEvent{a, b, c}, "orders-api: 3 rota yavaşladı (p99 3,1× taban, 15 dk)"},
		{"tek rota gecikme", k, []chstore.AnomalyEvent{b}, "orders-api: GET /cart yavaşladı (p99 2,4× taban, 12 dk)"},
		{"çok rota hata", ek, []chstore.AnomalyEvent{e1, e1}, "orders-api: 2 rotada hata oranı arttı (2,4× taban, 1 sa 15 dk)"},
		{"tek rota hata, taban yok", ek, []chstore.AnomalyEvent{e0}, "orders-api: GET /cart rotasında hata oranı arttı (yeni hata, taban yok, 1 sa 15 dk)"},
	}
	for _, c := range cases {
		if got := subjectGroupTitle(c.k, c.mem, subjNow); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}

	var many []chstore.AnomalyEvent
	for i, r := range []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g"} {
		many = append(many, subjEv("GET "+r, "p99_ms", "up", float64(9-i), 15*time.Minute))
	}
	got := subjectGroupDescription(k, many)
	want := "Etkilenen rotalar (p99, tabana oranla): GET /a 9,0×; GET /b 8,0×; GET /c 7,0×; GET /d 6,0×; GET /e 5,0× ve 2 rota daha."
	if got != want {
		t.Errorf("detay:\n got %q\nwant %q", got, want)
	}
	if got := subjectGroupDescription(ek, []chstore.AnomalyEvent{e0}); got != "Etkilenen rotalar (hata oranı, tabana oranla): GET /cart taban yok." {
		t.Errorf("tek tabansız detay: %q", got)
	}
}

func TestFormatDurationTR(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "1 dk", 59 * time.Second: "1 dk", 15 * time.Minute: "15 dk", 59 * time.Minute: "59 dk",
		time.Hour: "1 sa", 80 * time.Minute: "1 sa 20 dk", 26 * time.Hour: "26 sa",
	} {
		if got := formatDurationTR(d); got != want {
			t.Errorf("%v → %q, want %q", d, got, want)
		}
	}
}

func TestSubjectGroupProblem(t *testing.T) {
	scfg, pcfg := subjCfgs()
	gate := func(ev chstore.AnomalyEvent) bool { return subjectPromotionGate(ev, scfg, pcfg, subjNow) }
	a := subjEv("POST /pay", "p99_ms", "up", 3.1, 20*time.Minute)
	b := subjEv("GET /cart", "p99_ms", "up", 2.4, 18*time.Minute)
	late := subjEv("GET /search", "p99_ms", "up", 1.4, 2*time.Minute)
	c := groupSubjectEvents([]chstore.AnomalyEvent{a, b, late}, nil, gate)[0]

	// Açılış: yalnız pencere üyeleri, yeni kimlik, en erken başlangıç.
	p := subjectGroupProblem(c, nil, scfg, pcfg, subjNow)
	if p.ID != "anomaly-auto:subject:orders-api:p99_ms:up:orders-api" || p.RuleID != c.RuleID || p.Service != "orders-api" {
		t.Errorf("kimlik: %+v", p)
	}
	if p.RuleName != "orders-api: 2 rota yavaşladı (p99 3,1× taban, 20 dk)" {
		t.Errorf("başlık %q", p.RuleName)
	}
	if p.Severity != "warning" || p.Metric != "anomaly_ratio" || p.Value != 3.1 || p.Threshold != 2 || p.StartedAt != a.StartedAt || p.Status != "open" {
		t.Errorf("açılış gövdesi: %+v", p)
	}
	// Tazeleme: bütün üyeler (geç rota dahil), açık satırın kimliği / başlangıcı / ack'i.
	open := &chstore.Problem{ID: "old-id", StartedAt: 42, Status: "acknowledged", Assignee: "oncall"}
	r := subjectGroupProblem(c, open, scfg, pcfg, subjNow)
	if r.ID != "old-id" || r.StartedAt != 42 || r.Status != "acknowledged" || r.Assignee != "oncall" {
		t.Errorf("tazeleme operatör alanları: %+v", r)
	}
	if !strings.Contains(r.RuleName, "3 rota yavaşladı") || !strings.Contains(r.Description, "GET /search") {
		t.Errorf("tazelemede rotalar tazelenmedi: %q / %q", r.RuleName, r.Description)
	}
	// Kritik eşik ve tabansız sayım kolu.
	hot := subjEv("GET /cart", "p99_ms", "up", 25, 20*time.Minute)
	if got := subjectGroupProblem(groupSubjectEvents([]chstore.AnomalyEvent{hot}, nil, gate)[0], nil, scfg, pcfg, subjNow).Severity; got != "critical" {
		t.Errorf("25× şiddet %q, want critical", got)
	}
	e0 := subjEv("GET /cart", "error_rate", "up", 0, 20*time.Minute)
	e0.CurrentCount = 80
	cp := subjectGroupProblem(groupSubjectEvents([]chstore.AnomalyEvent{e0}, nil, gate)[0], nil, scfg, pcfg, subjNow)
	if cp.Severity != "warning" || cp.Metric != "anomaly_count" || cp.Comparator != ">=" || cp.Value != 80 || cp.Threshold != 20 {
		t.Errorf("tabansız gövde: %+v", cp)
	}
}
