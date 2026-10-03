// anomaly_episode_test.go — v0.10.1049: yinelenen anomali ayrımı.
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor. 'Yeni mi,
// yinelenen mi' ayrımı için küçük bir şema eki gerekir."
//
// v0.10.1045 yeni bölümde HİÇBİR şey taşımıyordu (started_at ve tepe gelen
// olaydan); "bu satır daha önce de ateşledi mi" cevabı satırda yoktu ve deploy
// atfı her yeniden tetiklenmeyi ondan önceki deploy'a yazıyordu. İki kolon
// (episode_count, first_started_at) aynı üç dalda taşınır. Bu dosya:
//
//   - taşıma tablosu + gece işi dizisi (MergeAnomalyCarry);
//   - deploy atfının tek kuralı (AnomalyPredatesDeploy — "düzenli yinelenme":
//     ≥ 3 bölüm VE ortalama aralık ≤ 48 sa) ve çip seçimi (pickAnomalyDeploy).
//     İnceleme düzeltmesi: ilk taslak "herhangi bir eski bölüm"ü yeterli
//     sayıyordu; 30 günlük satır ömründe sık görülen operasyonların neredeyse
//     hepsinin ilk görülmesi her yeni deploy'dan önce olduğu için GERÇEK bir
//     deploy gerilemesini gizliyordu. Vaka A (deploy kırdı → rollback → bozuk
//     yeniden deploy, 2. bölüm) ve vaka B (20 gün önce tek kıpırtı, bugün
//     gerçek gerileme) atfı KORUR;
//   - şema: alters dilimindeki iki ADD COLUMN + DEFAULT'ları; INSERT/SELECT
//     kolon listeleriyle değer/hedef listelerinin aritesi, probe'un iki
//     hâlinde de (rolling deploy'da code 16 / "no such column" sınıfı);
//   - probe donmaz: false → yeniden probe (ertelenen DDL sonrası) true →
//     bir sonraki çağrı kolonları yazar;
//   - uçtan uca yazım: sahte bağlantıyla taşıma okuması → INSERT satırı.
package chstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// TestMergeAnomalyCarryEpisodeCount — sayaç + ilk başlangıç tablosu.
//
// Mutasyon kontrolü: YENİ BÖLÜM dalında `+ 1` silinirse "yeni bölüm" satırları
// 2 yerine 1 döndürür; first_started_at taşınmazsa (gelen started_at yazılırsa)
// "ilk başlangıç korunur" satırları kırılır.
func TestMergeAnomalyCarryEpisodeCount(t *testing.T) {
	const s = int64(time.Second)
	t0 := int64(1_700_000_000) * s
	gap := int64(anomalyEpisodeGap)

	cases := []struct {
		name      string
		stored    AnomalyEvent
		exists    bool
		incGap    int64 // gelen last_seen − saklı last_seen
		wantCount uint32
		wantFirst int64
		wantStart int64 // 0 → gelen olayın started_at'i
	}{
		{
			name:      "ilk görülme — sayaç 1, ilk = gelen started_at",
			exists:    false,
			wantCount: 1,
		},
		{
			name:      "aynı bölüm — sayaç ve ilk değişmeden taşınır",
			stored:    AnomalyEvent{StartedAt: t0, LastSeen: t0 + 600*s, EpisodeCount: 3, FirstStartedAt: t0 - 3*24*3600*s},
			exists:    true,
			incGap:    60 * s,
			wantCount: 3, wantFirst: t0 - 3*24*3600*s, wantStart: t0,
		},
		{
			name:      "yeni bölüm — sayaç artar, ilk başlangıç korunur",
			stored:    AnomalyEvent{StartedAt: t0, LastSeen: t0 + 600*s, EpisodeCount: 2, FirstStartedAt: t0 - 24*3600*s},
			exists:    true,
			incGap:    24 * 3600 * s,
			wantCount: 3, wantFirst: t0 - 24*3600*s,
		},
		{
			name:      "yeni bölüm, ilk başlangıç bilinmiyor (eski satır, 0) — saklı started_at",
			stored:    AnomalyEvent{StartedAt: t0, LastSeen: t0 + 600*s, EpisodeCount: 1, FirstStartedAt: 0},
			exists:    true,
			incGap:    24 * 3600 * s,
			wantCount: 2, wantFirst: t0,
		},
		{
			// Probe'suz okuma (kolon yok) sayaç 0 bırakır; 1 sayılır.
			name:      "yeni bölüm, saklı sayaç 0 (okunmamış) — 2",
			stored:    AnomalyEvent{StartedAt: t0, LastSeen: t0 + 600*s},
			exists:    true,
			incGap:    24 * 3600 * s,
			wantCount: 2, wantFirst: t0,
		},
		{
			name:      "aynı bölüm, saklı sayaç 0 — 1 (asla 0 yazılmaz)",
			stored:    AnomalyEvent{StartedAt: t0, LastSeen: t0 + 600*s},
			exists:    true,
			incGap:    60 * s,
			wantCount: 1, wantFirst: 0, wantStart: t0,
		},
		{
			// Sırası bozuk yazım bölüm açmaz → sayaç da artmaz.
			name:      "gelen olay saklıdan ESKİ (2 × bölüm boşluğu) — artmaz",
			stored:    AnomalyEvent{StartedAt: t0, LastSeen: t0 + 600*s, EpisodeCount: 4, FirstStartedAt: t0 - 9*24*3600*s},
			exists:    true,
			incGap:    -2 * gap,
			wantCount: 4, wantFirst: t0 - 9*24*3600*s, wantStart: t0,
		},
		{
			name:      "tam bölüm boşluğu — aynı bölüm, artmaz",
			stored:    AnomalyEvent{StartedAt: t0, LastSeen: t0 + 600*s, EpisodeCount: 2, FirstStartedAt: t0 - 24*3600*s},
			exists:    true,
			incGap:    gap,
			wantCount: 2, wantFirst: t0 - 24*3600*s, wantStart: t0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			incLast := c.stored.LastSeen + c.incGap
			if !c.exists {
				incLast = t0 + 5*24*3600*s
			}
			incStart := incLast - 30*s
			in := AnomalyEvent{ID: "fp", StartedAt: incStart, LastSeen: incLast, CurrentRatio: 3}
			got := MergeAnomalyCarry(in, c.stored, c.exists)

			wantFirst := c.wantFirst
			if !c.exists {
				wantFirst = incStart
			}
			wantStart := c.wantStart
			if wantStart == 0 {
				wantStart = incStart
			}
			if got.EpisodeCount != c.wantCount {
				t.Errorf("episode_count = %d, want %d", got.EpisodeCount, c.wantCount)
			}
			if got.FirstStartedAt != wantFirst {
				t.Errorf("first_started_at = %d, want %d", got.FirstStartedAt, wantFirst)
			}
			if got.StartedAt != wantStart {
				t.Errorf("started_at = %d, want %d (bölüm kuralı v0.10.1045 aynen)", got.StartedAt, wantStart)
			}
		})
	}
}

// TestMergeAnomalyCarryNightlyRecurrence — operatörün tarif ettiği şekil:
// her gece 02:00'de ~20 dk ateşleyen iş. Kayıtçının 60 sn'lik yazımları,
// her yazım bir öncekinin YAZDIĞI satırı saklı satır olarak görür. Üçüncü
// gecenin sonunda sayaç 3, ilk başlangıç birinci gecenin başlangıcı; started_at
// (v0.10.1045) üçüncü gecenin başlangıcı.
func TestMergeAnomalyCarryNightlyRecurrence(t *testing.T) {
	const s = int64(time.Second)
	night := func(n int64) int64 { return int64(1_700_000_000)*s + n*24*3600*s }
	var stored AnomalyEvent
	exists := false
	for n := int64(0); n < 3; n++ {
		for m := int64(0); m < 20; m++ { // 20 dakika, dakikada bir yazım
			at := night(n) + m*60*s
			in := AnomalyEvent{ID: "fp", StartedAt: at, LastSeen: at, CurrentRatio: 4}
			stored, exists = MergeAnomalyCarry(in, stored, exists), true
		}
		if stored.EpisodeCount != uint32(n+1) {
			t.Fatalf("gece %d sonunda episode_count = %d, want %d", n+1, stored.EpisodeCount, n+1)
		}
		if stored.FirstStartedAt != night(0) {
			t.Fatalf("gece %d: first_started_at = %d, want birinci gece %d", n+1, stored.FirstStartedAt, night(0))
		}
		if stored.StartedAt != night(n) {
			t.Fatalf("gece %d: started_at = %d, want bu gecenin başlangıcı %d", n+1, stored.StartedAt, night(n))
		}
	}
}

// TestAnomalyPredatesDeploy — deploy atfının tek kuralı ("düzenli yinelenme").
//
// Mutasyon kontrolleri: (1) kural "herhangi bir eski bölüm"e geri genişletilirse
// (earliest < deploy → true) vaka A ve B satırları true döner ve kırılır;
// (2) firstStartedAt yok sayılırsa (v0.10.1045 davranışı) gece işi satırları
// false döner ve kırılır.
func TestAnomalyPredatesDeploy(t *testing.T) {
	const (
		m = int64(time.Minute)
		h = int64(time.Hour)
		d = 24 * h
	)
	deploy := int64(1_700_000_000) * int64(time.Second)
	cases := []struct {
		name           string
		first, started int64
		count          uint32
		want           bool
	}{
		// Tek bölüm — bugünkü `StartedAt >= since` kuralı birebir.
		{"tek bölüm, deploy'dan sonra başladı", 0, deploy + 10*m, 1, false},
		{"tek bölüm, deploy'dan önce başladı (started < deploy)", 0, deploy - 10*m, 1, true},
		{"tek bölüm, tam deploy anında (kapsayıcı sınır)", 0, deploy, 1, false},
		{"tek bölüm, kolonlu satır (first == started)", deploy + 10*m, deploy + 10*m, 1, false},
		// started < deploy her zaman true — sayaçtan bağımsız.
		{"bu bölüm deploy'dan önce başladı, sayaç 2", deploy - 3*d, deploy - 10*m, 2, true},
		// Vaka A: deploy kırdı → 1. bölüm; rollback; "düzeltme" yeniden deploy
		// hâlâ bozuk → 2. bölüm 5 dk sonra. Atıf KORUNUR.
		{"vaka A: sayaç 2, ilk bölüm 1 sa önce", deploy - h, deploy + 5*m, 2, false},
		// Vaka B: 20 gün önce tek kıpırtı, bugün gerçek gerileme. Atıf KORUNUR.
		{"vaka B: sayaç 2, ilk görülme 20 gün önce", deploy - 20*d, deploy + 5*m, 2, false},
		// Gece işi: 3. gece (ortalama 24 sa) → deploy'dan önce de görülüyordu.
		{"gece işi, 3. gece (ortalama 24 sa)", deploy - 2*d + 30*m, deploy + 30*m, 3, true},
		{"gece işi, 2. gece — henüz düzenli değil", deploy - d + 30*m, deploy + 30*m, 2, false},
		// Seyrek: 5 bölüm, ortalama 20 gün → düzenli DEĞİL.
		{"sayaç 5, ortalama aralık 20 gün", deploy - 80*d + 5*m, deploy + 5*m, 5, false},
		// Ortalama aralık sınırı: tam 48 sa düzenli, 1 ns fazlası değil.
		{"sayaç 3, ortalama tam 48 sa (kapsayıcı)", deploy + m - 96*h, deploy + m, 3, true},
		{"sayaç 3, ortalama 48 sa + 1 ns", deploy + m - 96*h - 2, deploy + m, 3, false},
		// İlk görülme bilinmiyor (0) → started: deploy'dan sonra → false.
		{"first 0, sayaç 9 — ilk görülme bilinmiyor, started deploy'dan sonra", 0, deploy + 5*m, 9, false},
		{"negatif first bilinmiyor sayılır", -1, deploy + 5*m, 9, false},
		// İlk görülme de deploy'dan sonra → false.
		{"yinelenen ama ilk görülme deploy'dan sonra", deploy + 5*m, deploy + 2*d, 3, false},
		{"yinelenen, ilk görülme tam deploy anında", deploy, deploy + 2*d, 3, false},
		// Bozuk satır: first > started — küçüğü (started) alınır.
		{"bozuk satır: first > started, started < deploy", deploy + 50*m, deploy - 10*m, 1, true},
	}
	for _, c := range cases {
		if got := AnomalyPredatesDeploy(c.first, c.started, c.count, deploy); got != c.want {
			t.Errorf("%s: AnomalyPredatesDeploy(first=%d, started=%d, count=%d, deploy=%d) = %v, want %v",
				c.name, c.first, c.started, c.count, deploy, got, c.want)
		}
	}
}

// TestPickAnomalyDeploy — EnrichAnomaliesWithDeploys'un seçim yarısı (satır
// çipi, detay deploy kutusu, kök-neden ucu buradan okur). Yalnız DÜZENLİ
// yinelenen olaya (gece işinin 3. gecesi) çip iliştirilmez; vaka A / B ve
// yeni olayda seçim bugünkü gibi.
func TestPickAnomalyDeploy(t *testing.T) {
	const m = int64(time.Minute)
	const day = 24 * 60 * m
	start := int64(1_700_000_000) * int64(time.Second)
	lookback := 30 * m
	list := []spanDeploy{ // ARTAN
		{version: "v1", ns: start - 40*m}, // pencere dışı
		{version: "v2", ns: start - 25*m},
		{version: "v3", ns: start - 5*m},
		{version: "v4", ns: start + 2*m}, // başlangıçtan sonra
	}
	cases := []struct {
		name  string
		ev    AnomalyEvent
		wantV string // "" = çip yok
	}{
		{"yeni olay (first 0) — en son pencere içi deploy", AnomalyEvent{StartedAt: start}, "v3"},
		{"yeni olay (first == started) — aynı", AnomalyEvent{StartedAt: start, FirstStartedAt: start, EpisodeCount: 1}, "v3"},
		{"vaka A: 2. bölüm, ilk bölüm 1 sa önce — çip KALIR", AnomalyEvent{StartedAt: start, FirstStartedAt: start - 60*m, EpisodeCount: 2}, "v3"},
		{"vaka B: 2. bölüm, ilk görülme 20 gün önce — çip KALIR", AnomalyEvent{StartedAt: start, FirstStartedAt: start - 20*day, EpisodeCount: 2}, "v3"},
		{"sayaç 5, ortalama 20 gün — çip KALIR", AnomalyEvent{StartedAt: start, FirstStartedAt: start - 80*day, EpisodeCount: 5}, "v3"},
		{"gece işi 3. gece (ortalama 24 sa) — çip YOK", AnomalyEvent{StartedAt: start, FirstStartedAt: start - 2*day, EpisodeCount: 3}, ""},
		// Pencerede ilk görülmeden ÖNCEKİ bir deploy (v2) varsa o seçilir — o
		// deploy anomalinin ilk görülmesini açıklayabilir. (Saf yürüyüş testi:
		// 24 dk içinde 3 bölüm gerçekte olmaz, kural ortalamaya bakar.)
		{"düzenli, ilk görülme v2 ile v3 arasında — v2", AnomalyEvent{StartedAt: start, FirstStartedAt: start - 24*m, EpisodeCount: 3}, "v2"},
	}
	for _, c := range cases {
		got, _ := pickAnomalyDeploy(list, c.ev, lookback) // v0.10.1054: ikinci değer bastırılan deploy (promoted_anomaly_source_test.go)
		gotV := ""
		if got != nil {
			gotV = got.version
		}
		if gotV != c.wantV {
			t.Errorf("%s: seçilen deploy %q, want %q", c.name, gotV, c.wantV)
		}
	}
	// Zenginleştirme seçimi bu işlevden yapıyor (elle döngü geri gelmesin).
	if src := mustReadSource(t, "problem_telemetry.go"); !strings.Contains(src, "pickAnomalyDeploy(list, events[i], lookbackNs)") {
		t.Error("EnrichAnomaliesWithDeploys pickAnomalyDeploy'u çağırmıyor — çip yinelenen olayı deploy'a bağlar")
	}
}

// TestAnomalyEpisodeColumnsMigration — iki kolon `alters` diliminde, ADD
// COLUMN IF NOT EXISTS + DEFAULT ile (düşük hacimli state tablosu sınıfı,
// /clickhouse-schema §3). DEFAULT'lar "bilinmiyor = bugünkü davranış": eski
// satır 1. bölüm ve ilk görülme 0.
func TestAnomalyEpisodeColumnsMigration(t *testing.T) {
	want := []string{
		"ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS episode_count UInt32 DEFAULT 1",
		"ALTER TABLE anomaly_events ADD COLUMN IF NOT EXISTS first_started_at DateTime64(9) DEFAULT toDateTime64(0, 9)",
	}
	alters := migrateDDLSlice(t, "alters")
	for _, w := range want {
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
		// planDDL'in kolon elemesi bu kalıbı tanımalı (yoksa her boot gönderilir).
		if mm := addColumnRe.FindStringSubmatch(w); mm == nil || mm[1] != "anomaly_events" {
			t.Errorf("addColumnRe eşleşmiyor: %q", w)
		}
	}
	// ORDER BY / TTL değişmedi: dedup anahtarı id, TTL son bölümün started_at'i.
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "anomaly_events")
	if !strings.Contains(ddl, "ORDER BY id") || !strings.Contains(ddl, "TTL toDate(started_at) + INTERVAL 30 DAY") {
		t.Errorf("anomaly_events ORDER BY / TTL değişmiş:\n%s", ddl)
	}
	// Boot probe'u bayrağı kurar; ertelenen DDL sonrası yeniden probe da var.
	src := mustReadSource(t, "store.go")
	if !strings.Contains(src, "aeOK, aeErr := s.probeAnomalyEpisodeCols(ctx)") ||
		!strings.Contains(src, "s.hasAnomalyEpisodeCols.Store(aeOK)") {
		t.Error("hasAnomalyEpisodeCols boot probe'u store.go migrate()'te yok")
	}
	if !strings.Contains(mustReadSource(t, "ddl_defer.go"), "s.reprobeAnomalyEpisodeCols(ctx)") {
		t.Error("reprobePromotedAttrs bölüm kolonlarını yeniden denemiyor — küme kipinde bayrak süreç ömrü boyunca false donar")
	}
}

// sqlColumnCount — virgülle ayrılmış SELECT / INSERT listesinin üst düzey
// eleman sayısı (parantez içindeki virgüller sayılmaz).
func sqlColumnCount(list string) int {
	depth, n := 0, 1
	for _, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				n++
			}
		}
	}
	return n
}

// insertColumnList — "INSERT INTO t (a, b)" → "a, b".
func insertColumnList(t *testing.T, q string) string {
	t.Helper()
	i, j := strings.Index(q, "("), strings.LastIndex(q, ")")
	if i < 0 || j < i {
		t.Fatalf("INSERT kolon listesi bulunamadı: %q", q)
	}
	return q[i+1 : j]
}

// selectColumnList — "SELECT a, b FROM …" → "a, b".
func selectColumnList(t *testing.T, q string) string {
	t.Helper()
	i, j := strings.Index(q, "SELECT "), strings.Index(q, "FROM ")
	if i < 0 || j < i {
		t.Fatalf("SELECT listesi bulunamadı: %q", q)
	}
	return q[i+len("SELECT ") : j]
}

// TestAnomalyEventColumnArity — INSERT/SELECT kolon listesi ile değer/hedef
// listesinin aritesi, probe'un İKİ hâlinde de aynı. Rolling deploy'da kolon
// henüz inmemişken (probe false) hiçbir liste yeni kolonu anmaz — anarsa her
// yazım code 16, her okuma "no such column" ile düşerdi.
func TestAnomalyEventColumnArity(t *testing.T) {
	// v0.10.1080 — ikinci probe (verified_ratio): dört kombinasyon.
	for _, ep := range []bool{false, true} {
		for _, vr := range []bool{false, true} {
			ins := anomalyInsertSQL(ep, vr)
			if got, want := sqlColumnCount(insertColumnList(t, ins)), len(anomalyInsertRow(AnomalyEvent{}, ep, vr)); got != want {
				t.Errorf("ep=%v vr=%v: INSERT %d kolon, satır %d değer", ep, vr, got, want)
			}
			var e AnomalyEvent
			if got, want := sqlColumnCount(anomalyEventSelectExpr(ep, vr)), len(anomalyEventScanDest(&e, ep, vr)); got != want {
				t.Errorf("ep=%v vr=%v: okuma listesi %d kolon, Scan %d hedef", ep, vr, got, want)
			}
			var id string
			carry := anomalyCarrySelectSQL(3, ep)
			if got, want := sqlColumnCount(selectColumnList(t, carry)), len(anomalyCarryScanDest(&id, &e, ep)); got != want {
				t.Errorf("ep=%v: taşıma okuması %d kolon, Scan %d hedef", ep, got, want)
			}
			for _, q := range []string{ins, anomalyEventSelectExpr(ep, vr), carry} {
				has := strings.Contains(q, "episode_count") || strings.Contains(q, "first_started_at")
				if has != ep {
					t.Errorf("ep=%v ama liste bölüm kolonu anıyor=%v: %q", ep, has, q)
				}
			}
			// Oran taşınmaz (her yazım son tikin oranı): taşıma okuması onu
			// HİÇ anmaz; INSERT/okuma yalnız probe true iken.
			for _, q := range []string{ins, anomalyEventSelectExpr(ep, vr)} {
				if has := strings.Contains(q, "verified_ratio"); has != vr {
					t.Errorf("vr=%v ama liste verified_ratio anıyor=%v: %q", vr, has, q)
				}
			}
			if strings.Contains(carry, "verified_ratio") {
				t.Errorf("taşıma okuması verified_ratio anmamalı: %q", carry)
			}
		}
	}
	// Probe false hâli bugünkü (v0.10.1045) listelerin birebir aynısı: 10 + 10 + 4.
	var e AnomalyEvent
	var id string
	if n := len(anomalyInsertRow(AnomalyEvent{}, false, false)); n != 10 {
		t.Errorf("probe false INSERT %d değer, want 10", n)
	}
	if n := len(anomalyEventScanDest(&e, false, false)); n != 10 {
		t.Errorf("probe false okuma %d hedef, want 10", n)
	}
	if n := len(anomalyCarryScanDest(&id, &e, false)); n != 4 {
		t.Errorf("probe false taşıma %d hedef, want 4", n)
	}
	// Üç yazım/okuma yeri listeleri ELLE değil kuruculardan alıyor.
	src := mustReadSource(t, "anomaly_event.go")
	for _, pin := range []struct {
		s string
		n int
	}{
		{"anomalyCarrySelectSQL(len(ids), ep)", 1},
		{"anomalyCarryScanDest(&id, &p, ep)", 1},
		{"anomalyInsertSQL(ep, vr)", 1},
		{"anomalyInsertRow(w, ep, vr)", 1},
		{"SELECT `+anomalyEventSelectExpr(ep, vr)+`", 2},
		{"anomalyEventScanDest(&e, ep, vr), &e.Status", 2},
	} {
		if got := strings.Count(src, pin.s); got != pin.n {
			t.Errorf("anomaly_event.go'da %q %d kez, want %d", pin.s, got, pin.n)
		}
	}
}

// ── uçtan uca yazım: sahte bağlantı ─────────────────────────────────────

type episodeFakeRows struct {
	driver.Rows
	rows [][]any
	i    int
}

func (r *episodeFakeRows) Next() bool { r.i++; return r.i <= len(r.rows) }
func (r *episodeFakeRows) Err() error { return nil }
func (r *episodeFakeRows) Close() error {
	return nil
}
func (r *episodeFakeRows) Scan(dest ...any) error {
	row := r.rows[r.i-1]
	if len(dest) != len(row) {
		return errors.New("Scan arite uyuşmazlığı")
	}
	for k, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = row[k].(string)
		case *int64:
			*p = row[k].(int64)
		case *float64:
			*p = row[k].(float64)
		case *uint32:
			*p = row[k].(uint32)
		default:
			return errors.New("beklenmeyen Scan hedefi")
		}
	}
	return nil
}

type episodeFakeBatch struct {
	driver.Batch
	appended *[][]any
}

func (b episodeFakeBatch) Append(v ...any) error { *b.appended = append(*b.appended, v); return nil }
func (b episodeFakeBatch) Send() error           { return nil }

type episodeFakeConn struct {
	driver.Conn
	stored   [][]any
	queries  *[]string
	appended *[][]any
	// colCount — system.columns probe'unun cevabı (kaç bölüm kolonu var).
	colCount *uint64
}

func (c episodeFakeConn) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	*c.queries = append(*c.queries, q)
	return &episodeFakeRows{rows: c.stored}, nil
}

type episodeFakeRow struct {
	driver.Row
	n uint64
}

func (r episodeFakeRow) Err() error { return nil }
func (r episodeFakeRow) Scan(dest ...any) error {
	*(dest[0].(*uint64)) = r.n
	return nil
}

func (c episodeFakeConn) QueryRow(ctx context.Context, q string, args ...any) driver.Row {
	*c.queries = append(*c.queries, q)
	return episodeFakeRow{n: *c.colCount}
}

func (c episodeFakeConn) PrepareBatch(ctx context.Context, q string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	*c.queries = append(*c.queries, q)
	return episodeFakeBatch{appended: c.appended}, nil
}

// TestUpsertAnomalyEventsCarriesEpisode — probe true: saklı satır (2. bölüm,
// ilk görülme 3 gün önce) bir gün sonra yeniden ateşleyince INSERT satırı
// sayaç 3 ve AYNI ilk başlangıcı taşır. Probe false: aynı akış kolonsuz
// (bugünkü 10 değer) yazar — kolon inmemiş tabloya yazım düşmez.
func TestUpsertAnomalyEventsCarriesEpisode(t *testing.T) {
	const s = int64(time.Second)
	t0 := int64(1_700_000_000) * s
	first := t0 - 3*24*3600*s
	storedRow := []any{"fp", t0, t0 + 600*s, 9.0, uint32(2), first}
	incoming := AnomalyEvent{ID: "fp", Kind: "log_pattern", StartedAt: t0 + 24*3600*s, LastSeen: t0 + 24*3600*s, CurrentRatio: 3}

	t.Run("probe true", func(t *testing.T) {
		var qs []string
		var app [][]any
		st := &Store{conn: episodeFakeConn{stored: [][]any{storedRow}, queries: &qs, appended: &app}}
		st.hasAnomalyEpisodeCols.Store(true)
		if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{incoming}); err != nil {
			t.Fatal(err)
		}
		if len(app) != 1 || len(app[0]) != 12 {
			t.Fatalf("INSERT satırı = %v, want 12 değerli tek satır", app)
		}
		if got := app[0][10]; got != uint32(3) {
			t.Errorf("episode_count = %v, want 3", got)
		}
		if got := app[0][11].(time.Time).UnixNano(); got != first {
			t.Errorf("first_started_at = %d, want %d (ilk bölüm)", got, first)
		}
		if got := app[0][4].(time.Time).UnixNano(); got != incoming.StartedAt {
			t.Errorf("started_at = %d, want yeni bölümün %d", got, incoming.StartedAt)
		}
	})
	t.Run("probe false", func(t *testing.T) {
		var qs []string
		var app [][]any
		st := &Store{conn: episodeFakeConn{stored: [][]any{storedRow[:4]}, queries: &qs, appended: &app}}
		if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{incoming}); err != nil {
			t.Fatal(err)
		}
		if len(app) != 1 || len(app[0]) != 10 {
			t.Fatalf("INSERT satırı = %v, want 10 değerli tek satır", app)
		}
		for _, q := range qs {
			if strings.Contains(q, "episode_count") || strings.Contains(q, "first_started_at") {
				t.Errorf("probe false iken sorgu bölüm kolonunu anıyor: %q", q)
			}
		}
	})
}

// TestReprobeAnomalyEpisodeCols — bayrak süreç ömrü boyunca DONMAZ. Küme
// kipinde kolonu EKLEYEN boot ALTER'ı ertelediği için false okur; sayaç
// birikimli olduğu için bu pod'un her yazımı onu DEFAULT'a sıfırlar. Ertelenen
// DDL indikten sonra reprobePromotedAttrs → reprobeAnomalyEpisodeCols bayrağı
// çevirir ve BİR SONRAKİ çağrı kolonları yazar.
//
// Mutasyon kontrolü: reprobeAnomalyEpisodeCols bayrağı çevirmezse (boot
// değeri dondurulursa) ikinci yazım 10 değerle kalır ve test kırılır.
func TestReprobeAnomalyEpisodeCols(t *testing.T) {
	var qs []string
	var app [][]any
	cols := uint64(0) // kolonlar henüz inmedi
	st := &Store{conn: episodeFakeConn{queries: &qs, appended: &app, colCount: &cols}}
	ev := AnomalyEvent{ID: "fp", Kind: "log_pattern", StartedAt: 1_700_000_000e9, LastSeen: 1_700_000_000e9, CurrentRatio: 3}

	if st.reprobeAnomalyEpisodeCols(context.Background()) {
		t.Fatal("kolonlar yokken bayrak true döndü")
	}
	if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{ev}); err != nil {
		t.Fatal(err)
	}
	if len(app) != 1 || len(app[0]) != 10 {
		t.Fatalf("kolonlar yokken INSERT %d değer, want 10", len(app[len(app)-1]))
	}

	cols = 2 // ertelenen ALTER indi
	if !st.reprobeAnomalyEpisodeCols(context.Background()) {
		t.Fatal("kolonlar indikten sonra yeniden probe bayrağı çevirmedi")
	}
	if err := st.UpsertAnomalyEvents(context.Background(), []AnomalyEvent{ev}); err != nil {
		t.Fatal(err)
	}
	if len(app) != 2 || len(app[1]) != 12 {
		t.Fatalf("yeniden probe sonrası INSERT %d değer, want 12 (episode_count + first_started_at)", len(app[len(app)-1]))
	}
	if app[1][10] != uint32(1) {
		t.Errorf("ilk görülme yazımında episode_count = %v, want 1", app[1][10])
	}

	// Tek kolon görünürse (yarım kalmış ALTER) bayrak çevrilmez.
	st2 := &Store{conn: episodeFakeConn{queries: &qs, appended: &app, colCount: new(uint64)}}
	*st2.conn.(episodeFakeConn).colCount = 1
	if st2.reprobeAnomalyEpisodeCols(context.Background()) {
		t.Error("iki kolondan yalnız biri varken bayrak true döndü")
	}
}
