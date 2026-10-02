// promoted_anomaly_source_test.go — v0.10.1054: anomaliden terfi eden Problem
// de "yinelenen" kuralına uyar.
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun;
// bugün deploy'a hâlâ eski kurala göre bağlanıyor."
//
// v0.10.1049 deploy atfının tek kuralını (AnomalyPredatesDeploy) yalnız anomali
// olayına bağlamıştı: Problems kuyruğunda anomali satırı "yinelenen" der ve
// çip göstermezken, aynı olaydan terfi eden `anomaly-auto:` Problem'i yanında
// aynı deploy'u "olası neden" diye gösteriyordu. Bu dosya:
//
//   - kural id → olay kimliği ayrıştırıcısı (sorgusuz; son ekli / eksiz,
//     bozuk, başka önekler);
//   - toplu okuma: N terfi Problem'i için TEK okuma, terfi yoksa / kolon yoksa
//     okuma YOK, hata → bugünkü atıf;
//   - EnrichProblemsWithDeploys uçtan uca: gece işinin 3. gecesi çip YOK +
//     bölüm alanları; vaka A (2. bölüm), vaka B (20 gün önce tek kıpırtı),
//     seyrek (ortalama 20 gün), tek bölüm, olay yok (TTL), başka bölüm, terfi
//     DEĞİL (kural, çıplak parmak izi, `anomaly:`) → çip BUGÜNKÜ gibi;
//   - terfi olmayan Problem'in seçimi eski satır-içi döngüyle bayt bayt aynı.
//
// Mutasyon kontrolleri: (1) kural terfi Problem'ine uygulanmazsa (pickProblemDeploy
// bölüm alanlarını geçmezse ya da attachPromotedEpisodes iliştirmezse) gece
// işi satırı deploy taşır ve kırılır; (2) kural terfi OLMAYAN Problem'e
// uygulanırsa (önek kontrolü ayrıştırıcıdan silinirse) çıplak parmak izi
// satırı deploy'u kaybeder ve kırılır; (3) okuma hatası "yinelenen" sayılırsa
// hata alt testi kırılır.
package chstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// TestPromotedAnomalyEventID — kural id (ve problem id) biçimi → olay kimliği.
func TestPromotedAnomalyEventID(t *testing.T) {
	fp := FingerprintAnomaly("trace_op", "nightly-job", "batch-svc")
	cases := []struct {
		name   string
		ruleID string
		wantID string // "" = terfi Problem'i değil
	}{
		{"saklanan rule_id (son eksiz)", PromotedAnomalyRulePrefix + fp, fp},
		{"problem id şekli (servis son ekli)", PromotedAnomalyRulePrefix + fp + ":batch-svc", fp},
		{"servis adında ':' (db öznesi)", PromotedAnomalyRulePrefix + fp + ":db:postgres@pg-1", fp},
		{"boş servis son eki", PromotedAnomalyRulePrefix + fp + ":", fp},
		{"parmak izinden sonra ':' olmayan karakter", PromotedAnomalyRulePrefix + fp + "x", ""},
		{"17 hex (uzun parmak izi)", PromotedAnomalyRulePrefix + fp + "a", ""},
		{"15 hex (kısa)", PromotedAnomalyRulePrefix + fp[:15], ""},
		{"büyük harf hex", PromotedAnomalyRulePrefix + strings.ToUpper(fp), ""},
		{"hex olmayan", PromotedAnomalyRulePrefix + "zzzzzzzzzzzzzzzz", ""},
		{"yalnız önek", PromotedAnomalyRulePrefix, ""},
		{"boş", "", ""},
		{"metrik dedektörü öneki", "anomaly:batch-svc:p99_ms", ""},
		{"metrik öneki + parmak izi", "anomaly:" + fp, ""},
		{"küme öneki", "anomaly-cluster:" + fp, ""},
		{"çıplak parmak izi (kural id'si gibi)", fp, ""},
		{"önek büyük harf", strings.ToUpper(PromotedAnomalyRulePrefix) + fp, ""},
		{"alarm kuralı", "builtin:error_rate", ""},
	}
	for _, c := range cases {
		got, ok := PromotedAnomalyEventID(c.ruleID)
		if ok != (c.wantID != "") || got != c.wantID {
			t.Errorf("%s: PromotedAnomalyEventID(%q) = (%q, %v), want (%q, %v)", c.name, c.ruleID, got, ok, c.wantID, c.wantID != "")
		}
	}
	// Terfi yazımı ile ayrıştırıcı aynı şekli konuşuyor: evaluator rule id'yi
	// önek + olay kimliği, problem id'yi rule id + ":" + servis diye kurar.
	if src := mustReadSource(t, "../evaluator/evaluator.go"); !strings.Contains(src, "ruleID := promoteAnomalyRuleID + ev.ID") ||
		!strings.Contains(src, `id = ruleID + ":" + ev.Service`) {
		t.Error("evaluator terfi kimliklerinin şekli değişmiş — PromotedAnomalyEventID'yi birlikte güncelle")
	}
}

// TestPromotedAnomalyIDs — tekil, ilk görülme sırası, terfi olmayan atlanır, tavan.
func TestPromotedAnomalyIDs(t *testing.T) {
	a := FingerprintAnomaly("trace_op", "a", "s")
	b := FingerprintAnomaly("trace_op", "b", "s")
	probs := []Problem{
		{RuleID: PromotedAnomalyRulePrefix + b},
		{RuleID: "builtin:error_rate"},
		{RuleID: PromotedAnomalyRulePrefix + a},
		{RuleID: PromotedAnomalyRulePrefix + b}, // aynı olay, başka servis satırı
		{RuleID: "anomaly:" + a},
	}
	got := promotedAnomalyIDs(probs)
	if strings.Join(got, ",") != b+","+a {
		t.Errorf("promotedAnomalyIDs = %v, want [%s %s]", got, b, a)
	}
	if promotedAnomalyIDs([]Problem{{RuleID: "builtin:x"}}) != nil {
		t.Error("terfi Problem'i yokken kimlik listesi boş değil")
	}
	many := make([]Problem, 0, promotedSourceLookupMax+5)
	for i := 0; i < promotedSourceLookupMax+5; i++ {
		many = append(many, Problem{RuleID: PromotedAnomalyRulePrefix + FingerprintAnomaly("k", "p", string(rune('a'+i%26))+strings.Repeat("x", i))})
	}
	if n := len(promotedAnomalyIDs(many)); n != promotedSourceLookupMax {
		t.Errorf("tavan: %d kimlik, want %d", n, promotedSourceLookupMax)
	}
}

// ── sahte bağlantı ───────────────────────────────────────────────────────

type promotedFakeRows struct {
	driver.Rows
	rows [][]any
	i    int
}

func (r *promotedFakeRows) Next() bool   { r.i++; return r.i <= len(r.rows) }
func (r *promotedFakeRows) Err() error   { return nil }
func (r *promotedFakeRows) Close() error { return nil }
func (r *promotedFakeRows) Scan(dest ...any) error {
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
		case *uint64:
			*p = row[k].(uint64)
		case *uint32:
			*p = row[k].(uint32)
		default:
			return errors.New("beklenmeyen Scan hedefi")
		}
	}
	return nil
}

type promotedFakeConn struct {
	driver.Conn
	events  []AnomalyEvent
	err     error
	queries *[]string
	args    *[][]any
}

func (c promotedFakeConn) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	*c.queries = append(*c.queries, q)
	*c.args = append(*c.args, args)
	if c.err != nil {
		return nil, c.err
	}
	want := map[string]bool{}
	for _, a := range args {
		if s, ok := a.(string); ok {
			want[s] = true
		}
	}
	var rows [][]any
	for _, e := range c.events {
		if want[e.ID] {
			rows = append(rows, []any{e.ID, e.StartedAt, e.LastSeen, e.EpisodeCount, e.FirstStartedAt})
		}
	}
	return &promotedFakeRows{rows: rows}, nil
}

// TestPromotedAnomalySourcesOneRead — N terfi Problem'i → TEK okuma, IN listesi
// tekil kimlikler + LIMIT 2n, sınırlı (max_execution_time). Terfi yok / kolon
// yok → okuma yok. Hata → (nil, err).
func TestPromotedAnomalySourcesOneRead(t *testing.T) {
	ctx := context.Background()
	fps := []string{
		FingerprintAnomaly("trace_op", "a", "s"),
		FingerprintAnomaly("trace_op", "b", "s"),
		FingerprintAnomaly("log_pattern", "c", "s"),
	}
	var probs []Problem
	for i := 0; i < 9; i++ { // 9 Problem, 3 tekil olay
		probs = append(probs, Problem{RuleID: PromotedAnomalyRulePrefix + fps[i%3], Service: "s"})
	}
	probs = append(probs, Problem{RuleID: "builtin:error_rate", Service: "s"})
	events := []AnomalyEvent{
		{ID: fps[0], StartedAt: 10, LastSeen: 20, EpisodeCount: 3, FirstStartedAt: 1},
		{ID: fps[0], StartedAt: 10, LastSeen: 15, EpisodeCount: 2, FirstStartedAt: 1}, // bayat partition kopyası
		{ID: fps[1], StartedAt: 10, LastSeen: 20, EpisodeCount: 1, FirstStartedAt: 10},
	}

	var qs []string
	var args [][]any
	st := &Store{conn: promotedFakeConn{events: events, queries: &qs, args: &args}}
	st.hasAnomalyEpisodeCols.Store(true)
	got, err := st.PromotedAnomalySources(ctx, probs)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 {
		t.Fatalf("%d okuma, want TEK okuma (sayfa başına)", len(qs))
	}
	q := qs[0]
	for _, must := range []string{"FROM anomaly_events FINAL", "WHERE id IN (?,?,?)", "LIMIT ?", "max_execution_time = 2", "episode_count", "first_started_at"} {
		if !strings.Contains(q, must) {
			t.Errorf("okuma %q içermiyor:\n%s", must, q)
		}
	}
	// Sıcak okuma yolunda yalnız kullanılan kolonlar (v0.10.1054 inceleme).
	if got := sqlColumnCount(selectColumnList(t, q)); got != 5 {
		t.Errorf("okuma %d kolon, want 5 (id, started_at, last_seen, iki bölüm kolonu):\n%s", got, q)
	}
	for _, never := range []string{"pattern", "sample", "peak_ratio", "service"} {
		if strings.Contains(q, never) {
			t.Errorf("okuma kullanılmayan %q kolonunu çekiyor:\n%s", never, q)
		}
	}
	if len(args[0]) != 4 || args[0][3] != 6 {
		t.Errorf("bind argümanları = %v, want 3 kimlik + LIMIT 6", args[0])
	}
	if len(got) != 2 || got[fps[0]].EpisodeCount != 3 || got[fps[1]].EpisodeCount != 1 {
		t.Errorf("kaynaklar = %+v, want iki olay (bayat kopya değil, last_seen'i büyük olan)", got)
	}
	if strings.Count(q, "SELECT") != 1 {
		t.Errorf("alt sorgu var — IN değer listesi olmalı:\n%s", q)
	}

	t.Run("terfi Problem'i yok → okuma yok", func(t *testing.T) {
		qs = nil
		if m, err := st.PromotedAnomalySources(ctx, []Problem{{RuleID: "builtin:x"}, {RuleID: "anomaly:s:p99_ms"}}); m != nil || err != nil || len(qs) != 0 {
			t.Errorf("terfi yokken okuma yapıldı ya da sonuç döndü: m=%v err=%v okuma=%d", m, err, len(qs))
		}
	})
	t.Run("bölüm kolonları yok (probe false) → okuma yok", func(t *testing.T) {
		qs = nil
		st2 := &Store{conn: promotedFakeConn{events: events, queries: &qs, args: &args}}
		if m, err := st2.PromotedAnomalySources(ctx, probs); m != nil || err != nil || len(qs) != 0 {
			t.Errorf("probe false iken okuma yapıldı: m=%v err=%v okuma=%d", m, err, len(qs))
		}
	})
	t.Run("hata → (nil, err)", func(t *testing.T) {
		qs = nil
		st3 := &Store{conn: promotedFakeConn{err: errors.New("CH down"), queries: &qs, args: &args}}
		st3.hasAnomalyEpisodeCols.Store(true)
		if m, err := st3.PromotedAnomalySources(ctx, probs); m != nil || err == nil {
			t.Errorf("hata yutuldu: m=%v err=%v", m, err)
		}
	})
}

// enrichFixture — EnrichProblemsWithDeploys'u CH'siz koşturur: deploy okuması
// önbellekten (aynı anahtar), anomaly_events okuması sahte bağlantıdan.
func enrichFixture(t *testing.T, probs []Problem, deploys map[string][]spanDeploy, conn promotedFakeConn, lookback time.Duration) *Store {
	t.Helper()
	st := &Store{conn: conn}
	st.hasAnomalyEpisodeCols.Store(true)
	services, from, to, ok := deployEnrichWindow(probs, lookback, time.Now())
	if !ok {
		t.Fatal("pencere kurulamadı")
	}
	now := time.Now()
	st.storeDeploysCacheEntry(deploysCacheKey(services, from, to), deploysCacheEntry{at: now, byService: deploys}, now)
	return st
}

// TestEnrichProblemsWithDeploysPromotedRecurring — tüketici 1 (çip / deploy
// kutusu / kök-neden ucu / Insight / istemler: hepsi bu seçimden).
func TestEnrichProblemsWithDeploysPromotedRecurring(t *testing.T) {
	const (
		m   = int64(time.Minute)
		day = 24 * 60 * m
	)
	start := time.Now().Add(-2 * time.Hour).UnixNano()
	const svc = "batch-svc"
	fp := func(p string) string { return FingerprintAnomaly("trace_op", p, svc) }
	deploys := map[string][]spanDeploy{svc: {
		{version: "v1.9.0", ns: start - 40*m}, // pencere dışı
		{version: "v2.0.0", ns: start - 10*m},
	}}
	ev := func(p string, started, first int64, count uint32) AnomalyEvent {
		return AnomalyEvent{ID: fp(p), Kind: "trace_op", Pattern: p, Service: svc, StartedAt: started,
			LastSeen: started + 20*m, PeakRatio: 5, CurrentRatio: 4, CurrentCount: 9, EpisodeCount: count, FirstStartedAt: first}
	}
	events := []AnomalyEvent{
		ev("nightly", start, start-2*day, 3),     // gece işi 3. gece
		ev("caseA", start, start-60*m, 2),        // deploy kırdı → rollback → bozuk yeniden deploy
		ev("caseB", start, start-20*day, 2),      // 20 gün önce tek kıpırtı
		ev("sparse", start, start-80*day, 5),     // ortalama 20 gün
		ev("single", start, start, 1),            // ilk görülme
		ev("moved", start+1*day, start-2*day, 4), // olay o zamandan beri yeni bölüme geçti
	}
	promoted := func(p string) Problem {
		return Problem{ID: PromotedAnomalyRulePrefix + fp(p) + ":" + svc, RuleID: PromotedAnomalyRulePrefix + fp(p), Service: svc, StartedAt: start, Status: "open"}
	}
	probs := []Problem{
		promoted("nightly"),
		promoted("caseA"),
		promoted("caseB"),
		promoted("sparse"),
		promoted("single"),
		promoted("gone"), // olay TTL ile düşmüş
		promoted("moved"),
		{ID: "r1", RuleID: "builtin:error_rate", Service: svc, StartedAt: start, Status: "open"},
		// Terfi DEĞİL ama kural id'si yinelenen olayın çıplak parmak izi /
		// metrik dedektörü öneki — kural UYGULANMAZ.
		{ID: "r2", RuleID: fp("nightly"), Service: svc, StartedAt: start, Status: "open"},
		{ID: "r3", RuleID: "anomaly:" + svc + ":p99_ms", Service: svc, StartedAt: start, Status: "open"},
	}
	type want struct {
		deploy string // "" = çip yok
		count  uint32
		first  int64
	}
	wants := []want{
		{"", 3, start - 2*day},
		{"v2.0.0", 2, start - 60*m},
		{"v2.0.0", 2, start - 20*day},
		{"v2.0.0", 5, start - 80*day},
		{"v2.0.0", 0, 0},
		{"v2.0.0", 0, 0},
		{"v2.0.0", 0, 0},
		{"v2.0.0", 0, 0},
		{"v2.0.0", 0, 0},
		{"v2.0.0", 0, 0},
	}

	t.Run("tek okuma, kural yalnız terfi Problem'ine", func(t *testing.T) {
		var qs []string
		var args [][]any
		in := append([]Problem(nil), probs...)
		st := enrichFixture(t, in, deploys, promotedFakeConn{events: events, queries: &qs, args: &args}, 30*time.Minute)
		got := st.EnrichProblemsWithDeploys(context.Background(), in, 30*time.Minute)
		if len(qs) != 1 {
			t.Fatalf("%d okuma, want TEK (deploy okuması önbellekten)", len(qs))
		}
		if n := len(args[0]) - 1; n != 7 {
			t.Errorf("IN listesi %d kimlik, want 7 (yalnız terfi Problem'leri)", n)
		}
		for i, w := range wants {
			p := got[i]
			gotV := ""
			if p.RecentDeploy != nil {
				gotV = p.RecentDeploy.Version
			}
			if gotV != w.deploy {
				t.Errorf("%s: deploy %q, want %q", p.ID, gotV, w.deploy)
			}
			if p.EpisodeCount != w.count || p.FirstStartedAt != w.first {
				t.Errorf("%s: bölüm alanları (%d, %d), want (%d, %d)", p.ID, p.EpisodeCount, p.FirstStartedAt, w.count, w.first)
			}
			if p.PredatesDeploy {
				t.Errorf("%s: PredatesDeploy zenginleştirmede dolmamalı (yalnız deploy raporu)", p.ID)
			}
			// v0.10.1054 — "hiçbir şey kaybolmaz": kuralın bastırdığı deploy
			// nötr PriorDeploy'da; bastırma yoksa alan boş (tel bugünkü gibi).
			wantPrior := w.deploy == ""
			if (p.PriorDeploy != nil) != wantPrior {
				t.Errorf("%s: PriorDeploy=%+v, beklenen dolu=%v", p.ID, p.PriorDeploy, wantPrior)
			}
			if wantPrior && (p.PriorDeploy.Version != "v2.0.0" || p.PriorDeploy.AgeSeconds != 600) {
				t.Errorf("%s: PriorDeploy=%+v, want v2.0.0 600 sn önce", p.ID, p.PriorDeploy)
			}
		}
	})

	t.Run("okuma hatası → bugünkü atıf, işaret yok", func(t *testing.T) {
		var qs []string
		var args [][]any
		in := append([]Problem(nil), probs...)
		st := enrichFixture(t, in, deploys, promotedFakeConn{err: errors.New("CH down"), queries: &qs, args: &args}, 30*time.Minute)
		got := st.EnrichProblemsWithDeploys(context.Background(), in, 30*time.Minute)
		for _, p := range got {
			if p.RecentDeploy == nil || p.RecentDeploy.Version != "v2.0.0" || p.EpisodeCount != 0 {
				t.Errorf("%s: okunamayan süzgeç süzdü: deploy=%+v sayaç=%d", p.ID, p.RecentDeploy, p.EpisodeCount)
			}
		}
	})

	t.Run("bölüm kolonları yok → okuma yok, bugünkü atıf", func(t *testing.T) {
		var qs []string
		var args [][]any
		in := append([]Problem(nil), probs...)
		st := enrichFixture(t, in, deploys, promotedFakeConn{events: events, queries: &qs, args: &args}, 30*time.Minute)
		st.hasAnomalyEpisodeCols.Store(false)
		got := st.EnrichProblemsWithDeploys(context.Background(), in, 30*time.Minute)
		if len(qs) != 0 {
			t.Errorf("probe false iken %d okuma", len(qs))
		}
		if got[0].RecentDeploy == nil {
			t.Error("kolonsuz kurulumda gece işi deploy'u kaybetti")
		}
	})

	t.Run("terfi Problem'i yoksa okuma yok", func(t *testing.T) {
		var qs []string
		var args [][]any
		in := []Problem{probs[7], probs[8], probs[9]}
		st := enrichFixture(t, in, deploys, promotedFakeConn{events: events, queries: &qs, args: &args}, 30*time.Minute)
		st.EnrichProblemsWithDeploys(context.Background(), in, 30*time.Minute)
		if len(qs) != 0 {
			t.Errorf("terfi yokken %d okuma", len(qs))
		}
	})
}

// legacyProblemPick — bu sürümden önceki EnrichProblemsWithDeploys satır-içi
// seçim döngüsünün BİREBİR kopyası (referans).
func legacyProblemPick(list []spanDeploy, startedAt, lookbackNs int64) *spanDeploy {
	for j := len(list) - 1; j >= 0; j-- {
		if list[j].ns > startedAt {
			continue
		}
		if list[j].ns < startedAt-lookbackNs {
			break
		}
		return &list[j]
	}
	return nil
}

// TestPickProblemDeployByteIdenticalForOthers — bölüm alanı iliştirilmemiş her
// Problem'de (kural, exception, `anomaly:`, kaynağı okunamayan terfi) seçim eski
// döngüyle AYNI eleman (işaretçi eşitliği) — kenar durumlar dahil.
func TestPickProblemDeployByteIdenticalForOthers(t *testing.T) {
	const m = int64(time.Minute)
	s0 := int64(1_700_000_000) * int64(time.Second)
	lists := [][]spanDeploy{
		nil,
		{{version: "a", ns: s0}},
		{{version: "a", ns: s0 - 40*m}, {version: "b", ns: s0 - 25*m}, {version: "c", ns: s0 - 5*m}, {version: "d", ns: s0 + 2*m}},
		{{version: "a", ns: s0 - 5*m}, {version: "b", ns: s0 - 5*m}}, // eşit zaman
		{{version: "a", ns: s0 - 30*m}},     // tam pencere kenarı
		{{version: "a", ns: s0 - 30*m - 1}}, // bir ns dışarıda
	}
	rules := []string{"builtin:error_rate", "anomaly:svc:p99_ms", "exception:shared-dependency", PromotedAnomalyRulePrefix + "0123456789abcdef", ""}
	for li, list := range lists {
		for _, off := range []int64{-10 * m, -1, 0, 1, 3 * m, 30 * m} {
			for _, lb := range []int64{0, 5 * m, 30 * m, 120 * m} {
				for _, rid := range rules {
					p := Problem{RuleID: rid, StartedAt: s0 + off}
					got, prior := pickProblemDeploy(list, p, lb)
					if want := legacyProblemPick(list, p.StartedAt, lb); got != want {
						t.Errorf("liste %d, off %d, lookback %d, kural %q: seçim %v, eski döngü %v", li, off, lb, rid, got, want)
					}
					if prior != nil {
						t.Errorf("liste %d, off %d, lookback %d, kural %q: bastırılan deploy %v — diğer Problem'de hep nil olmalı", li, off, lb, rid, prior)
					}
				}
			}
		}
	}
	// Zenginleştirme seçimi bu işlevden yapıyor; elle döngü geri gelmesin.
	src := mustReadSource(t, "problem_telemetry.go")
	if !strings.Contains(src, "pickProblemDeploy(list, problems[i], lookbackNs)") {
		t.Error("EnrichProblemsWithDeploys pickProblemDeploy'u çağırmıyor — terfi Problem'i deploy'a eski kuralla bağlanır")
	}
	if !strings.Contains(src, "s.PromotedAnomalySources(ctx, problems)") || !strings.Contains(src, "attachPromotedEpisodes(problems, srcs)") {
		t.Error("EnrichProblemsWithDeploys terfi kaynaklarını okumuyor / iliştirmiyor")
	}
}

// TestProblemJSONUnchangedForOthers — iliştirme olmayan Problem'in teli
// bugünküyle aynı: yeni anahtarlar (omitempty) basılmaz.
func TestProblemJSONUnchangedForOthers(t *testing.T) {
	b, err := json.Marshal(Problem{ID: "r1", RuleID: "builtin:error_rate", Service: "s", StartedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"episodeCount", "firstStartedAt", "predatesDeploy", "priorDeploy"} {
		if strings.Contains(string(b), k) {
			t.Errorf("iliştirmesiz Problem JSON'unda %q var: %s", k, b)
		}
	}
	b, _ = json.Marshal(Problem{ID: "p", EpisodeCount: 3, FirstStartedAt: 7, PredatesDeploy: true})
	for _, k := range []string{`"episodeCount":3`, `"firstStartedAt":7`, `"predatesDeploy":true`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("terfi Problem JSON'unda %q yok: %s", k, b)
		}
	}
}
