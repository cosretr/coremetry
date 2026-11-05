package evaluator

// v0.10.1799 — Davis FAZ 3 dilim 3: gölge izleri (subjectShadowMarks, saf)
// + süpürmenin promoteStrongAnomalies'e bağlanışı.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSubjectShadowMarks(t *testing.T) {
	scfg, pcfg := subjCfgs()
	gate := func(ev chstore.AnomalyEvent) bool { return subjectPromotionGate(ev, scfg, pcfg, subjNow) }
	a := subjEv("POST /pay", "p99_ms", "up", 3.1, 20*time.Minute)
	b := subjEv("GET /cart", "p99_ms", "up", 2.4, 18*time.Minute)
	already := subjEv("GET /item/{id}", "p99_ms", "up", 2.2, 17*time.Minute)
	already.Promotion = chstore.AnomalyPromotionShadow
	under := subjEv("GET /search", "error_rate", "up", 1.3, 20*time.Minute) // kapı altı tek rota
	db := subjEv("x", "error_rate", "up", 4, 20*time.Minute)
	db.Pattern, db.SubjectKind, db.SubjectID, db.Service = "error_rate", chstore.AnomalySubjectDB, "db:postgresql@orders-db", "db:postgresql@orders-db"
	db.ID = "dbfp"
	dbUnder := db
	dbUnder.ID, dbUnder.PeakRatio = "dbfp2", 1.1
	dbMuted := db
	dbMuted.ID = "dbfp3"
	muted := map[string]bool{"dbfp3": true}

	evs := []chstore.AnomalyEvent{a, b, already, under, db, dbUnder, dbMuted}
	marks, n := subjectShadowMarks(evs, groupSubjectEvents(evs, muted, gate), muted, gate)
	if n != 2 {
		t.Errorf("aday sayısı %d, want 2 (orders p99 grubu + db)", n)
	}
	var got []string
	for _, m := range marks {
		if m.Promotion != chstore.AnomalyPromotionShadow {
			t.Errorf("iz yanlış: %+v", m)
		}
		got = append(got, m.ID)
	}
	want := []string{a.ID, b.ID, "dbfp"} // zaten izli rota yeniden yazılmaz
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("izler %v, want %v", got, want)
	}
	// Bölüm alanları aynen taşınır (yeniden yazım satırı sıfırlamaz).
	if marks[0].StartedAt != a.StartedAt || marks[0].PeakRatio != a.PeakRatio || marks[0].SubjectID != a.SubjectID {
		t.Errorf("iz satırı alanları bozdu: %+v", marks[0])
	}
}

// TestPromoteStrongAnomaliesRunsSubjectSweep — süpürme genel döngüden SONRA,
// çözülme geçişinden ÖNCE çağrılır (gölge izleri aynı tikte yazılır).
func TestPromoteStrongAnomaliesRunsSubjectSweep(t *testing.T) {
	src := readEvaluatorSource(t)
	i := strings.Index(src, "func (e *Evaluator) promoteStrongAnomalies(")
	if i < 0 {
		t.Fatal("promoteStrongAnomalies yok")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j > 0 {
		body = body[:j+1]
	}
	sw := strings.Index(body, "activeGroups := e.promoteSubjectAnomalies(ctx, cfg, muted, now)")
	rs := strings.Index(body, "e.resolveClearedAnomalyPromotions(ctx, muted, activeGroups)")
	if sw < 0 || rs < 0 || sw > rs {
		t.Errorf("özne süpürmesi çağrılmıyor ya da çözülmeden sonra (sw=%d rs=%d)", sw, rs)
	}
}

// v0.10.1800 (Davis FAZ 3 dilim 4) — canlı kip.

func TestSubjectLiveCandidates(t *testing.T) {
	scfg, pcfg := subjCfgs()
	gate := func(ev chstore.AnomalyEvent) bool { return subjectPromotionGate(ev, scfg, pcfg, subjNow) }
	a := subjEv("POST /pay", "p99_ms", "up", 3.1, 20*time.Minute)
	db := subjEv("x", "error_rate", "up", 4, 20*time.Minute)
	db.Pattern, db.SubjectKind, db.SubjectID, db.Service, db.ID = "error_rate", chstore.AnomalySubjectDB, "db:postgresql@orders-db", "db:postgresql@orders-db", "dbfp"
	dbUnder := db
	dbUnder.ID, dbUnder.PeakRatio, dbUnder.Pattern = "dbfp2", 1.2, "p99_ms"
	dbDown := db
	dbDown.ID, dbDown.Direction = "dbfp3", "down"
	q := db
	q.ID, q.SubjectKind, q.Service = "qfp", chstore.AnomalySubjectQueue, "queue:kafka@orders-events"
	got := subjectLiveCandidates([]chstore.AnomalyEvent{a, db, dbUnder, dbDown, q}, map[string]bool{"qfp": true}, gate)
	if len(got) != 3 {
		t.Fatalf("aday sayısı %d, want 3 (endpoint grubu + db + kapı altı db): %+v", len(got), got)
	}
	if got[0].RuleID != "anomaly-auto:subject:orders-api:p99_ms:up" || len(got[0].Opening) != 1 {
		t.Errorf("endpoint adayı: %+v", got[0])
	}
	if got[1].RuleID != "anomaly-auto:dbfp" || len(got[1].Opening) != 1 {
		t.Errorf("db adayı mevcut yolun rule_id'si değil / açılış yok: %+v", got[1])
	}
	if got[2].RuleID != "anomaly-auto:dbfp2" || len(got[2].Opening) != 0 || len(got[2].Members) != 1 {
		t.Errorf("kapı altı db yalnız tazelenebilir olmalı: %+v", got[2])
	}
	// db Problem gövdesi: düz cümle, rota listesi yok.
	p := subjectGroupProblem(got[1], nil, scfg, pcfg, subjNow)
	if p.RuleName != "db:postgresql@orders-db: hata oranı arttı (4,0× taban, 20 dk)" || p.ID != "anomaly-auto:dbfp:db:postgresql@orders-db" {
		t.Errorf("db Problem: %q / %q", p.RuleName, p.ID)
	}
	if p.Description != "hata oranı: tabanın 4,0×." {
		t.Errorf("db detayı %q", p.Description)
	}
	if strings.Contains(p.Description, "rota") {
		t.Errorf("db detayı rota anıyor: %q", p.Description)
	}
}

func TestSubjectLiveMarks(t *testing.T) {
	a := subjEv("POST /pay", "p99_ms", "up", 3.1, 20*time.Minute)
	b := a
	b.ID, b.Promotion = "b", chstore.AnomalyPromotionLive
	c := a
	c.ID, c.Promotion = "c", chstore.AnomalyPromotionShadow // gölgeden canlıya geçiş
	got := subjectLiveMarks([]chstore.AnomalyEvent{a, b, c})
	if len(got) != 2 || got[0].ID != a.ID || got[1].ID != "c" || got[0].Promotion != "live" || got[1].Promotion != "live" {
		t.Errorf("canlı izler: %+v", got)
	}
}

func TestPromotedResolveReason(t *testing.T) {
	grp := "anomaly-auto:subject:orders-api:p99_ms:up"
	active := map[string]bool{"fpA": true, "fpM": true}
	muted := map[string]bool{"fpM": true}
	cases := []struct {
		rule   string
		groups map[string]bool
		reason string
		ok     bool
	}{
		{"anomaly-auto:fpA", nil, "", false},
		{"anomaly-auto:fpGone", nil, "anomaly cleared", true},
		{"anomaly-auto:fpM", nil, "anomaly muted", true},
		{grp, map[string]bool{grp: true}, "", false},
		{grp, map[string]bool{}, "anomaly cleared", true},
		{grp, nil, "", false}, // bilinmiyor → dokunma
	}
	for _, c := range cases {
		r, ok := promotedResolveReason(c.rule, active, muted, c.groups)
		if r != c.reason || ok != c.ok {
			t.Errorf("%s groups=%v: (%q,%v), want (%q,%v)", c.rule, c.groups, r, ok, c.reason, c.ok)
		}
	}
	// activeSubjectGroupRules: kapı altı ama kötüleşen üye grubu AKTİF tutar;
	// düşüş ve susturulan üye tutmaz.
	under := subjEv("GET /cart", "p99_ms", "up", 1.1, time.Minute)
	down := subjEv("GET /x", "error_rate", "down", 0.2, time.Minute)
	m := subjEv("GET /m", "p95_ms", "up", 9, time.Hour)
	g := activeSubjectGroupRules([]chstore.AnomalyEvent{under, down, m}, map[string]bool{m.ID: true})
	if len(g) != 1 || !g[grp] {
		t.Errorf("aktif gruplar %v, want yalnız %s", g, grp)
	}
}

// TestGenericPromotionSkipsSubjects — genel döngü özne olayına dokunmaz
// (tazelemesi özne gövdesini ezerdi); canlı açılış incident'a bağlanır +
// bildirir, tazeleme bildirmez.
func TestGenericPromotionSkipsSubjects(t *testing.T) {
	src := readEvaluatorSource(t)
	i := strings.Index(src, "for _, ev := range events {\n\t\t// v0.10.1800")
	if i < 0 || !strings.Contains(src[i:i+600], "if chstore.IsSubjectAnomaly(ev) {\n\t\t\tcontinue") {
		t.Error("genel terfi döngüsü özne olaylarını atlamıyor")
	}
	b, err := os.ReadFile("anomaly_subject_sweep.go")
	if err != nil {
		t.Fatal(err)
	}
	sw := string(b)
	j := strings.Index(sw, "func (e *Evaluator) promoteSubjectsLive(")
	if j < 0 {
		t.Fatal("promoteSubjectsLive yok")
	}
	body := sw[j:]
	cont := strings.Index(body, "if step != promoteCreate {\n\t\t\tcontinue")
	att := strings.Index(body, "e.store.AttachProblemToIncident(ctx, p)")
	ntf := strings.Index(body, "e.notifier.SendProblemAlert(context.Background(), p)")
	if cont < 0 || att < cont || ntf < cont || !strings.Contains(body, "pcfg.AttachesToIncident()") {
		t.Errorf("açılış kolu: bağlama/bildirim tazeleme kapısından sonra olmalı (cont=%d att=%d ntf=%d)", cont, att, ntf)
	}
}
