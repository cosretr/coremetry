package chstore

// mv_shape_guard.go — v0.10.834. VERİ KAYBI KAPISI.
//
// SORUN. `clusterMode()` YALNIZCA cfg.ClusterName dolu mu diye bakar
// (cluster.go) — veritabanının GERÇEK şekline değil. Operatörün test
// kümesinde bu ikisi ayrıştı: cluster_name DOLU ama `spans` ve bazı MV'ler
// hâlâ TEK DÜĞÜM şeklinde (çıplak ad = MV'nin KENDİSİ, `_local` kardeşi YOK).
// Boot'taki MV yükseltme dalları çıplak adın "metadata-only Distributed
// sarmalayıcı" olduğunu VARSAYIP DROP ediyordu → gizli iç tabloya
// (`.inner_id.<uuid>`) kaskad → tarihçe yanıyor, hiçbir yerde hata görünmüyor.
// trace_summary_5m dalında DROP ayrıca `purgeGuard` taşıyordu, yani CH'nin
// kazara-DROP emniyeti BİLEREK kapalıydı.
//
// ÜÇ SÖZLEŞME (834 inceleme turunda keskinleştirildi):
//
//  1. ÖLÇÜM EYLEMİN KAPSAMINDA YAPILIR. Onayladığımız ifadeler ON CLUSTER;
//     o yüzden şekil de KÜME GENELİNDE okunur (`clusterAllReplicas`).
//     Koordinatörde sarmalayıcı + başka host'ta terfi öncesi çıplak MV
//     gerçek bir hâl (mv_leftover.go `MVLeftoverBare`, v0.10.830) ve tek-host
//     okuma onu göremez. `skip_unavailable_shards` BİLEREK yok: erişilemeyen
//     host bir HATA'dır, "orada sorun yok" değil.
//
//  2. KAPI BEKLENTİ KARŞILAŞTIRMASIDIR, izin listesi değil. Küme kipinde
//     BEKLENEN şekil Wrapper|Absent; tek düğüm kipinde Data|Absent.
//     `cluster_name` boş olması çıplak adın sarmalayıcı OLMADIĞINI
//     kanıtlamaz (elle uygulanmış `_local`+wrapper göçü, dış-Distributed
//     degrade kipi): orada Wrapper görürsek de dalı atlarız, yoksa gerçek
//     sarmalayıcıyı düşürüp yerine host-yerel MV kurar ve kümenin fan-out
//     okuma yolunu tek düğümün dilimine indiririz.
//
//  3. BİLMİYORKEN YIKMA. Şekil okunamadıysa (Unknown) dal atlanır; boot
//     DÜŞMEZ, probe her boot yeniden koşar.
//
// Dalın YALNIZ DROP'unu atlamak YETMEZ: dal devam ederse `execDDL(findMV(…))`
// küme şeklinde `<ad>_local` kurmaya çalışır, gövdesi `FROM spans_local`
// olur ve tek düğüm şeklinde o tablo yoktur → ya boot hatası ya da gerçek
// MV'nin yanında ÖLÜ bir `_local`. Bu yüzden kapı DALIN TAMAMINI keser.
//
// KAPSAM DIŞI (bilinçli): terfi sihirbazı yok, "tablo şekli" kartı yok,
// boot hard-error'a çevrilmedi.

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
)

// ── 1. SAF ÇEKİRDEK ───────────────────────────────────────────────────

// bareNameIsWrapper — SAF: çıplak ad GERÇEKTEN ince Distributed sarmalayıcı mı.
// Yalnız 'Distributed' motoru bunu garanti eder; MaterializedView / *MergeTree
// ise o ad VERİNİN KENDİSİDİR ve DROP kaskadla tarihçeyi götürür.
func bareNameIsWrapper(engine string) bool {
	return strings.TrimSpace(engine) == "Distributed"
}

// bareShape — çıplak adın, KÜME GENELİNDE, üç (+1) hâli.
type bareShape uint8

const (
	// bareShapeWrapper — HER host'ta ince Distributed sarmalayıcı.
	bareShapeWrapper bareShape = iota
	// bareShapeAbsent — hiçbir host'ta yok: düşürülecek bir şey de yok.
	bareShapeAbsent
	// bareShapeData — EN AZ BİR host'ta ad verinin kendisi. DROP = kayıp.
	bareShapeData
	// bareShapeUnknown — şekil okunamadı. Bilmiyorken yıkmayız.
	bareShapeUnknown
)

func (sh bareShape) String() string {
	switch sh {
	case bareShapeWrapper:
		return "Wrapper"
	case bareShapeAbsent:
		return "Absent"
	case bareShapeData:
		return "Data"
	default:
		return "Unknown"
	}
}

// bareHostShape — çıplak adın BİR host'taki motoru. Şekil bir host
// LİSTESİDİR: tek bir skaler, ON CLUSTER bir DROP'un kapsamını ifade edemez.
type bareHostShape struct {
	Host   string
	Engine string
}

// classifyBareNameHosts — SAF karar. hosts: adın bulunduğu host'lar (adı
// taşımayan host listede YOKTUR). probeErr: okuma hatası.
//
// Kural KATI: bir tek host'ta bile ad veriyse şekil Data'dır. ON CLUSTER bir
// DROP o host'un gerçek MV'sini ve iç tablosunu götürürdü; "çoğunluk
// sarmalayıcı" diye bir emniyet yok.
//
// İkinci dönüş, operatöre HANGİ host'un sorunlu olduğunu söyleyen ayrıntı.
// Sıralı: mesaj host sırasına göre oynamasın.
func classifyBareNameHosts(hosts []bareHostShape, probeErr error) (bareShape, string) {
	if probeErr != nil {
		return bareShapeUnknown, ""
	}
	if len(hosts) == 0 {
		return bareShapeAbsent, ""
	}
	var bad, good []string
	for _, h := range hosts {
		if bareNameIsWrapper(h.Engine) {
			good = append(good, h.Host)
			continue
		}
		bad = append(bad, h.Host+"="+strings.TrimSpace(h.Engine))
	}
	sort.Strings(bad)
	sort.Strings(good)
	if len(bad) > 0 {
		return bareShapeData, strings.Join(bad, ", ")
	}
	return bareShapeWrapper, "Distributed@" + strings.Join(good, ", ")
}

// bareShapeMatchesExpectation — SAF. Kapının kalbi: kipin BEKLEDİĞİ şekil mi?
//
//   - Unknown → asla (bilmiyorken yıkmayız).
//   - Absent  → her kipte kabul: düşürülecek bir şey yok, dal bugünkü gibi
//     koşar (`DROP … IF EXISTS` zaten no-op'tu).
//   - küme kipi → Wrapper beklenir; Data ise TEK DÜĞÜM ŞEKLİ.
//   - tek düğüm kipi → Data beklenir; Wrapper ise ayarın bilmediği bir
//     DAĞITIK şekil (ters yön, aynı sınıf).
func bareShapeMatchesExpectation(sh bareShape, clusterMode bool) bool {
	switch sh {
	case bareShapeUnknown:
		return false
	case bareShapeAbsent:
		return true
	case bareShapeWrapper:
		return clusterMode
	default: // bareShapeData
		return !clusterMode
	}
}

// bareShapeSkipReason — SAF: atlama loglarının gövdesi. Operatöre NE
// YAPACAĞINI söyler; "atlandı" tek başına bir sonraki boot'ta unutulur.
//
// effect, ATLAMANIN O DAL İÇİN sonucudur ve çağırandan gelir: tek bir genel
// cümle ("eski şemasıyla okunmaya devam eder") dalların yarısında YANLIŞ
// olurdu — apdex/db_name dallarında MV eski şemada kalır ama yeni boyutu
// kullanan okuma SIFIR satır döndürür, "devam eder" demek yanıltıcıdır.
func bareShapeSkipReason(mv, detail string, sh bareShape, clusterMode bool, effect string) string {
	switch {
	case sh == bareShapeUnknown:
		scope := "system.tables"
		if clusterMode {
			// NOT: metinde `clusterAllReplicas` + parantez YAZILMAZ —
			// TestClusterAllReplicasTableArgQualified kaynağı metin olarak
			// tarıyor ve kendi gerekçe cümlemizi çağrı sanıp kızarıyordu
			// ([[feedback-gate-matches-its-own-text]]).
			scope = "küme geneli system.tables okuması — erişilemeyen host da HATA sayılır"
		}
		return fmt.Sprintf(
			"[chstore] `%s` çıplak adının şekli OKUNAMADI (%s) — MV göçü bu boot'ta ATLANDI "+
				"(bilinmeyen şekilde DROP koşmayız); sonraki boot yeniden dener. Bu boot'ta: %s",
			mv, scope, effect)
	case sh == bareShapeData: // küme kipi bekliyordu
		return fmt.Sprintf(
			"[chstore] TEK DÜĞÜM ŞEKLİ SAPTANDI: cluster_name dolu ama `%s` çıplak adı Distributed "+
				"sarmalayıcı DEĞİL (%s) — o ad VERİNİN KENDİSİ. MV göçü bu obje için ATLANDI, "+
				"tarihçe KORUNDU. Gerekli işlem: objeyi dağıtık şekle TERFİ ettir (%s_local + "+
				"Distributed sarmalayıcı), sonra yeniden boot et. Terfiye kadar: %s",
			mv, detail, mv, effect)
	default: // bareShapeWrapper, tek düğüm kipi bekliyordu
		return fmt.Sprintf(
			"[chstore] DAĞITIK ŞEKİL SAPTANDI: cluster_name BOŞ ama `%s` çıplak adı Distributed "+
				"sarmalayıcı (%s) — sarmalayıcıyı düşürüp yerine host-yerel bir MV KURMAYIZ "+
				"(kümenin fan-out okuma yolu tek düğümün dilimine inerdi). MV göçü ATLANDI. "+
				"Gerekli işlem: config.clickhouse.cluster_name'i kümenin adına ayarla. O ana kadar: %s",
			mv, detail, effect)
	}
}

// bareDestructiveTarget — SAF. YAPISAL muhafız (v0.10.834 inceleme, KRİTİK 1):
// bir DDL ifadesi yüksek-hacimli bir objenin ÇIPLAK adını düşürüyor/boşaltıyorsa
// o adı döndürür, yoksa "".
//
// Neden gerekli: `adaptDDL` yalnız CREATE TABLE / ALTER TABLE / CREATE
// MATERIALIZED VIEW desenlerini tanır (identifyDDLTarget, cluster.go). Ham bir
// `DROP VIEW IF EXISTS <mv>` execDDL'den DEĞİŞTİRİLMEDEN geçerdi: `_local`'a
// çevrilmez, ON CLUSTER almaz ve kapının YANINDAN BİLE geçmez. Bu muhafız
// olmadan "kapısız çıplak-ad düşürmek" yalnız disiplinle engelleniyordu.
var reBareDestructive = regexp.MustCompile(
	`(?is)^\s*(?:DROP\s+TABLE|DROP\s+VIEW|TRUNCATE\s+TABLE)\s+(?:IF\s+EXISTS\s+)?` + "`?" + `([A-Za-z_][A-Za-z0-9_]*)` + "`?" + `\b`)

func bareDestructiveTarget(sql string) string {
	m := reBareDestructive.FindStringSubmatch(sql)
	if m == nil {
		return ""
	}
	if !highVolumeTables[m[1]] {
		return ""
	}
	return m[1]
}

// ── 2. ÖLÇÜM (bağlantıyla konuşan yarı) ───────────────────────────────

// currentDatabaseName — clusterAllReplicas okumalarının AÇIK db niteliği
// (v0.10.826 dersi): uzak host'ta `currentDatabase()` o host'un bağlamında
// çözülür, bizimkinde değil. Adı BURADA çözüp bind ediyoruz.
func (s *Store) currentDatabaseName(ctx context.Context) (string, error) {
	var db string
	if err := s.conn.QueryRow(ctx, "SELECT currentDatabase()").Scan(&db); err != nil {
		return "", fmt.Errorf("currentDatabase: %w", err)
	}
	return db, nil
}

// clusterSystemSource — system.<tbl> okumasının KAPSAMI. Küme kipinde
// clusterAllReplicas; `skip_unavailable_shards` BİLEREK yok (sözleşme 1).
func (s *Store) clusterSystemSource(tbl string) string {
	if !s.clusterMode() {
		return "system." + tbl
	}
	c := strings.ReplaceAll(strings.TrimSpace(s.cfg.ClusterName), "'", "")
	return fmt.Sprintf("clusterAllReplicas('%s', system.%s)", c, tbl)
}

// bareNameHosts — çıplak adı taşıyan host'lar ve oradaki motorları.
func (s *Store) bareNameHosts(ctx context.Context, name string) ([]bareHostShape, error) {
	db, err := s.currentDatabaseName(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT hostName(), engine
		FROM `+s.clusterSystemSource("tables")+`
		WHERE database = ? AND name = ?
		SETTINGS max_execution_time = 15`, db, name)
	if err != nil {
		// v0.8.185 disiplini: HATA'da rows'a DOKUNMA (yarı kurulmuş olabilir).
		return nil, err
	}
	defer rows.Close()
	var out []bareHostShape
	for rows.Next() {
		var h bareHostShape
		if err := rows.Scan(&h.Host, &h.Engine); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// bareNameShape — ölçüm + saf sınıflandırma.
func (s *Store) bareNameShape(ctx context.Context, mv string) (bareShape, string) {
	hosts, err := s.bareNameHosts(ctx, mv)
	return classifyBareNameHosts(hosts, err)
}

// resolvedStorageName — `mvStorageName`in ÖLÇÜLMÜŞ hâli. `<ad>_local`i ancak
// çıplak ad KANITLANMIŞ sarmalayıcıysa (ya da hiç yoksa: sarmalayıcı henüz
// kurulmamıştır, depo `_local`dir) döndürür. Şekil okunamazsa HATA döner —
// çağıran yıkıcı hiçbir şey yapmamalı.
//
// Neden `mvStorageName` yetmiyor: o SAF bir ad dönüşümü ve "küme kipindeyim,
// demek ki `_local` var" varsayımını taşıyor. Uyuşmazlıkta bu varsayım
// probe'u var olmayan bir tabloya yöneltir, "kolon yok" cevabı üretir ve
// yıkıcı kurtarma dalını YANLIŞ yere ateşler.
func (s *Store) resolvedStorageName(ctx context.Context, name string) (string, error) {
	if !s.clusterMode() || !highVolumeTables[name] {
		return name, nil
	}
	sh, detail := s.bareNameShape(ctx, name)
	switch sh {
	case bareShapeWrapper, bareShapeAbsent:
		return name + "_local", nil
	case bareShapeData:
		return name, nil
	default:
		return "", fmt.Errorf("`%s` çıplak adının şekli okunamadı (%s) — depo adı çözülemedi", name, detail)
	}
}

// columnPresentAllHosts — <table>.<col> objeyi taşıyan HER host'ta var mı.
//
// METADATA okuması (system.columns), VERİ okuması değil (v0.10.834 inceleme,
// KRİTİK 1): `SELECT <col> FROM spans … max_execution_time = 3` şeklindeki
// probe'ta tek bir geçici hata (kod 159 zaman aşımı, 241 bellek, 202
// eşzamanlılık, readonly replika) "kolon YOK" anlamına geliyordu ve o boot'ta
// MV düşüyordu. Emsal: hasTraceEntrySvcCol (v0.10.111) aynı sebeple
// system.columns'a çevrilmişti.
//
// KÜME GENELİ: kolonun koordinatörde olması yetmez — INSERT her shard'a
// fan-out eder, bir shard'da eksikse kod 16 ile TÜM ingest düşer (v0.8.186).
// Dönen hata "BİLMİYORUM" demektir ve çağıran onu "kolon yok" saymamalı.
func (s *Store) columnPresentAllHosts(ctx context.Context, table, col string) (bool, error) {
	db, err := s.currentDatabaseName(ctx)
	if err != nil {
		return false, err
	}
	var tableHosts uint64
	if err := s.conn.QueryRow(ctx,
		"SELECT toUInt64(countDistinct(hostName())) FROM "+s.clusterSystemSource("tables")+
			" WHERE database = ? AND name = ? SETTINGS max_execution_time = 15", db, table).Scan(&tableHosts); err != nil {
		return false, err
	}
	if tableHosts == 0 {
		return false, nil // tablo hiçbir host'ta yok → kolon da yok (hata DEĞİL)
	}
	var colHosts uint64
	if err := s.conn.QueryRow(ctx,
		"SELECT toUInt64(countDistinct(hostName())) FROM "+s.clusterSystemSource("columns")+
			" WHERE database = ? AND table = ? AND name = ? SETTINGS max_execution_time = 15",
		db, table, col).Scan(&colHosts); err != nil {
		return false, err
	}
	return colHosts == tableHosts, nil
}

// ── 3. KAPI ───────────────────────────────────────────────────────────

// mvMigrationShapeOK — GÖÇ dallarının kapısı. false → çağıran dalı TAMAMEN
// atlamalı (yalnız DROP'u değil).
//
// effect: atlamanın BU DAL için sonucu; log'a girer (bkz. bareShapeSkipReason).
func (s *Store) mvMigrationShapeOK(ctx context.Context, mv, effect string) bool {
	// TERFİ EDİLMEMİŞ MV (highVolumeTables dışı, v0.8.388): küme kipinde BİLE
	// çıplak ad objenin KENDİSİDİR — `_local` kardeşi hiç yoktur ve
	// dropCombinedMV(<çıplak ad>) kasıtlı, doğru davranıştır. Bugün hiçbir göç
	// hedefi bu kola düşmüyor (hepsi highVolumeTables üyesi); kol ileride
	// terfi edilmemiş bir MV göçe girerse onu KALICI olarak atlamamak için var.
	if s.clusterMode() && s.mvStorageName(mv) == mv {
		return true
	}
	sh, detail := s.bareNameShape(ctx, mv)
	if bareShapeMatchesExpectation(sh, s.clusterMode()) {
		return true
	}
	log.Print(bareShapeSkipReason(mv, detail, sh, s.clusterMode(), effect))
	return false
}

// ── 4. kapının ARDINDAKİ göç dalları ──────────────────────────────────
//
// İki dal v0.10.834'te migrate() gövdesinden BURAYA taşındı. Sebep mühendislik
// disiplini: ikisi de ÇIPLAK ADI DROP eden dallardı ve migrate() tek parça
// ~2900 satır olduğu için davranışları hiçbir testten geçmiyordu — yalnız
// kaynak-metin pinleri vardı, onlar da davranışı ölçmez. Ayrı birer metot
// olunca sahte bir driver.Conn ile GERÇEKTEN koşturulabiliyorlar
// (mv_shape_guard_test.go). Taşınan metni ölçen kapılar da buraya yönlendirildi
// (trace_backfill_test.go hacim-guard'ı, messaging_operation_dim_test.go).

const (
	effectMVDim       = "yeni boyut bu MV'de OLUŞMAZ; boyuta göre kırılım SIFIR satır döner (hata değil, boş)"
	effectEntrySvc    = "entry_service_state OLUŞMAZ; /traces kök-servis 'unknown' düşüşü kapalı kalır"
	effectEntryRoute  = "entry_route_state OLUŞMAZ; giriş-endpoint kırılımı boş kalır"
	effectApdex       = "apdex durumları bu MV'de OLUŞMAZ; /services apdex kolonları boş kalır"
	effectDBStmtEx    = "MV gömülü exemplar'lar OLUŞMAZ; /slow-queries exemplar pivotu ham spans yoluna düşer"
	effectDBStmtDrift = "Distributed sarmalayıcı eski kolon listesinde kalır; çıplak addan okuma kod 47 verebilir"
	effectPeerChain   = "peer.service fallback zinciri bu MV'de ESKİ kalır; satırlar 'unknown'a çökebilir"
	effectTDigest     = "quantile state reservoir olarak kalır; geniş pencerede kod 241/159 riski sürer"
)

// upgradeMVDim — mvDimMigrations defterinin BİR satırının göç dalı
// (gövde v0.10.563; kapı + taşıma v0.10.834).
//
// ddl = findMV(m.Table) — çağıranda çözülür (v0.8.52: ASLA dilim
// pozisyonuyla). Boş DDL execDDL tarafından hata sayılır (v0.9.1319).
func (s *Store) upgradeMVDim(ctx context.Context, m mvDimMigration, ddl string) error {
	// VERİ KAYBI KAPISI: şekil beklenmedikse dalın TAMAMI atlanır.
	if !s.mvMigrationShapeOK(ctx, m.Table, effectMVDim) {
		return nil
	}
	log.Printf("[chstore] upgrading %s MV (adding %s dim) — past 5-min buckets will be dropped", m.Table, m.Dim)
	dropTarget := m.Table
	if s.clusterMode() {
		dropTarget = m.Table + "_local"
		// v0.10.563 — DAĞITIK GÜVENLİK. Cluster modunda çıplak ad bir
		// Distributed SARMALAYICI ve o sarmalayıcı KENDİ kolon listesini
		// taşır. adaptDDL sarmalayıcıyı `CREATE TABLE IF NOT EXISTS` ile
		// yeniden kurar, yani eskisi ayakta kalırsa recreate NO-OP olur:
		// `_local` yeni kolonu kazanır, sarmalayıcı KAZANMAZ ve çıplak addan
		// gelen her `SELECT operation` CH kod 47 verir (v0.8.162 sınıfı,
		// prod'u iki kez kıran şekil).
		//
		// "Sarmalayıcı metadata-only (0 bayt), drop'u veri kaybı DEĞİL"
		// VARSAYIMI v0.10.834'te kapıya bağlandı: yukarıdaki kapı bu adın
		// KÜME GENELİNDE Distributed olduğunu (ya da hiç olmadığını)
		// KANITLAMADAN buraya gelinmiyor.
		if e := s.conn.Exec(ctx, "DROP TABLE IF EXISTS "+m.Table+s.onCluster()+" SYNC"); e != nil {
			return fmt.Errorf("drop distributed wrapper of %s: %w", m.Table, e)
		}
	}
	// v0.5.436 — SYNC; ZK metadata temizliğini bekler, yoksa hemen ardından
	// gelen CREATE REPLICA_ALREADY_EXISTS (253) yer.
	if err := s.dropCombinedMV(ctx, dropTarget); err != nil {
		return fmt.Errorf("drop old %s for upgrade: %w", m.Table, err)
	}
	if err := s.execDDL(ctx, ddl); err != nil {
		return fmt.Errorf("recreate %s with %s: %w", m.Table, m.Column, err)
	}
	return nil
}

// upgradeTraceSummaryEntryService — trace_summary_5m entry_service_state göç
// dalı (gövde v0.10.97; kapı + taşıma v0.10.834).
//
// Sınıfın en tehlikelisiydi: (1) küme kipinde probe `trace_summary_5m_local`e
// bakar, o tablo YOKSA "kolon eksik" çıkar ve dal TEK DÜĞÜM şeklinde de
// ateşler; (2) çıplak addaki DROP `purgeGuard` taşır, yani CH'nin kazara-DROP
// emniyeti BİLEREK kapatılmıştır — çıplak ad gerçek MV ise 200 GB'lık iç tablo
// bile sessizce gider.
func (s *Store) upgradeTraceSummaryEntryService(ctx context.Context, ddl string) error {
	const mv = "trace_summary_5m"
	if !s.mvMigrationShapeOK(ctx, mv, effectEntrySvc) {
		return nil
	}
	log.Println("[chstore] upgrading trace_summary_5m MV (adding entry_service_state) — past 5-min buckets will be dropped; in-place recipe preserves them (see v0.8.52 note)")
	dropTarget := mv
	if s.clusterMode() {
		dropTarget = mv + "_local"
	}
	if err := s.dropCombinedMV(ctx, dropTarget); err != nil {
		return fmt.Errorf("drop old trace_summary_5m for entry_service upgrade: %w", err)
	}
	if s.clusterMode() {
		// ⚠ DAĞITIK İKİNCİ YARI (bu sınıf prod'u iki kez kırdı): `_local`i
		// yeniden yaratmak yetmez — BARE Distributed sarmalayıcı ESKİ şemayla
		// kalır ve yeni kolonu okuyan her sorgu UNKNOWN_COLUMN ile ölür.
		// Sarmalayıcı burada düşürülür; boot sırası migrate →
		// ensureDistributedWrappers olduğundan AYNI boot'ta yeni şemayla
		// (CREATE … AS _local) geri kurulur.
		//
		// v0.10.110 — purgeGuard ŞART (operatör, test ortamı): bu ad eski
		// kurulumda İNCE wrapper değil TO'suz MV'nin kendisi olabilir; DROP
		// inner'a cascade eder ve 50 GB max_table_size_to_drop sınırında code
		// 359 ile boot sonsuz döngüye girer (211 GB inner, canlıda ölçüldü).
		// v0.10.834: "olabilir" artık tahmin değil — kapı o hâli KÜME
		// GENELİNDE görüp dalı tümden iptal ediyor.
		if err := s.conn.Exec(ctx, "DROP TABLE IF EXISTS trace_summary_5m"+s.onCluster()+" SYNC"+purgeGuard); err != nil {
			return fmt.Errorf("drop stale trace_summary_5m wrapper: %w", err)
		}
	}
	if err := s.execDDL(ctx, ddl); err != nil {
		return fmt.Errorf("recreate trace_summary_5m with entry_service: %w", err)
	}
	return nil
}

// dropIngestBlockingMV — KURTARMA dalı (v0.8.186 sınıfı, v0.10.834'te kapıya
// alındı). `spans` bir kolonu gerçekten taşımıyorsa, o kolonu SELECT'leyen MV
// her INSERT'te kod 16 verir ve CH varsayılanında KAYNAK INSERT'i de iptal
// eder → TÜM ingest durur. MV'yi düşürmek o kurtarmanın ta kendisidir.
//
// POLİTİKA FARKI (bilinçli): göç dallarında beklenmedik şekil "dalı atla"
// demek, burada DEĞİL — burada çıplak adın VERİ olması BEKLENEN hâldir ve
// düşürülmesi gereken şey odur. Ortak ve pazarlıksız olan tek kural
// sözleşme 3: BİLMİYORKEN YIKMA. O yüzden kapı burada `resolvedStorageName`
// biçiminde uygulanır — şekil okunamazsa hata döner ve hiçbir şey koşmaz —
// ve düşürme ham `DROP VIEW` yerine guard-safe `dropCombinedMV`den geçer.
func (s *Store) dropIngestBlockingMV(ctx context.Context, mv, col string) {
	target, err := s.resolvedStorageName(ctx, mv)
	if err != nil {
		log.Printf("[chstore] %s MV DÜŞÜRÜLMEDİ (%s eksik görünüyor ama şekil okunamadı: %v) — "+
			"bilinmeyen şekilde DROP koşmayız; ingest hâlâ bloke ise sonraki boot yeniden dener", mv, col, err)
		return
	}
	if err := s.dropCombinedMV(ctx, target); err != nil {
		// Non-fatal: başarısız bir DROP pod'u crash-loop'a sokmamalı.
		log.Printf("[chstore] could not drop stale %s MV (%s absent): %v", mv, col, err)
		return
	}
	log.Printf("[chstore] %s MV DROP edildi (depo: %s; %s yok — insert trigger'ı TÜM ingest'i bloke ederdi)", mv, target, col)
}
