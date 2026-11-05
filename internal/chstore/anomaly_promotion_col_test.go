package chstore

// v0.10.1797 — Davis paritesi FAZ 3 dilim 1: anomaly_events.promotion terfi
// izi kolonu ('' | shadow | live). FAZ 1 beşlisinin sözleşmesi
// (anomaly_subject_cols_test.go) burada tek kolon için pinli: DDL, dağıtık
// güvenlik, iki-boot probe/reprobe, yazım/okuma gidiş-dönüşü ve — beşliden
// FARKLI olarak — bölüm içi taşıma (dedektörün işaretsiz tiki evaluator'ün
// gölge işaretini silmez).

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

const anomalyPromotionALTER = "ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS promotion LowCardinality(String) DEFAULT ''"

func TestAnomalyPromotionColumnMigration(t *testing.T) {
	found := false
	for _, a := range migrateDDLSlice(t, "alters") {
		if a == anomalyPromotionALTER {
			found = true
		}
	}
	if !found {
		t.Fatalf("alters diliminde yok: %q", anomalyPromotionALTER)
	}
	if mm := addColumnRe.FindStringSubmatch(anomalyPromotionALTER); mm == nil || mm[1] != "anomaly_events" {
		t.Errorf("addColumnRe eşleşmiyor: %q", anomalyPromotionALTER)
	}
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "anomaly_events")
	if regexp.MustCompile(`\bpromotion\b`).MatchString(ddl) {
		t.Error("CREATE TABLE anomaly_events promotion anıyor — ALTER ile eklenmeli (kolon sırası)")
	}
	src := mustReadSource(t, "store.go")
	if !strings.Contains(src, "prOK, prErr := s.probeAnomalyPromotionCol(ctx)") ||
		!strings.Contains(src, "s.hasAnomalyPromotionCol.Store(prOK)") {
		t.Error("hasAnomalyPromotionCol boot probe'u migrate()'te yok")
	}
	defer_ := mustReadSource(t, "ddl_defer.go")
	if !strings.Contains(defer_, "promotionDone := s.reprobeAnomalyPromotionCol(ctx)") ||
		!strings.Contains(defer_, "&& promotionDone &&") {
		t.Error("reprobePromotedAttrs promotion kolonunu yeniden denemiyor — küme kipinde bayrak donar")
	}
	s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	got := s.adaptDDL(anomalyPromotionALTER)
	if len(got) != 1 || !strings.Contains(got[0], "anomaly_events ON CLUSTER `c`") || strings.Contains(got[0], "_local") {
		t.Errorf("ALTER ON CLUSTER'a çevrilmedi ya da _local'a gitti: %v", got)
	}
}

// TestMergeAnomalyCarryPromotion — terfi izinin üç dalı.
func TestMergeAnomalyCarryPromotion(t *testing.T) {
	const s = int64(time.Second)
	t0 := int64(1_700_000_000) * s
	stored := AnomalyEvent{StartedAt: t0, LastSeen: t0 + 60*s, Promotion: AnomalyPromotionShadow}
	cases := []struct {
		name     string
		in       AnomalyEvent
		exists   bool
		wantProm string
	}{
		{"ilk görülme, işaretsiz", AnomalyEvent{LastSeen: t0}, false, ""},
		{"aynı bölüm, dedektör tiki işaretsiz → korunur", AnomalyEvent{LastSeen: t0 + 120*s}, true, AnomalyPromotionShadow},
		{"aynı bölüm, evaluator canlı işareti → yazılır", AnomalyEvent{LastSeen: t0 + 120*s, Promotion: AnomalyPromotionLive}, true, AnomalyPromotionLive},
		{"yeni bölüm → sıfırdan", AnomalyEvent{LastSeen: t0 + 60*s + int64(anomalyEpisodeGap) + s}, true, ""},
	}
	for _, c := range cases {
		if got := MergeAnomalyCarry(c.in, stored, c.exists).Promotion; got != c.wantProm {
			t.Errorf("%s: promotion=%q, want %q", c.name, got, c.wantProm)
		}
	}
}

// TestUpsertAnomalyEventsCarriesPromotion — probe true: saklı "shadow" aynı
// bölümdeki işaretsiz yazımda INSERT'e taşınır; probe false: hiçbir sorgu
// kolonu anmaz.
func TestUpsertAnomalyEventsCarriesPromotion(t *testing.T) {
	const s = int64(time.Second)
	t0 := int64(1_700_000_000) * s
	incoming := AnomalyEvent{ID: "fp", Kind: SubjectAnomalyKind, StartedAt: t0 + 120*s, LastSeen: t0 + 120*s, CurrentRatio: 3}
	t.Run("probe true", func(t *testing.T) {
		var qs []string
		var app [][]any
		st := &Store{conn: episodeFakeConn{stored: [][]any{{"fp", t0, t0 + 60*s, 4.0, AnomalyPromotionShadow}}, queries: &qs, appended: &app}}
		st.hasAnomalyPromotionCol.Store(true)
		if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{incoming}); err != nil {
			t.Fatal(err)
		}
		if len(app) != 1 || len(app[0]) != 11 || app[0][10] != AnomalyPromotionShadow {
			t.Fatalf("INSERT satırı = %v, want 11 değer ve sonuncusu shadow", app)
		}
	})
	t.Run("probe false", func(t *testing.T) {
		var qs []string
		var app [][]any
		st := &Store{conn: episodeFakeConn{stored: [][]any{{"fp", t0, t0 + 60*s, 4.0}}, queries: &qs, appended: &app}}
		if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{incoming}); err != nil {
			t.Fatal(err)
		}
		if len(app) != 1 || len(app[0]) != 10 {
			t.Fatalf("INSERT satırı = %v, want 10 değer", app)
		}
		for _, q := range qs {
			if strings.Contains(q, "promotion") {
				t.Errorf("probe false iken sorgu promotion anıyor: %q", q)
			}
		}
	})
}

// TestReprobeAnomalyPromotionCol — bayrak donmaz; kolon inince çevrilir.
func TestReprobeAnomalyPromotionCol(t *testing.T) {
	var qs []string
	var app [][]any
	cols := uint64(0)
	st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app, colCount: &cols}}
	if st.reprobeAnomalyPromotionCol(context.Background()) {
		t.Fatal("kolon yokken bayrak true döndü")
	}
	cols = 1
	if !st.reprobeAnomalyPromotionCol(context.Background()) {
		t.Fatal("kolon indikten sonra yeniden probe bayrağı çevirmedi")
	}
}
