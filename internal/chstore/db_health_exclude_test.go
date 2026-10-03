package chstore

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// v0.10.1084 — db-health hariç sistemler (operatör: "%100 hata oranı gerçek
// değil"). Couchbase SDK'sı KV "bulunamadı" cevabını span'de ERROR işaretliyor;
// önbellek deseninde her ıska hata sayılıyor ve veritabanı %100 hata oranıyla
// P1 açıyordu. Bu dosya ana okumanın hariç sistemi SQL'de GERÇEKTEN düşürdüğünü
// canlı motorla ölçer (`clickhouse local`; ikili yoksa atlanır — şekil/golden
// db_health_test.go'da her ortamda koşar): aynı fikstürde couchbase %100 hata
// satırları varsayılan ayarla HİÇ gelmez, boş listeyle gelir; oracle satırı iki
// durumda da gelir. Go yüklemi (SystemExcluded) SQL ikiziyle aynı kümeyi seçer.

// dbHealthFixtureSQL — db_caller_summary_5m'in durum kolonlarıyla AYNI tipte
// bellek tablosu + iki 5 dk kova × iki veritabanı × iki çağıran.
func dbHealthFixtureSQL(b1, b2 time.Time) string {
	type row struct {
		sys, inst, db, svc string
		bucket             time.Time
		calls, errs        int
	}
	var rows []row
	for _, b := range []time.Time{b1, b2} {
		for _, svc := range []string{"svc-a", "svc-b"} {
			// Couchbase: her çağrı "hata" (KV get ıskası) — %100.
			rows = append(rows, row{"Couchbase", "cb-node-1", "orders", svc, b, 200, 200})
			// Oracle: gerçek bir olay — %10.
			rows = append(rows, row{"oracle", "db-host-01", "crm-db", svc, b, 100, 10})
		}
	}
	var ins []string
	for _, r := range rows {
		// Her satır kendi çağrı sayısı kadar span'den durum üretir (numbers()).
		ins = append(ins, fmt.Sprintf(`INSERT INTO db_caller_summary_5m
			SELECT %s, %s, %s, %s, '(unknown)', toDateTime('%s', 'UTC'),
			       countState(), countIfState(number < %d), quantilesTDigestState(0.5, 0.95, 0.99)(toInt64(1000000))
			FROM numbers(%d)`,
			chQuote(r.sys), chQuote(r.inst), chQuote(r.db), chQuote(r.svc),
			r.bucket.UTC().Format("2006-01-02 15:04:05"), r.errs, r.calls))
	}
	return `CREATE TABLE db_caller_summary_5m (
			db_system String, instance String, db_name String, service_name String, host_name String,
			time_bucket DateTime('UTC'),
			span_count_state AggregateFunction(count),
			error_count_state AggregateFunction(countIf, UInt8),
			duration_q_state AggregateFunction(quantilesTDigest(0.5, 0.95, 0.99), Int64)
		) ENGINE = Memory;
		` + strings.Join(ins, ";\n") + ";\n"
}

// inlineDBHealthArgs — `?` yer tutucularını sırayla CH değişmezleriyle doldurur
// (sürücünün istemci tarafı bind'inin karşılığı: dizi → ['a', 'b'], zaman →
// toDateTime).
func inlineDBHealthArgs(t *testing.T, q string, args []any) string {
	t.Helper()
	var b strings.Builder
	i := 0
	for _, r := range q {
		if r != '?' {
			b.WriteRune(r)
			continue
		}
		if i >= len(args) {
			t.Fatalf("argüman eksik: %q", q)
		}
		switch v := args[i].(type) {
		case string:
			b.WriteString(chQuote(v))
		case []string:
			parts := make([]string, len(v))
			for j, s := range v {
				parts[j] = chQuote(s)
			}
			b.WriteString("[" + strings.Join(parts, ", ") + "]")
		case time.Time:
			b.WriteString("toDateTime('" + v.UTC().Format("2006-01-02 15:04:05") + "', 'UTC')")
		default:
			b.WriteString(fmt.Sprint(v))
		}
		i++
	}
	if i != len(args) {
		t.Fatalf("fazla argüman: %d/%d", i, len(args))
	}
	return b.String()
}

func TestDBHealthExcludeSystemsLiveEngine(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — canlı motor karşılaştırması atlandı (golden db_health_test.go'da)")
	}
	cur := time.Date(2026, 10, 3, 9, 45, 0, 0, time.UTC)
	b2, b1 := cur.Add(-10*time.Minute), cur.Add(-5*time.Minute)
	fixture := dbHealthFixtureSQL(b2, b1)
	sens := AnomalySensitivityConfig{BatchServicePatterns: ptrStrings([]string{})}

	systemsFor := func(cfg DBHealthConfig) map[string]int {
		q, args := dbHealthBucketsQuery(b2, cur.Add(5*time.Minute), cfg, sens, nil)
		sel := strings.Replace(inlineDBHealthArgs(t, q, args), "SETTINGS max_execution_time = 10", "FORMAT TSVRaw SETTINGS max_execution_time = 10", 1)
		out, err := exec.Command(bin, "local", "--multiquery", "--query", fixture+sel).CombinedOutput()
		if err != nil {
			t.Fatalf("clickhouse local: %v\n%s", err, out)
		}
		got := map[string]int{}
		for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if ln == "" {
				continue
			}
			got[strings.Split(ln, "\t")[0]]++
		}
		return got
	}

	// Varsayılan (["couchbase"]): %100 hatalı couchbase satırı HİÇ gelmez —
	// büyük harfli "Couchbase" da (kural id'siyle aynı katlama).
	def := systemsFor(DefaultDBHealth())
	if def["Couchbase"] != 0 {
		t.Errorf("varsayılan ayarda couchbase satırı geldi: %v", def)
	}
	if def["oracle"] != 2 {
		t.Errorf("oracle (gerçek olay) iki kovada da gelmeli: %v", def)
	}
	// Boş liste: bugünkü davranış — couchbase da gelir (koşul yok).
	none := DefaultDBHealth()
	none.ExcludeSystems = ptrStrings([]string{})
	if all := systemsFor(none); all["Couchbase"] != 2 || all["oracle"] != 2 {
		t.Errorf("boş hariç listesinde iki sistem de gelmeli: %v", all)
	}
	// Go yüklemi SQL ikiziyle aynı kümeyi seçer.
	for sys, want := range map[string]bool{"Couchbase": true, "oracle": false} {
		if got := DefaultDBHealth().SystemExcluded(sys); got != want {
			t.Errorf("SystemExcluded(%q) = %v, SQL %v diyor", sys, got, want)
		}
	}
}
