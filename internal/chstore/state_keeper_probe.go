// v0.10.978 — boot Keeper muhafızı (operatör onayı 2026-09-27, "boot'ta
// ZooKeeper eski yol kontrolü").
//
// SORUN. v0.10.971'den beri kural 4 "tablo hiçbir node'da yok → birleşik yol"
// der. Ama probe'un küme geneli okuması `skip_unavailable_shards=1` ile koşar:
// bağlantıyı reddeden bir replika HATASIZ atlanır ve o replikada ESKİ yolda
// duran tablo "hiçbir yerde yok" sanılır. Boot onu birleşik yola kurar — yeni
// bölünme, sessiz. 971 bunu yalnız ⚠ ile logluyordu (stateProbeCoverage).
//
// ÇÖZÜM. Gözlem EKSİKKEN (roster'daki bir replika cevap vermedi ya da küme
// okuması düştü) ve gözlenmemiş bir state tablosu kurulmak üzereyken, önce
// Keeper'a bakılır: Keeper kümenin ORTAK belleğidir, ulaşılamayan replikanın
// tablosunu da bilir. Bakılan znode'lar (her biri `…/replicas`, kanıt = en az
// bir replika znode'u; HER İKİSİ de okunur):
//
//	<önek>/<shard-dizini>/<t>/replicas   gözlenen ESKİ yollardaki her shard dizini
//	                                     (hiç gözlenmediyse: kümenin {shard} makroları)
//	<önek>/state/<t>/replicas            birleşik yol
//
// Keeper'da YALNIZ eski yolda replika kümesi varsa ona KATILIR (kural 2'nin
// Keeper'dan tamamlanmış hâli — muhafızın gerçek kör noktası bu şekildir);
// birleşik yolda varsa (eski yolda da olsa) ya da hiçbir yerde yoksa birleşik
// (kural 4). Keeper okuması düşerse v0.10.971 davranışı (birleşik) korunur ama
// muhafızın koşamadığı GÜRÜLTÜLÜ loglanır.
//
// HER İKİ YOLDA DA replika kümesi varsa BİRLEŞİK + ⚠ — eski KAZANMAZ. Keeper
// tek başına ayıramaz: 0009 penceresinde `<t>_old` yedeği eski yolda, canlı
// `<t>` birleşik yolda durur (RENAME znode taşımaz; ADIM 5 DROP'a dek en az
// bir gün) ve gözlem `_old`'u hiç görmez (stateProbeTable). Eski kazansaydı
// tablosu olmayan node YEDEĞİN grubuna katılır, var olan birleşik grup
// BÖLÜNÜRDÜ. v0.10.971 burada zaten birleşik diyordu; muhafız o hükmü korur
// ve çakışmayı gerekçede + log'da söyler (0009 ADIM 5 / geri alma / bölünmüş
// kurulum: Admin › ClickHouse › Replika tutarlılığı).
//
// SINIRLAR. Salt okuma; her okuma `path = ?` (kısıtsız Keeper taraması değil),
// LIMIT 1 ve max_execution_time ile tavanlı; shard dizini sayısı
// stateKeeperMaxDirs ile tavanlı; shard dizinleri ({shard} makro okuması dahil)
// gözlem başına BİR KEZ çözülür; gözlem TAMKEN hiç koşmaz (muhafız gözleme
// bağlanmaz bile: bindStateKeeperGuard). Karar SAF (decideStatePath), okuma
// ayrı (keeperStateEvidence), log metni SAF (stateKeeperGuardLogLine).
//
// KALAN KÖR NOKTA (bilinçli): bir shard'ın TÜM replikaları kapalıysa o shard'ın
// dizini ne gözlenir ne makrolardan gelir; muhafız o shard'ın eski yolunu
// bilemez. Tek çare Keeper'da öneğin çocuklarını listelemek olurdu — ayrı karar.
package chstore

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
)

// keeperEvidence — Keeper'da (system.zookeeper) bir tablo için bulunan replika
// kümesi kanıtı. err doluysa muhafız KOŞAMADI (kanıt yok demek DEĞİL).
type keeperEvidence struct {
	// legacyPath — boş değilse eski yolda replika kümesi var: `<önek>/<dizin>/<t>`
	// (ilk bulunan; dizinler sıralı gezilir).
	legacyPath string
	// unified — birleşik yolda replika kümesi var. legacyPath ile birlikte
	// doluysa iki yolda da küme var (0009 `_old` penceresi ya da bölünmüş
	// kurulum): karar birleşik, gerekçe ⚠ taşır.
	unified bool
	err     error
}

// stateKeeperLookup — muhafızın Keeper okuması (I/O). resolveStateReplicaPaths
// gözlem eksikken gözleme bağlar; nil = muhafız yok (tam gözlem, elle kurulan
// gözlemler: sihirbazın gölge Store'u, testler) → kural 4 olduğu gibi.
type stateKeeperLookup func(table string) keeperEvidence

// stateGuard — muhafızın bir karar üzerindeki izi (testler ve gerekçe için).
type stateGuard uint8

const (
	stateGuardSkipped    stateGuard = iota // sorulmadı: kural 1/2, tam gözlem ya da muhafız bağlı değil
	stateGuardLegacy                       // Keeper: YALNIZ eski yolda replika kümesi → katıl
	stateGuardUnified                      // Keeper: birleşik yolda replika kümesi (eski yolda da olsa)
	stateGuardNoEvidence                   // Keeper: hiçbir yolda yok → kural 4
	stateGuardFailed                       // Keeper okunamadı → kural 4 + ⚠
)

// statePathKeeperLegacyReason / statePathKeeperUnifiedReason — muhafızın gerekçe
// metinleri (seed planının kanıt satırı ve testler aynı sabiti görür).
// statePathKeeperBothTail — her iki yolda da küme varken gerekçeye ve log
// satırına eklenen ⚠ kuyruğu: Keeper tek başına ayıramaz, kural 4 korunur.
const (
	statePathKeeperLegacyReason  = "Keeper kanıtı: eski yolda replika var, gözlem eksikti"
	statePathKeeperUnifiedReason = "Keeper kanıtı: birleşik yolda replika var, gözlem eksikti"
	statePathKeeperBothTail      = "— Keeper tek başına ayıramaz (0009 `_old` yedeği, geri alınmış göç ya da bölünmüş kurulum): " +
		"kural 4 korunur, birleşik yola katılıyor; 0009 ADIM 5 / Admin › ClickHouse › Replika tutarlılığı"
)

// stateKeeperReplicasSQL — tek znode'un çocukları, tavanlı. `path = ?` şart:
// system.zookeeper yol filtresi olmadan okunamaz (ve okunmamalı). LIMIT 1:
// kanıt "en az bir replika znode'u"dur, listenin tamamı gerekmez. Node-yerel:
// Keeper kümenin ortak belleği, clusterAllReplicas'a gerek yok.
const stateKeeperReplicasSQL = "SELECT name FROM system.zookeeper WHERE path = ? LIMIT 1 SETTINGS max_execution_time = 5"

// stateKeeperMaxDirs — bakılacak eski yol shard dizini tavanı: muhafızın
// Keeper okuması sayısı tablo başına en fazla stateKeeperMaxDirs + 1 (birleşik yol).
const stateKeeperMaxDirs = 16

// decideStatePath — SAF. useUnifiedStatePath'in karar zinciri (kural 1 → 2 →
// 4) + kural 4'ün ÖNÜNE giren Keeper muhafızı. Muhafız YALNIZ şu üç koşul
// birlikte sağlanınca sorulur: probe koştu, tablo gözlenmedi, gözlem EKSİK
// (obs.complete=false) ve bir muhafız bağlı. Diğer her dalda keeper çağrılmaz.
func decideStatePath(obs stateObservation, zkPrefix, name string, keeper stateKeeperLookup) (unified bool, reason string, guard stateGuard) {
	if !obs.ok {
		return false, "probe koşmadı — mevcut kuruluma dokunulmuyor (eski yol)", stateGuardSkipped
	}
	if p, seen := obs.paths[name]; seen {
		if p == unifiedStatePath(zkPrefix, name) {
			return true, "tablo zaten birleşik yolda", stateGuardSkipped
		}
		return false, "tablo kümede eski yolda (" + p + ") — komşularına katılıyor", stateGuardSkipped
	}
	if obs.complete || keeper == nil {
		return true, statePathFreshReason, stateGuardSkipped
	}
	ev := keeper(name)
	switch {
	case ev.err != nil:
		return true, statePathFreshReason + "; ⚠ gözlem eksikti ve Keeper muhafızı koşamadı (" + ev.err.Error() + ") — v0.10.971 davranışı", stateGuardFailed
	case ev.unified:
		// Birleşik yolda küme VARSA hüküm birleşiktir — eski yolda da olsa.
		// Gözlem birleştirmesi (mergeObservedStatePath) "eski kazanır" der ama
		// o `_old`'u hiç görmez (stateProbeTable); Keeper ise bir yedeğin
		// replika kümesini `<t>`'ninkinden ayıramaz. 0009 penceresinde eski
		// kazansaydı muhafız var olan birleşik grubu BÖLERDİ — v0.10.971 burada
		// zaten birleşik diyordu, muhafız o hükmü korur ve çakışmayı söyler.
		if ev.legacyPath != "" {
			return true, statePathKeeperUnifiedReason + "; ⚠ eski yolda da replika kümesi var (" + ev.legacyPath + "/replicas) " + statePathKeeperBothTail, stateGuardUnified
		}
		return true, statePathKeeperUnifiedReason + " — birleşik yola katılıyor", stateGuardUnified
	case ev.legacyPath != "":
		// YALNIZ eski yolda küme var: muhafızın gerçek kör noktası — cevap
		// vermeyen komşuda eski yolda duran tablo. Ona katıl.
		return false, statePathKeeperLegacyReason + " (" + ev.legacyPath + ") — komşularına katılıyor", stateGuardLegacy
	}
	return true, statePathFreshReason + "; gözlem eksikti, Keeper'da hiçbir yolda replika kümesi yok (v0.10.978)", stateGuardNoEvidence
}

// legacyShardDirs — SAF: gözlenen ESKİ yollardan shard dizinleri. Yalnız
// `<önek>/<dizin>/<tablo>` biçimindeki yollar sayılır (0010'un
// `/state/<ad>_repart` ara yolu ve yabancı önekler elenir; genişlememiş
// `{shard}` literal'i Keeper'da var olamaz, elenir). Tekil, sıralı, tavanlı.
func legacyShardDirs(paths map[string]string, zkPrefix string) []string {
	set := map[string]bool{}
	for t, p := range paths {
		if p == unifiedStatePath(zkPrefix, t) {
			continue
		}
		// Uzunluk kapısı: önek ile sonek ÜST ÜSTE binerse (`<önek>/<t>`, dizin
		// yok) dilim sınırları çaprazlanır — boot'ta panik olmaz, yol elenir.
		if !strings.HasPrefix(p, zkPrefix+"/") || !strings.HasSuffix(p, "/"+t) || len(p) < len(zkPrefix)+len(t)+3 {
			continue
		}
		mid := p[len(zkPrefix)+1 : len(p)-len(t)-1]
		if mid == "" || strings.ContainsAny(mid, "{}") {
			continue
		}
		set[mid] = true
	}
	dirs := make([]string, 0, len(set))
	for d := range set {
		dirs = append(dirs, d)
	}
	return capShardDirs(dirs)
}

// capShardDirs — SAF: sıralı, boş/makro-literal elenmiş, stateKeeperMaxDirs'e
// kırpılmış. Makro değerleri (clusterMacrosByHost) da buradan geçer.
func capShardDirs(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		d = strings.TrimSpace(d)
		if d == "" || strings.ContainsAny(d, "{}") {
			continue
		}
		out = append(out, d)
	}
	sort.Strings(out)
	if len(out) > stateKeeperMaxDirs {
		out = out[:stateKeeperMaxDirs]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// keeperReplicasPath — SAF: `<önek>/<dizin>/<tablo>/replicas`. dir =
// stateReplicaDir iken unifiedStatePath ile aynı gövde.
func keeperReplicasPath(zkPrefix, dir, table string) string {
	return zkPrefix + "/" + dir + "/" + table + "/replicas"
}

// stateKeeperGuardLogLine — SAF: muhafızın boot log satırı. zkPrefix yalnız
// iki-yol satırında (birleşik znode'un tam adı) kullanılır.
func stateKeeperGuardLogLine(zkPrefix, table string, dirs []string, via string, ev keeperEvidence) string {
	head := "[chstore] Keeper muhafızı: " + table + " gözlenmedi ve gözlem eksikti"
	switch {
	case ev.err != nil:
		return fmt.Sprintf("[chstore] ⚠ Keeper muhafızı KOŞAMADI (%v): %s gözlenmedi ve gözlem eksikti — v0.10.971 davranışı: birleşik yola kurulur; "+
			"cevap vermeyen bir komşuda eski yolda duruyorsa o shard BÖLÜNÜR (Admin › ClickHouse › Replika tutarlılığı)", ev.err, table)
	case ev.unified && ev.legacyPath != "":
		// İki yolda da küme: birleşik kazanır, çakışma gürültülü (0009 ADIM 5 /
		// geri alma / bölünmüş kurulum — her znode adıyla).
		return head + " → ⚠ HER İKİ yolda da replika kümesi var: birleşik (" + keeperReplicasPath(zkPrefix, stateReplicaDir, table) + ") ve eski (" +
			ev.legacyPath + "/replicas) " + statePathKeeperBothTail
	case ev.legacyPath != "":
		return head + " → eski yolda replika kümesi VAR (" + ev.legacyPath + "/replicas) — komşularına katılıyor"
	case ev.unified:
		return head + " → birleşik yolda replika kümesi var — birleşik yola katılıyor"
	}
	looked := "birleşik"
	if len(dirs) > 0 {
		looked = strings.Join(dirs, ", ") + " (" + via + "), birleşik"
	}
	return head + " → hiçbir yolda replika kümesi yok (bakılan: " + looked + ") — kural 4: birleşik yola kurulur"
}

// bindStateKeeperGuard — gözlem EKSİKKEN çağrılır; gözleme muhafızı bağlar.
// ctx boot'un bağlamıdır (context.Background: New → migrate); ertelenen DDL ve
// admin yolu (seed planı) aynı kapanışı sonra da çağırabilir. Shard dizinleri
// TEMBEL ve gözlem başına BİR KEZ çözülür: taze kurulumda ~55 state
// tablosunun hiçbiri gözlenmemiştir ve gözlenen eski yol yoktur; makro okuması
// küme genelidir (clusterAllReplicas, 10 s tavan, ulaşılamayan replikanın
// bağlantı denemelerini de öder). Tablo başına tekrarlansa senkron migrate()
// 55 küme turu beklerdi. Hata da bir kez saklanır: aynı kümeye 55 kez sormak
// boot'u uzatır, cevabı değiştirmez; ⚠ satırı yine tablo başına loglanır.
// Keeper okumaları ise tablo başına kalır (her tablonun znode'u ayrı).
func (s *Store) bindStateKeeperGuard(ctx context.Context, obs *stateObservation) {
	zkPrefix := s.zkPrefix()
	paths := obs.paths
	var (
		once sync.Once
		dirs []string
		via  string
		derr error
	)
	resolve := func() ([]string, string, error) {
		once.Do(func() {
			dirs, via = legacyShardDirs(paths, zkPrefix), "gözlenen eski yollar"
			if len(dirs) != 0 {
				return
			}
			_, macroShards, err := s.clusterMacrosByHost(ctx, s.cfg.ClusterName)
			if err != nil {
				derr = fmt.Errorf("eski yol shard dizinleri için %w", err)
				return
			}
			dirs, via = capShardDirs(macroShards), "{shard} makroları"
		})
		return dirs, via, derr
	}
	obs.keeper = func(table string) keeperEvidence {
		dirs, via, err := resolve()
		if err != nil {
			ev := keeperEvidence{err: err}
			log.Print(stateKeeperGuardLogLine(zkPrefix, table, nil, "", ev))
			return ev
		}
		ev := s.keeperStateEvidence(ctx, zkPrefix, dirs, table)
		log.Print(stateKeeperGuardLogLine(zkPrefix, table, dirs, via, ev))
		return ev
	}
}

// keeperStateEvidence — Keeper okuması. Eski yol dizinleri sırayla, ilk
// kanıtta o döngü durur; SONRA birleşik yol HER ZAMAN okunur: iki yolda da
// küme varsa karar birleşiktir (decideStatePath), eski yoldaki kanıt tek
// başına hüküm değildir. Okuma sayısı en kötü hâlde değişmez (dirs + 1).
func (s *Store) keeperStateEvidence(ctx context.Context, zkPrefix string, dirs []string, table string) keeperEvidence {
	var legacyPath string
	for _, d := range dirs {
		p := keeperReplicasPath(zkPrefix, d, table)
		found, err := s.keeperHasReplica(ctx, p)
		if err != nil {
			return keeperEvidence{err: err}
		}
		if found {
			legacyPath = strings.TrimSuffix(p, "/replicas")
			break
		}
	}
	found, err := s.keeperHasReplica(ctx, keeperReplicasPath(zkPrefix, stateReplicaDir, table))
	if err != nil {
		return keeperEvidence{err: err}
	}
	return keeperEvidence{legacyPath: legacyPath, unified: found}
}

// keeperHasReplica — `<yol>` altında en az bir znode var mı? Olmayan yol
// (ZNONODE hatası ya da yeni CH'de boş sonuç) = yok; başka hata = okunamadı.
func (s *Store) keeperHasReplica(ctx context.Context, path string) (bool, error) {
	rows, err := s.conn.Query(ctx, stateKeeperReplicasSQL, path)
	if err != nil {
		if zkNoNode(err) {
			return false, nil
		}
		return false, err
	}
	defer rows.Close()
	found := rows.Next()
	if err := rows.Err(); err != nil {
		if zkNoNode(err) {
			return false, nil
		}
		return false, err
	}
	return found, nil
}
