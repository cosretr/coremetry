package chstore

// v0.10.1789 — Davis paritesi FAZ 1.1: anomaly_events'e yön / taban / özne
// kolonları (direction, baseline_value, baseline_kind, subject_kind,
// subject_id). Sözleşme beş yerde yaşar ve her biri burada pinli
// ([[feedback-fixes-have-second-halves]]):
//
//  1. DDL: alters dilimi, ADD COLUMN IF NOT EXISTS + DEFAULT; LC yalnız kapalı
//     küme kolonlarında, subject_id düz String (C2-C3).
//  2. Dağıtık güvenlik: anomaly_events highVolumeTables'da DEĞİL (Distributed
//     sarmalayıcı yok); adaptDDL ALTER'a ON CLUSTER enjekte eder, _local'a
//     çevirmez.
//  3. İki-boot: boot probe + ertelenen DDL sonrası reprobe; beşten az kolon
//     (yarım ALTER turu) bayrağı çevirmez.
//  4. Yazım: probe true iken INSERT beşliyi taşır, boş özne türü DEFAULT'a
//     ('service') eşitlenir; false iken liste bugünkü 10 değer.
//  5. Okuma: INSERT'te yazılan değer, okuma listesinde AYNI ADLA aynı Scan
//     hedefine döner (ada göre gidiş-dönüş — arite eşitliği yer değiştirmeyi
//     yakalamaz).

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

var anomalySubjectALTERs = []string{
	"ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS direction LowCardinality(String) DEFAULT ''",
	"ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS baseline_value Float64 DEFAULT 0",
	"ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS baseline_kind LowCardinality(String) DEFAULT ''",
	"ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS subject_kind LowCardinality(String) DEFAULT 'service'",
	"ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS subject_id String DEFAULT ''",
}

func TestAnomalySubjectColumnsMigration(t *testing.T) {
	alters := migrateDDLSlice(t, "alters")
	for _, w := range anomalySubjectALTERs {
		found := false
		for _, a := range alters {
			if a == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("alters diliminde yok: %q", w)
		}
		if mm := addColumnRe.FindStringSubmatch(w); mm == nil || mm[1] != "anomaly_events" {
			t.Errorf("addColumnRe eşleşmiyor (her boot yeniden gönderilir): %q", w)
		}
	}
	// C2-C3: subject_id serbest (rota / instance / kuyruk adı), LC DEĞİL.
	if strings.Contains(anomalySubjectALTERs[4], "LowCardinality") {
		t.Error("subject_id LowCardinality olmamalı — kardinalite ölçülmedi")
	}
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "anomaly_events")
	if !strings.Contains(ddl, "ORDER BY id") || !strings.Contains(ddl, "TTL toDate(started_at) + INTERVAL 30 DAY") {
		t.Errorf("anomaly_events ORDER BY / TTL değişmiş:\n%s", ddl)
	}
	// CREATE TABLE'a bilinçli eklenmedi: taze/yükseltilmiş kurulumda kolon sırası aynı.
	for _, c := range []string{"direction", "baseline_value", "baseline_kind", "subject_kind", "subject_id"} {
		if regexp.MustCompile(`\b` + c + `\b`).MatchString(ddl) {
			t.Errorf("CREATE TABLE anomaly_events %s anıyor — ALTER ile eklenmeli", c)
		}
	}
	src := mustReadSource(t, "store.go")
	if !strings.Contains(src, "sjOK, sjErr := s.probeAnomalySubjectCols(ctx)") ||
		!strings.Contains(src, "s.hasAnomalySubjectCols.Store(sjOK)") {
		t.Error("hasAnomalySubjectCols boot probe'u store.go migrate()'te yok")
	}
	defer_ := mustReadSource(t, "ddl_defer.go")
	if !strings.Contains(defer_, "subjectDone := s.reprobeAnomalySubjectCols(ctx)") ||
		!strings.Contains(defer_, "&& subjectDone &&") {
		t.Error("reprobePromotedAttrs FAZ 1 kolonlarını yeniden denemiyor — küme kipinde bayrak süreç ömrü boyunca false donar")
	}
}

// TestAnomalySubjectColumnsDistributedSafe — anomaly_events kendi state
// tablomuz: Distributed sarmalayıcısı yok, ALTER ON CLUSTER ile her replikaya
// iner. highVolumeTables'a girerse ALTER `_local`'a gider ve çıplak adlı
// sarmalayıcı yeni kolonları görmez (v0.8.185/186 sınıfı).
func TestAnomalySubjectColumnsDistributedSafe(t *testing.T) {
	if highVolumeTables["anomaly_events"] {
		t.Fatal("anomaly_events highVolumeTables'da — Distributed sarmalayıcı için ayrı kolon yolu gerekir")
	}
	s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	for _, w := range anomalySubjectALTERs {
		got := s.adaptDDL(w)
		if len(got) != 1 {
			t.Fatalf("%q → %d ifade, want 1", w, len(got))
		}
		if !strings.Contains(got[0], "anomaly_events ON CLUSTER `c`") || strings.Contains(got[0], "_local") {
			t.Errorf("ALTER ON CLUSTER'a çevrilmedi ya da _local'a gitti: %s", got[0])
		}
	}
}

// TestAnomalyEventSubjectRoundTrip — INSERT'in yazdığı değer okuma listesinde
// aynı kolon adıyla aynı alana döner (ada göre gidiş-dönüş).
func TestAnomalyEventSubjectRoundTrip(t *testing.T) {
	in := AnomalyEvent{
		ID: "fp", Kind: "red_p99", Pattern: "p99", Service: "checkout",
		StartedAt: 1_700_000_000e9, LastSeen: 1_700_000_060e9,
		PeakRatio: 3, CurrentRatio: 2.5, CurrentCount: 7, Sample: "s",
		EpisodeCount: 2, FirstStartedAt: 1_699_000_000e9, VerifiedRatio: 0.5,
		Direction: AnomalyDirectionDown, BaselineValue: 120.5, BaselineKind: AnomalyBaselineSeasonal,
		SubjectKind: AnomalySubjectEndpoint, SubjectID: "GET /api/cart/{id}",
	}
	for _, ep := range []bool{false, true} {
		for _, vr := range []bool{false, true} {
			for _, sj := range []bool{false, true} {
				cols := splitTopLevel(insertColumnList(t, anomalyInsertSQL(ep, vr, sj)))
				vals := anomalyInsertRow(in, ep, vr, sj)
				byName := map[string]any{}
				for i, c := range cols {
					v := vals[i]
					if tm, ok := v.(time.Time); ok {
						v = tm.UnixNano() // okuma toUnixTimestamp64Nano ile döner
					}
					byName[c] = v
				}
				var out AnomalyEvent
				sel := splitTopLevel(anomalyEventSelectExpr(ep, vr, sj))
				dst := anomalyEventScanDest(&out, ep, vr, sj)
				for i, expr := range sel {
					name := expr
					if m := regexp.MustCompile(`^toUnixTimestamp64Nano\((\w+)\)$`).FindStringSubmatch(expr); m != nil {
						name = m[1]
					}
					v, ok := byName[name]
					if !ok {
						t.Fatalf("ep=%v vr=%v sj=%v: okuma %q INSERT'te yok", ep, vr, sj, name)
					}
					assignScan(t, dst[i], v)
				}
				if sj {
					if out.Direction != in.Direction || out.BaselineValue != in.BaselineValue ||
						out.BaselineKind != in.BaselineKind || out.SubjectKind != in.SubjectKind || out.SubjectID != in.SubjectID {
						t.Errorf("ep=%v vr=%v: FAZ 1 alanları gidiş-dönüşte bozuldu: %+v", ep, vr, out)
					}
				} else if out.Direction != "" || out.BaselineValue != 0 || out.BaselineKind != "" || out.SubjectKind != "" || out.SubjectID != "" {
					t.Errorf("ep=%v vr=%v: probe false iken FAZ 1 alanları dolu: %+v", ep, vr, out)
				}
				if out.Service != in.Service || out.CurrentRatio != in.CurrentRatio || out.LastSeen != in.LastSeen {
					t.Errorf("ep=%v vr=%v sj=%v: taban alanlar bozuldu: %+v", ep, vr, sj, out)
				}
			}
		}
	}
	// Boş özne türü kolon DEFAULT'una ('service') eşitlenir; kolon listede
	// olduğu için CH DEFAULT'u devreye girmez.
	row := anomalyInsertRow(AnomalyEvent{}, false, false, true)
	if row[13] != AnomalySubjectService {
		t.Errorf("boş SubjectKind yazımda %v, want %q", row[13], AnomalySubjectService)
	}
	if row[10] != "" || row[12] != "" {
		t.Errorf("boş yön/taban türü '' yazılmalı (bilinmiyor): %v %v", row[10], row[12])
	}
}

func splitTopLevel(list string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(list[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(list[start:]))
}

func assignScan(t *testing.T, dst, v any) {
	t.Helper()
	switch p := dst.(type) {
	case *string:
		*p = v.(string)
	case *int64:
		*p = v.(int64)
	case *float64:
		*p = v.(float64)
	case *uint64:
		*p = v.(uint64)
	case *uint32:
		*p = v.(uint32)
	default:
		t.Fatalf("beklenmeyen Scan hedefi %T", dst)
	}
}

// TestUpsertAnomalyEventsWritesSubjectCols — yazım yolu probe'u okur: true
// iken INSERT satırı FAZ 1 beşlisini olaydan taşır, false iken bugünkü 10
// değer ve hiçbir sorgu kolonları anmaz.
func TestUpsertAnomalyEventsWritesSubjectCols(t *testing.T) {
	ev := AnomalyEvent{ID: "fp", Kind: "red_error_rate", StartedAt: 1_700_000_000e9, LastSeen: 1_700_000_000e9, CurrentRatio: 3,
		Direction: AnomalyDirectionUp, BaselineValue: 0.02, BaselineKind: AnomalyBaselineConsecutive,
		SubjectKind: AnomalySubjectDB, SubjectID: "orders-db"}
	t.Run("probe true", func(t *testing.T) {
		var qs []string
		var app [][]any
		st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app}}
		st.hasAnomalySubjectCols.Store(true)
		if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{ev}); err != nil {
			t.Fatal(err)
		}
		if len(app) != 1 || len(app[0]) != 15 {
			t.Fatalf("INSERT satırı = %v, want 15 değer", app)
		}
		want := []any{"up", 0.02, "consecutive", "db", "orders-db"}
		for i, w := range want {
			if app[0][10+i] != w {
				t.Errorf("değer %d = %v, want %v", 10+i, app[0][10+i], w)
			}
		}
	})
	t.Run("probe false", func(t *testing.T) {
		var qs []string
		var app [][]any
		st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app}}
		if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{ev}); err != nil {
			t.Fatal(err)
		}
		if len(app) != 1 || len(app[0]) != 10 {
			t.Fatalf("INSERT satırı = %v, want 10 değer", app)
		}
		for _, q := range qs {
			if strings.Contains(q, "subject_kind") || strings.Contains(q, "baseline_kind") {
				t.Errorf("probe false iken sorgu FAZ 1 kolonunu anıyor: %q", q)
			}
		}
	})
}

// TestReprobeAnomalySubjectCols — bayrak donmaz; beşten az kolon (yarım ALTER
// turu) bayrağı çevirmez.
func TestReprobeAnomalySubjectCols(t *testing.T) {
	var qs []string
	var app [][]any
	cols := uint64(4)
	st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app, colCount: &cols}}
	if st.reprobeAnomalySubjectCols(context.Background()) {
		t.Fatal("beş kolondan dördü varken bayrak true döndü")
	}
	cols = 5
	if !st.reprobeAnomalySubjectCols(context.Background()) {
		t.Fatal("kolonlar indikten sonra yeniden probe bayrağı çevirmedi")
	}
	ev := AnomalyEvent{ID: "fp", Kind: "log_pattern", StartedAt: 1_700_000_000e9, LastSeen: 1_700_000_000e9}
	if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{ev}); err != nil {
		t.Fatal(err)
	}
	if n := len(app[len(app)-1]); n != 15 {
		t.Fatalf("reprobe sonrası INSERT %d değer, want 15", n)
	}
}
