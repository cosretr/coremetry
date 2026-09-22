package chstore

// mv_leftover_test.go — v0.10.830 MV artığı sınıflandırması (güncel /
// kalıntı / öksüz) ve iki düğüm-yerel eylemin kapıları.
//
// Sözleşme: operatörün test kümesinde (is_local kör host'lar, küme IP ile
// tanımlı) v0.10.825 kartı "MV'ler sağlıklı · 21 MV × 4 host" derken Replika
// tutarlılığı kartı `.inner_id.<uuid>` satırlarında KALICI kırmızı
// gösteriyordu. Ölçülmeyen iki sınıf vardı: terfi öncesi ÇIPLAK MV (kendi
// iç tablosuyla; promoteCombinedMVs dört adı, ensureDistributedWrappers
// yalnız Replicated* motorları taşıdığı için YAPISAL olarak kalıcı) ve
// SAHİPSİZ iç tablo. Bu testler sınıflandırmayı, tek düğümde kalıntı
// üretilemediğini ve sahibi BAŞKA host'ta duran bir uuid'nin öksüz
// SAYILMADIĞINI çiviler.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// v0.10.832 — bu sabitler VIEW uuid'leridir: iç tablonun ADI onlardan doğar
// (`.inner_id.<view uuid>`). Nesne uuid'si (`TO INNER UUID`) ayrı bir
// değerdir ve ada hiç girmez — lfObjectOf ile türetilir.
const (
	lfCurView  = "aaaaaaaa-1111-2222-3333-444444444444"
	lfBareView = "bbbbbbbb-1111-2222-3333-444444444444"
	lfOrphan   = "cccccccc-1111-2222-3333-444444444444"
)

// lfObjectOf — view uuid'sinden TÜREYEN, ona ASLA eşit olmayan nesne uuid'si
// (CH `to_inner_uuid == view uuid` durumunu yasaklar).
func lfObjectOf(viewUUID string) string { return "9" + viewUUID[1:] }

// mvRowFor — Atomic DB'de combined MV satırı, ayar AÇIK biçimi: metin hem
// view uuid'sini (ADIN kaynağı) hem nesne uuid'sini taşır.
func mvRowFor(host, name, viewUUID string) mvTableRow {
	return mvTableRow{Host: host, Name: name, UUID: viewUUID, Engine: "MaterializedView",
		CreateQuery: "CREATE MATERIALIZED VIEW coremetry." + name + " UUID '" + viewUUID +
			"' TO INNER UUID '" + lfObjectOf(viewUUID) + "' (x Int) ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1"}
}

// innerRowFor — CANLI iç tablo: adı VIEW uuid'sinden, uuid KOLONU nesne
// uuid'si.
func innerRowFor(host, viewUUID, engine string) mvTableRow {
	return mvTableRow{Host: host, Name: innerTablePrefix + viewUUID, UUID: lfObjectOf(viewUUID), Engine: engine}
}

// testTargetSet — ÖLÇÜLMÜŞ hedef kümesi (v0.10.833). uuids unexported
// olduğu için paket dışından kurulamaz; testler de tek gövdeden kurar.
func testTargetSet(objectUUIDs ...string) MVTargetSet {
	m := map[innerObjectUUID]bool{}
	for _, u := range objectUUIDs {
		m[innerObjectUUID(strings.ToLower(u))] = true
	}
	return MVTargetSet{Measured: true, uuids: m}
}

// testStorage — mvStorageName'in saf ikizi: highVolume adlar küme kipinde
// `_local`. Gerçek gövde cluster.go'da; burada tablo sürücüsü.
func testStorage(highVolume map[string]bool, cluster bool) func(string) string {
	return func(mv string) string {
		if cluster && highVolume[mv] {
			return mv + "_local"
		}
		return mv
	}
}

func TestMVLeftoversFromRows(t *testing.T) {
	hv := map[string]bool{"db_summary_5m": true, "service_summary_5m": true}
	cases := []struct {
		name    string
		rows    []mvTableRow
		cluster bool
		// targets — v0.10.833: MV'lerin `TO INNER UUID` kümesi. Sıfır değer =
		// ÖLÇÜLMEDİ; o hâlde öksüz satırı görünür ama DÜĞMESİZ (Blocked).
		targets MVTargetSet
		// want: host/iç tablo → sınıf ("" = bulgu YOK)
		want map[string]string
		view map[string]string // host/iç tablo → beklenen view adı (kalıntıda)
		// wantBlocked: host/iç tablo → Blocked metninin içermesi gereken parça
		wantBlocked map[string]string
	}{
		{
			name: "güncel _local bulgu değil",
			rows: []mvTableRow{
				mvRowFor("ch-01", "db_summary_5m_local", lfCurView),
				innerRowFor("ch-01", lfCurView, "ReplicatedAggregatingMergeTree"),
			},
			cluster: true,
			want:    map[string]string{"ch-01/" + lfCurView: ""},
		},
		{
			name: "çıplak MV = kalıntı; _local yanında dursa bile",
			rows: []mvTableRow{
				mvRowFor("ch-02", "db_summary_5m_local", lfCurView),
				innerRowFor("ch-02", lfCurView, "ReplicatedAggregatingMergeTree"),
				mvRowFor("ch-02", "db_summary_5m", lfBareView),
				innerRowFor("ch-02", lfBareView, "AggregatingMergeTree"),
			},
			cluster: true,
			want: map[string]string{
				"ch-02/" + lfCurView:  "",
				"ch-02/" + lfBareView: MVLeftoverBare,
			},
			view: map[string]string{"ch-02/" + lfBareView: "db_summary_5m"},
		},
		{
			name: "sahibi hiç yok → öksüz",
			rows: []mvTableRow{
				mvRowFor("ch-01", "db_summary_5m_local", lfCurView),
				innerRowFor("ch-01", lfCurView, "ReplicatedAggregatingMergeTree"),
				innerRowFor("ch-01", lfOrphan, "AggregatingMergeTree"),
			},
			cluster: true,
			targets: testTargetSet(lfObjectOf(lfCurView)),
			want:    map[string]string{"ch-01/" + lfOrphan: MVLeftoverOrphan},
		},
		{
			// v0.10.833 — ASIL TEHLİKE: adı kimsenin adreslemediği iç tablo,
			// NESNE uuid'siyle bir MV'nin HEDEFİ. MV ona YAZIYOR; "Öksüzü
			// temizle" CANLI toplamayı silerdi. v0.10.780–831 "Eşten kur"
			// yolunun bıraktığı şekil tam olarak budur.
			name: "nesne uuid'si bir MV'nin HEDEFİ → öksüz DEĞİL",
			rows: []mvTableRow{
				mvRowFor("ch-01", "db_summary_5m_local", lfCurView),
				innerRowFor("ch-01", lfCurView, "ReplicatedAggregatingMergeTree"),
				// adı `.inner_id.<lfOrphan>` ama uuid KOLONU lfCurView'ın hedefi
				{Host: "ch-01", Name: innerTablePrefix + lfOrphan, UUID: lfObjectOf(lfCurView), Engine: "ReplicatedAggregatingMergeTree"},
			},
			cluster: true,
			targets: testTargetSet(lfObjectOf(lfCurView)),
			want:    map[string]string{"ch-01/" + lfOrphan: ""},
		},
		{
			// Küme ÖLÇÜLMEDİYSE öksüz kararı verilmez: boş küme "hiçbir MV
			// hedeflemiyor" DEMEK DEĞİLDİR. Satır görünür, düğmesi YOK.
			name: "hedef kümesi ölçülmedi → öksüz ama DÜĞMESİZ",
			rows: []mvTableRow{
				mvRowFor("ch-01", "db_summary_5m_local", lfCurView),
				innerRowFor("ch-01", lfCurView, "ReplicatedAggregatingMergeTree"),
				innerRowFor("ch-01", lfOrphan, "AggregatingMergeTree"),
			},
			cluster:     true,
			targets:     MVTargetSet{},
			want:        map[string]string{"ch-01/" + lfOrphan: MVLeftoverOrphan},
			wantBlocked: map[string]string{"ch-01/" + lfOrphan: "ÖLÇÜLMEDİ"},
		},
		{
			// KÜME GENELİ kural: ch-02'de sahip yok ama ch-01'de VAR → ch-02'de
			// öksüz DEĞİL (o uuid ch-01'in GÜNCEL iç tablosu; düşürmek
			// Replicated ZK yolunu onun altından çeker). ch-02'nin derdi
			// "eksik MV"dir ve kapsama kartının işidir.
			name: "sahip BAŞKA host'ta → burada öksüz DEĞİL",
			rows: []mvTableRow{
				mvRowFor("ch-01", "db_summary_5m_local", lfCurView),
				innerRowFor("ch-01", lfCurView, "ReplicatedAggregatingMergeTree"),
				innerRowFor("ch-02", lfCurView, "ReplicatedAggregatingMergeTree"),
			},
			cluster: true,
			want: map[string]string{
				"ch-01/" + lfCurView: "",
				"ch-02/" + lfCurView: "",
			},
		},
		{
			// Tek düğüm: çıplak ad ZATEN kanonik depolama adı → kalıntı sınıfı
			// üretilemez. (Sahipsiz iç tablo tek düğümde de öksüzdür.)
			name: "tek düğüm kalıntı ÜRETMEZ",
			rows: []mvTableRow{
				mvRowFor("ch-01", "db_summary_5m", lfBareView),
				innerRowFor("ch-01", lfBareView, "AggregatingMergeTree"),
			},
			cluster: false,
			want:    map[string]string{"ch-01/" + lfBareView: ""},
		},
		{
			// `TO <tablo>` biçimli MV'nin gizli iç tablosu YOKTUR: ne sahip
			// haritasına girer ne de bir bulgu üretir.
			name: "TO'lu MV: iç tablo yok, bulgu yok",
			rows: []mvTableRow{{Host: "ch-01", Name: "span_links_reverse_mv", UUID: mvTestView, Engine: "MaterializedView",
				CreateQuery: "CREATE MATERIALIZED VIEW coremetry.span_links_reverse_mv TO coremetry.span_links_reverse AS SELECT 1"}},
			cluster: true,
			want:    map[string]string{},
		},
		{
			// Kanonik katalogda olmayan MV (migrations/*.sql): depolama adı
			// bilinmiyor, kalıntı diye YARGILANMAZ.
			name: "kanonik olmayan MV yargılanmaz",
			rows: []mvTableRow{
				mvRowFor("ch-01", "rollup_custom_mv", lfBareView),
				innerRowFor("ch-01", lfBareView, "AggregatingMergeTree"),
			},
			cluster: true,
			want:    map[string]string{"ch-01/" + lfBareView: ""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mvLeftoversFromRows(c.rows, c.cluster, testStorage(hv, c.cluster), nil, c.targets)
			byKey := map[string]MVLeftover{}
			for _, l := range got {
				byKey[l.Host+"/"+l.UUID] = l
			}
			for key, want := range c.want {
				l, has := byKey[key]
				if want == "" {
					if has {
						t.Errorf("%s bulgu üretmemeliydi: %+v", key, l)
					}
					continue
				}
				if !has {
					t.Fatalf("%s bulgusu yok: %+v", key, got)
				}
				if l.Kind != want {
					t.Errorf("%s: sınıf %q, beklenen %q", key, l.Kind, want)
				}
				if l.Inner != innerTablePrefix+strings.Split(key, "/")[1] {
					t.Errorf("%s: iç tablo adı %q", key, l.Inner)
				}
			}
			for key, want := range c.wantBlocked {
				if l := byKey[key]; !strings.Contains(l.Blocked, want) {
					t.Errorf("%s: Blocked %q, %q içermeliydi (ölçülmemiş küme EYLEM AÇMAZ)", key, l.Blocked, want)
				}
			}
			for key, l := range byKey {
				if c.wantBlocked[key] == "" && l.Kind == MVLeftoverOrphan && l.Blocked != "" {
					t.Errorf("%s: ÖLÇÜLMÜŞ kümede öksüz satırı düğmesiz kalmamalı: %q", key, l.Blocked)
				}
			}
			for key, wantView := range c.view {
				l := byKey[key]
				if l.View != wantView {
					t.Errorf("%s: view %q, beklenen %q", key, l.View, wantView)
				}
				if l.Storage != wantView+"_local" {
					t.Errorf("%s: depolama adı %q", key, l.Storage)
				}
			}
			// Bulgu sayısı: want'ta olmayan bir sınıf sessizce doğmasın.
			n := 0
			for _, w := range c.want {
				if w != "" {
					n++
				}
			}
			if len(got) != n {
				t.Errorf("%d bulgu, beklenen %d: %+v", len(got), n, got)
			}
		})
	}
	if got := mvLeftoversFromRows(nil, true, testStorage(hv, true), nil, MVTargetSet{}); got == nil || len(got) != 0 {
		t.Errorf("boş girdi → BOŞ DİZİ (null değil): %+v", got)
	}
}

// v0.10.830 inceleme (MINOR): guarded MV (kaynak kolonu yok → boot MV'yi
// bilerek kurmaz) kalıntısı için kanonik `_local` HİÇ doğmaz, yani
// "_local sağlıklı" kapısı asla geçmez. Satır görünür kalır ama DÜĞMESİZ:
// eylem gösterip 409 atmak var olmayan bir düğmeyi işaret eden bir metin
// üretiyordu.
func TestMVLeftoverGuardedHasNoAction(t *testing.T) {
	hv := map[string]bool{"db_statement_summary_5m": true, "db_summary_5m": true}
	rows := []mvTableRow{
		mvRowFor("ch-01", "db_statement_summary_5m", lfBareView),
		innerRowFor("ch-01", lfBareView, "AggregatingMergeTree"),
		mvRowFor("ch-01", "db_summary_5m", lfCurView),
		innerRowFor("ch-01", lfCurView, "AggregatingMergeTree"),
	}
	guarded := func(base string) bool { return base == "db_statement_summary_5m" }
	got := mvLeftoversFromRows(rows, true, testStorage(hv, true), guarded, MVTargetSet{})
	if len(got) != 2 {
		t.Fatalf("iki kalıntı beklenir: %+v", got)
	}
	for _, l := range got {
		switch l.View {
		case "db_statement_summary_5m":
			if l.Blocked == "" {
				t.Error("guarded kalıntı DÜĞMESİZ olmalı (Blocked dolu)")
			}
			if !strings.Contains(l.Blocked, "db_statement_summary_5m_local") {
				t.Errorf("neden kanonik adı söylemeli: %q", l.Blocked)
			}
		case "db_summary_5m":
			if l.Blocked != "" {
				t.Errorf("guarded olmayan kalıntı düğmeli kalmalı: %q", l.Blocked)
			}
		}
	}
	// guarded=nil (eski çağrı biçimi) hiçbir satırı engellemez.
	for _, l := range mvLeftoversFromRows(rows, true, testStorage(hv, true), nil, MVTargetSet{}) {
		if l.Blocked != "" {
			t.Errorf("guarded yokken engel olmamalı: %+v", l)
		}
	}
}

// v0.10.830 inceleme (KRİTİK): `artik` yapı gereği yalnız highVolume adıdır —
// ürünün SORGULADIĞI çıplak ad. Tek başına DROP o adı host'ta yok eder ve
// is_local-kör host'ta sarmalayıcıyı kuran her yol ON CLUSTER taşıdığı için
// bir daha doğmaz → UNKNOWN_TABLE. Eylem TAMAMLAYICI: TAM 2 ifade.
func TestLeftoverDropStmts(t *testing.T) {
	wrap := "CREATE TABLE IF NOT EXISTS db_summary_5m ON CLUSTER `uptrace_all` AS db_summary_5m_local ENGINE = Distributed(`uptrace_all`, currentDatabase(), db_summary_5m_local, rand())"
	local := distributedWrapperStmt([]string{
		"CREATE MATERIALIZED VIEW IF NOT EXISTS db_summary_5m_local ON CLUSTER `uptrace_all` ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1",
		wrap,
	})
	if strings.Contains(local, "ON CLUSTER") {
		t.Fatalf("sarmalayıcı düğüm-yerel olmalı: %q", local)
	}
	if !strings.Contains(local, "ENGINE = Distributed(") {
		t.Fatalf("sarmalayıcı ifadesi seçilemedi: %q", local)
	}
	// adaptDDL çıktısında Distributed yoksa (high-volume olmayan MV) ifade YOK.
	if got := distributedWrapperStmt([]string{"CREATE MATERIALIZED VIEW x ENGINE = AggregatingMergeTree AS SELECT 1"}); got != "" {
		t.Errorf("Distributed yokken ifade üretilmemeli: %q", got)
	}

	cases := []struct {
		name, view, wrapper string
		wantN               int
	}{
		{"sarmalayıcı var → DROP + CREATE", "db_summary_5m", local, 2},
		{"sarmalayıcı YOK → eylem reddedilir", "db_summary_5m", "", 0},
		{"boşluk sarmalayıcı → reddedilir", "db_summary_5m", "   ", 0},
		{"view boş → reddedilir", "", local, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := leftoverDropStmts(c.view, c.wrapper)
			if len(got) != c.wantN {
				t.Fatalf("%d ifade, beklenen %d: %v", len(got), c.wantN, got)
			}
			if c.wantN == 0 {
				return
			}
			if !strings.HasPrefix(got[0], "DROP TABLE `"+c.view+"` SYNC") || !strings.Contains(got[0], "max_table_size_to_drop") {
				t.Errorf("1. ifade boyut kalkanlı DROP olmalı: %q", got[0])
			}
			if !strings.Contains(got[1], "ENGINE = Distributed(") {
				t.Errorf("2. ifade Distributed sarmalayıcı olmalı: %q", got[1])
			}
			for i, st := range got {
				if strings.Contains(st, "ON CLUSTER") {
					t.Errorf("%d. ifade ON CLUSTER taşıyor (yalnız bu host): %q", i, st)
				}
			}
		})
	}
}

// v0.10.830 inceleme (MAJOR): `_local` kapısı VARLIK + motor ailesi ölçer,
// VERİ ölçmez — yeni kurulmuş boş bir `_local` de "ok"tur. Kalıntı doluyken
// kanonik boşsa temizlik o düğümün TEK dolu toplamasını yok eder.
func TestLeftoverDataGate(t *testing.T) {
	cases := []struct {
		name                      string
		leftoverRows, storageRows uint64
		wantRefuse                bool
	}{
		{"kalıntı dolu + kanonik BOŞ → REDDET", 1_200_000, 0, true},
		{"kalıntı dolu + kanonik dolu → izin", 1_200_000, 900_000, false},
		{"kalıntı BOŞ + kanonik boş → izin", 0, 0, false},
		{"kalıntı BOŞ + kanonik dolu → izin", 0, 900_000, false},
		{"tek satırlık kalıntı bile korunur", 1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := leftoverDataGate("db_summary_5m", "db_summary_5m_local", c.leftoverRows, c.storageRows)
			if (msg != "") != c.wantRefuse {
				t.Fatalf("ret=%v, beklenen %v (%q)", msg != "", c.wantRefuse, msg)
			}
			if c.wantRefuse {
				for _, want := range []string{"db_summary_5m_local", "BOŞ", "TAŞIMAZ"} {
					if !strings.Contains(msg, want) {
						t.Errorf("ret metni %q içermeli: %q", want, msg)
					}
				}
			}
		})
	}
}

// Sahip haritası TEK GÖVDE: v0.10.824'ün innerTableViews'ü mvOwnersByHost'tan
// türer ve host ekseni korunur (küme geneli "view: X" o HOST'ta X var demek
// DEĞİLDİR — 830'un kalıcı kırmızı satırının kökü buydu).
func TestMVOwnersByHostSingleBody(t *testing.T) {
	rows := []mvTableRow{
		mvRowFor("ch-01", "db_summary_5m_local", lfCurView),
		mvRowFor("ch-02", "db_summary_5m", lfBareView),
		innerRowFor("ch-02", lfBareView, "AggregatingMergeTree"),
	}
	owners := mvOwnersByHost(rows)
	if owners["ch-01"][innerTablePrefix+lfCurView] != "db_summary_5m_local" {
		t.Errorf("ch-01 sahibi: %v", owners["ch-01"])
	}
	if _, has := owners["ch-01"][innerTablePrefix+lfBareView]; has {
		t.Error("ch-02'nin çıplak MV'si ch-01'e sızdı — host ekseni kayboldu")
	}
	all := innerTableOwners(rows)
	o := all[innerTablePrefix+lfBareView]
	if o.View != "db_summary_5m" || strings.Join(o.Hosts, ",") != "ch-02" {
		t.Errorf("küme geneli sahip: %+v", o)
	}
	// innerTableViews düzleştirmedir: aynı gövde, aynı sonuç.
	if innerTableViews(rows)[innerTablePrefix+lfCurView] != "db_summary_5m_local" {
		t.Error("innerTableViews artık aynı gövdeden türemiyor")
	}
}

// Kaynak pinleri: eylemler NODE-YEREL bağlantıda, ON CLUSTER'sız, boyut
// kalkanlı; durum Exec'ten ÖNCE TAZE ölçülür; iç tablo adı SUNUCUDA kurulur.
func TestMVLeftoverActionPins(t *testing.T) {
	b, err := os.ReadFile("mv_leftover.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"s.shardConn(", "purgeGuard", "` SYNC", "mvUUIDRe",
		"context.WithTimeout(ctx, mvLeftoverStepTimeout)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	// Koşulan ifadelerde ON CLUSTER YOK (yorumlar serbest).
	for _, m := range regexp.MustCompile(`"[^"\n]*ON CLUSTER[^"\n]*"`).FindAllString(src, -1) {
		t.Errorf("koşulan ifadede ON CLUSTER: %s", m)
	}
	// system.* okumaları tavanlı.
	if n, m := strings.Count(src, "FROM system."), strings.Count(src, "max_execution_time"); m < n {
		t.Errorf("system.* okumaları tavanlı olmalı: %d okuma, %d tavan", n, m)
	}
	// uuid kalıbı TAM 36 karakter ve nokta kabul etmez: istemci
	// `.inner_id.…` adı gönderemez, ad sunucuda kurulur.
	for _, bad := range []string{".inner_id." + lfOrphan, "abc", lfOrphan + "x", "../etc", ""} {
		if mvUUIDRe.MatchString(bad) {
			t.Errorf("uuid kalıbı %q kabul etti", bad)
		}
	}
	if !mvUUIDRe.MatchString(lfOrphan) || !mvUUIDRe.MatchString(strings.ToUpper(lfOrphan)) {
		t.Error("geçerli uuid reddedildi")
	}
	if chObjRe.MatchString(innerTablePrefix + lfOrphan) {
		t.Error("chObjRe iç tablo adını kabul ediyor — nokta kapısı kayboldu")
	}

	drop := funcBody(t, "mv_leftover.go", "func (s *Store) DropLeftoverMV(")
	for _, want := range []string{
		"chObjRe.MatchString(view)",  // ad kapısı
		"!s.clusterMode()",           // tek düğümde sınıf yok
		"canonicalMVForObject(view)", // kanonik katalog
		"storage == view",            // kanonik depolama adı düşürülmez
		"s.mvGuardedOff(base)",       // guarded MV'nin _local'i hiç doğmaz
		"distributedWrapperStmt(",    // sarmalayıcı ifadesi (tek gövde: adaptDDL)
		"s.adaptDDL(canonicalMVDDL(base))",
		"s.MVLeftovers(ctx, rep.Targets)",  // TAZE tespit + hedef kümesi (v0.10.833)
		"s.mvCoverageReport(ctx, false)",   // TAZE kapsama; kapaklı SÜPÜRME atlanır (D)
		"s.mvHardenCell(ctx, storageCell,", // kapının hedef kolu ÖLÇÜLÜR
		"mvLeftoverStorageGate(storage, ",  // <base>_local sağlıklı VE hedefini çözebiliyor olmalı
		"s.innerTableSizes(ctx, cluster)",  // VERİ kapısı, hata denetimli
		"leftoverDataGate(",
		"leftoverDropStmts(view, wrapper)",
		`engine != "MaterializedView"`, // Distributed sarmalayıcıya dokunma
		"s.shardConn(ctx, row.Addr)",   // yalnız o host
		`bareEngine != "Distributed"`,  // doğrulama: ad geri geldi mi
	} {
		if !strings.Contains(drop, want) {
			t.Errorf("DropLeftoverMV içinde eksik: %s", want)
		}
	}
	// Kapıların HEPSİ Exec'ten ÖNCE — sarmalayıcı ve veri kapısı dahil.
	for _, gate := range []string{
		"mvLeftoverStorageGate(storage, ", `engine != "MaterializedView"`, "s.MVLeftovers(ctx, rep.Targets)",
		"distributedWrapperStmt(", "leftoverDataGate(", "s.mvGuardedOff(base)",
	} {
		if i, j := strings.Index(drop, gate), strings.Index(drop, "conn.Exec("); i < 0 || j < 0 || i > j {
			t.Errorf("%s kapısı DROP'tan ÖNCE olmalı", gate)
		}
	}
	// Koşulan ifadeler TEK gövdeden (leftoverDropStmts) gelir: DropLeftoverMV
	// kendi DROP metnini KURMAZ, yoksa sarmalayıcı yarısı sessizce düşerdi.
	if strings.Contains(drop, "\"DROP TABLE `\"") {
		t.Error("DROP metni burada kurulmuş — adım listesi leftoverDropStmts'ten gelmeli")
	}
	if !strings.Contains(drop, "for _, st := range stmts {") {
		t.Error("iki ifade de koşmalı (tek ifadelik Exec sarmalayıcıyı atlar)")
	}

	orph := funcBody(t, "mv_leftover.go", "func (s *Store) DropOrphanInner(")
	for _, want := range []string{
		"mvUUIDRe.MatchString(uuid)",
		"innerTableName(uuid)",            // ad SUNUCUDA, tek gövdeden kurulur
		"s.MVLeftovers(ctx, rep.Targets)", // küme geneli sıfır referans + hedef kümesi, TAZE
		"MVLeftoverOrphan",                // yalnız öksüz sınıfı
		"s.distributedRefs(ctx,",          // sarmalayıcı denetimi
		`strings.HasPrefix(row.InnerEngine, "Replicated")`,
		"total <= 1", // son kayıtlı replika düşürülmez
	} {
		if !strings.Contains(orph, want) {
			t.Errorf("DropOrphanInner içinde eksik: %s", want)
		}
	}
	for _, gate := range []string{"MVLeftoverOrphan", "s.distributedRefs(ctx,", "total <= 1"} {
		if i, j := strings.Index(orph, gate), strings.Index(orph, "conn.Exec("); i < 0 || j < 0 || i > j {
			t.Errorf("%s kapısı DROP'tan ÖNCE olmalı", gate)
		}
	}
	// Envanter TEK kaynak: ikinci bir system.tables taraması açılmadı
	// (mvInventory — skip_unavailable_shards YOK, erişilemeyen host HATA).
	if !strings.Contains(src, "s.mvInventorySnap(ctx)") { // v0.10.848 — snapshot sarmalayıcısı, kaynak yine mvInventory
		t.Error("MVLeftovers mvInventory'den okumalı")
	}
	// Muhafız KENDİ metnini ısırmasın: yorumlarda ayarın adı geçer, ARANAN
	// şey SQL'deki ATAMADIR.
	if strings.Contains(src, "skip_unavailable_shards = 1") {
		t.Error("artık taraması skip_unavailable_shards taşımamalı — düşen host sahte 'öksüz' üretir")
	}
}
