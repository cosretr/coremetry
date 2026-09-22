package chstore

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
)

// removed_tables.go — ÜRÜNÜN KALDIRDIĞI tabloların DEFTERİ (v0.10.846).
//
// Operatör-bildirimli (prod, 2026-09-20, ekran görüntüsü): replika
// tutarlılığı kartı "1/90 tablo sorunlu · eksik replika" diyordu ve tek
// sorunlu tablo `feedbacks`'ti — shard 1'in İKİ host'unda da "tablo yok"
// (kırmızı, "İlk replikayı kur" düğmesiyle), shard 2'nin iki host'unda
// 2/2 Replicated ve "tutarlı".
//
// KÖK NEDEN: `feedbacks` v0.8.240'ta KALDIRILDI ve boot satır içi bir
// `DROP TABLE IF EXISTS feedbacks` koşuyordu. O ifade ON CLUSTER TAŞIMIYOR,
// `adaptDDL` de DROP şeklini yeniden yazmıyor (identifyDDLTarget yalnız
// CREATE TABLE / ALTER TABLE / CREATE MATERIALIZED VIEW eşler — v0.10.834'te
// ölçüldü). Yani kaldırma YALNIZ koordinatörün düştüğü host'ta koştu: bir
// shard temizlendi, ötekinde tablo DURDU. Kartın "eksik replika" dediği şey
// aslında YARIM KALMIŞ SİLME'ydi; doğru eylem kurmak değil TEMİZLEMEK.
//
// Bu dosyanın sözleşmeleri (hepsi testli):
//
//  1. Kaldırma DEFTERDEN üretilir ve küme kipinde `ON CLUSTER … SYNC` koşar.
//  2. SENKRON koşar — erteleme yolundan (deferDDL) GEÇMEZ. Gerekçe v0.9.633
//     dersi: "kuyruğa alındı" ile "uygulandı" AYRI fiillerdir ve erteleme
//     kipinde execDDL nil dönerdi, yani kartın "bir sonraki boot temizler"
//     cümlesi tutulamayan bir söz olurdu. Emsal: dropIngestBlockingMV →
//     dropCombinedMV (v0.10.834) de doğrudan koşar.
//  3. BİLMİYORKEN YIKMA: kaldırma önce kümede ÖLÇÜLÜR (hangi host'ta var,
//     hangi motorla). Ölçüm alınamazsa hiçbir şey koşmaz.
//  4. TEMİZLİK BOOT'U DÜŞÜRMEZ. Bu iş bir onarım değil; okuyucusu olmayan
//     bir kalıntının kalması sistemi çalışmaz yapmaz. Hata yüksek sesle
//     loglanır ve boot devam eder (taze kurulum / RESET_SCHEMA yolu canlı).
//  5. Defter satırı NE olduğunu BEYAN eder (Kind) ve kod o beyanı ölçümle
//     DOĞRULAMADAN DROP koşmaz. Basit yol yalnız düz tablo içindir.
//
// SATIR EKLEME KURALI: ad + kaldıran sürüm + tek cümlelik gerekçe + Kind.
// Gerekçe "veri neydi, dış tüketicisi var mıydı" sorusunu cevaplamalı;
// kaldırma geri alınamaz ve bu defter kaldırmanın TEK belgesi olur.

// removedKind — kaldırılan nesnenin ŞEKLİ. Beyan, ölçümle doğrulanır.
type removedKind string

const (
	// removedPlainTable — tek adlı (Replicated) MergeTree tablo. Basit
	// `DROP TABLE … ON CLUSTER … SYNC` yolu YALNIZ bunun içindir.
	removedPlainTable removedKind = "table"
	// removedMV — combined MaterializedView: gizli `.inner_id.<uuid>` iç
	// tablosu var. Basit DROP yanlıştır — dropCombinedMV (hacim guard'lı,
	// iç tablo önce) gerekir. Böyle bir satır bugün YOK; düşerse kod
	// reddeder, sessizce yanlış yoldan geçmez.
	removedMV removedKind = "mv"
	// removedHighVolume — `<ad>_local` + Distributed sarmalayıcı şekli.
	// v0.10.834 muhafızı çıplak adın DROP'unu zaten reddeder; burada
	// ayrıca ve açıkça reddediyoruz ki muhafız değişirse sessizce
	// geçmesin.
	removedHighVolume removedKind = "high_volume"
)

// removedTable — defterin bir satırı.
type removedTable struct {
	// Name — CH nesne adı, TAM. Kaldırma yalnız bu adı düşürür, o yüzden
	// defter araması da türev adlara (`_local`, `_old`, `_fix`…) YAYILMAZ:
	// tutulamayacak bir söz vermeyiz (v0.10.846 incelemesi).
	Name string
	// Since — özelliği kaldıran sürüm etiketi.
	Since string
	// Why — tek cümle: ne kaldırıldı ve veri neden geri istenmiyor.
	Why string
	// Kind — nesnenin şekli. Ölçümle doğrulanır; uyuşmazlık = DROP YOK.
	Kind removedKind
}

var removedTables = []removedTable{
	{
		Name:  "feedbacks",
		Since: "v0.8.240",
		Why:   "community-feedback özelliği kaldırıldı; veri uygulama içi mesajlardı, dış tüketici yok",
		Kind:  removedPlainTable,
	},
}

// removedTableEntry — SAF: ad defterde mi (TAM eşleşme, türev ad değil).
func removedTableEntry(name string) (removedTable, bool) {
	for _, rt := range removedTables {
		if rt.Name == name {
			return rt, true
		}
	}
	return removedTable{}, false
}

// removedSimpleDropBlocked — SAF: defter satırı basit DROP yolundan
// geçebilir mi? Boş dize = geçebilir; dolu = ENGEL GEREKÇESİ.
//
// Bugün tek satır (`feedbacks`) düz tablo ve yüksek hacimli değil. Bu kapı
// GELECEK için var: defter büyüdüğünde (yüksek hacimli ya da MV bir nesne
// kaldırıldığında) bugünkü tek şekil muhafızı — `bareDestructiveTarget`'ın
// highVolumeTables üyeliği — yeşile dönerdi ve çıplak ad ON CLUSTER
// düşürülürdü.
func removedSimpleDropBlocked(rt removedTable) string {
	switch rt.Kind {
	case removedMV:
		return fmt.Sprintf("%s bir MaterializedView olarak beyan edildi (%s): basit DROP gizli iç tabloyu "+
			"hacim guard'ısız bırakır — dropCombinedMV(s.resolvedStorageName(...)) yolu gerekir", rt.Name, rt.Since)
	case removedHighVolume:
		return fmt.Sprintf("%s yüksek hacimli telemetri olarak beyan edildi (%s): küme kipinde `%s_local` + "+
			"Distributed sarmalayıcı olarak yaşar, çıplak adın DROP'u yanlış nesneyi hedefler", rt.Name, rt.Since, rt.Name)
	case removedPlainTable:
		if highVolumeTables[rt.Name] {
			return fmt.Sprintf("%s düz tablo BEYAN edildi ama highVolumeTables üyesi — beyan ile kayıt "+
				"çelişiyor; birini düzelt (v0.10.834 muhafızı da bu DROP'u reddeder)", rt.Name)
		}
		return ""
	default:
		return fmt.Sprintf("%s defter satırında Kind boş/bilinmiyor (%q) — ne olduğunu bilmeden DROP koşmayız", rt.Name, rt.Kind)
	}
}

// removedObservedBlocked — SAF: KÜMEDE ÖLÇÜLEN şekil beyanla uyuşuyor mu?
// engines: nesnenin görüldüğü host'lardaki ayrık motor adları.
// Boş dize = basit DROP uygulanabilir.
func removedObservedBlocked(rt removedTable, engines []string) string {
	for _, e := range engines {
		if e == "MaterializedView" {
			return fmt.Sprintf("%s kümede MaterializedView olarak duruyor ama defterde %q beyan edilmiş — "+
				"basit DROP iç tabloyu guard'sız bırakır; beyanı düzelt ya da dropCombinedMV yoluna al", rt.Name, rt.Kind)
		}
		if !strings.Contains(e, "MergeTree") {
			return fmt.Sprintf("%s kümede beklenmedik motorla duruyor (%s) — ne olduğunu bilmeden DROP koşmayız", rt.Name, e)
		}
	}
	return ""
}

// removedTableDropDDL — SAF: defter satırının DROP ifadesi.
//
// onCluster: `s.onCluster()` çıktısı — küme kipinde " ON CLUSTER `<küme>`",
// tek düğümde "". İkinci bir yazım AÇMIYORUZ (v0.10.846 incelemesi): küme
// cümlesinin tek gövdesi cluster.go'daki onCluster().
//
//   - ON CLUSTER: kaldırma dağıtık DDL kuyruğundan HER host'a gider.
//     v0.10.846'nın düzelttiği eksik tam olarak budur ve ifadenin İÇİNDE
//     doğmak ZORUNDA: adaptDDL DROP'u tanımaz, sonradan ekleyecek kimse yok.
//   - SYNC (yalnız küme kipinde): CH'nin gecikmeli silmesi yerine tablo
//     dizinini hemen boşaltır; kalıntı dizin aynı adın yeniden
//     yaratılmasını engelleyebilirdi. Tek düğümde ifade v0.8.240'tan beri
//     koşanın BİREBİR aynısı kalır.
func removedTableDropDDL(name, onCluster string) string {
	if onCluster == "" {
		return "DROP TABLE IF EXISTS " + name
	}
	return "DROP TABLE IF EXISTS " + name + onCluster + " SYNC"
}

// removedTableSightings — nesne kümede NEREDE ve HANGİ motorla duruyor?
//
// Küme kipinde clusterAllReplicas (clusterSystemSource; skip_unavailable_shards
// BİLEREK yok — sessizce düşen bir host "temiz" okunurdu ve kaldırma o host'ta
// hiç koşmazdı). Bu okuma aynı zamanda "her boot bir dağıtık DDL turu"
// israfını da kapatır: nesne hiçbir yerde yoksa DROP HİÇ gönderilmez.
//
// DİKKAT — koordinatör-yerel okuma YETMEZ: prod'da kaldırma koordinatörün
// host'unda zaten koşmuştu, yani `system.tables` orada boştu; yerel bir
// varlık kontrolü "temiz" deyip öteki shard'ı sonsuza dek bırakırdı.
func (s *Store) removedTableSightings(ctx context.Context, name string) (hosts, engines []string, err error) {
	db, err := s.currentDatabaseName(ctx)
	if err != nil {
		return nil, nil, err
	}
	hostExpr := "''"
	if s.clusterMode() {
		hostExpr = "hostName()"
	}
	rows, err := s.conn.Query(ctx, fmt.Sprintf(
		`SELECT %s, engine FROM %s WHERE database = ? AND name = ? SETTINGS max_execution_time = 10`,
		hostExpr, s.clusterSystemSource("tables")), db, name)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var h, e string
		if err := rows.Scan(&h, &e); err != nil {
			return nil, nil, err
		}
		hosts = append(hosts, h)
		if !seen[e] {
			seen[e] = true
			engines = append(engines, e)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.Strings(hosts)
	sort.Strings(engines)
	return hosts, engines, nil
}

// dropRemovedTables — defterdeki her satırı küme genelinde düşürür.
//
// HATA DÖNDÜRMEZ ve bu bilinçli (v0.10.846 incelemesi): erteleme KAPALI
// kipte (taze kurulum, RESET_SCHEMA sonrası — operatörün test kümesinde
// canlı olan yol) bir DROP hatası boot'u düşürürdü. Okuyucusu olmayan bir
// kalıntının temizliği sistemi çalışmaz yapmamalı; hata yüksek sesle
// loglanır ve bir sonraki boot yeniden dener.
//
// migrate() içinde `tables` dilimi KOŞTUKTAN SONRA, `alters` diliminin
// anlık görüntüsünden ÖNCE çağrılır. `existing` önbelleği sözleşmesi
// (store.go, "İKİ ANLIK GÖRÜNTÜ DE BURADA TAZE OKUNUYOR") KORUNUR:
// `alters` planı kendi system.tables/system.columns okumasını bu
// kaldırmalardan SONRA yapar, yani düşürülmüş bir nesnenin CREATE'i bayat
// bir "zaten var" kaydıyla elenemez.
//
// SIRA BİR RİSK TAŞIR ve o risk KAPIDA: kaldırma `tables`'tan SONRA koştuğu
// için deftere YAŞAYAN bir ad düşerse boot o tabloyu kurup hemen küme
// genelinde silerdi — her boot, sessizce. TestLedgerNeverNamesALivingTable
// (removed_tables_test.go) kesişimi GERÇEK katalogla (canonicalTables +
// canonicalMVs + migrations) tutuyor.
func (s *Store) dropRemovedTables(ctx context.Context) {
	for _, rt := range removedTables {
		s.dropRemovedTable(ctx, rt)
	}
}

// dropRemovedTable — tek defter satırı: ÖLÇ → KAPILAR → DROP → YENİDEN ÖLÇ.
//
// Son adım "kuyruğa alındı" ile "uygulandı"yı ayırır (v0.9.633 dersi):
// dağıtık DDL kuyruğu tıkalıysa execWithReadonlyRetry kod 159'u BAŞARI
// sayar, yani dönüş değeri uygulamayı KANITLAMAZ. Kanıt yalnız ikinci
// ölçümdedir ve log onu yazar.
func (s *Store) dropRemovedTable(ctx context.Context, rt removedTable) {
	if reason := removedSimpleDropBlocked(rt); reason != "" {
		log.Printf("[chstore] KALDIRILMIŞ TABLO TEMİZLİĞİ ATLANDI — %s", reason)
		return
	}
	hosts, engines, err := s.removedTableSightings(ctx, rt.Name)
	if err != nil {
		// Sözleşme 3: BİLMİYORKEN YIKMA (dropIngestBlockingMV ile aynı politika).
		log.Printf("[chstore] `%s` (ürün %s'te kaldırdı) temizliği ATLANDI — kümedeki şekli okunamadı: %v; "+
			"bir sonraki boot yeniden dener", rt.Name, rt.Since, err)
		return
	}
	if len(hosts) == 0 {
		return // temiz: DDL hiç gönderilmez (her boot bir dağıtık DDL turu israfı yok)
	}
	if reason := removedObservedBlocked(rt, engines); reason != "" {
		log.Printf("[chstore] KALDIRILMIŞ TABLO TEMİZLİĞİ ATLANDI — %s", reason)
		return
	}
	ddl := removedTableDropDDL(rt.Name, s.onCluster())
	// v0.10.834 muhafızı execDDL'de yaşıyor; bu yol SENKRON koşmak için
	// execDDL'i atlıyor, o yüzden muhafızı AÇIKÇA burada koşturuyoruz —
	// atlanan bir kapı, olmayan bir kapıdır.
	if n := bareDestructiveTarget(ddl); n != "" {
		log.Printf("[chstore] KALDIRILMIŞ TABLO TEMİZLİĞİ ATLANDI — `%s` çıplak adını düşüren DDL yapısal "+
			"muhafızca reddedildi (v0.10.834); defter satırının Kind'ı gerçeği anlatmıyor", n)
		return
	}
	log.Printf("[chstore] `%s` ürün %s'te kaldırdı (%s) — %d host'ta duruyor (%s), küme genelinde düşürülüyor",
		rt.Name, rt.Since, rt.Why, len(hosts), strings.Join(hosts, ", "))
	if err := s.execWithReadonlyRetry(ctx, ddl); err != nil {
		// TEMİZLİK BOOT'U DÜŞÜRMEZ.
		log.Printf("[chstore] `%s` düşürülemedi (%v) — kalıntı duruyor, sistem etkilenmez; bir sonraki boot "+
			"yeniden dener. Admin → ClickHouse → Replika tutarlılığı kartı satırı `kaldırıldı` gösterir", rt.Name, err)
		return
	}
	left, _, err := s.removedTableSightings(ctx, rt.Name)
	switch {
	case err != nil:
		log.Printf("[chstore] `%s` DROP gönderildi; sonuç DOĞRULANAMADI (%v) — kart hâlâ gösteriyorsa "+
			"dağıtık DDL kuyruğuna bak", rt.Name, err)
	case len(left) == 0:
		log.Printf("[chstore] `%s` küme genelinde DÜŞÜRÜLDÜ (uygulandı)", rt.Name)
	default:
		log.Printf("[chstore] `%s` DROP KUYRUĞA ALINDI ama henüz uygulanmadı — hâlâ %d host'ta (%s). "+
			"Dağıtık DDL kuyruğu işleyince inecek; bir sonraki boot yeniden dener", rt.Name, len(left), strings.Join(left, ", "))
	}
}
