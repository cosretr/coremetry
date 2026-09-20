package chstore

// mv_shape_guard_test.go — v0.10.834 VERİ KAYBI düzeltmesinin kanıtı.
//
// SEMPTOM (operatör test kümesi): cluster_name DOLU ama `spans` ve bazı MV'ler
// hâlâ TEK DÜĞÜM şeklinde (çıplak ad = MV'nin KENDİSİ, `_local` kardeşi YOK).
// Boot'taki MV yükseltme dalları çıplak adı "metadata-only Distributed
// sarmalayıcı" sanıp DROP ediyordu; trace_summary_5m dalında DROP `purgeGuard`
// de taşıyordu, yani CH'nin kazara-DROP emniyeti bilerek kapalıydı. Şekil
// "düzeliyor", 90 günlük tarihçe YANIYOR, hiçbir yerde hata görünmüyor.
//
// Bu dosya KAYNAK METNİ değil DAVRANIŞI ölçer: sahte bir driver.Conn'a hangi
// ifadelerin, hangi SIRAYLA gittiğini sayar. Şekil bir host LİSTESİ olarak
// modellenir — onayladığımız DROP'lar ON CLUSTER olduğu için tek-host bir
// sahte bağlantı sözleşmeyi ifade EDEMEZ.

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/cilcenk/coremetry/internal/config"
)

// ── sahte bağlantı (host listeli) ─────────────────────────────────────

type shapeRows struct {
	driver.Rows
	rows []bareHostShape
	i    int
}

func (r *shapeRows) Next() bool { r.i++; return r.i <= len(r.rows) }
func (r *shapeRows) Scan(dest ...any) error {
	h := r.rows[r.i-1]
	if len(dest) != 2 {
		return fmt.Errorf("şekil okuması iki kolon bekler, %d geldi", len(dest))
	}
	hp, ok1 := dest[0].(*string)
	ep, ok2 := dest[1].(*string)
	if !ok1 || !ok2 {
		return fmt.Errorf("şekil okuması (string, string) bekler")
	}
	*hp, *ep = h.Host, h.Engine
	return nil
}
func (r *shapeRows) Close() error { return nil }
func (r *shapeRows) Err() error   { return nil }

// shapeConn — desen eşleşmeli sahte bağlantı. scriptConn
// (mv_inner_conn_test.go) sıralı bir betik bekler; buradaki yollar
// dropCombinedMV → dropLeftoverViewObjects → mvInventory zincirini de
// sürüklediği için sıra değil DESEN eşleşmesi doğru araç.
//
// KAPSAM MODELLENİR: sahte bağlantı, okumanın KÜME GENELİ mi yoksa HOST-YEREL
// mi olduğunu SQL'den ayırt eder ve host-yerel okumaya YALNIZ koordinatörün
// satırını döndürür. Bu şart — aksi hâlde "ölçüm eylemin kapsamında yapılır"
// sözleşmesi test edilemez: kapsamı tek-host'a düşüren bir gerileme, her
// sorguya aynı listeyi veren bir sahte bağlantıda GÖRÜNMEZ kalırdı.
type shapeConn struct {
	driver.Conn
	hosts       []bareHostShape // çıplak adın host başına motoru; boş = hiçbir host'ta yok
	coordinator string          // host-yerel okumanın gördüğü tek host (boş = ilk host)
	shapeErr    error           // şekil okumasının hatası (bareShapeUnknown yolu)
	innerUUID   string          // dropCombinedMV'nin bulacağı iç tablo uuid'si
	execs       []string
	queries     []string
}

// visibleHosts — okumanın KAPSAMI. Küme geneli olmayan bir sorgu yalnız
// koordinatörü görür; gerçek CH davranışı budur.
func (c *shapeConn) visibleHosts(q string) []bareHostShape {
	if strings.Contains(q, "clusterAllReplicas") {
		return c.hosts
	}
	coord := c.coordinator
	if coord == "" && len(c.hosts) > 0 {
		coord = c.hosts[0].Host
	}
	var out []bareHostShape
	for _, h := range c.hosts {
		if h.Host == coord {
			out = append(out, h)
		}
	}
	return out
}

func (c *shapeConn) QueryRow(ctx context.Context, q string, args ...any) driver.Row {
	c.queries = append(c.queries, q)
	switch {
	case strings.Contains(q, "SELECT currentDatabase()"):
		return scriptRow{vals: []any{"shopobs"}}
	case strings.Contains(q, "SELECT toString(uuid) FROM system.tables"):
		return scriptRow{vals: []any{c.innerUUID}}
	}
	return scriptRow{err: fmt.Errorf("sahte bağlantı: beklenmeyen tek-satır okuma: %.90s", q)}
}

func (c *shapeConn) Exec(ctx context.Context, q string, args ...any) error {
	c.execs = append(c.execs, q)
	return nil
}

func (c *shapeConn) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	c.queries = append(c.queries, q)
	if strings.Contains(q, "SELECT hostName(), engine") {
		if c.shapeErr != nil {
			return nil, c.shapeErr
		}
		return &shapeRows{rows: c.visibleHosts(q)}, nil
	}
	// Sarkan-view taraması (mvInventory) buradan geçer; hata dönmek SESSİZ
	// değil — dropLeftoverViewObjects loglayıp geçer, davranış aynıdır.
	return nil, fmt.Errorf("sahte bağlantı: satır akışı yok")
}

const (
	shapeCluster  = "shop_cluster"
	shapeInnerID  = "44444444-4444-4444-4444-444444444444"
	shapeTraceDDL = `CREATE MATERIALIZED VIEW IF NOT EXISTS trace_summary_5m
		 ENGINE = AggregatingMergeTree ORDER BY trace_id
		 AS SELECT trace_id FROM spans GROUP BY trace_id`
	shapeDimDDL = `CREATE MATERIALIZED VIEW IF NOT EXISTS db_summary_5m
		 ENGINE = AggregatingMergeTree ORDER BY db_name
		 AS SELECT db_name FROM spans GROUP BY db_name`
)

func wrapperHosts() []bareHostShape {
	return []bareHostShape{
		{Host: "ch-01", Engine: "Distributed"},
		{Host: "ch-02", Engine: "Distributed"},
		{Host: "ch-03", Engine: "Distributed"},
		{Host: "ch-04", Engine: "Distributed"},
	}
}

func shapeStore(c *shapeConn, cluster string) *Store {
	return &Store{conn: c, cfg: config.CHConfig{ClusterName: cluster}}
}

var dimDBSummary = mvDimMigration{Table: "db_summary_5m", Column: "db_name", Dim: "db_name"}

// assertExecSequence — koşan ifadelerin TAM dizisi ve SIRASI. Üyelik ölçmek
// yetmez: v0.10.563 sözleşmesi "sarmalayıcı ÖNCE, `_local` SONRA" der ve
// sırayı ters çeviren bir düzenleme üyelik testinden geçerdi.
func assertExecSequence(t *testing.T, execs []string, want []string) {
	t.Helper()
	if len(execs) != len(want) {
		t.Fatalf("ifade sayısı %d, beklenen %d:\ngelen: %s\nbeklenen: %s",
			len(execs), len(want), strings.Join(execs, "\n  | "), strings.Join(want, "\n  | "))
	}
	for i, w := range want {
		if !strings.Contains(execs[i], w) {
			t.Errorf("%d. ifade %q içermeli, gelen: %s", i+1, w, execs[i])
		}
	}
}

// ── 1. SAF ÇEKİRDEK ───────────────────────────────────────────────────

func TestBareNameIsWrapper(t *testing.T) {
	cases := []struct {
		engine string
		want   bool
	}{
		{"Distributed", true},
		{" Distributed ", true}, // system.tables boşluk döndürürse kapı kapanmasın
		{"MaterializedView", false},
		{"MergeTree", false},
		{"AggregatingMergeTree", false},
		{"ReplicatedAggregatingMergeTree", false},
		{"ReplicatedMergeTree", false},
		{"View", false},
		{"", false},
		{"distributed", false}, // CH motor adları büyük/küçük duyarlı
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			if got := bareNameIsWrapper(tc.engine); got != tc.want {
				t.Errorf("bareNameIsWrapper(%q) = %v, beklenen %v", tc.engine, got, tc.want)
			}
		})
	}
}

// TestClassifyBareNameHosts — KRİTİK 2'nin çekirdeği: şekil bir host
// LİSTESİDİR. Tek bir host'ta bile ad veriyse ON CLUSTER bir DROP o host'un
// gerçek MV'sini götürür; "çoğunluk sarmalayıcı" diye bir emniyet yok.
func TestClassifyBareNameHosts(t *testing.T) {
	boom := fmt.Errorf("code: 279, ALL_CONNECTION_TRIES_FAILED")
	cases := []struct {
		name       string
		hosts      []bareHostShape
		err        error
		want       bareShape
		wantDetail string
	}{
		{name: "her host sarmalayıcı", hosts: wrapperHosts(), want: bareShapeWrapper},
		{name: "hiçbir host'ta yok", hosts: nil, want: bareShapeAbsent},
		{name: "tek host, MV'nin kendisi", hosts: []bareHostShape{{Host: "ch-01", Engine: "MaterializedView"}}, want: bareShapeData, wantDetail: "ch-01=MaterializedView"},
		{
			name: "KARIŞIK — koordinatörde sarmalayıcı, ch-03'te gerçek MV",
			hosts: []bareHostShape{
				{Host: "ch-01", Engine: "Distributed"},
				{Host: "ch-02", Engine: "Distributed"},
				{Host: "ch-03", Engine: "MaterializedView"},
				{Host: "ch-04", Engine: "Distributed"},
			},
			want: bareShapeData, wantDetail: "ch-03=MaterializedView",
		},
		{
			name: "iki host bozuk — ikisi de rapora girer, SIRALI",
			hosts: []bareHostShape{
				{Host: "ch-04", Engine: "ReplicatedAggregatingMergeTree"},
				{Host: "ch-01", Engine: "Distributed"},
				{Host: "ch-02", Engine: "MaterializedView"},
			},
			want: bareShapeData, wantDetail: "ch-02=MaterializedView, ch-04=ReplicatedAggregatingMergeTree",
		},
		{name: "okuma düştü", hosts: nil, err: boom, want: bareShapeUnknown},
		{name: "okuma düştü ama satır da geldi", hosts: wrapperHosts(), err: boom, want: bareShapeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := classifyBareNameHosts(tc.hosts, tc.err)
			if got != tc.want {
				t.Fatalf("classifyBareNameHosts = %v, beklenen %v (ayrıntı %q)", got, tc.want, detail)
			}
			if tc.wantDetail != "" && detail != tc.wantDetail {
				t.Errorf("ayrıntı %q, beklenen %q — operatör HANGİ host'u onaracağını buradan okuyor", detail, tc.wantDetail)
			}
		})
	}
}

// TestBareShapeMatchesExpectation — ÖNEMLİ 3: kapı bir İZİN LİSTESİ değil,
// KİP başına BEKLENTİ karşılaştırmasıdır. İki yön de kapalı.
func TestBareShapeMatchesExpectation(t *testing.T) {
	cases := []struct {
		name    string
		sh      bareShape
		cluster bool
		want    bool
	}{
		{"küme kipi + sarmalayıcı = beklenen", bareShapeWrapper, true, true},
		{"küme kipi + yok = zararsız", bareShapeAbsent, true, true},
		{"küme kipi + VERİ = tek düğüm şekli", bareShapeData, true, false},
		{"küme kipi + okunamadı", bareShapeUnknown, true, false},
		{"tek düğüm + VERİ = beklenen", bareShapeData, false, true},
		{"tek düğüm + yok = zararsız", bareShapeAbsent, false, true},
		{"tek düğüm + SARMALAYICI = ters yön, aynı sınıf", bareShapeWrapper, false, false},
		{"tek düğüm + okunamadı", bareShapeUnknown, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bareShapeMatchesExpectation(tc.sh, tc.cluster); got != tc.want {
				t.Errorf("bareShapeMatchesExpectation(%v, cluster=%v) = %v, beklenen %v", tc.sh, tc.cluster, got, tc.want)
			}
		})
	}
}

// TestBareShapeSkipReasonTellsOperatorWhatToDo — atlama SESSİZ olmamalı ve
// mesaj DALA GÖRE doğru olmalı: tek genel cümle ("eski şemasıyla okunmaya
// devam eder") dalların yarısında yanlıştı (KÜÇÜK 8).
func TestBareShapeSkipReasonTellsOperatorWhatToDo(t *testing.T) {
	const effect = "yeni boyut bu MV'de OLUŞMAZ"
	t.Run("küme kipinde VERİ → terfi talimatı", func(t *testing.T) {
		msg := bareShapeSkipReason("shop_summary_5m", "ch-03=MaterializedView", bareShapeData, true, effect)
		for _, must := range []string{"shop_summary_5m", "ch-03=MaterializedView", "TERFİ", "tarihçe KORUNDU", effect} {
			if !strings.Contains(msg, must) {
				t.Errorf("eksik %q: %s", must, msg)
			}
		}
	})
	t.Run("tek düğüm kipinde SARMALAYICI → cluster_name talimatı", func(t *testing.T) {
		msg := bareShapeSkipReason("shop_summary_5m", "Distributed@ch-01", bareShapeWrapper, false, effect)
		for _, must := range []string{"cluster_name", "KURMAYIZ", effect} {
			if !strings.Contains(msg, must) {
				t.Errorf("eksik %q: %s", must, msg)
			}
		}
	})
	t.Run("okunamadı → erişilemeyen host da HATA", func(t *testing.T) {
		msg := bareShapeSkipReason("shop_summary_5m", "", bareShapeUnknown, true, effect)
		if !strings.Contains(msg, "erişilemeyen host da HATA sayılır") {
			t.Errorf("skip_unavailable_shards sözleşmesi mesajda yok: %s", msg)
		}
	})
	t.Run("koşabilen şekiller için mesaj üretilmez", func(t *testing.T) {
		// Absent her iki kipte de kabul; onun için gerekçe basılmamalı.
		if bareShapeMatchesExpectation(bareShapeAbsent, true) != true {
			t.Fatal("Absent küme kipinde kabul edilmeli")
		}
	})
}

// TestBareDestructiveTarget — KRİTİK 1(d): YAPISAL muhafızın saf çekirdeği.
func TestBareDestructiveTarget(t *testing.T) {
	cases := []struct {
		sql  string
		want string
	}{
		{"DROP VIEW IF EXISTS operation_group_summary_5m", "operation_group_summary_5m"},
		{"DROP VIEW IF EXISTS db_statement_summary_5m", "db_statement_summary_5m"},
		{"  drop   table   if   exists   trace_summary_5m  SYNC", "trace_summary_5m"},
		{"DROP TABLE `spans` SYNC", "spans"},
		{"TRUNCATE TABLE IF EXISTS metric_points", "metric_points"},
		// `_local` ve iç tablo ADLARI çıplak ad DEĞİL — onlar doğru hedefler.
		{"DROP TABLE IF EXISTS trace_summary_5m_local ON CLUSTER `shop_cluster` SYNC", ""},
		{"DROP TABLE IF EXISTS `.inner_id.4444` SYNC", ""},
		// Yüksek hacimli olmayan objeler bu muhafızın konusu değil.
		{"DROP TABLE IF EXISTS feedbacks", ""},
		{"DROP TABLE IF EXISTS root_cause_hypotheses ON CLUSTER `c` SYNC", ""},
		// Yıkıcı olmayan DDL.
		{"CREATE MATERIALIZED VIEW IF NOT EXISTS trace_summary_5m AS SELECT 1", ""},
		{"ALTER TABLE spans ADD COLUMN IF NOT EXISTS x UInt8", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.sql, func(t *testing.T) {
			if got := bareDestructiveTarget(tc.sql); got != tc.want {
				t.Errorf("bareDestructiveTarget(%q) = %q, beklenen %q", tc.sql, got, tc.want)
			}
		})
	}
}

// TestExecDDLRejectsBareDestructiveInClusterMode — muhafızın DAVRANIŞI:
// küme kipinde ham bir çıplak-ad DROP'u sunucuya HİÇ gitmemeli.
func TestExecDDLRejectsBareDestructiveInClusterMode(t *testing.T) {
	c := &shapeConn{}
	s := shapeStore(c, shapeCluster)
	err := s.execDDL(context.Background(), `DROP VIEW IF EXISTS operation_group_summary_5m`)
	if err == nil {
		t.Fatal("küme kipinde çıplak-ad DROP'u REDDEDİLMELİ — adaptDDL onu yeniden yazmaz, ifade şekil kapısını atlar")
	}
	if !strings.Contains(err.Error(), "dropCombinedMV") {
		t.Errorf("hata doğru yolu göstermiyor: %v", err)
	}
	if len(c.execs) != 0 {
		t.Errorf("reddedilen ifade yine de sunucuya gitmiş: %v", c.execs)
	}

	// Tek düğüm kipinde muhafız GÖRÜNMEZ: orada çıplak ad zaten objenin
	// kendisi ve execDDL'den geçen DROP'lar (ör. v0.8.186 kurtarması) doğru.
	c2 := &shapeConn{}
	s2 := shapeStore(c2, "")
	if err := s2.execDDL(context.Background(), `DROP VIEW IF EXISTS operation_group_summary_5m`); err != nil {
		t.Fatalf("tek düğüm kipinde reddedilmemeli: %v", err)
	}
	if len(c2.execs) != 1 {
		t.Errorf("tek düğümde ifade koşmalıydı: %v", c2.execs)
	}
}

// ── 2. DAVRANIŞ: trace_summary_5m entry_service dalı ──────────────────

// TestEntryServiceBranchSkipsWhenAnyHostHoldsData — KIRMIZI→YEŞİL çekirdeği
// ve KRİTİK 2'nin davranışsal yarısı: koordinatörde sarmalayıcı olsa bile
// TEK bir host'ta gerçek MV varsa hiçbir ifade GİTMEMELİ.
func TestEntryServiceBranchSkipsWhenAnyHostHoldsData(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hosts []bareHostShape
	}{
		{"her host tek düğüm şekli", []bareHostShape{
			{Host: "ch-01", Engine: "MaterializedView"},
			{Host: "ch-02", Engine: "MaterializedView"},
		}},
		{"YALNIZ ch-03 terfi öncesi (koordinatör sarmalayıcı)", []bareHostShape{
			{Host: "ch-01", Engine: "Distributed"},
			{Host: "ch-02", Engine: "Distributed"},
			{Host: "ch-03", Engine: "MaterializedView"},
			{Host: "ch-04", Engine: "Distributed"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &shapeConn{hosts: tc.hosts, innerUUID: shapeInnerID}
			s := shapeStore(c, shapeCluster)
			if err := s.upgradeTraceSummaryEntryService(context.Background(), shapeTraceDDL); err != nil {
				t.Fatalf("atlama HATA değildir (boot düşmemeli): %v", err)
			}
			if len(c.execs) != 0 {
				t.Errorf("ON CLUSTER bir DROP o host'un gerçek MV'sini + iç tablosunu götürürdü; "+
					"dal TAMAMEN atlanmalıydı. Koşan ifadeler: %v", c.execs)
			}
		})
	}
}

// TestShapeReadIsClusterWideAndQualified — KRİTİK 2'nin POZİTİF yarısı.
// Onayladığımız ifadeler ON CLUSTER; ölçüm de küme geneli olmak ZORUNDA.
// Ayrıca: db AÇIK bind edilir (v0.10.826 — tablo fonksiyonunun argümanı
// BAŞLATAN düğümde çözülür) ve `skip_unavailable_shards` KULLANILMAZ
// (erişilemeyen host "orada sorun yok" değil, HATA'dır).
func TestShapeReadIsClusterWideAndQualified(t *testing.T) {
	c := &shapeConn{hosts: wrapperHosts(), innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)
	if _, _ = s.bareNameShape(context.Background(), "trace_summary_5m"); len(c.queries) == 0 {
		t.Fatal("şekil hiç okunmamış")
	}
	var shapeQ string
	for _, q := range c.queries {
		if strings.Contains(q, "SELECT hostName(), engine") {
			shapeQ = q
		}
	}
	if shapeQ == "" {
		t.Fatal("şekil okuması bulunamadı")
	}
	if !strings.Contains(shapeQ, "clusterAllReplicas('"+shapeCluster+"', system.tables)") {
		t.Errorf("şekil okuması küme geneli DEĞİL — koordinatörde sarmalayıcı, başka host'ta terfi öncesi "+
			"çıplak MV hâli görünmez kalır ve ON CLUSTER DROP o host'un verisini götürür: %s", shapeQ)
	}
	if strings.Contains(shapeQ, "skip_unavailable_shards") {
		t.Errorf("skip_unavailable_shards ölçümü SAHTE-yeşile çevirir: erişilemeyen host HATA olmalı: %s", shapeQ)
	}
	if strings.Contains(shapeQ, "database = currentDatabase()") {
		t.Errorf("db AÇIK bind edilmeli — currentDatabase() uzak host'un bağlamında çözülür (v0.10.826): %s", shapeQ)
	}
	// Tek düğüm kipinde küme geneli okuma YAPILMAZ (ortada küme yok).
	c2 := &shapeConn{hosts: []bareHostShape{{Host: "shop-ch", Engine: "MergeTree"}}}
	s2 := shapeStore(c2, "")
	_, _ = s2.bareNameShape(context.Background(), "trace_summary_5m")
	for _, q := range c2.queries {
		if strings.Contains(q, "clusterAllReplicas") {
			t.Errorf("tek düğüm kipinde küme geneli okuma: %s", q)
		}
	}
}

// TestEntryServiceBranchUnchangedOnRealWrapper — REGRESYON KAPISI: her host
// sarmalayıcıysa davranış BİREBİR aynı, TAM DİZİ ve SIRAYLA (KÜÇÜK 7).
func TestEntryServiceBranchUnchangedOnRealWrapper(t *testing.T) {
	c := &shapeConn{hosts: wrapperHosts(), innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)

	if err := s.upgradeTraceSummaryEntryService(context.Background(), shapeTraceDDL); err != nil {
		t.Fatalf("sağlam küme kurulumunda dal düştü: %v", err)
	}
	assertExecSequence(t, c.execs, []string{
		// 1) iç tablo DOĞRUDAN, hacim-guard'ıyla (v0.8.190)
		"DROP TABLE IF EXISTS `.inner_id." + shapeInnerID + "` ON CLUSTER `" + shapeCluster + "` SYNC SETTINGS max_table_size_to_drop = 0",
		// 2) `_local` MV
		"DROP TABLE IF EXISTS trace_summary_5m_local ON CLUSTER `" + shapeCluster + "` SYNC",
		// 3) bayat sarmalayıcı — DAĞITIK İKİNCİ YARI, purgeGuard'lı (v0.10.110)
		"DROP TABLE IF EXISTS trace_summary_5m ON CLUSTER `" + shapeCluster + "` SYNC SETTINGS max_table_size_to_drop = 0, max_partition_size_to_drop = 0",
		// 4) MV `_local` olarak geri kurulur
		"CREATE MATERIALIZED VIEW IF NOT EXISTS trace_summary_5m_local ON CLUSTER",
		// 5) sarmalayıcı geri kurulur
		"CREATE TABLE IF NOT EXISTS trace_summary_5m ON CLUSTER",
	})
}

// TestEntryServiceBranchRunsWhenBareNameAbsent — çıplak ad HİÇ YOKSA
// düşürülecek veri de yok: dal bugünkü gibi koşmalı. Fazla korumacı bir kapı
// gerçek bir göçü KALICI olarak asardı.
func TestEntryServiceBranchRunsWhenBareNameAbsent(t *testing.T) {
	c := &shapeConn{hosts: nil, innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)

	if err := s.upgradeTraceSummaryEntryService(context.Background(), shapeTraceDDL); err != nil {
		t.Fatalf("dal düştü: %v", err)
	}
	if len(c.execs) != 5 {
		t.Errorf("ad yokken dal bugünkü gibi koşmalıydı (5 ifade): %v", c.execs)
	}
}

// TestEntryServiceBranchSkipsWhenShapeUnreadable — bilmiyorken yıkmayız.
// `skip_unavailable_shards` bilerek yok: erişilemeyen host HATA'dır.
func TestEntryServiceBranchSkipsWhenShapeUnreadable(t *testing.T) {
	c := &shapeConn{shapeErr: fmt.Errorf("code: 279, ALL_CONNECTION_TRIES_FAILED"), innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)

	if err := s.upgradeTraceSummaryEntryService(context.Background(), shapeTraceDDL); err != nil {
		t.Fatalf("okunamayan şekil boot'u DÜŞÜRMEMELİ: %v", err)
	}
	if len(c.execs) != 0 {
		t.Errorf("şekil okunamazken hiçbir ifade koşmamalı: %v", c.execs)
	}
}

// ── 3. DAVRANIŞ: mvDimMigrations dalı ─────────────────────────────────

func TestMVDimBranchSkipsWhenAnyHostHoldsData(t *testing.T) {
	c := &shapeConn{hosts: []bareHostShape{
		{Host: "ch-01", Engine: "Distributed"},
		{Host: "ch-02", Engine: "AggregatingMergeTree"},
	}, innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)

	if err := s.upgradeMVDim(context.Background(), dimDBSummary, shapeDimDDL); err != nil {
		t.Fatalf("atlama HATA değildir: %v", err)
	}
	if len(c.execs) != 0 {
		t.Errorf("dal TAMAMEN atlanmalıydı; koşan ifadeler: %v", c.execs)
	}
}

// TestMVDimBranchUnchangedOnRealWrapper — REGRESYON KAPISI. v0.10.563
// SIRASI iddiaya girer: sarmalayıcı ÖNCE, `_local` SONRA. Üyelik ölçen bir
// test sırayı ters çeviren düzenlemeyi göremezdi (KÜÇÜK 7).
func TestMVDimBranchUnchangedOnRealWrapper(t *testing.T) {
	c := &shapeConn{hosts: wrapperHosts(), innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)

	if err := s.upgradeMVDim(context.Background(), dimDBSummary, shapeDimDDL); err != nil {
		t.Fatalf("sağlam küme kurulumunda dal düştü: %v", err)
	}
	assertExecSequence(t, c.execs, []string{
		"DROP TABLE IF EXISTS db_summary_5m ON CLUSTER `" + shapeCluster + "` SYNC",
		"DROP TABLE IF EXISTS `.inner_id." + shapeInnerID + "` ON CLUSTER `" + shapeCluster + "` SYNC SETTINGS max_table_size_to_drop = 0",
		"DROP TABLE IF EXISTS db_summary_5m_local ON CLUSTER `" + shapeCluster + "` SYNC",
		"CREATE MATERIALIZED VIEW IF NOT EXISTS db_summary_5m_local ON CLUSTER",
		"CREATE TABLE IF NOT EXISTS db_summary_5m ON CLUSTER",
	})
}

// ── 4. TEK DÜĞÜM KİPİ — iki yön de kapalı ─────────────────────────────

// TestSingleNodeModeExpectsDataShape — tek düğüm kipinde çıplak ad VERİDİR
// ve dal onu drop+recreate etmelidir (kasıtlı davranış).
func TestSingleNodeModeExpectsDataShape(t *testing.T) {
	c := &shapeConn{hosts: []bareHostShape{{Host: "shop-ch", Engine: "AggregatingMergeTree"}}, innerUUID: shapeInnerID}
	s := shapeStore(c, "")

	if err := s.upgradeMVDim(context.Background(), dimDBSummary, shapeDimDDL); err != nil {
		t.Fatalf("tek düğüm göçü düştü: %v", err)
	}
	assertExecSequence(t, c.execs, []string{
		"DROP TABLE IF EXISTS `.inner_id." + shapeInnerID + "` SYNC SETTINGS max_table_size_to_drop = 0",
		"DROP TABLE IF EXISTS db_summary_5m SYNC",
		"CREATE MATERIALIZED VIEW IF NOT EXISTS db_summary_5m",
	})
	for _, e := range c.execs {
		if strings.Contains(e, "_local") || strings.Contains(e, "ON CLUSTER") {
			t.Errorf("tek düğümde `_local`/ON CLUSTER diye bir şey YOK: %s", e)
		}
	}
}

// TestSingleNodeModeSkipsWhenBareNameIsWrapper — ÖNEMLİ 3, TERS YÖN.
// `cluster_name` BOŞ olması çıplak adın sarmalayıcı OLMADIĞINI kanıtlamaz
// (elle uygulanmış `_local`+wrapper göçü). Eski kapı burada hiç sorgu
// yapmadan true dönüyordu: gerçek sarmalayıcı düşüp yerine host-yerel MV
// kuruluyor ve kümenin fan-out okuma yolu tek düğümün dilimine iniyordu.
func TestSingleNodeModeSkipsWhenBareNameIsWrapper(t *testing.T) {
	c := &shapeConn{hosts: []bareHostShape{{Host: "shop-ch", Engine: "Distributed"}}, innerUUID: shapeInnerID}
	s := shapeStore(c, "")

	if err := s.upgradeMVDim(context.Background(), dimDBSummary, shapeDimDDL); err != nil {
		t.Fatalf("atlama HATA değildir: %v", err)
	}
	if len(c.execs) != 0 {
		t.Errorf("cluster_name boş ama ad Distributed — sarmalayıcıyı düşürüp yerine yerel MV KURMAMALIYIZ. "+
			"Koşan ifadeler: %v", c.execs)
	}
}

// TestShapeGateIsInvisibleForUnpromotedMV — TERFİ EDİLMEMİŞ MV'de
// (highVolumeTables dışı) küme kipinde bile çıplak ad OBJENİN KENDİSİDİR
// (v0.8.388) ve `_local` kardeşi HİÇ yoktur. Kapı orada da beklenti
// karşılaştırması yapsaydı Data görüp dalı KALICI olarak atardı.
//
// Bu kol bugün hiçbir göç hedefi tarafından kullanılmıyor (hepsi
// highVolumeTables üyesi) — tam da bu yüzden PİNLENİYOR: kimsenin sürmediği
// bir kol sessizce silinir ya da bozulur ([[feedback-tested-but-unreachable]]).
func TestShapeGateIsInvisibleForUnpromotedMV(t *testing.T) {
	const unpromoted = "shop_unpromoted_5m"
	if highVolumeTables[unpromoted] {
		t.Fatalf("%s terfi edilmiş görünüyor — test adı yanlış seçilmiş", unpromoted)
	}
	c := &shapeConn{hosts: []bareHostShape{{Host: "ch-01", Engine: "MaterializedView"}}}
	s := shapeStore(c, shapeCluster)
	if !s.mvMigrationShapeOK(context.Background(), unpromoted, "test") {
		t.Error("terfi edilmemiş MV'de kapı HER ZAMAN açık olmalı — `_local` kardeşi yok, çıplak ad objenin kendisi")
	}
	if len(c.queries) != 0 {
		t.Errorf("terfi edilmemiş MV için şekil okuması yapılmamalı (gereksiz boot sorgusu): %v", c.queries)
	}
	// …ve bugünkü göç hedeflerinin HİÇBİRİ bu kola düşmemeli; düşseydi kapı
	// o dal için sessizce devre dışı kalırdı.
	for _, m := range mvDimMigrations {
		if !highVolumeTables[m.Table] {
			t.Errorf("%s terfi edilmemiş ama göç defterinde — kapı o dalda devre dışı kalır", m.Table)
		}
	}
}

// ── 5. resolvedStorageName — kurtarma dallarının kapısı ───────────────

// TestResolvedStorageNameMeasuresInsteadOfAssuming — `mvStorageName` SAF bir
// ad dönüşümü ve "küme kipindeyim, demek ki `_local` var" varsayımını taşır.
// Uyuşmazlıkta bu varsayım probe'u var olmayan bir tabloya yöneltir.
func TestResolvedStorageNameMeasuresInsteadOfAssuming(t *testing.T) {
	ctx := context.Background()
	t.Run("gerçek sarmalayıcı → _local", func(t *testing.T) {
		s := shapeStore(&shapeConn{hosts: wrapperHosts()}, shapeCluster)
		got, err := s.resolvedStorageName(ctx, "spans")
		if err != nil || got != "spans_local" {
			t.Errorf("got %q err %v", got, err)
		}
	})
	t.Run("hiç yok → _local (sarmalayıcı henüz kurulmadı)", func(t *testing.T) {
		s := shapeStore(&shapeConn{}, shapeCluster)
		got, err := s.resolvedStorageName(ctx, "spans")
		if err != nil || got != "spans_local" {
			t.Errorf("got %q err %v", got, err)
		}
	})
	t.Run("TEK DÜĞÜM ŞEKLİ → çıplak ad (mvStorageName yanılırdı)", func(t *testing.T) {
		s := shapeStore(&shapeConn{hosts: []bareHostShape{{Host: "ch-01", Engine: "MergeTree"}}}, shapeCluster)
		got, err := s.resolvedStorageName(ctx, "spans")
		if err != nil {
			t.Fatalf("err %v", err)
		}
		if got != "spans" {
			t.Errorf("got %q — uyuşmazlıkta depo ÇIPLAK addır; `spans_local` sorulsaydı 'kolon yok' çıkar ve kurtarma YANLIŞ ateşlerdi", got)
		}
		if want := s.mvStorageName("spans"); want == got {
			t.Errorf("test anlamsız: mvStorageName de %q döndü", want)
		}
	})
	t.Run("okunamadı → HATA (çağıran yıkıcı hiçbir şey yapmamalı)", func(t *testing.T) {
		s := shapeStore(&shapeConn{shapeErr: fmt.Errorf("code: 159")}, shapeCluster)
		if _, err := s.resolvedStorageName(ctx, "spans"); err == nil {
			t.Error("şekil okunamazken depo adı 'çözüldü' sayılmamalı")
		}
	})
	t.Run("tek düğüm kipi → ölçüm yok, çıplak ad", func(t *testing.T) {
		c := &shapeConn{}
		s := shapeStore(c, "")
		got, err := s.resolvedStorageName(ctx, "spans")
		if err != nil || got != "spans" {
			t.Errorf("got %q err %v", got, err)
		}
		if len(c.queries) != 0 {
			t.Errorf("tek düğümde `_local` diye bir ihtimal yok, ölçüm gereksiz: %v", c.queries)
		}
	})
}

// TestDropIngestBlockingMVNeverDropsWhileBlind — KRİTİK 1(b): şekil
// okunamazken kurtarma dalı da yıkmaz.
func TestDropIngestBlockingMVNeverDropsWhileBlind(t *testing.T) {
	c := &shapeConn{shapeErr: fmt.Errorf("code: 202, TOO_MANY_SIMULTANEOUS_QUERIES"), innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)
	s.dropIngestBlockingMV(context.Background(), "operation_group_summary_5m", "op_group")
	if len(c.execs) != 0 {
		t.Errorf("şekil okunamazken MV düşürülmemeli: %v", c.execs)
	}
}

// TestDropIngestBlockingMVUsesGuardSafeDrop — KRİTİK 1(c): ham `DROP VIEW`
// değil, iç tabloyu hacim-guard'ıyla düşüren dropCombinedMV.
func TestDropIngestBlockingMVUsesGuardSafeDrop(t *testing.T) {
	c := &shapeConn{hosts: wrapperHosts(), innerUUID: shapeInnerID}
	s := shapeStore(c, shapeCluster)
	s.dropIngestBlockingMV(context.Background(), "operation_group_summary_5m", "op_group")
	assertExecSequence(t, c.execs, []string{
		"DROP TABLE IF EXISTS `.inner_id." + shapeInnerID + "` ON CLUSTER `" + shapeCluster + "` SYNC SETTINGS max_table_size_to_drop = 0",
		"DROP TABLE IF EXISTS operation_group_summary_5m_local ON CLUSTER `" + shapeCluster + "` SYNC",
	})
	for _, e := range c.execs {
		if strings.HasPrefix(strings.TrimSpace(e), "DROP VIEW") {
			t.Errorf("ham DROP VIEW geri gelmiş: %s", e)
		}
	}
}

// ── 6. İKİNCİL: HER yıkıcı dal ŞEKLİ ÖLÇÜYOR mu (kaynaktan SAYARAK) ───

// TestEveryDestructiveBranchIsShapeMeasured — ÖNEMLİ 6. Sabit bir Contains
// listesi kapısız YENİ bir dalı göremezdi. Bu kapı dalları KAYNAKTAN sayar:
// `dropCombinedMV` çağıran HER fonksiyon, en az o kadar şekil-ölçen çağrı
// (`mvMigrationShapeOK` ya da `resolvedStorageName`) içermek ZORUNDA.
//
// KÜÇÜK 9: kapı artık KONUM değil KAPI ölçüyor — gövde hangi dosyaya taşınırsa
// taşınsın aynı iddia geçerli ve kızarma mesajı doğru teşhis veriyor.
func TestEveryDestructiveBranchIsShapeMeasured(t *testing.T) {
	const (
		dropFn = "dropCombinedMV"
	)
	gateFns := map[string]bool{"mvMigrationShapeOK": true, "resolvedStorageName": true}

	fset := token.NewFileSet()
	totalDrops := 0
	for _, file := range []string{"store.go", "mv_shape_guard.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == dropFn {
				continue // dropCombinedMV'nin KENDİ gövdesi konu dışı
			}
			drops, gates := 0, 0
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch {
				case sel.Sel.Name == dropFn:
					drops++
				case gateFns[sel.Sel.Name]:
					gates++
				}
				return true
			})
			totalDrops += drops
			if drops > gates {
				t.Errorf("%s: %s() içinde %d adet %s çağrısı var ama yalnız %d şekil ölçümü — "+
					"yıkıcı bir dal şekli ÖLÇMEDEN koşuyor (v0.10.834 sınıfı: çıplak ad sarmalayıcı sanılıp DROP edilir)",
					file, fn.Name.Name, drops, dropFn, gates)
			}
		}
	}
	// Ayrıştırma sessizce boşa düşerse — ya da bir dal dropCombinedMV'yi
	// bırakıp ham bir DROP'a dönerse — kapı yeşil kalırdı. Taban SAYI:
	// bugünkü sekiz çağrının biri bile kaybolursa burası kızarır.
	if totalDrops < 8 {
		t.Errorf("yalnız %d adet %s çağrısı bulundu — kapı gövdeleri göremiyor olabilir (ayrıştırma/ad değişimi)", totalDrops, dropFn)
	}
}

// TestNoRawDropViewOnHighVolumeName — ham `DROP VIEW <yüksek hacimli ad>`
// paket genelinde YASAK; doğru yol dropCombinedMV(resolvedStorageName(...)).
// execDDL bunu çalışma anında da reddeder (bareDestructiveTarget), bu kapı
// niyeti kaynakta yakalar.
func TestNoRawDropViewOnHighVolumeName(t *testing.T) {
	// Yazım DEĞİL, FİİL yasak. Adı tek tek aramak (`"DROP VIEW … spans"`)
	// birleştirmeli bir yazımı ( `"DROP VIEW IF EXISTS "+mv` ) kaçırırdı —
	// ölçülmüş tuzak ([[feedback-gate-single-spelling]]). Doğru yol hiçbir
	// zaman DROP VIEW üretmez, o yüzden fiilin KENDİSİ bu dosyalarda yasak.
	for _, file := range []string{"store.go", "mv_shape_guard.go", "cluster.go"} {
		code := stripGoComments(readGoSource(t, file))
		if i := strings.Index(strings.ToUpper(code), "DROP VIEW"); i >= 0 {
			t.Errorf("%s: `DROP VIEW` — adaptDDL DROP'u yeniden yazmaz, ifade şekil kapısını atlar; "+
				"dropCombinedMV(s.resolvedStorageName(ctx, <ad>)) kullan. Bağlam: %.100s", file, code[max(0, i-40):])
		}
	}
}
