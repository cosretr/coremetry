// v0.9.1308 — state tabloları TEK replikasyon grubunda.
//
// ÖLÇÜLEN BUG. Küme kipinde adaptDDL HER Replicated tablo için ZK yolunu
// `<prefix>/{shard}/<ad>` üretiyordu. Telemetri için doğru: her shard
// kendi dilimini tutar, Distributed sarmalayıcı okurken birleştirir.
// State tabloları (problems, alert_rules, users, system_settings, …)
// ise `defaultShardPolicy`/`highVolumeTables` kayıtlarında YOK — yani
// Distributed sarmalayıcıları da yok. Sonuç: her shard AYRI bir
// replikasyon grubu, uygulama hangi host'a bağlanırsa onun dilimini
// görür, veri sessizce bölünür.
//
//	LOKAL (chc-0 shard=01 / chc-1 shard=02), 2026-08-23:
//	  problems 4631/191 · anomaly_events 222/60 · incidents 2894/57
//	  alert_rules 8/0 · users 1/0 · system_settings 3/0 · dashboards 10/0
//	PROD (uptrace_all, 2 shard × 2 replica):
//	  problems 633.236 (bp01/bp02) vs 4.169 (bp03/bp04) — AÇIK
//	  problemlerin tamamı küçük shard'da.
//
// HEDEF TASARIM (operatör onaylı, ayrı küme tanımı YOK):
//
//	ENGINE = ReplicatedReplacingMergeTree(
//	  '<prefix>/state/<ad>',   -- {shard} YOK: tek grup
//	  '{shard}-{replica}',     -- her node benzersiz
//	  version)
//
// `{shard}-{replica}` bilinçli: prod'da `{replica}` makrosunun shard
// başına tekrar ediyor olması İHTİMAL (doğrulanmadı); bu yazım her iki
// makro düzeninde de benzersiz kalır.
//
// SPLIT-BRAIN KARARI — bkz. useUnifiedStatePath.
//
// v0.10.971 — boot kuralı sadeleşti (operatör kararı 2026-09-27, "Önerini
// yapalım", öneri 2): hiç var olmayan state tablosu, kümede başka tablolar
// eski yolda olsa bile HER ZAMAN birleşik yola kurulur. Gerekçe ve prod
// olayı useUnifiedStatePath'in belgesinde.
//
// v0.10.978 — kural 4'ün kör noktası kapandı: gözlem EKSİKKEN (cevap vermeyen
// replika) hiç gözlenmemiş tablo için önce Keeper'a bakılır
// (state_keeper_probe.go); eski yolda replika kümesi varsa ona katılır.
package chstore

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// stateReplicaDir — birleşik state yolunda `{shard}` yerine geçen sabit
// segment. `/clickhouse/tables/state/problems` gibi.
//
// "state" bir shard makrosu DEĞİL, sabit metin: bu yüzden dört host da
// aynı znode'a bakar ve tek replikasyon grubu oluşur.
const stateReplicaDir = "state"

// stateReplicaName — birleşik gruptaki replika adı şablonu.
//
// Telemetri tarafı `{replica}` kullanır ve bu yeterlidir, çünkü orada
// grup zaten shard başına ayrıdır. Birleşik grupta dört host TEK grupta
// buluşur; `{replica}` shard başına tekrar ediyorsa iki host aynı
// replika adını iddia eder ve ikincisi REPLICA_ALREADY_EXISTS alır.
// `{shard}-{replica}` her iki makro düzeninde de benzersizdir.
const stateReplicaName = "{shard}-{replica}"

// shardedTelemetryTable — bu ad cluster.go'daki ÜÇ kayıttan herhangi
// birinde geçiyor mu?
//
// TEK KAYNAK. Elle yazılmış ikinci bir "state tabloları" listesi
// TUTULMUYOR: iki liste zamanla ıraksar ve tam da bu repo bunu iki kez
// yaşadı (v0.5.426 defaultShardPolicy⊅highVolumeTables, v0.9.350
// operation_group_summary_5m). Kural tersine çevrilmiştir:
//
//	"shard'lı telemetri" = üç kayıttan birinde ADI GEÇEN
//	"state"              = geçmeyen
//
// Yani yeni bir telemetri tablosu eklerken kaydı UNUTMAK, tabloyu
// state sınıfına düşürür — sessiz shard bölünmesi değil, gürültülü
// "tek grupta çok fazla veri" yönünde hata. Yeni bir state tablosu
// eklerken ise yapılacak HİÇBİR ŞEY yok: doğru davranışı otomatik alır.
func shardedTelemetryTable(name string) bool {
	if highVolumeTables[name] || tablesWithoutTraceID[name] {
		return true
	}
	_, ok := defaultShardPolicy[name]
	return ok
}

// stateTableDDL — bu DDL hedefi birleşik yolda yaşaması gereken bir
// state tablosu mu? SAF.
//
// kind: identifyDDLTarget'ın döndürdüğü tür.
//   - "table"      → aday
//   - "mv"         → ASLA. Bir MV'nin hedefi shard-yerel `_local`
//     kaynaktan besleniyor; birleşik gruba yazmak iki shard'ın MV'sini
//     aynı tabloya toplardı. Bu ayrı (ve çok daha büyük) bir karar,
//     0009'un kapsamı DEĞİL.
//   - "altertable" → ALTER'ın ENGINE cümlesi yok, ZK yolu üretilmiyor.
func stateTableDDL(name, kind string) bool {
	if kind != "table" || name == "" {
		return false
	}
	return !shardedTelemetryTable(name)
}

// stateProbeTable — boot probe'unda GÖZLEME dahil edilecek mi?
//
// system.replicas gerçek CH tablo adlarını döndürür; adaptDDL ise
// çıplak adı görür. Küme kipine özgü üç ad biçimi elenir, yoksa
// `spans_local` "state" sanılırdı:
//   - `.inner_id.…`  → combined MV'nin iç tablosu
//   - `…_local`      → shard-yerel telemetri
//   - `…_old` / `…_unified` → 0009 göçünün geçici tabloları. Bilinçli
//     dışarıda: `_old` göç sonrası doğrulama bitene dek YAŞAR ve
//     eski (shard'lı) yolda durur — sayılsaydı boot log'u ve Replika
//     tutarlılığı kartı onu "eski yolda bölünmüş state tablosu" diye
//     sayardı. (v0.10.971 öncesi kural 3 varken kuşak kararını da "göç
//     öncesi"ne kilitlerdi; o kural kalktı, süzgeç sayım ve kart için durur.)
//
// v0.10.846 — liste BURADA BİTMİYORDU: 0010'un `…_repart` / `…_pathfix` /
// `…_pathfix_old` aileleri ile onarım sihirbazının `…_fix` tablosu
// eleniyordu sanılıyordu, elenmiyordu. Aynı soruyu soran ikinci bir liste
// (table_catalog.go catalogSuffixes) vardı ve ikisi ayrışmıştı — hiçbir kapı
// onları birbirine bağlamıyordu. Artık TEK GÖVDE: catalogDerivedName.
// Yeni elenenler aynı gerekçenin kapsamındadır: hepsi eski (shard'lı) yolda
// duran göç/onarım yedekleridir ve sayılsalardı kart ile boot log'unda
// sahte "bölünmüş state tablosu" satırı üretirlerdi.
func stateProbeTable(name string) bool {
	if catalogInnerName(name) || catalogDerivedName(name) {
		return false
	}
	return stateTableDDL(name, "table")
}

// replicatedArgs — Replicated*MergeTree'nin İLK İKİ argümanı.
//
// unified=false dalı v0.9.1308 ÖNCESİYLE BAYT BAYT AYNIDIR
// (state_replication_test.go bunu eski biçim dizesine karşı pinliyor):
// telemetri tablolarının ZK yolu bu değişiklikte bir karakter bile
// değişmez.
func replicatedArgs(zkPrefix, name string, unified bool) string {
	if unified {
		return fmt.Sprintf("'%s', '%s'", unifiedStatePath(zkPrefix, name), stateReplicaName)
	}
	return fmt.Sprintf("'%s/{shard}/%s', '{replica}'", zkPrefix, name)
}

// unifiedStatePath — birleşik ZK yolunun tam metni.
func unifiedStatePath(zkPrefix, name string) string {
	return zkPrefix + "/" + stateReplicaDir + "/" + name
}

// stateObservation — boot probe'unun sonucu.
//
// Sıfır değeri (ok=false) BİLİNÇLİ olarak "eski yol" demektir: probe
// hiç koşmamış bir Store (testler, tek düğüm, adaptDDL'i migrate'ten
// önce çağıran bir yol) v0.9.1308 öncesiyle birebir aynı DDL üretir.
type stateObservation struct {
	ok bool
	// paths: state tablosu adı → kümede GÖZLENEN zookeeper_path.
	paths map[string]string
	// complete — v0.10.971 — küme tanımındaki (system.clusters) HER replika
	// cevap verdi mi (stateProbeCoverage). false iken kural 4'ün "hiçbir
	// node'da yok" hükmü yalnız cevap verenler için doğrudur; boot bunu ⚠
	// satırıyla söyler. v0.10.978 — artık kararı da yönlendirir: false iken
	// gözlenmemiş tablo için önce `keeper` sorulur.
	complete bool
	// keeper — v0.10.978 — Keeper muhafızı (state_keeper_probe.go). YALNIZ
	// complete=false iken bağlanır (bindStateKeeperGuard); nil = sorulmaz.
	// Elle kurulan gözlemler (sihirbazın gölge Store'u, testler) nil bırakır
	// ve v0.10.971 hükmünü birebir alır.
	keeper stateKeeperLookup
}

// useUnifiedStatePath — SPLIT-BRAIN MUHAFIZI. SAF.
//
// SORUN. `CREATE TABLE IF NOT EXISTS` var olan tabloya dokunmaz, yani
// mevcut kurulum göç koşana dek eski yolda kalır — doğru davranış. AMA
// kümeye YENİ bir node eklenirse ya da bir node'un tablosu silinmişse,
// o node kodun ürettiği yeni yolla yaratır ve komşularından FARKLI bir
// ZK grubuna katılır: bugünkü bug'ın birebir tekrarı, üstelik sessiz.
//
// KARAR: (a) — yol KODDAN değil, KÜMEDEN okunur.
//
// Kodun ürettiği birleşik yol yalnızca bir ÖNERİdir. Gerçekte CREATE'e
// giren şey "komşularımın bu tablo için kullandığı yol"dur; birleşik
// yol yalnız tabloyu HİÇ KİMSE tutmuyorsa kullanılır.
//
// Neden (b) — system_settings bayrağı — DEĞİL:
//   - Bayrak İKİNCİ bir doğruluk kaynağıdır ve iki yönde de yanlış
//     olabilir: göçten önce açılırsa bölünme yaratır, göçten sonra
//     kapalı kalırsa uygulama birleşik tabloların yanına eski yollu
//     tablolar kurar.
//   - system_settings'in KENDİSİ bir state tablosudur; yolunu ondan
//     okumak, okumak için var olmasını gerektirir (döngü).
//   - (a) kendiliğinden tamamlanır: 0009'un RENAME'i bittiği an dört
//     host da birleşik yolu raporlar, uygulama bir sonraki boot'ta
//     ONU görür. Çevrilecek bir düğme, ikinci bir deploy yoktur.
//
// KARAR SIRASI (v0.10.971):
//  1. Probe koşmadı → ESKİ yol. "Hiçbir şeyi değiştirme" mevcut bir
//     kümeyi bölemez: tablonun başka bir node'da var olup olmadığını
//     BİLEMEYİZ. (Taze kurulumda bugünkü davranışı tekrarlar — ve o
//     durumda probe'un başarısız olması için bağlantının ölmüş olması
//     gerekir, ki o hâlde boot zaten çöker.)
//  2. Tablo kümede GÖZLENDİ → gözlenen yolun kendisi. Komşularına
//     katıl, ne olursa olsun. "Kümeye YENİ node eklendi" senaryosunun
//     tam karşılığı budur: eksik tablo yeni node'da yok ama
//     clusterAllReplicas onu komşularda görür ve yeni node komşularının
//     yoluna (eski ya da birleşik) kurar.
//  3. (KALDIRILDI, v0.10.971 — aşağıda.) Numaralar operatör kararındaki
//     gibi korunur: "kural 2" / "kural 4" başka yerlerde bu anlamla geçer.
//  4. Tablo hiçbir node'da yok → BİRLEŞİK yol, HER ZAMAN — kümede başka
//     state tabloları hâlâ eski yolda olsa bile. Katılınacak bir grup
//     yoktur; eski yol ona yalnız shard başına bölünme verir.
//     v0.10.978 — "hiçbir node'da yok" hükmü gözlem EKSİKKEN (obs.complete=
//     false: roster'daki bir replika cevap vermedi ya da küme okuması düştü)
//     yalnız cevap verenler için doğrudur. O hâlde 4'ten ÖNCE Keeper
//     muhafızı sorulur (decideStatePath, state_keeper_probe.go):
//     - Keeper'da YALNIZ eski yolda replika kümesi var → ESKİ yol, ona
//     katıl ("Keeper kanıtı: eski yolda replika var, gözlem eksikti");
//     - birleşik yolda var (eski yolda da olsa) ya da hiçbir yerde yok →
//     BİRLEŞİK (kural 4). İki yolda da varsa ⚠: 0009 penceresinde `_old`
//     yedeği eski yolda durur ve gözlem onu görmez (stateProbeTable);
//     Keeper yedeğin kümesini `<t>`'ninkinden ayıramaz, eski kazansaydı var
//     olan birleşik grup BÖLÜNÜRDÜ — v0.10.971'in hükmü korunur;
//     - Keeper okunamadı → BİRLEŞİK (v0.10.971 davranışı) + ⚠ "muhafız
//     koşamadı". Gözlem TAMKEN muhafız hiç sorulmaz (bağlanmaz bile).
//
// v0.10.971 — KALDIRILAN KURAL 3 (operatör kararı 2026-09-27, "Önerini
// yapalım", öneri 2). Kurulumun "KUŞAĞI"na bakıyordu: kümede
// herhangi bir state tablosu eski yoldaysa kurulum "göç ÖNCESİ" sayılır ve
// hiç var olmayan yeni tablo da ESKİ yola kurulurdu. Kendini besleyen bir
// KİLİTTİ: prod'da ingest_ledger eski yolda doğdu (v0.10.767) ve ondan
// sonraki her state tablosu — ai_eval_runs, sekiz Rollouts v2 tablosu —
// shard başına BÖLÜNMÜŞ doğdu (v0.10.960 ölçümü: on tablo, her biri iki ayrı
// replikasyon grubu; uygulama bağlandığı host'un yarısını görüyordu).
// Korumak istediği "yeni node" senaryosu zaten kural 2'dir. Eski yoldaki
// tablolar hâlâ bölünmüştür ve kendiliğinden düzelmez (Admin › ClickHouse ›
// Replika tutarlılığı › "State tablolarının ZK yolu", 0009/0010 runbook'u);
// ama artık YENİ tabloları eski yola çekmezler.
//
// DAR KALAN RİSK (kural 1'in gerekçesinin yarım probe'daki hâli): gözlem
// EKSİKSE, gözlenmeyen ama cevap vermeyen bir komşuda eski yolda duran tablo
// "hiçbir yerde yok" sanılır ve birleşik yola kurulur (yeni bölünme). İki
// sebebi var (v0.10.971 — ikincisi önceden belgelenmemişti):
//   - clusterAllReplicas okuması HATA döndü → gözlem YALNIZ yereldir;
//   - okuma BAŞARILI ama `skip_unavailable_shards=1` bağlantıyı reddeden bir
//     replikayı HATASIZ ve SESSİZCE düşürdü (clusterAllReplicas her replikayı
//     ayrı shard sayar) → gözlem yalnız cevap verenlerindir.
//
// Eski kural 3 bu dar durumu yalnız TESADÜFEN örtüyordu (eski yoldaki tablo
// için doğru, birleşik yoldaki için yanlış). resolveStateReplicaPaths İKİ
// durumu da GÜRÜLTÜLÜ loglar (ikincisini roster ile cevap verenleri sayarak
// ayırır: stateProbeCoverage) ve obs.complete'e yazar. v0.10.978 — iki
// durumda da gözleme Keeper muhafızı bağlanır (bindStateKeeperGuard); karar
// zinciri decideStatePath'te, bu sarmalayıcı yalnız imzayı korur (cluster.go
// adaptDDL ve replica_repair.go seed planı buradan çağırır).
func useUnifiedStatePath(obs stateObservation, zkPrefix, name string) (bool, string) {
	unified, reason, _ := decideStatePath(obs, zkPrefix, name, obs.keeper)
	return unified, reason
}

// statePathFreshReason — v0.10.971 — kural 4'ün gerekçe metni (seed planının
// kanıt satırı ve testler aynı sabiti görür).
const statePathFreshReason = "tablo kümede yok — hiç var olmayan state tablosu birleşik yola kurulur (v0.10.971)"

// resolveStateReplicaPaths — boot probe'u. migrate()'in ilk DDL'inden
// ÖNCE, tek goroutine'den çağrılır.
//
// İKİ AŞAMA:
//  1. YEREL system.replicas — probe'un KOŞTUĞUNU bu belirler. Bellek
//     içi bir sistem tablosu; başarısız olması bağlantının ölmesi
//     demektir, o hâlde boot zaten sürmez.
//  2. clusterAllReplicas(…) — best effort, `skip_unavailable_shards=1`
//     ile: rolling CH restart sırasında bir replika kapalıyken probe'un
//     tamamen düşmesini engeller. Ulaşılabilen node'ların cevabı
//     "bu tablo hangi grupta" sorusu (kural 2) için yeterlidir.
//     v0.10.971 — "bu tablo HİÇBİR yerde yok" sorusu (kural 4) için
//     YETERLİ DEĞİLDİR: o, HER replikanın cevap vermesini ister. Bu yüzden
//     okuma başarılıysa cevap veren replika sayısı küme tanımıyla
//     (system.clusters) karşılaştırılır; eksikse ⚠ loglanır ve (v0.10.978)
//     gözleme Keeper muhafızı bağlanır: gözlenmemiş tablo kurulmadan önce
//     Keeper'da eski yol kanıtı aranır (state_keeper_probe.go).
//
// ÇAKIŞMA (aynı tablo, iki farklı yol) = kurulum ZATEN bölünmüş.
// Muhafazakâr davranılır: ESKİ yol kazanır. Yeni bir yola geçmek
// bölünmeyi büyütür; eski yolda kalmak en fazla "hiçbir şey yapma"dır.
func (s *Store) resolveStateReplicaPaths(ctx context.Context) {
	s.stateObs = stateObservation{}
	if !s.clusterMode() {
		return
	}
	zkPrefix := s.zkPrefix()

	local, err := s.queryReplicaPaths(ctx, "system.replicas", false)
	if err != nil {
		log.Printf("[chstore] state ZK yolu probe'u BAŞARISIZ (%v) — state tabloları ESKİ (shard'lı) yolda kalıyor; "+
			"birleşik yola geçiş 0009 göçüyle operatörde", err)
		return
	}
	obs := stateObservation{ok: true, paths: local}

	if cl, cerr := s.queryReplicaPaths(ctx, fmt.Sprintf("clusterAllReplicas(%s, system.replicas)",
		quoteCHIdent(s.cfg.ClusterName)), true); cerr != nil {
		// v0.10.971 — GÜRÜLTÜLÜ: kural 4 "hiçbir yerde yok → birleşik" der;
		// yalnız yerel gözlemde komşuların eski yollu tablosu görünmez.
		log.Printf("[chstore] ⚠ küme geneli state yolu okunamadı (%v) — yalnız YEREL gözleme göre karar veriliyor: "+
			stateProbeBlindTail, cerr)
	} else {
		// v0.10.971 — okuma BAŞARILI olsa da eksik olabilir: skip_unavailable_shards=1
		// cevap vermeyen replikayı hatasız atlar. Cevap verenler roster'la sayılır.
		answered, roster, verr := s.stateProbeReplicaCoverage(ctx)
		complete, warn := stateProbeCoverage(answered, roster, verr)
		obs.complete = complete
		if warn != "" {
			log.Printf("[chstore] ⚠ %s — küme geneli gözlem EKSİK olabilir: %s", warn, stateProbeBlindTail)
		}
		for t, p := range cl {
			old := obs.paths[t]
			// v0.10.965 — birleştirme kuralı mergeObservedStatePath'te: yeniden
			// kurulum sihirbazının ölçümü (statePathObserved) AYNI gövdeyi
			// çağırır, boot ile sihirbaz ayrışamaz.
			if mergeObservedStatePath(obs.paths, t, p, zkPrefix) {
				log.Printf("[chstore] ⚠ %s İKİ farklı ZK yolunda görüldü (%q vs %q) — kurulum ZATEN bölünmüş; "+
					"eski yol tercih ediliyor, 0009 göçü bunu onarır", t, old, p)
			}
		}
	}
	// v0.10.978 — gözlem eksikse (küme okuması düştü YA DA bir replika
	// atlandı) kural 4'ün önüne Keeper muhafızı girer; tam gözlemde bağlanmaz.
	if !obs.complete {
		s.bindStateKeeperGuard(ctx, &obs)
	}
	s.stateObs = obs

	unified, legacy := 0, 0
	for t, p := range obs.paths {
		if p == unifiedStatePath(zkPrefix, t) {
			unified++
		} else {
			legacy++
		}
	}
	log.Print(stateProbeLogLine(len(obs.paths), unified, legacy, obs.complete))
}

// stateProbeBlindTail — v0.10.971 — eksik gözlemin (hata ya da atlanan
// replika) ⚠ satırlarının ORTAK kuyruğu: kural 4'ün kör noktası tek metinle.
// v0.10.978 — kör nokta Keeper muhafızıyla daraldı; kuyruk bunu söyler.
const stateProbeBlindTail = "GÖZLENMEYEN bir state tablosu için önce Keeper'da eski yol kanıtı aranır (v0.10.978); " +
	"Keeper de okunamazsa birleşik yola kurulur ve cevap vermeyen bir komşuda eski yolda duruyorsa " +
	"o shard BÖLÜNÜR (Admin › ClickHouse › Replika tutarlılığı)"

// stateProbeCoverage — v0.10.971 — SAF: küme geneli state yolu okumasının
// KAPSAM hükmü. answered = clusterAllReplicas(system.one)'a cevap veren
// replika sayısı, roster = system.clusters'taki replika sayısı, err = bu iki
// okumadan birinin hatası. complete=false ise warn boş değildir.
//
// Neden ayrı sayım: system.replicas okuması `skip_unavailable_shards=1` ile
// koşar ve bağlantıyı reddeden replikayı HATASIZ düşürür; dönen satırlarda
// host yoktur, yani kısmi cevap tam cevaptan ayırt edilemez. Replikasız
// (taze) bir node da satır döndürmez — bu yüzden sayım system.one'dan.
func stateProbeCoverage(answered, roster int, err error) (complete bool, warn string) {
	switch {
	case err != nil:
		return false, fmt.Sprintf("küme geneli okumanın kapsamı doğrulanamadı (%v)", err)
	case roster <= 0:
		return false, "küme geneli okumanın kapsamı doğrulanamadı (system.clusters'ta bu küme için replika yok)"
	case answered < roster:
		return false, fmt.Sprintf("yalnız %d/%d replika cevap verdi (skip_unavailable_shards=1 cevap vermeyeni HATASIZ atlar)", answered, roster)
	}
	return true, ""
}

// stateProbeAnsweredSQL — v0.10.971 — SAF: cevap veren replika sayısı. Her
// replika system.one'dan tek satır döndürür; ulaşılamayan atlanır (probe'un
// kendi okumasıyla AYNI ayar), tavanlı.
func stateProbeAnsweredSQL(cluster string) string {
	return "SELECT count() FROM clusterAllReplicas(" + quoteCHIdent(cluster) + ", system.one) " +
		"SETTINGS skip_unavailable_shards = 1, max_execution_time = 10"
}

// stateProbeReplicaCoverage — v0.10.971 — roster (system.clusters, bellek
// içi) ve cevap veren replika sayısı. Hata → kapsam doğrulanamadı.
func (s *Store) stateProbeReplicaCoverage(ctx context.Context) (answered, roster int, err error) {
	var r, a uint64
	if err = s.conn.QueryRow(ctx, `SELECT count() FROM system.clusters WHERE cluster = ?`, s.cfg.ClusterName).Scan(&r); err != nil {
		return 0, 0, err
	}
	if err = s.conn.QueryRow(ctx, stateProbeAnsweredSQL(s.cfg.ClusterName)).Scan(&a); err != nil {
		return 0, int(r), err
	}
	return int(a), int(r), nil
}

// stateProbeLogLine — v0.10.971 — SAF: boot probe'unun özet satırı.
// "(N birleşik, M eski)" parçası runbook'larda alıntılanır (0010 AŞAMA B,
// sihirbazın başarı notu) — biçimi değişmez. Eski kuşak eki ("yeni state
// tabloları ESKİ yola kurulacak") kalktı: hiç var olmayan tablo her zaman
// birleşik yola kurulur. Eski yolda tablo varsa bölünmüş oldukları söylenir.
// v0.10.978 — gözlem eksikse (complete=false) satır Keeper muhafızını duyurur.
func stateProbeLogLine(observed, unified, legacy int, complete bool) string {
	line := fmt.Sprintf("[chstore] state ZK yolu probe'u: %d tablo gözlendi (%d birleşik, %d eski)", observed, unified, legacy)
	if legacy > 0 {
		line += " — eski yoldakiler shard başına BÖLÜNMÜŞ (Admin › ClickHouse › Replika tutarlılığı); hiç var olmayan state tabloları birleşik yola kurulur"
	}
	if !complete {
		line += " — gözlem EKSİK: hiç var olmayan tablo için önce Keeper'da eski yol kanıtı aranır (v0.10.978)"
	}
	return line
}

// mergeObservedStatePath — v0.10.965 — SAF. Boot probe'unun birleştirme
// döngüsünden (resolveStateReplicaPaths) ÇIKARILDI, anlamı birebir aynı:
//
//   - tablo ilk kez görülüyorsa yolu yazılır;
//   - aynı tablo İKİ farklı yolda görülürse kurulum zaten bölünmüştür:
//     ESKİ yol kazanır (birleşik olan eskisiyle değiştirilir), iki eski yol
//     çakışırsa İLK görülen kalır.
//
// split=true → çakışma vardı (boot bunu log satırıyla bildirir). Yeniden
// kurulum sihirbazının "eski yolda kalanlar" hesabı (state_path_rebuild.go
// statePathObserved) AYNI gövdeyi çağırır: sihirbazın saydığı yol ile boot'un
// kural 2'de katıldığı yol ayrışamaz. v0.10.971 — kilit kavramı kalktı;
// Replika tutarlılığı kartının denetimi (state_path_check.go) artık gözlem
// katlamaz, türü host satırlarından doğrudan çıkarır.
func mergeObservedStatePath(paths map[string]string, table, path, zkPrefix string) (split bool) {
	old, dup := paths[table]
	if dup && old != path {
		if old == unifiedStatePath(zkPrefix, table) {
			paths[table] = path // eski olan kazanır
		}
		return true
	}
	paths[table] = path
	return false
}

// queryReplicaPaths — <src>'ten (tablo → zookeeper_path) okur, yalnız
// state adaylarını tutar.
// replicaPathsSQL — SAF (v0.10.858): küme geneli system.replicas taraması
// tavansızdı (diğer clusterAllReplicas(system.*) okumalarının hepsi tavanlı).
func replicaPathsSQL(src string, skipUnavailable bool) string {
	q := "SELECT DISTINCT table, zookeeper_path FROM " + src + " WHERE database = currentDatabase() SETTINGS max_execution_time = 10"
	if skipUnavailable {
		q += ", skip_unavailable_shards = 1"
	}
	return q
}

func (s *Store) queryReplicaPaths(ctx context.Context, src string, skipUnavailable bool) (map[string]string, error) {
	q := replicaPathsSQL(src, skipUnavailable)
	rows, err := s.conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var t, p string
		if err := rows.Scan(&t, &p); err != nil {
			return nil, err
		}
		if !stateProbeTable(t) {
			continue
		}
		out[t] = p
	}
	return out, rows.Err()
}

// zkPrefix — Replicated yollarının kök öneki. adaptDDL ile AYNI
// varsayılana düşer; iki yerde ayrı yazılsaydı probe'un karşılaştırdığı
// yol ile üretilen yol sessizce ıraksardı.
func (s *Store) zkPrefix() string {
	p := strings.TrimRight(s.cfg.ReplicaPath, "/")
	if p == "" {
		p = "/clickhouse/tables"
	}
	return p
}

// quoteCHIdent — küme adını clusterAllReplicas'a string literal olarak
// geçirir. Tek tırnak ikilenir; makul olmayan bir küme adı sorguyu
// bozamaz.
func quoteCHIdent(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
