package evaluator

// v0.10.1799 — Davis FAZ 3 dilim 3: gölge izleri (subjectShadowMarks, saf)
// + süpürmenin promoteStrongAnomalies'e bağlanışı.

import (
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
	sw := strings.Index(body, "e.promoteSubjectAnomalies(ctx, cfg, muted, now)")
	rs := strings.Index(body, "e.resolveClearedAnomalyPromotions(ctx, muted)")
	if sw < 0 || rs < 0 || sw > rs {
		t.Errorf("özne süpürmesi çağrılmıyor ya da çözülmeden sonra (sw=%d rs=%d)", sw, rs)
	}
}
