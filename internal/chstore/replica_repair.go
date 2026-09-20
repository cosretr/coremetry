package chstore

// replica_repair.go — v0.10.820 "Replika onarımı" sihirbazı (spec onayı
// 2026-09-19 "Onay"; kart 791/792/818, test kümesi bulgusu: shard'ın bir
// host'unda state tablosu ya HİÇ yok (missing_replica) ya düz MergeTree
// (not_replicated) — ON CLUSTER DDL o host'ta işlenmemiş).
//
// Üç aşama, hepsi HOST'A ÖZEL bağlantıda (shardConn; ON CLUSTER yok — zaten
// ON CLUSTER'ın işlemediği host'u onarıyoruz):
//
//	PLAN (salt okuma): rapor yeniden ölçülür (FE satırına güvenilmez), eş
//	replikadan SHOW CREATE alınır, motor argümanları ayrıştırılır, ZK yolu
//	eşin GENİŞLETİLMİŞ yolu (system.replicas.zookeeper_path) literal yazılır,
//	{replica} hedefin makrosuyla genişletilip eşlerle ve ZK znode'larıyla
//	çakışmadığı doğrulanır, DB motoru Atomic mi (EXCHANGE için), kolonlar
//	eşle aynı mı (ATTACH için), partition listesi + boyut. Herhangi bir
//	koşul düşerse Blocked dolar ve APPLY reddeder.
//	APPLY: plan taze kurulur, adımlar sırayla koşar; SYNC LIGHTWEIGHT +
//	receive_timeout ile sınırlı, zaman aşımı "sürüyor" demektir (hata değil);
//	sonra system.replicas ile doğrulama. Koşulan adımlar hata yolunda da döner.
//	CLEANUP (yalnız düz tablo): EXCHANGE sonrası eski düz tablo `<t>_fix`
//	adındadır; motorlar DROP'tan hemen önce yeniden okunur — `<t>` Replicated
//	ve `<t>_fix` düz ise düşer; EXCHANGE olmamışsa (`<t>_fix` Replicated, `<t>`
//	düz) `_fix` düşürmek temiz geri alma; ikisi de aynı aileyse REDDEDİLİR.
//
// ATTACH PARTITION FROM eklemelidir (idempotent DEĞİL): yarım kalan apply'dan
// sonra plan `_fix` zaten var diye engeller; operatör önce cleanup koşar.
// ATTACH ile EXCHANGE arasında düz tabloya yazılan satırlar `_fix`'te kalır
// (uyarı olarak plana yazılır; state tabloları için pratikte sıfır).
// `.inner_id.*` MV iç tabloları kapsam dışı (EXCHANGE uuid'yi taşımaz; MV
// eski tabloya yazmaya devam ederdi) — sarkan MV sihirbazına.
//
// v0.10.829 — ÜÇÜNCÜ KİP: "İlk replikayı kur" (seed). Operatör bildirimi
// (test kümesi, 2026-09-20): `events · shard 1` İKİ host'ta da kırmızı,
// ikisinde de "Replicated değil (ReplacingMergeTree)". 820'nin merdiveni
// burada hiç başlamıyordu — eş replikadan DDL ve ZK yolu alır, shard'da
// Replicated eş YOK. Kök neden blindHosts ile aynı: küme tanımı IP ile
// yazılı, host'lar kendini is_local görmüyor, küme geneli DDL o tabloları
// hiç Replicated kurmadı; her host kendi DÜZ kopyasını, kendi satırlarıyla
// tutuyor.
//
// Seed kipi shard'ın SEÇİLEN host'unu shard'ın İLK replikası yapar:
//
//	kaynak DDL   — eşten değil, HEDEFİN KENDİ SHOW CREATE'inden (kolon
//	               kimliği garantisi: ATTACH edilecek düz tablo ile `_fix`
//	               aynı metinden doğar, sürüklenmiş katalog riski yok).
//	ZK yolu      — UYDURULMAZ: kanonik katalogdaki CREATE adaptDDL'den
//	               geçirilir, motor argümanları ORADAN okunur
//	               (replicatedArgs/useUnifiedStatePath → birleşik
//	               `<prefix>/state/<ad>` ya da eski `<prefix>/{shard}/<ad>`).
//	               Katalogda olmayan tablo ENGEL.
//	çapraz kontrol — düz motorun ailesi ("Replicated"+aile) kanonik ailenin
//	               kendisi olmalı; değilse ENGEL.
//
// Sonrası 820'nin düz-tablo merdiveninin AYNISI (`_fix` + ATTACH + EXCHANGE
// + SYNC). Bittiğinde shard'da 1/1 replika olur; ÖTEKİ host sıradan bir
// "eksik replika" satırına döner ve MEVCUT Onar onu aynı yola katar —
// iki yarı o zaman birleşir. O ana dek okumalar hâlâ ayrışabilir (plan
// bunu açıkça yazar).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// ReplicaRepairRequest — (tablo, shard, host) üçlüsü; host = hostName().
//
// Mode (v0.10.829): "" = 820'nin eşe katılma kipi (eksik/düz tablo),
// "seed" = "İlk replikayı kur". Kip İSTEKTEN gelir çünkü iki eylem farklı
// sözler verir (birinde eşin verisi gelir, ötekinde yalnız bu host'un
// verisi replike EDİLEBİLİR hâle gelir); uygunluğu yine de SUNUCU taze
// rapordan doğrular, istemcinin dediğine değil.
type ReplicaRepairRequest struct {
	Table string `json:"table"`
	Shard int    `json:"shard"`
	Host  string `json:"host"`
	Mode  string `json:"mode,omitempty"`
}

// ReplicaRepairPartition — hedefteki düz tablonun bir partition'ı.
type ReplicaRepairPartition struct {
	ID    string `json:"id"`
	Parts int    `json:"parts"`
	Rows  uint64 `json:"rows"`
	Bytes uint64 `json:"bytes"`
}

// ReplicaRepairPlan — ekranda gösterilen ve APPLY'ın koştuğu plan.
type ReplicaRepairPlan struct {
	Table         string                   `json:"table"`
	Shard         int                      `json:"shard"`
	Host          string                   `json:"host"`
	Database      string                   `json:"database"`
	Mode          string                   `json:"mode"` // missing | plain
	Peer          string                   `json:"peer"`
	ZKPath        string                   `json:"zkPath"`
	PeerReplica   string                   `json:"peerReplica"`
	TargetReplica string                   `json:"targetReplica"`
	Engine        string                   `json:"engine"`
	Partitions    []ReplicaRepairPartition `json:"partitions"`
	TotalRows     uint64                   `json:"totalRows"`
	TotalBytes    uint64                   `json:"totalBytes"`
	Steps         []string                 `json:"steps"`
	Cleanup       []string                 `json:"cleanup,omitempty"`
	Blocked       []string                 `json:"blocked,omitempty"`
	Warnings      []string                 `json:"warnings,omitempty"`
	Checks        []string                 `json:"checks"`
	// FixExists — hedefte `<t>_fix` duruyor (yarım kalmış onarım / EXCHANGE
	// sonrası eski düz tablo): FE Temizle'yi bundan türetir (oturum
	// durumundan değil — inceleme 2026-09-19).
	FixExists bool `json:"fixExists"`
	// PeerBytes / TargetFreeBytes — eşin tablo boyutu (klonlanacak) ve hedefin
	// boş alanı (tablonun depolama politikasındaki diskler); yetmiyorsa engel.
	PeerBytes       uint64 `json:"peerBytes"`
	TargetFreeBytes uint64 `json:"targetFreeBytes"`

	targetAddr, peerAddr string
}

// ReplicaRepairVerify — APPLY sonrası hedefin system.replicas satırı.
type ReplicaRepairVerify struct {
	Registered     bool   `json:"registered"`
	ZKPath         string `json:"zkPath,omitempty"`
	ReplicaName    string `json:"replicaName,omitempty"`
	TotalReplicas  int    `json:"totalReplicas"`
	ActiveReplicas int    `json:"activeReplicas"`
	ReadOnly       bool   `json:"readonly"`
	Engine         string `json:"engine,omitempty"`
}

// ReplicaRepairResult — koşulan adımlar + doğrulama.
type ReplicaRepairResult struct {
	Table       string              `json:"table"`
	Shard       int                 `json:"shard"`
	Host        string              `json:"host"`
	Mode        string              `json:"mode"`
	Steps       []string            `json:"steps"`
	SyncPending bool                `json:"syncPending"`
	Verify      ReplicaRepairVerify `json:"verify"`
	// VerifyError — doğrulama OKUNAMADI (DDL koştu; kartı yeniden ölç). Okuma
	// hatası başarılı onarımı "başarısız" göstermez (inceleme 2026-09-19).
	VerifyError string `json:"verifyError,omitempty"`
}

const (
	replicaRepairModeMissing = "missing"
	replicaRepairModePlain   = "plain"
	// replicaRepairModeSeed — v0.10.829: shard'da HİÇ Replicated replika
	// yok; bu host düz tablosuyla shard'ın İLK replikası olur.
	replicaRepairModeSeed = "seed"
	// replicaRepairModeSeedEmpty — v0.10.829 ikinci dal: shard'ın HİÇBİR
	// host'unda tablo yok (operatör: span_links_reverse · shard 2, iki host
	// da "tablo yok"). Taşınacak veri de yok: boş Replicated tablo doğrudan
	// `<t>` adıyla kurulur — `_fix`, ATTACH, EXCHANGE ve Temizle YOK.
	//
	// Bu bir PLAN kipidir, İSTEK kipi DEĞİL: istemci yine mode="seed"
	// gönderir (API allowlist'i "" | "seed"), dalı sunucu TAZE rapordan seçer.
	replicaRepairModeSeedEmpty = "seed_empty"
	replicaRepairFixSuffix     = "_fix"
	replicaRepairStepTimeout   = 2 * time.Minute
	// replicaRepairSyncTimeout — sürücü ReadTimeout 30 sn (store.go); SYNC
	// receive_timeout ile 25 sn'de sunucuda kesilir, zaman aşımı "sürüyor".
	replicaRepairSyncTimeout  = 30 * time.Second
	replicaRepairSyncReceiveS = 25
	// replicaRepairSeedHeadroom — v0.10.829: seed kiplerinde aranan SABİT
	// pay (64 MiB). ATTACH sabit bağ kurar, eşten çekilen parça yok; burada
	// yalnız yeni tablonun meta/log dosyaları ve ilk birleşmeler için yer
	// aranır. Yerel veri büyüklüğü bu kipte ÖLÇÜT DEĞİLDİR.
	replicaRepairSeedHeadroom = 64 << 20
)

var (
	reReplicatedEngine = regexp.MustCompile(`(?s)ENGINE\s*=\s*(Replicated[A-Za-z]*MergeTree)\s*\(\s*'([^']*)'\s*,\s*'([^']*)'`)
	reCreateHeader     = regexp.MustCompile(`^\s*CREATE\s+TABLE\s+([^\s(]+)`)
	reMacro            = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
	rePartitionID      = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// reCreateUUID — v0.10.829: `CREATE TABLE db.t UUID '<uuid>' (…)`.
	// Sunucu ayarı show_table_uuid_in_table_create_query_if_not_nil açıksa
	// SHOW CREATE uuid'yi YAZAR; o metni `_fix` başlığıyla koşmak yeni
	// tabloya ESKİ tablonun uuid'sini verirdi (çakışma / MV'nin yanlış
	// hedefi). Görürsek durur, uydurmayız.
	reCreateUUID = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+[^\s(]+\s+UUID\s+'`)
)

func qualifyCH(db, name string) string { return "`" + db + "`.`" + name + "`" }

// expandMacros — SAF: {ad} → system.macros değeri; çözülemeyen makro hata
// (yanlış yolda tablo kurmak = no_replication sınıfını yeniden üretmek).
func expandMacros(s string, macros map[string]string) (string, error) {
	var missing []string
	out := reMacro.ReplaceAllStringFunc(s, func(m string) string {
		k := m[1 : len(m)-1]
		if v, ok := macros[k]; ok && v != "" {
			return v
		}
		missing = append(missing, k)
		return m
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("çözülemeyen makro: {%s}", strings.Join(missing, "}, {"))
	}
	return out, nil
}

// replicatedEngineArgs — SAF: SHOW CREATE çıktısından motor ailesi + iki
// argüman (yol, replika). Argümansız Replicated (default_replica_path) → ok=false.
func replicatedEngineArgs(ddl string) (family, pathArg, replicaArg string, ok bool) {
	m := reReplicatedEngine.FindStringSubmatch(ddl)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

// ddlTableName — SAF: CREATE TABLE başlığındaki (nitelikli) ad → çıplak tablo adı.
func ddlTableName(ddl string) string {
	m := reCreateHeader.FindStringSubmatch(ddl)
	if m == nil {
		return ""
	}
	tok := m[1]
	if i := strings.LastIndex(tok, "."); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.Trim(tok, "`")
}

// rewriteReplicaDDL — SAF: eşin DDL'i → hedefte koşacak DDL. Başlık
// `db`.`newName`, ON CLUSTER sökülür, motorun İLK argümanı eşin genişletilmiş
// ZK yolu (literal), ikincisi ({replica}) aynen kalır.
func rewriteReplicaDDL(ddl, db, expectTable, newName, zkPath string) (string, error) {
	if got := ddlTableName(ddl); got != expectTable {
		return "", fmt.Errorf("eşin DDL'i %q tablosuna ait, %q bekleniyordu", got, expectTable)
	}
	hm := reCreateHeader.FindStringSubmatchIndex(ddl)
	out := "CREATE TABLE " + qualifyCH(db, newName) + ddl[hm[3]:]
	out = stripOnCluster(out)
	em := reReplicatedEngine.FindStringSubmatchIndex(out)
	if em == nil {
		return "", errors.New("motor argümanları (Replicated) bulunamadı")
	}
	// grup 2 = yol argümanı (tırnaksız)
	out = out[:em[4]] + zkPath + out[em[5]:]
	return out, nil
}

// seedEligibility — SAF: "İlk replikayı kur" bu (tablo × shard × host) için
// uygun mu? "" = uygun, dolu dize = RET GEREKÇESİ (operatöre gösterilir).
//
// Girdi TAZE ReplicaConsistency'den gelir, istemciden değil — FE'deki
// canSeedFirstReplica aynı dört kuralı okur ama o yalnız DÜĞMEYİ gizler;
// karar burada verilir (bayat bir kart satırı DDL koşturamaz).
//
// Dört kural:
//  1. `.inner*` MV iç tablosu ve `_fix` sihirbazın kendi geçici tablosu —
//     ikisi de tablo düzeyi onarımın kapsamı dışında (820/824).
//  2. Karar yapısal olmalı (missing_replica / not_replicated); ıraksama,
//     readonly, gecikme BAŞKA hastalıklar, ilk replika onları onarmaz.
//  3. Shard'da HİÇ Replicated replika olmamalı. Bir tane bile varsa doğru
//     eylem ona KATILMAKTIR (mevcut Onar): ikinci bir ZK yolu açmak
//     no_replication sınıfını elle üretmek olurdu.
//  4. Host'un DÜZ tablosu olmalı — YA DA shard'ın hiçbir host'unda tablo
//     olmamalı (v0.10.829 boş-shard dalı: taşınacak veri yok, boş Replicated
//     tablo kurulur). Shard'da düz tablo TAŞIYAN başka bir host varsa,
//     tablosu olmayan host uygun DEĞİLDİR: ilk replika veriyi taşıyan host'ta
//     kurulmalı, yoksa shard'ın verisi ikinci sınıf kalır (boş replika
//     birinci olur, dolu host sonradan "Onar"la katılırken yerel satırlarını
//     ATTACH etmek zorunda kalır — sıra tersine dönerse veri yolculuğu uzar).
func seedEligibility(table string, shard *ReplicaShard, host string) string {
	switch {
	case strings.HasPrefix(table, ".inner"):
		return table + " bir MV iç tablosu — tablo düzeyi onarım uygulanmaz (MV onarımı kartı)"
	case strings.HasSuffix(table, replicaRepairFixSuffix):
		return table + " sihirbazın geçici tablosu — sahibi tablonun satırında Temizle"
	case shard == nil:
		return "shard raporda yok — yeniden Ölç"
	case shard.Verdict != ReplicaMissing && shard.Verdict != ReplicaNotReplicated:
		return fmt.Sprintf("%s / shard %d kararı %q — ilk replika yalnız eksik/replike-olmayan tabloda kurulur", table, shard.Shard, shard.Verdict)
	case len(shard.Replicas) > 0:
		return fmt.Sprintf("%s / shard %d'de zaten %d Replicated replika var — ilk replika kurulmaz; o satırdaki \"Onar\" bu host'u eşe katar", table, shard.Shard, len(shard.Replicas))
	}
	for _, m := range shard.Missing {
		if m.Host != host {
			continue
		}
		if strings.HasPrefix(m.Engine, "Replicated") {
			return fmt.Sprintf("%s'ta tablo zaten Replicated (%s) ama kayıtsız — yeniden Ölç, kalıcıysa elle incele", host, m.Engine)
		}
		if m.Engine == "" {
			if seeder := shardPlainSeeder(shard, host); seeder != "" {
				return fmt.Sprintf("%s'ta tablo YOK ama %s'ta düz tablo var — ilk replikayı ÖNCE %s'ta kur (veri oradan gelsin), sonra bu satırdaki \"Onar\" bu host'u o yola katar", host, seeder, seeder)
			}
		}
		return ""
	}
	return host + " bu shard'da eksik host değil — yeniden Ölç"
}

// seedObservedPath — kümede GÖZLENEN bir Replicated kayıt (hangi host, hangi
// ZK yolu). Kaynağı TAZE ReplicaConsistency raporu; tablonun TÜM shard'ları
// taranır, yalnız istenen shard değil.
type seedObservedPath struct{ Host, Path string }

// tableObservedPaths — SAF: bir tablonun bütün shard'larındaki sağlam
// Replicated kayıtların (host, zookeeper_path) çiftleri, host'a göre sıralı.
func tableObservedPaths(tbl *ReplicaTable) []seedObservedPath {
	if tbl == nil {
		return nil
	}
	var out []seedObservedPath
	for _, sh := range tbl.Shards {
		for _, r := range sh.Replicas {
			if r.ZKPath == "" || !strings.HasPrefix(r.Engine, "Replicated") {
				continue
			}
			out = append(out, seedObservedPath{Host: r.Host, Path: r.ZKPath})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// seedPathDecision — SAF ve v0.10.829 incelemesinin 1 NUMARALI bulgusu:
// ikinci bir replikasyon grubu yaratmayı imkânsız kılan kapı.
//
// SORUN. Kanonik yol `adaptDDL` → `useUnifiedStatePath` üzerinden geliyor ve o
// karar POD'un BOOT anındaki `stateObs` sondajına dayanıyor. Sondaj tabloyu
// görmediyse (shard erişilemezken boot, clusterAllReplicas hatası, zaten
// bölünmüş kurulum) kuşak kuralı "eski yol" diyebilir; birleşik biçimde
// KÜME GENELİNDE TEK grup olan bir state tablosu için bu, `<prefix>/<shard>/
// <ad>` diye UYDURULMUŞ bir yol demektir. ZK kontrolü o yolu sorar, ZNONODE
// alır, "ilk replika onu yaratır" der — ve ikinci bağımsız grup doğar. Kart
// sonrasında iki shard'ı da `ok` gösterir, çünkü replicaVerdict yolları
// yalnız AYNI shard içinde karşılaştırır: bölünme görünmez olur.
//
// KARAR (kanıt kodun değil KÜMENİN elinde):
//
//	(a) Kanonik biçim MAKROSUZ (birleşik state yolu = tek grup) ve kümede bu
//	    tablo bir yerde Replicated ise → GÖZLENEN literal yol kullanılır
//	    (komşularına katıl). İki FARKLI yol gözlendiyse kurulum zaten
//	    bölünmüş: ENGEL.
//	(b) Kanonik biçim MAKROLU (shard başına ayrı grup) ise formül
//	    DOĞRULANIR: gözlenen her kaydın yolu, o host'un makrolarıyla
//	    genişletilmiş kanonik yola EŞİT olmalı. Değilse hesap kümeyle
//	    çelişiyordur: ENGEL.
//	(c) Hiçbir shard'da Replicated kayıt yoksa kanonik hesap sürer.
//
// Dönüş: kullanılacak yol, plana yazılacak KANIT satırı, engel ("" = yok).
func seedPathDecision(table, pathArg, computed string, observed []seedObservedPath, macrosOf map[string]map[string]string) (path, evidence, blocked string) {
	if len(observed) == 0 {
		return computed, fmt.Sprintf("ZK yolu: kümede %s hiçbir shard'da Replicated değil — yol kanonik katalogdan (%s)", table, computed), ""
	}
	hosts := make([]string, 0, len(observed))
	for _, o := range observed {
		hosts = append(hosts, o.Host+" → "+o.Path)
	}
	if !strings.Contains(pathArg, "{") {
		// (a) Makrosuz kanonik yol: bu tablo KÜME GENELİNDE tek grup.
		distinct := map[string]bool{}
		for _, o := range observed {
			distinct[o.Path] = true
		}
		if len(distinct) > 1 {
			return "", "", fmt.Sprintf("%s kümede İKİ FARKLI ZK yolunda Replicated (%s) — kurulum zaten bölünmüş; ilk replika kurulmaz, önce hangi grubun kalacağına karar verilmeli (runbook)", table, strings.Join(hosts, ", "))
		}
		got := observed[0].Path
		ev := fmt.Sprintf("ZK yolu: kümede GÖZLENEN yol benimsendi (%s) — birleşik state yolu küme genelinde TEK gruptur", strings.Join(hosts, ", "))
		if got != computed {
			ev += fmt.Sprintf("; kanonik hesap %s diyordu, gözlenen kazandı (uydurulmuş yola ikinci grup açılmaz)", computed)
		}
		return got, ev, ""
	}
	// (b) Makrolu kanonik yol: shard başına ayrı grup — formül doğrulanır.
	var unverified []string
	for _, o := range observed {
		want, err := expandMacros(pathArg, macrosOf[o.Host])
		if err != nil {
			unverified = append(unverified, o.Host)
			continue
		}
		if want != o.Path {
			return "", "", fmt.Sprintf("%s zaten %s host'unda %s yolunda Replicated; kanonik hesap o host için %s üretiyor — hesap kümeyle ÇELİŞİYOR, uydurulmuş yola ilk replika kurulmaz (makroları/kuşağı düzelt, runbook)", table, o.Host, o.Path, want)
		}
	}
	ev := fmt.Sprintf("ZK yolu: kanonik hesap kümede gözlenen yolları ÜRETİYOR (%s) — shard başına ayrı grup, hedef için %s", strings.Join(hosts, ", "), computed)
	if len(unverified) > 0 {
		ev += " (doğrulanamayan host: " + strings.Join(unverified, ", ") + ")"
	}
	return computed, ev, ""
}

// seedGenerationEvidence — SAF: kanonik yolun KUŞAĞI hangi kanıtla seçildi?
// Operatör Uygula'dan önce görsün diye plan.Checks'e yazılır.
func seedGenerationEvidence(table string, isState, unified bool, reason string) string {
	if !isState {
		return fmt.Sprintf("ZK kuşağı: %s shard kayıtlarında geçiyor → shard'lı telemetri yolu", table)
	}
	return fmt.Sprintf("ZK kuşağı: %s state tablosu → %s yol (gerekçe: %s)", table,
		map[bool]string{true: "BİRLEŞİK", false: "ESKİ (shard'lı)"}[unified], reason)
}

// shardPlainSeeder — SAF: shard'ın BAŞKA bir host'unda düz (Replicated
// olmayan) tablo var mı? Varsa adı ("" = yok). Boş-shard dalının kapısı:
// tohumlanacak veri bir yerdeyse ilk replika ORADA kurulur.
func shardPlainSeeder(shard *ReplicaShard, except string) string {
	for _, o := range shard.Missing {
		if o.Host == except || o.Engine == "" || strings.HasPrefix(o.Engine, "Replicated") {
			continue
		}
		return o.Host
	}
	return ""
}

// isSeedMode — SAF: plan kipi "ilk replika" ailesinden mi (düz tablodan
// tohumlama ya da boş tablo kurulumu)? Doğrulama notu (1/1 beklentisi) ikisi
// için de geçerli; `repairMovesLocalData` ise YALNIZ düz dal için true.
func isSeedMode(mode string) bool {
	return mode == replicaRepairModeSeed || mode == replicaRepairModeSeedEmpty
}

// repairMovesLocalData — SAF: bu kipte hedefin YEREL verisi `_fix`'e ATTACH
// edilip EXCHANGE ile takas edilir mi? plain ve seed EVET (ikisi de düz bir
// tablonun üstünde çalışır), missing HAYIR (tablo yok, veri eşten klonlanır).
// Tek kaynak: `_fix` varlık kapısı, Cleanup metni, kolon/anahtar/indeks
// kapıları, partition listesi ve EXCHANGE adımı hep buna bakar.
func repairMovesLocalData(mode string) bool {
	return mode == replicaRepairModePlain || mode == replicaRepairModeSeed
}

// plainEngineFamily — SAF: bir CREATE metninin DÜZ MergeTree ailesi
// ("ReplacingMergeTree"…); Replicated motor ya da tanınmayan aile → "".
// adaptDDL'in tanıdığı ailelerin AYNISI (reEngine), böylece "düz motorun
// Replicated karşılığı" ile "kanonik katalogun ürettiği aile" aynı sözlükten
// okunur.
func plainEngineFamily(ddl string) string {
	m := reEngine.FindStringSubmatch(ddl)
	if m == nil {
		return ""
	}
	return m[1]
}

// rewriteSeedDDL — SAF: hedefin KENDİ (düz) SHOW CREATE'i → `<t>_fix` için
// Replicated CREATE. Yalnız İKİ şey değişir:
//
//	(a) başlık `db`.`newName` olur (ON CLUSTER sökülür — zaten SHOW CREATE
//	    onu yazmaz, ama 820 ile aynı kemer),
//	(b) motor Replicated aileye çevrilir ve ZK argümanları ÖNE girer.
//
// Kolon bloğu, atlama indeksleri, PARTITION/ORDER BY, TTL ve SETTINGS
// BAYT BAYT aynı kalır: `_fix` ATTACH kaynağının ikizidir, o yüzden
// checkStructureAndGetMergeTreeData kapısı tanım gereği geçer.
//
// zkPath GENİŞLETİLMİŞ literal yazılır (planın gösterdiği yol ile koşan
// yolun ayrışmasını imkânsız kılar); replicaArg makro ŞABLONU olarak kalır
// ({replica} / {shard}-{replica}) — her host kendi adını üretir.
func rewriteSeedDDL(ddl, db, expectTable, newName, zkPath, replicaArg string) (string, error) {
	if got := ddlTableName(ddl); got != expectTable {
		return "", fmt.Errorf("hedefin DDL'i %q tablosuna ait, %q bekleniyordu", got, expectTable)
	}
	if reCreateUUID.MatchString(ddl) {
		return "", errors.New("hedefin DDL'i UUID cümlesi taşıyor — `_fix` eski uuid'yi devralırdı; runbook ile elle")
	}
	if _, _, _, ok := replicatedEngineArgs(ddl); ok {
		return "", errors.New("hedefteki tablo zaten Replicated — ilk replika kurulmaz")
	}
	fam := plainEngineFamily(ddl)
	if fam == "" {
		return "", errors.New("hedefin motoru tanınan MergeTree ailesinden değil — runbook ile elle")
	}
	hm := reCreateHeader.FindStringSubmatchIndex(ddl)
	if hm == nil {
		return "", errors.New("CREATE TABLE başlığı okunamadı")
	}
	out := stripOnCluster("CREATE TABLE " + qualifyCH(db, newName) + ddl[hm[3]:])
	out = spliceReplicatedEngine(out, fmt.Sprintf("'%s', '%s'", zkPath, replicaArg))
	gotFam, gotPath, gotRep, ok := replicatedEngineArgs(out)
	if !ok || gotPath != zkPath || gotRep != replicaArg || gotFam != "Replicated"+fam {
		return "", fmt.Errorf("motor Replicated aileye çevrilemedi (%q)", gotFam)
	}
	return out, nil
}

// rewriteCanonicalSeedDDL — SAF (v0.10.829 boş-shard dalı): kanonik katalog
// fragmanı (adaptDDL çıktısı: `CREATE TABLE IF NOT EXISTS <ad> ON CLUSTER <c>
// (…) ENGINE = Replicated…('<yol {shard} ile>', '<replika>', …)`) → hedefte
// koşacak DDL.
//
// ÜÇ değişiklik: başlık `db`.`<ad>` olur, `IF NOT EXISTS` ve `ON CLUSTER`
// sökülür, ZK yolu GENİŞLETİLMİŞ literal yazılır.
//
// IF NOT EXISTS neden sökülüyor: bu dalda tablo olmadığı RAPORLA saptandı;
// yine de varsa bu bir SÜRPRİZDİR ve sessizce geçilmemeli — CREATE gürültülü
// düşsün, operatör kartı yeniden ölçsün. ON CLUSTER neden sökülüyor: zaten
// ON CLUSTER'ın ulaşmadığı host'u onarıyoruz (820 ile aynı gerekçe).
func rewriteCanonicalSeedDDL(frag, db, expectTable, zkPath string) (string, error) {
	name, kind := identifyDDLTarget(frag)
	if kind != "table" || name != expectTable {
		return "", fmt.Errorf("kanonik fragman %q tablosuna ait, %q bekleniyordu", name, expectTable)
	}
	m := reCreateTable.FindStringSubmatchIndex(frag)
	if m == nil {
		return "", errors.New("kanonik fragmanın CREATE TABLE başlığı okunamadı")
	}
	out := stripOnCluster("CREATE TABLE " + qualifyCH(db, expectTable) + frag[m[5]:])
	em := reReplicatedEngine.FindStringSubmatchIndex(out)
	if em == nil {
		return "", errors.New("kanonik fragmanda Replicated motor argümanı yok")
	}
	// grup 2 = yol argümanı (tırnaksız) — rewriteReplicaDDL ile aynı splice.
	out = out[:em[4]] + zkPath + out[em[5]:]
	if _, gotPath, _, ok := replicatedEngineArgs(out); !ok || gotPath != zkPath {
		return "", errors.New("ZK yolu literal yazılamadı")
	}
	// Kemer: stripOnCluster'ın SÖKTÜĞÜNÜ iddia ettiği şey gerçekten gitti mi?
	// Kontrol aynı regexle (reOnCluster) yapılır — çıplak dize yazmak hem
	// ikinci bir tanım olurdu hem de küme-öneki grep kapısını (820 kaynak
	// pini) KENDİ metnimizle tetiklerdi: kapı Go dize literallerine bakar,
	// kelimeyi anlatan bir literal de eşleşir (v0.10.829).
	if reOnCluster.MatchString(out) || strings.Contains(out, "IF NOT EXISTS") {
		return "", errors.New("başlık normalize edilemedi (küme öneki / koşullu yaratma kaldı)")
	}
	return out, nil
}

// seedReplicaCollisions — SAF: hedefin replika adı, AYNI shard'ın öteki
// host'larının aynı şablondan üreteceği adla çakışıyor mu?
//
// Seed kipinde shard'da kayıtlı replika YOKTUR, yani 820'nin
// "eşin replicaName'i ile aynı mı" kapısı hiç ısırmaz. Ama çakışma riski
// ORADA: makroları bozuk bir kümede iki host da {replica}=r1 taşıyabilir;
// o zaman ilk replika kurulur, ÖTEKİ host'un Onar'ı "Replica already exists"
// ile düşer ve operatör bunu ancak ikinci adımda öğrenir. Şimdi söylenir.
//
// others: host → makrolar (yalnız shard'ın ÖTEKİ host'ları). Makroları
// okunamayan host sessiz geçer (ayrı uyarı); yalnız ÜRETİLEN ad eşitse engel.
func seedReplicaCollisions(replicaArg, selfName string, others map[string]map[string]string) []string {
	hosts := make([]string, 0, len(others))
	for h := range others {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	var out []string
	for _, h := range hosts {
		name, err := expandMacros(replicaArg, others[h])
		if err != nil || name != selfName {
			continue
		}
		out = append(out, h)
	}
	return out
}

// seedReplicaCountNote — SAF: seed sonrası system.replicas'ın kayıtlı replika
// sayısı. 1 = beklenen (bu host shard'ın İLK ve tek replikası). 0 = tablo
// Replicated ama Keeper'a kayıt DÜŞMEMİŞ: onarım sayılmaz. >1 = yola bu
// arada bir eş katılmış; DDL koştu, tablo çalışıyor — HATA DEĞİL, not.
func seedReplicaCountNote(total int) (string, error) {
	switch {
	case total == 1:
		return "", nil
	case total < 1:
		return "", errors.New("doğrulama: tablo Replicated ama Keeper'da kayıtlı replika yok (total_replicas=0)")
	default:
		return fmt.Sprintf("doğrulama: yolda %d kayıtlı replika var (ilk replika için 1 beklenir) — bu arada bir eş katılmış olabilir", total), nil
	}
}

// seedZKOwnershipGate — SAF ve v0.10.829 incelemesinin 2 NUMARALI bulgusu:
// seed dalında bu kapı AÇIK DÜŞEMEZ.
//
// "Okuyamadım" ile "sahipsiz" aynı şey DEĞİLDİR. Eski hâlinde ZNONODE
// dışındaki her okuma hatası uyarıya dönüyordu ve Apply yine koşuyordu —
// okunamayan bir yola ilk replika kurmak tam da ikinci grup yaratma
// senaryosudur. İlk replika YALNIZCA sahipsiz olduğu DOĞRULANMIŞ yola kurulur.
//
// Dönüş: plana yazılacak kanıt satırı, engel ("" = geç).
func seedZKOwnershipGate(path string, names []string, err error) (evidence, blocked string) {
	switch {
	case err != nil && zkNoNode(err):
		return fmt.Sprintf("ZK yolu %s henüz yok (ZNONODE doğrulandı) — ilk replika onu yaratır", path), ""
	case err != nil:
		return "", fmt.Sprintf("ZK yolu %s okunamadı (%v) — ilk replika yalnız SAHİPSİZ olduğu DOĞRULANMIŞ yola kurulur; Keeper erişimini düzeltip yeniden planla", path, err)
	case len(names) > 0:
		// v0.10.829 incelemesi (7): sahiplerin ADI yazılır ve gerçek çıkış
		// yolu söylenir. Kart bu satıra "Onar" çizemez — o düğme AYNI shard'da
		// KAYITLI bir eş ister; buradaki sahip başka bir shard'ın (ya da başka
		// bir kurulumun) replikası olabilir.
		return "", fmt.Sprintf("ZK'da %s/replicas altında zaten %d replika kayıtlı (%s) — bu yolun sahibi var, ilk replika kurulmaz. Sahipler bu shard'ın host'larıysa rapor bayat demektir: yeniden Ölç, satır \"Onar\"a döner. Değilse yol PAYLAŞILIYOR: makro/kuşak yapılandırmasını düzelt (runbook)", path, len(names), strings.Join(names, ", "))
	default:
		return fmt.Sprintf("ZK yolu %s var ama altında kayıtlı replika yok — ilk replika onu sahiplenir", path), ""
	}
}

// zkNoNode — SAF: system.zookeeper okuması "böyle bir znode yok" dedi mi?
// Seed kipinde bu BEKLENEN cevaptır (yolu ilk replika yaratacak), okuma
// hatası değil — ayırmazsak sağlıklı durum "ZK okunamadı" uyarısı üretirdi.
func zkNoNode(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "ZNONODE") || strings.Contains(msg, "No node")
}

// attachStatements — SAF: partition başına ATTACH; kimlik allowlist'i
// (system.parts.partition_id: tarih, yyyymm, 'all', hash).
func attachStatements(db, table string, parts []ReplicaRepairPartition) ([]string, error) {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if !rePartitionID.MatchString(p.ID) {
			return nil, fmt.Errorf("beklenmeyen partition_id %q", p.ID)
		}
		out = append(out, "ALTER TABLE "+qualifyCH(db, table+replicaRepairFixSuffix)+" ATTACH PARTITION ID '"+p.ID+"' FROM "+qualifyCH(db, table))
	}
	return out, nil
}

// cleanupGate — SAF: DROP'tan hemen önce okunan motorlara göre `_fix`
// düşürülebilir mi. ok=true + note: hangi durum.
func cleanupGate(engineT, engineFix string) (note string, err error) {
	repT, repF := strings.HasPrefix(engineT, "Replicated"), strings.HasPrefix(engineFix, "Replicated")
	switch {
	case engineFix == "":
		return "", errors.New("`_fix` tablosu yok — temizlenecek bir şey yok")
	case engineT == "":
		return "", errors.New("canlı tablo yok, `_fix` var — belirsiz durum, elle incele")
	case repT && !repF:
		return "EXCHANGE olmuş: eski düz tablo düşer", nil
	case !repT && repF:
		return "EXCHANGE olmamış: Replicated `_fix` düşer (geri alma; eşe çekilmiş parçalar geri gelmez)", nil
	default:
		return "", fmt.Errorf("iki tablo da aynı ailede (%s / %s) — belirsiz, elle incele", engineT, engineFix)
	}
}

type chColumn struct{ Name, Type string }

// chTableKeys — ATTACH PARTITION FROM'un kolonların ötesinde baktığı alanlar:
// partition/sıralama/birincil anahtar (ŞART) + depolama politikası (ŞART DEĞİL,
// v0.10.828) — hepsi system.tables'tan.
type chTableKeys struct{ PartitionKey, SortingKey, PrimaryKey, StoragePolicy string }

// tableKeysDiff — SAF: eş ile hedef anahtar farkı, İKİ KOVAYA ayrılmış.
//
// blocking: partition key / ORDER BY / PRIMARY KEY. Bunlar gerçekten
// checkStructureAndGetMergeTreeData'da, AST METNİ olarak tam eşitlik ister
// (MergeTreeData.cpp: "Tables have different ordering / partition key /
// primary key") — yani burada sıra da önemli.
//
// notes: DEPOLAMA POLİTİKASI. v0.10.828: politika eşitliği ATTACH'ın şartı
// DEĞİL. Bizim yolumuzda ATTACH hedefi `_fix` (Replicated) olduğu için
// StorageReplicatedMergeTree::replacePartitionFrom (satır 8757, v26.2.4.23)
// koşar: orada politika hiç karşılaştırılmaz, ATTACH dalı parçayı
// `must_on_same_disk=false` ile klonlar ("Attach can work on another disk",
// satır ~8972). UNKNOWN_POLICY fırlatması movePartitionToTable'da
// (satır 9102/9111) — o BAŞKA ifade (MOVE PARTITION TO TABLE). Düz motorda da
// aynı: StorageMergeTree::replacePartitionFrom (2553) politika uyumunu yalnız
// must_on_same_disk'i gevşetmek için hesaplar, fırlatma movePartitionToTable'da
// (2704).
func tableKeysDiff(peer, target chTableKeys) (blocking, notes []string) {
	if peer.PartitionKey != target.PartitionKey {
		blocking = append(blocking, fmt.Sprintf("partition key: eş %q ≠ hedef %q", peer.PartitionKey, target.PartitionKey))
	}
	if peer.SortingKey != target.SortingKey {
		blocking = append(blocking, fmt.Sprintf("ORDER BY: eş %q ≠ hedef %q", peer.SortingKey, target.SortingKey))
	}
	if peer.PrimaryKey != target.PrimaryKey {
		blocking = append(blocking, fmt.Sprintf("PRIMARY KEY: eş %q ≠ hedef %q", peer.PrimaryKey, target.PrimaryKey))
	}
	if peer.StoragePolicy != target.StoragePolicy {
		notes = append(notes, fmt.Sprintf("depolama politikası farklı: eş %q ≠ hedef %q", peer.StoragePolicy, target.StoragePolicy))
	}
	return blocking, notes
}

// tableKeysGate — SAF: anahtar karşılaştırmasının plan çıktısı (engel · uyarı · ✓).
func tableKeysGate(peer, target chTableKeys) (blocked, warnings []string, check string) {
	blocking, notes := tableKeysDiff(peer, target)
	if len(blocking) > 0 {
		blocked = append(blocked, "anahtarlar eşle aynı değil (ATTACH PARTITION FROM başarısız olur): "+strings.Join(blocking, "; "))
	}
	for _, n := range notes {
		warnings = append(warnings, n+" (ATTACH için engel değil: politika eşitliğini yalnız MOVE PARTITION TO TABLE ister; ATTACH parçayı başka diske klonlayabilir) — disk payı hesabı hedefin politikasındaki diskleri okur")
	}
	if len(blocking) == 0 {
		check = "partition/ORDER BY/PRIMARY KEY eşle aynı (depolama politikası ATTACH'ın şartı değil, projeksiyonlar karşılaştırılmadı)"
	}
	return blocked, warnings, check
}

// skipIndexDiff — SAF: atlama indeksleri (ad|tür|ifade) kümesi farkı, İKİ KOVA.
//
// YÖN ÖNEMLİ. Koştuğumuz ifade `ALTER TABLE <t>_fix ATTACH PARTITION … FROM <t>`:
// ATTACH HEDEFİ `_fix` (eşin DDL'i → EŞİN indeksleri), ATTACH KAYNAĞI hedef
// host'taki düz `<t>` (HEDEFİN indeksleri). CH v26.2.4.23,
// MergeTreeData::checkStructureAndGetMergeTreeData içindeki check_definitions
// lambda'sı `(my_descriptions=ATTACH hedefi, src_descriptions=ATTACH kaynağı)`
// ile çağrılır ve şunu ister: hedef, kaynağın ÜST KÜMESİ olmalı
// (`my.size() < src.size()` → false; kaynaktaki her tanım hedefte de olmalı).
// Hedefteki FAZLA indeks serbesttir — TAM eşitlik yalnız
// enforce_index_structure_match_on_partition_manipulation açıkken istenir
// (MergeTreeSettings.cpp:739, varsayılan false).
//
// blocking: düz tabloda (ATTACH kaynağı) olup eşte (ATTACH hedefi) olmayan.
// notes:    eşte olup düz tabloda olmayan — ayar kapalıyken engel değil.
func skipIndexDiff(peer, target []string) (blocking, notes []string) {
	pm, tm := map[string]bool{}, map[string]bool{}
	for _, x := range peer {
		pm[x] = true
	}
	for _, x := range target {
		tm[x] = true
	}
	for _, x := range peer {
		if !tm[x] {
			notes = append(notes, "yalnız eşte var: "+x)
		}
	}
	for _, x := range target {
		if !pm[x] {
			blocking = append(blocking, "eşte yok: "+x)
		}
	}
	return blocking, notes
}

// strictIndexMatch — SAF: system.merge_tree_settings.value → bool.
// enforce_index_structure_match_on_partition_manipulation açıksa ATTACH
// indeks/projeksiyon kümelerinin BİREBİR aynı olmasını ister.
func strictIndexMatch(value string) bool {
	return value == "1" || strings.EqualFold(value, "true")
}

// skipIndexGate — SAF: indeks karşılaştırmasının plan çıktısı (engel · uyarı · ✓).
// strictKnown=false → ayar okunamadı; fazla indeks engel sayılmaz ama uyarı
// riski söyler (küme ayarı açıksa ATTACH yine düşer).
func skipIndexGate(peer, target []string, strictOn, strictKnown bool) (blocked, warnings []string, check string) {
	blocking, notes := skipIndexDiff(peer, target)
	if len(blocking) > 0 {
		blocked = append(blocked, "atlama indeksleri: düz tabloda olup eşte olmayan indeks ATTACH'ı düşürür (ATTACH hedefi kaynağın ÜST kümesi olmalı): "+strings.Join(blocking, "; "))
	}
	for _, n := range notes {
		switch {
		case strictKnown && strictOn:
			blocked = append(blocked, n+" — enforce_index_structure_match_on_partition_manipulation hedefte AÇIK: ATTACH indeks kümelerinin BİREBİR aynı olmasını ister")
		case strictKnown:
			warnings = append(warnings, n+" (ATTACH için engel değil: ATTACH hedefi kaynağın ÜST kümesi olabilir; enforce_index_structure_match_on_partition_manipulation hedefte kapalı okundu)")
		default:
			warnings = append(warnings, n+" (ATTACH için engel değil: ATTACH hedefi kaynağın ÜST kümesi olabilir); ayar okunamadı — küme `enforce_index_structure_match_on_partition_manipulation` açıksa ATTACH yine düşebilir")
		}
	}
	if len(blocked) == 0 {
		check = fmt.Sprintf("atlama indeksleri: düz tablonun %d indeksinin hepsi eşte de var (eşte %d; fazlası ATTACH'ın şartı değil)", len(target), len(peer))
	}
	return blocked, warnings, check
}

// repairHeadroomNeed — SAF: bu kipte hedefte gerçekten gereken boş alan.
//
// v0.10.829 incelemesi (3): seed kiplerinde KOPYALANAN bayt YOKTUR.
// `ALTER TABLE <t>_fix ATTACH PARTITION … FROM <t>` aynı veritabanında,
// aynı diskte SABİT BAĞ kurar (parça dosyaları çoğaltılmaz) ve çekilecek eş
// de yoktur (peerBytes zaten 0). Yerel tablonun %110'unu istemek büyük
// tabloları bedava engelliyordu. Seed'de yalnız küçük bir sabit pay aranır
// (yeni tablonun meta/log dosyaları, birleşme başlangıcı).
//
// 820 kipleri değişmez: eş verisi + yerel veri + %10 — orada parçalar
// interserver üzerinden GERÇEKTEN kopyalanır.
func repairHeadroomNeed(mode string, peerBytes, localBytes uint64) uint64 {
	if isSeedMode(mode) {
		return replicaRepairSeedHeadroom
	}
	need := peerBytes + localBytes
	return need + need/10
}

// diskHeadroomOK — SAF: hedefin boş alanı gereken kadar mı?
// free=0 (okunamadı) → karar verilmez (true, çağıran uyarır).
func diskHeadroomOK(free, need uint64) bool {
	if free == 0 {
		return true
	}
	return free >= need
}

// columnsDiff — SAF: eş ile hedefin kolon listesi farkı, İKİ KOVAYA ayrılmış:
//
//	blocking — ATTACH PARTITION FROM'u gerçekten düşüren farklar: eksik/fazla
//	           kolon ve tip uyuşmazlığı.
//	notes    — engel OLMAYAN farklar; şimdilik yalnız kolon SIRASI.
//
// v0.10.828 (operatör bildirimi): sıra farkı ENGEL DEĞİL. Sabitlenen sunucu
// sürümünün kaynağı (v26.2.4.23, src/Storages/MergeTree/MergeTreeData.cpp,
// MergeTreeData::checkStructureAndGetMergeTreeData) yapıyı TEK satırda
// karşılaştırır: getColumns().getAllPhysical().sizeOfDifference(...).
// src/Core/NamesAndTypes.cpp'deki NamesAndTypesList::sizeOfDifference iki
// listeyi tek vektörde birleştirip SIRALAR (::sort) ve tekil sayısından
// simetrik fark büyüklüğünü çıkarır → karşılaştırma (ad, tip) KÜMESİ
// üzerinedir, sıra hiç görülmez. Parça dosyaları kolon ADIYLA anahtarlı ve
// her parça kendi columns.txt'siyle klonlanır; hedefin kendi kolon sırası
// (ki `_fix` eşin DDL'iyle kurulduğu için zaten eşin sırasıdır) ATTACH'ı
// etkilemez. ColumnsDescription::getAllPhysical ALIAS/EPHEMERAL kolonları
// dışarıda bırakır — kanonik şemada öyle kolon yok, readColumns hepsini
// okur, yani bu kapı CH'den yalnız o yönde katı kalabilir.
func columnsDiff(peer, target []chColumn) (blocking, notes []string) {
	pm := map[string]string{}
	for _, c := range peer {
		pm[c.Name] = c.Type
	}
	tm := map[string]string{}
	for _, c := range target {
		tm[c.Name] = c.Type
	}
	for _, c := range peer {
		if t, ok := tm[c.Name]; !ok {
			blocking = append(blocking, "hedefte yok: "+c.Name)
		} else if t != c.Type {
			blocking = append(blocking, fmt.Sprintf("tip farklı: %s (%s ≠ %s)", c.Name, c.Type, t))
		}
	}
	for _, c := range target {
		if _, ok := pm[c.Name]; !ok {
			blocking = append(blocking, "eşte yok: "+c.Name)
		}
	}
	// Küme aynıysa sırayı da söyle — engel değil ama ilk farkı adlandır.
	if len(blocking) == 0 && len(peer) == len(target) {
		for i := range peer {
			if peer[i].Name != target[i].Name {
				notes = append(notes, fmt.Sprintf("kolon sırası farklı (ilk fark #%d: eşte %s, hedefte %s)", i+1, peer[i].Name, target[i].Name))
				break
			}
		}
	}
	return blocking, notes
}

// columnGate — SAF: kolon karşılaştırmasının plan çıktısı (engel · uyarı · ✓).
// v0.10.828: ENGEL yalnız blocking kovasından doğar; sıra farkı UYARIYA gider,
// çünkü ATTACH PARTITION FROM kolon KÜMESİNİ karşılaştırır (gerekçe:
// columnsDiff başlığındaki kaynak alıntısı). ✓ satırı neyin karşılaştırıldığını
// söyler — "kolonlar aynı" demek sırayı da doğruladığımızı ima ederdi.
func columnGate(peer, target []chColumn, fixName string) (blocked, warnings []string, check string) {
	blocking, notes := columnsDiff(peer, target)
	if len(blocking) > 0 {
		blocked = append(blocked, "kolonlar eşle aynı değil (ATTACH PARTITION FROM başarısız olur): "+strings.Join(blocking, "; "))
	}
	for _, n := range notes {
		warnings = append(warnings, n+" (ATTACH için engel değil: ClickHouse kolon KÜMESİNİ karşılaştırır); `"+fixName+"` tablosu eşin kolon sırasını alır")
	}
	if len(blocking) == 0 {
		check = fmt.Sprintf("%d kolon eşle aynı (ad+tip KÜMESİ; ATTACH sırayı karşılaştırmaz)", len(peer))
	}
	return blocked, warnings, check
}

func isCHTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ex *clickhouse.Exception
	if errors.As(err, &ex) && ex.Code == 159 { // TIMEOUT_EXCEEDED
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "TIMEOUT_EXCEEDED")
}

// seedCanonicalArgs — kanonik katalogdan (store.go `tables`) tablonun CREATE
// metnini bulur, adaptDDL'den GEÇİRİR ve motor argümanlarını oradan okur.
//
// NEDEN KATALOG. Seed kipinde kopyalanacak eş YOK, yani ZK yolunu ya
// katalogdan okuruz ya UYDURURUZ. Uydurmak no_replication sınıfını yeniden
// üretir: repo iki ayrı sözleşme taşıyor (birleşik `<prefix>/state/<ad>` +
// `{shard}-{replica}`, migrations/0009 — ve eski `<prefix>/{shard}/<ad>` +
// `{replica}`), hangisinin geçerli olduğuna useUnifiedStatePath kümenin
// GÖZLENEN hâline bakarak karar verir. adaptDDL o kararın tek sahibi; burada
// yeniden yazmak iki gerçek üretirdi.
//
// `<ad>_local` (yüksek hacimli telemetri) kanonik katalogda ÇIPLAK adla
// durur — adaptDDL onu `_local`'e çevirir; o yüzden arama çıplak adla,
// eşleşme ÜRETİLEN adla yapılır. Distributed sarmalayıcı parçası
// replicatedEngineArgs'tan geçmez, yani çıplak yüksek-hacimli ad (küme
// kipinde Distributed olmalı) burada kendiliğinden engellenir.
func (s *Store) seedCanonicalArgs(table string) (seedCanonical, error) {
	base := table
	if b := strings.TrimSuffix(table, "_local"); b != table && highVolumeTables[b] {
		base = b
	}
	// Çıplak yüksek-hacim adı: kanonik şekli Distributed SARMALAYICI, yani
	// bu host tek-düğüm şeklinde kalmış. Tablo düzeyi onarım bunu çözmez;
	// mesaj neyin eksik olduğunu söyler (v0.10.829, operatör).
	if highVolumeTables[table] {
		return seedCanonical{}, fmt.Errorf("`%s` kümede `%s_local` (Replicated) + `%s` (Distributed sarmalayıcı) olarak yaşar; bu host tek-düğüm şeklinde kalmış. Tablo düzeyi onarım yetmez, terfi gerekir — ayrı sürüm", table, table, table)
	}
	ddl := tableDDLByName(s.canonicalTableDDL, base)
	if ddl == "" {
		return seedCanonical{}, fmt.Errorf("%s için kanonik tanım yok — ZK yolu sözleşmesi bilinmiyor; runbook ile elle", table)
	}
	for _, frag := range s.adaptDDL(ddl) {
		n, kind := identifyDDLTarget(frag)
		if kind != "table" || n != table {
			continue
		}
		f, p, r, ok := replicatedEngineArgs(frag)
		if !ok {
			return seedCanonical{}, fmt.Errorf("kanonik tanım %s'i Replicated tablo olarak kurmuyor (küme kipinde Distributed sarmalayıcı ya da argümansız motor) — runbook ile elle", table)
		}
		return seedCanonical{Frag: frag, Family: f, PathArg: p, ReplicaArg: r}, nil
	}
	return seedCanonical{}, fmt.Errorf("kanonik tanım %s adını üretmiyor (küme kipinde başka ada dönüşüyor) — runbook ile elle", table)
}

// seedCanonical — kanonik katalogdan okunan CREATE fragmanı + motor
// argümanları. Frag boş-shard dalında KAYNAK DDL'dir (hedefte tablo yok,
// SHOW CREATE alınamaz); düz-tablo dalında yalnız argümanları kullanılır.
type seedCanonical struct{ Frag, Family, PathArg, ReplicaArg string }

// resolveHostAddrs — hostName() → "host:port". Rapor hostName() ile anahtarlı;
// system.clusters IP ile yazılmışsa host_name/host_address hostName() ile
// UYUŞMAZ (792/818): tek güvenilir yol her satıra bağlanıp hostName() sormak.
// Bağlantılar shardConn'da önbellekli; onarım aynı bağlantıyı kullanır.
func (s *Store) resolveHostAddrs(ctx context.Context) (map[string]string, []string, error) {
	rows, _, err := s.clusterHostRows(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]string{}
	var notes []string
	for _, r := range rows {
		if r.Host == "" || r.Port <= 0 {
			continue
		}
		addr := fmt.Sprintf("%s:%d", r.Host, r.Port)
		c, err := s.shardConn(ctx, addr)
		if err != nil {
			notes = append(notes, addr+": bağlanılamadı ("+err.Error()+")")
			continue
		}
		var hn string
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = c.QueryRow(qctx, "SELECT hostName() SETTINGS max_execution_time = 5").Scan(&hn)
		cancel()
		if err != nil || hn == "" {
			notes = append(notes, addr+": hostName() okunamadı")
			continue
		}
		if _, dup := out[hn]; !dup {
			out[hn] = addr
		}
	}
	return out, notes, nil
}

// PlanReplicaRepair — salt okuma plan (bkz. dosya başlığı).
func (s *Store) PlanReplicaRepair(ctx context.Context, req ReplicaRepairRequest) (*ReplicaRepairPlan, error) {
	req.Table, req.Host = strings.TrimSpace(req.Table), strings.TrimSpace(req.Host)
	if !chObjRe.MatchString(req.Table) {
		return nil, fmt.Errorf("geçersiz tablo adı %q", req.Table)
	}
	if strings.HasPrefix(req.Table, ".inner") || req.Host == "" {
		return nil, errors.New("MV iç tabloları bu sihirbazın kapsamı dışında (MV onarımı kartı) / host zorunlu")
	}
	if strings.HasSuffix(req.Table, replicaRepairFixSuffix) {
		return nil, fmt.Errorf("%s sihirbazın geçici tablosu — onarılmaz; sahibi tablonun satırında Temizle", req.Table)
	}
	if !s.clusterMode() {
		return nil, errors.New("küme kipi değil")
	}
	rep, err := s.ReplicaConsistency(ctx)
	if err != nil {
		return nil, fmt.Errorf("rapor: %w", err)
	}
	plan := &ReplicaRepairPlan{Table: req.Table, Shard: req.Shard, Host: req.Host, Database: rep.Database, Partitions: []ReplicaRepairPartition{}, Steps: []string{}, Checks: []string{}}
	block := func(format string, a ...any) { plan.Blocked = append(plan.Blocked, fmt.Sprintf(format, a...)) }
	check := func(format string, a ...any) { plan.Checks = append(plan.Checks, fmt.Sprintf(format, a...)) }

	var shard *ReplicaShard
	var tbl *ReplicaTable // v0.10.829 — KARDEŞ shard'lar da lazım (seed yol kararı)
	for i := range rep.Tables {
		if rep.Tables[i].Table != req.Table {
			continue
		}
		tbl = &rep.Tables[i]
		for j := range rep.Tables[i].Shards {
			if rep.Tables[i].Shards[j].Shard == req.Shard {
				shard = &rep.Tables[i].Shards[j]
			}
		}
	}
	if shard == nil {
		return nil, fmt.Errorf("%s / shard %d raporda yok — yeniden Ölç", req.Table, req.Shard)
	}
	if shard.Verdict != ReplicaMissing && shard.Verdict != ReplicaNotReplicated {
		return nil, fmt.Errorf("%s / shard %d kararı %q — bu sihirbaz yalnız eksik/replike-olmayan tabloyu onarır; yeniden Ölç", req.Table, req.Shard, shard.Verdict)
	}
	var target *ReplicaMissingHost
	for i := range shard.Missing {
		if shard.Missing[i].Host == req.Host {
			target = &shard.Missing[i]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%s bu shard'da eksik host değil — yeniden Ölç", req.Host)
	}
	if strings.HasPrefix(target.Engine, "Replicated") {
		return nil, fmt.Errorf("%s'ta tablo zaten Replicated (%s) ama kayıtsız — yeniden Ölç, kalıcıysa elle incele", req.Host, target.Engine)
	}
	plan.Mode = replicaRepairModeMissing
	if target.Engine != "" {
		plan.Mode = replicaRepairModePlain
	}
	check("karar %s · hedef %s (%s)", shard.Verdict, req.Host, map[bool]string{true: "tablo yok", false: "düz " + target.Engine}[target.Engine == ""])

	// v0.10.829 — seed uygunluğu TAZE rapordan doğrulanır, istemcinin
	// dediğinden değil. Ret HATA'dır (engel değil): kip yanlış seçilmişse
	// plan penceresi açmanın anlamı yok, operatör öteki düğmeyi kullanır.
	seed := strings.TrimSpace(req.Mode) == replicaRepairModeSeed
	// seedEmpty — boş-shard dalı: hedefte tablo YOK (uygunluk kapısı shard'ın
	// hiçbir host'unda düz tablo olmadığını da doğruladı). Taşınacak veri yok.
	seedEmpty := false
	if seed {
		if reason := seedEligibility(req.Table, shard, req.Host); reason != "" {
			return nil, errors.New(reason)
		}
		plan.Mode = replicaRepairModeSeed
		if target.Engine == "" {
			plan.Mode, seedEmpty = replicaRepairModeSeedEmpty, true
		}
	}

	// Eş: aynı shard'da sağlam Replicated replika. Seed kipinde eş YOKTUR
	// (uygunluk kapısı bunu şart koşar): kaynak DDL hedefin kendisidir.
	var peer *ReplicaState
	if !seed {
		for i := range shard.Replicas {
			r := &shard.Replicas[i]
			if strings.HasPrefix(r.Engine, "Replicated") && !r.ReadOnly && !r.SessionExpired && r.ZKPath != "" {
				peer = r
				break
			}
		}
		if peer == nil {
			block("shard'da sağlam (Replicated, readonly olmayan) eş replika yok — kaynak DDL alınamaz. Shard'da HİÇ Replicated replika yoksa, düz tablosu olan host'un satırındaki \"İlk replikayı kur\" o host'u shard'ın ilk replikası yapar")
			return plan, nil
		}
		plan.Peer, plan.ZKPath, plan.PeerReplica, plan.Engine = peer.Host, peer.ZKPath, peer.ReplicaName, peer.Engine
		if strings.Contains(peer.ZKPath, "{") {
			block("eşin ZK yolu makro taşıyor (%s) — beklenmedik; elle incele", peer.ZKPath)
		}
	}

	// Adresler: hostName() → host:port (yoklama).
	addrs, notes, err := s.resolveHostAddrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("system.clusters: %w", err)
	}
	plan.Warnings = append(plan.Warnings, notes...)
	plan.targetAddr = addrs[req.Host]
	if plan.targetAddr == "" {
		block("%s adresi system.clusters'tan çözülemedi (hostName() hiçbir satıra bağlanınca çıkmadı)", req.Host)
	}
	if seed {
		// Kaynak = hedefin kendisi; ayrı bir eş adresi yok.
		plan.peerAddr = plan.targetAddr
	} else {
		plan.peerAddr = addrs[peer.Host]
		if plan.peerAddr == "" {
			block("eş %s adresi çözülemedi", peer.Host)
		}
	}
	if len(plan.Blocked) > 0 {
		return plan, nil
	}
	if seed {
		check("adres: hedef %s (kaynak DDL aynı host'tan — eş yok)", plan.targetAddr)
	} else {
		check("adresler: hedef %s, eş %s", plan.targetAddr, plan.peerAddr)
	}

	// Hedef makroları.
	var macros map[string]string
	for _, h := range rep.Hosts {
		if h.Host == req.Host {
			macros = h.Macros
		}
	}
	if strings.TrimSpace(macros["replica"]) == "" {
		block("%s'ta {replica} makrosu yok — Replicated tablo kurulamaz (system.macros)", req.Host)
		return plan, nil
	}

	peerSrc := req.Host
	if !seed {
		peerSrc = peer.Host
	}
	peerConn, err := s.shardConn(ctx, plan.peerAddr)
	if err != nil {
		return nil, fmt.Errorf("kaynak bağlantısı (%s): %w", peerSrc, err)
	}
	targetConn, err := s.shardConn(ctx, plan.targetAddr)
	if err != nil {
		return nil, fmt.Errorf("hedef bağlantısı (%s): %w", req.Host, err)
	}
	// Kaynak DDL: eşin (normal) ya da HEDEFİN KENDİ (seed) SHOW CREATE'i.
	// Boş-shard dalında hedefte tablo YOK: SHOW CREATE alınamaz, kaynak
	// kanonik katalogdur.
	var ddl string
	if !seedEmpty {
		if err := peerConn.QueryRow(ctx, "SHOW CREATE TABLE "+qualifyCH(rep.Database, req.Table)).Scan(&ddl); err != nil {
			return nil, fmt.Errorf("kaynak DDL (%s): %w", peerSrc, err)
		}
	}
	var family, pathArg, replicaArg, canonicalFrag string
	if seed {
		// ZK yolu sözleşmesi KANONİK KATALOGDAN (adaptDDL) — uydurulmaz.
		can, cerr := s.seedCanonicalArgs(req.Table)
		if cerr != nil {
			block("%v", cerr)
			return plan, nil
		}
		if !seedEmpty {
			// Çapraz kontrol: düz motorun Replicated karşılığı kanonik ailenin
			// KENDİSİ olmalı. Değilse tablo kanonik tanımdan sapmış (ya da
			// başka bir tablonun yolu okunuyor) — eş yola yanlış aileyle
			// katılmak mümkün değildir, plan burada durur. Boş-shard dalında
			// karşılaştırılacak yerel motor YOK: tablo kanonik DDL'den doğar.
			plainFam := plainEngineFamily(ddl)
			if plainFam == "" {
				block("hedefin motoru tanınan düz MergeTree ailesinden değil (SHOW CREATE) — runbook ile elle")
				return plan, nil
			}
			if "Replicated"+plainFam != can.Family {
				block("motor ailesi kanonik tanımla uyuşmuyor: hedefte düz %s (→ Replicated%s), kanonik %s — ZK yoluna yanlış aile katılamaz; runbook ile elle", plainFam, plainFam, can.Family)
				return plan, nil
			}
		}
		family, pathArg, replicaArg, canonicalFrag = can.Family, can.PathArg, can.ReplicaArg, can.Frag
	} else {
		var ok bool
		family, pathArg, replicaArg, ok = replicatedEngineArgs(ddl)
		if !ok {
			block("eşin DDL'inde Replicated motor argümanı yok (default_replica_path) — sihirbaz yalnız açık argümanlı motoru kopyalar")
			return plan, nil
		}
	}
	if strings.Contains(pathArg, "{uuid}") {
		block("yol {uuid} taşıyor — uuid'li yol bu sihirbazın kapsamı dışında (elle: UUID '<kaynağın uuid'si>' ile CREATE)")
		return plan, nil
	}
	if !seed && !strings.HasPrefix(peer.Engine, family) && !strings.HasPrefix(family, peer.Engine) {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("system.tables motoru (%s) ile DDL motoru (%s) farklı görünüyor", peer.Engine, family))
	}
	plan.Engine = family
	targetReplica, err := expandMacros(replicaArg, macros)
	if err != nil {
		block("hedefte {replica} argümanı genişletilemedi: %v", err)
		return plan, nil
	}
	plan.TargetReplica = targetReplica
	for _, r := range shard.Replicas {
		if r.ReplicaName == targetReplica {
			block("makro çakışması: hedefin {replica} değeri (%s) eş %s ile aynı — CREATE 'Replica already exists' der; önce hedefin system.macros {replica} değerini düzelt", targetReplica, r.Host)
		}
	}
	if seed {
		// Seed: yol hedefin makrolarıyla GENİŞLETİLİR (eşin literal yolu yok).
		expanded, perr := expandMacros(pathArg, macros)
		if perr != nil {
			block("kanonik ZK yolu hedefin makrolarıyla genişletilemedi: %v", perr)
			return plan, nil
		}
		// Kuşak KANITI plana yazılır: operatör hangi sözleşmenin hangi
		// gerekçeyle seçildiğini Uygula'dan ÖNCE görsün (inceleme 2026-09-20).
		seedBase := strings.TrimSuffix(req.Table, "_local")
		if !highVolumeTables[seedBase] {
			seedBase = req.Table
		}
		isState := stateTableDDL(seedBase, "table")
		unified, genReason := false, "shard kaydı"
		if isState {
			unified, genReason = useUnifiedStatePath(s.stateObs, s.zkPrefix(), seedBase)
		}
		check("%s", seedGenerationEvidence(req.Table, isState, unified, genReason))
		// 1 NUMARALI KAPI: kardeş shard'lardaki kayıtlar hesaba KARŞI
		// doğrulanır; uydurulmuş yola ikinci grup açılamaz.
		macrosOf := map[string]map[string]string{}
		for _, h := range rep.Hosts {
			if len(h.Macros) > 0 {
				macrosOf[h.Host] = h.Macros
			}
		}
		chosen, evidence, pathBlocked := seedPathDecision(req.Table, pathArg, expanded, tableObservedPaths(tbl), macrosOf)
		if pathBlocked != "" {
			block("%s", pathBlocked)
			return plan, nil
		}
		check("%s", evidence)
		plan.ZKPath = chosen
		// {replica} çakışması: shard'ın ÖTEKİ host'ları aynı şablondan aynı
		// adı üretiyorsa, bu onarım geçer ama ÖTEKİ host'un Onar'ı düşer.
		others := map[string]map[string]string{}
		for _, m := range shard.Missing {
			if m.Host == req.Host {
				continue
			}
			for _, h := range rep.Hosts {
				if h.Host == m.Host && len(h.Macros) > 0 {
					others[m.Host] = h.Macros
				}
			}
		}
		for _, h := range seedReplicaCollisions(replicaArg, targetReplica, others) {
			block("makro çakışması: %s da aynı replika adını (%s) üretiyor — bu host ilk replika olsa bile %s'ın \"Onar\"ı 'Replica already exists' ile düşer; önce system.macros {replica} değerlerini ayrıştır", h, targetReplica, h)
		}
		check("motor %s · yol %s (kanonik katalog) · replika adı %s", family, plan.ZKPath, targetReplica)
	} else {
		if predicted, perr := expandMacros(pathArg, macros); perr != nil {
			plan.Warnings = append(plan.Warnings, "hedef makroları eşin yol argümanını çözemiyor ("+perr.Error()+"); yol literal yazıldı")
		} else if predicted != peer.ZKPath {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("hedefin {shard} makrosu eşin yolunu üretmiyor (%s ≠ %s); yol LİTERAL yazıldı — küme makroları düzeltilmeden sonraki küme-geneli DDL'ler yine ayrışır", predicted, peer.ZKPath))
		}
		check("motor %s · yol %s · hedef replika adı %s (eş: %s)", family, peer.ZKPath, targetReplica, peer.ReplicaName)
	}

	// ZK: replicas altındaki znode'lar (bayat kayıt / çakışma). Seed kipinde
	// yol HENÜZ OLMAMALI: varsa ve altında replika varsa sahibi başkasıdır.
	names, zerr := zkChildren(ctx, peerConn, plan.ZKPath+"/replicas")
	if seed {
		// Seed: sahiplik kapısı SAF gövdede ve FAIL-CLOSED (inceleme 2).
		evidence, zkBlocked := seedZKOwnershipGate(plan.ZKPath, names, zerr)
		if zkBlocked != "" {
			block("%s", zkBlocked)
		} else {
			check("%s", evidence)
		}
	} else if zerr != nil {
		plan.Warnings = append(plan.Warnings, "system.zookeeper okunamadı: "+zerr.Error())
	}
	if zerr == nil {
		for _, n := range names {
			if n == targetReplica {
				block("ZK'da %s/replicas/%s znode'u zaten var (bayat kayıt ya da çakışma) — eşte `%s` koşup (yalnız hedef adı için!) yeniden planla", plan.ZKPath, targetReplica, dropReplicaZkStmt(targetReplica, plan.ZKPath))
			}
		}
		if !seed {
			check("ZK replicas: %s", strings.Join(names, ", "))
		}
	}

	// Hedef DB motoru (EXCHANGE için Atomic şart).
	var dbEngine string
	if err := targetConn.QueryRow(ctx, "SELECT engine FROM system.databases WHERE name = ? SETTINGS max_execution_time = 5", rep.Database).Scan(&dbEngine); err != nil {
		return nil, fmt.Errorf("hedef system.databases: %w", err)
	}
	check("hedef DB motoru %s", dbEngine)
	if repairMovesLocalData(plan.Mode) && dbEngine != "Atomic" && dbEngine != "Replicated" {
		block("hedef DB motoru %s — EXCHANGE TABLES Atomic ister", dbEngine)
	}

	fixName := req.Table + replicaRepairFixSuffix
	// Eşin tablo boyutu: yeni replika (her iki kipte) eşin TÜM parçalarını
	// interserver'dan klonlar — hedefin boş alanı bunu karşılamalı.
	var peerBytes uint64
	if !seed {
		if err := peerConn.QueryRow(ctx, "SELECT toUInt64(sum(bytes_on_disk)) FROM system.parts WHERE database = ? AND table = ? AND active SETTINGS max_execution_time = 10", rep.Database, req.Table).Scan(&peerBytes); err != nil {
			plan.Warnings = append(plan.Warnings, "eşin tablo boyutu okunamadı: "+err.Error())
		}
	}
	plan.PeerBytes = peerBytes
	if repairMovesLocalData(plan.Mode) {
		var n uint64
		if err := targetConn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = ? AND name = ? SETTINGS max_execution_time = 5", rep.Database, fixName).Scan(&n); err != nil {
			return nil, fmt.Errorf("hedef system.tables: %w", err)
		}
		// Temizlik ifadesi ve `_fix` durumu HER durumda plana yazılır (engelli
		// planda da): FE Temizle'yi buradan türetir.
		plan.Cleanup = []string{"DROP TABLE IF EXISTS " + qualifyCH(rep.Database, fixName) + " SYNC" + purgeGuard}
		if n > 0 {
			plan.FixExists = true
			block("hedefte `%s` zaten var (yarım kalmış onarım ya da EXCHANGE sonrası eski düz tablo) — önce Temizle", fixName)
		}
		// v0.10.829 incelemesi (4): seed dalında `_fix` HEDEFİN KENDİ SHOW
		// CREATE'inden doğar, yani ATTACH'ın iki tarafı aynı metindir.
		// 828'in üç kapısını koşmak hedefi KENDİSİYLE karşılaştırmak olurdu:
		// altı gereksiz gidiş-dönüş ve "eşle aynı" diyen yanlış ✓ satırları.
		// Atlanır; yerine tek dürüst satır yazılır. Depolama politikası yine
		// okunur (disk payı hesabı ona bakar).
		if isSeedMode(plan.Mode) {
			// v0.10.829 incelemesi (4): seed dalında `_fix` HEDEFİN KENDİ SHOW
			// CREATE'inden doğar, yani ATTACH'ın iki tarafı AYNI metindir.
			// 828'in üç kapısını koşmak hedefi KENDİSİYLE karşılaştırmak
			// olurdu: altı gereksiz gidiş-dönüş ve "eşle aynı" diyen YANLIŞ ✓
			// satırları. Atlanır, yerine tek dürüst satır yazılır. Depolama
			// politikası yine okunur (disk payı hesabı ona bakar).
			check("`%s` hedefin KENDİ SHOW CREATE'inden doğar; kolon/anahtar/indeks karşılaştırması gereksiz (ATTACH'ın iki tarafı aynı tanım)", fixName)
			var policy string
			_ = targetConn.QueryRow(ctx, "SELECT storage_policy FROM system.tables WHERE database = ? AND name = ? SETTINGS max_execution_time = 5", rep.Database, req.Table).Scan(&policy)
			plan.TargetFreeBytes = readFreeBytes(ctx, targetConn, policy)
		} else {
			// Kolonlar + anahtarlar: ATTACH PARTITION FROM aynı kolon KÜMESİNİ
			// (ad+tip; SIRA değil — v0.10.828), aynı partition/ORDER BY/PRIMARY
			// KEY'i ve aynı depolama politikasını ister (`_fix` eşin DDL'iyle
			// kurulur → hedefin düz tablosu eşle karşılaştırılır).
			pc, err := readColumns(ctx, peerConn, rep.Database, req.Table)
			if err != nil {
				return nil, fmt.Errorf("eş system.columns: %w", err)
			}
			tc, err := readColumns(ctx, targetConn, rep.Database, req.Table)
			if err != nil {
				return nil, fmt.Errorf("hedef system.columns: %w", err)
			}
			colBlocked, colWarnings, colCheck := columnGate(pc, tc, fixName)
			for _, m := range colBlocked {
				block("%s", m)
			}
			plan.Warnings = append(plan.Warnings, colWarnings...)
			if colCheck != "" {
				check("%s", colCheck)
			}
			pk, err := readTableKeys(ctx, peerConn, rep.Database, req.Table)
			if err != nil {
				return nil, fmt.Errorf("eş system.tables anahtarları: %w", err)
			}
			tk, err := readTableKeys(ctx, targetConn, rep.Database, req.Table)
			if err != nil {
				return nil, fmt.Errorf("hedef system.tables anahtarları: %w", err)
			}
			keyBlocked, keyWarnings, keyCheck := tableKeysGate(pk, tk)
			for _, m := range keyBlocked {
				block("%s", m)
			}
			plan.Warnings = append(plan.Warnings, keyWarnings...)
			if keyCheck != "" {
				check("%s", keyCheck)
			}
			pi, perr := readSkipIndices(ctx, peerConn, rep.Database, req.Table)
			ti, terr := readSkipIndices(ctx, targetConn, rep.Database, req.Table)
			switch {
			case perr != nil || terr != nil:
				plan.Warnings = append(plan.Warnings, "atlama indeksleri karşılaştırılamadı (system.data_skipping_indices okunamadı)")
			default:
				// Ayarın ETKİN değeri ATTACH HEDEFİNDE (`_fix`, hedef host)
				// geçerli olandır — o yüzden targetConn. Sunucu varsayılanı
				// okunur; eşin DDL'i SETTINGS ile ezerse plan iyimser kalır.
				var strictVal string
				strictKnown := targetConn.QueryRow(ctx, "SELECT value FROM system.merge_tree_settings WHERE name = 'enforce_index_structure_match_on_partition_manipulation' SETTINGS max_execution_time = 5").Scan(&strictVal) == nil
				idxBlocked, idxWarnings, idxCheck := skipIndexGate(pi, ti, strictIndexMatch(strictVal), strictKnown)
				for _, m := range idxBlocked {
					block("%s", m)
				}
				plan.Warnings = append(plan.Warnings, idxWarnings...)
				if idxCheck != "" {
					check("%s", idxCheck)
				}
			}
			// Hedefin boş alanı: tablonun depolama politikasındaki diskler.
			plan.TargetFreeBytes = readFreeBytes(ctx, targetConn, tk.StoragePolicy)
		}
		// Partition'lar.
		prow, err := targetConn.Query(ctx, `
			SELECT partition_id, count(), sum(rows), sum(bytes_on_disk)
			FROM system.parts WHERE database = ? AND table = ? AND active
			GROUP BY partition_id ORDER BY partition_id
			SETTINGS max_execution_time = 10`, rep.Database, req.Table)
		if err != nil {
			return nil, fmt.Errorf("hedef system.parts: %w", err)
		}
		for prow.Next() {
			var p ReplicaRepairPartition
			var cnt, rows, bytes uint64
			if err := prow.Scan(&p.ID, &cnt, &rows, &bytes); err != nil {
				prow.Close()
				return nil, err
			}
			p.Parts, p.Rows, p.Bytes = int(cnt), rows, bytes
			plan.Partitions = append(plan.Partitions, p)
			plan.TotalRows += rows
			plan.TotalBytes += bytes
		}
		prow.Close()
		if err := prow.Err(); err != nil {
			return nil, fmt.Errorf("hedef system.parts: %w", err)
		}
		check("%d partition · %d satır · %d bayt yerel veri ATTACH ile taşınacak (sabit bağ, kopya yok)", len(plan.Partitions), plan.TotalRows, plan.TotalBytes)
		plan.Warnings = append(plan.Warnings, "ATTACH ile EXCHANGE arasında düz tabloya yazılan satırlar `"+fixName+"`'te kalır (Temizle'den önce sayımları karşılaştır)")
	} else {
		var policy string
		_ = targetConn.QueryRow(ctx, "SELECT storage_policy FROM system.tables WHERE database = ? AND name = ? SETTINGS max_execution_time = 5", rep.Database, req.Table).Scan(&policy)
		plan.TargetFreeBytes = readFreeBytes(ctx, targetConn, policy)
	}
	if seed {
		// Klonlanacak eş YOK: bu host shard'ın İLK replikası olur ve yalnız
		// KENDİ satırlarını taşır. Plan bunu açıkça söyler — "onardım" sanıp
		// öteki host'u bırakmak ıraksamayı kalıcı hâle getirir.
		others := make([]string, 0, len(shard.Missing))
		for _, m := range shard.Missing {
			if m.Host != req.Host {
				others = append(others, m.Host+map[bool]string{true: " (tablo yok)", false: " (düz " + m.Engine + ")"}[m.Engine == ""])
			}
		}
		if seedEmpty {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("bu host'ta tablo YOK: boş Replicated tablo kurulur, VERİ TAŞINMAZ (`_fix` yok, ATTACH yok, EXCHANGE yok, Temizle yok). Shard'ın hiçbir host'unda tablo olmadığı için kaybolan satır da yok; %s shard'ın İLK replikası olur (1/1, yedeklilik yok)", req.Host))
			plan.Warnings = append(plan.Warnings, "karşılaştırılacak yerel tablo yok: kolon/anahtar/atlama-indeksi kapıları KOŞMADI — tablo kanonik DDL'den doğar, ATTACH edilecek parça yok")
			if len(others) > 0 {
				plan.Warnings = append(plan.Warnings, "shard'ın öteki host'u ("+strings.Join(others, ", ")+") bu onarımdan SONRA satırındaki \"Onar\" ile aynı yola katılmalı")
			}
		} else {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("bu host shard'ın İLK replikası olur (kayıtlı replika 1/1): YEDEKLİLİK YOK, eşten çekilecek parça yok — yalnız %s'ın kendi %d baytı Replicated tabloya geçer", req.Host, plan.TotalBytes))
			if len(others) > 0 {
				plan.Warnings = append(plan.Warnings, "shard'ın öteki host'ları KENDİ satırlarını tutmaya devam eder ("+strings.Join(others, ", ")+"): bu onarımdan SONRA her birinin satırındaki \"Onar\" ile aynı yola katılmalılar — o ana dek okumalar ayrışmaya devam eder")
			}
		}
	} else {
		// Yeni replika eşin TÜM parçalarını klonlar (CREATE hemen döner,
		// çekme arka planda; interserver 9009 erişimi + disk yeri şart).
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("tablo eşten klonlanır: eşte %d bayt, hedefte boş %d bayt (CREATE hemen döner, parçalar arka planda interserver'dan çekilir)", peerBytes, plan.TargetFreeBytes))
	}
	need := repairHeadroomNeed(plan.Mode, peerBytes, plan.TotalBytes)
	switch {
	case plan.TargetFreeBytes == 0:
		plan.Warnings = append(plan.Warnings, "hedefin boş alanı okunamadı (system.disks) — disk yerini elle doğrula")
	case !diskHeadroomOK(plan.TargetFreeBytes, need):
		if isSeedMode(plan.Mode) {
			block("hedefte boş alan yetersiz: %d bayt boş, en az %d bayt gerekir (ATTACH sabit bağ kurar, kopyalanan veri yok — bu yalnız yeni tablonun payı)", plan.TargetFreeBytes, need)
		} else {
			block("hedefte boş alan yetersiz: %d bayt boş, en az %d bayt gerekir (eş verisi + yerel veri + %%10)", plan.TargetFreeBytes, need)
		}
	}

	// Adımlar.
	createName := req.Table
	if repairMovesLocalData(plan.Mode) {
		createName = fixName
	}
	var createDDL string
	switch {
	case seedEmpty:
		// Hedefte tablo yok: kaynak KANONİK fragman, hedef adı `<t>`.
		createDDL, err = rewriteCanonicalSeedDDL(canonicalFrag, rep.Database, req.Table, plan.ZKPath)
	case seed:
		// Kaynak HEDEFİN KENDİ DDL'i: kolon bloğu bayt bayt korunur, yalnız
		// ad ve motor değişir (ATTACH kaynağıyla ikiz `_fix`).
		createDDL, err = rewriteSeedDDL(ddl, rep.Database, req.Table, createName, plan.ZKPath, replicaArg)
	default:
		createDDL, err = rewriteReplicaDDL(ddl, rep.Database, req.Table, createName, peer.ZKPath)
	}
	if err != nil {
		block("DDL yeniden yazılamadı: %v", err)
		return plan, nil
	}
	plan.Steps = append(plan.Steps, createDDL)
	if repairMovesLocalData(plan.Mode) {
		att, err := attachStatements(rep.Database, req.Table, plan.Partitions)
		if err != nil {
			block("%v", err)
			return plan, nil
		}
		plan.Steps = append(plan.Steps, att...)
		plan.Steps = append(plan.Steps, "EXCHANGE TABLES "+qualifyCH(rep.Database, req.Table)+" AND "+qualifyCH(rep.Database, fixName))
	}
	plan.Steps = append(plan.Steps, syncReplicaStmt(rep.Database, req.Table))
	return plan, nil
}

func syncReplicaStmt(db, table string) string {
	return "SYSTEM SYNC REPLICA " + qualifyCH(db, table) + " LIGHTWEIGHT"
}

func readTableKeys(ctx context.Context, conn clickhouse.Conn, db, table string) (chTableKeys, error) {
	var k chTableKeys
	err := conn.QueryRow(ctx, "SELECT partition_key, sorting_key, primary_key, storage_policy FROM system.tables WHERE database = ? AND name = ? SETTINGS max_execution_time = 5", db, table).Scan(&k.PartitionKey, &k.SortingKey, &k.PrimaryKey, &k.StoragePolicy)
	return k, err
}

func readSkipIndices(ctx context.Context, conn clickhouse.Conn, db, table string) ([]string, error) {
	rows, err := conn.Query(ctx, "SELECT name, type, expr FROM system.data_skipping_indices WHERE database = ? AND table = ? ORDER BY name SETTINGS max_execution_time = 5", db, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n, t, e string
		if err := rows.Scan(&n, &t, &e); err != nil {
			return nil, err
		}
		out = append(out, n+"|"+t+"|"+e)
	}
	return out, rows.Err()
}

// readFreeBytes — tablonun depolama politikasındaki disklerin boş alanı;
// politika boşsa 'default'; okunamazsa 0 (çağıran uyarır, engellemez).
func readFreeBytes(ctx context.Context, conn clickhouse.Conn, policy string) uint64 {
	if strings.TrimSpace(policy) == "" {
		policy = "default"
	}
	var free uint64
	// Skaler alt sorgu, has() ile: node-yerel system tabloları, GLOBAL gerekmez.
	err := conn.QueryRow(ctx, `SELECT toUInt64(sum(free_space)) FROM system.disks
		WHERE has((SELECT disks FROM system.storage_policies WHERE policy_name = ? LIMIT 1), name)
		SETTINGS max_execution_time = 5`, policy).Scan(&free)
	if err != nil || free == 0 {
		_ = conn.QueryRow(ctx, "SELECT toUInt64(sum(free_space)) FROM system.disks WHERE name = 'default' SETTINGS max_execution_time = 5").Scan(&free)
	}
	return free
}

func readColumns(ctx context.Context, conn clickhouse.Conn, db, table string) ([]chColumn, error) {
	rows, err := conn.Query(ctx, "SELECT name, type FROM system.columns WHERE database = ? AND table = ? ORDER BY position SETTINGS max_execution_time = 5", db, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chColumn
	for rows.Next() {
		var c chColumn
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ApplyReplicaRepair — planı taze kurar ve hedefte koşar; koşulan adımlar hata
// yolunda da döner (audit + ekran).
func (s *Store) ApplyReplicaRepair(ctx context.Context, req ReplicaRepairRequest) (*ReplicaRepairResult, error) {
	plan, err := s.PlanReplicaRepair(ctx, req)
	if err != nil {
		return nil, err
	}
	res := &ReplicaRepairResult{Table: plan.Table, Shard: plan.Shard, Host: plan.Host, Mode: plan.Mode, Steps: []string{}}
	if len(plan.Blocked) > 0 {
		return res, fmt.Errorf("plan engelli: %s", strings.Join(plan.Blocked, " · "))
	}
	conn, err := s.shardConn(ctx, plan.targetAddr)
	if err != nil {
		return res, fmt.Errorf("hedef bağlantısı: %w", err)
	}
	for _, st := range plan.Steps {
		res.Steps = append(res.Steps, st)
		if strings.HasPrefix(st, "SYSTEM SYNC REPLICA") {
			sctx := WithQuerySettings(ctx, clickhouse.Settings{"receive_timeout": replicaRepairSyncReceiveS})
			ectx, cancel := context.WithTimeout(sctx, replicaRepairSyncTimeout)
			err := conn.Exec(ectx, st)
			cancel()
			if err != nil {
				if isCHTimeout(err) {
					res.SyncPending = true
					continue
				}
				return res, fmt.Errorf("%s: %w", firstWords(st, 4), err)
			}
			continue
		}
		ectx, cancel := context.WithTimeout(ctx, replicaRepairStepTimeout)
		err := conn.Exec(ectx, st)
		cancel()
		if err != nil {
			return res, fmt.Errorf("%s: %w", firstWords(st, 4), err)
		}
	}
	v, verr := s.verifyReplicaRepair(ctx, conn, plan.Database, plan.Table)
	if verr != nil {
		// DDL koştu; doğrulama OKUNAMADI → başarı + verifyError (kartı yeniden ölç).
		res.VerifyError = verr.Error()
		return res, nil
	}
	res.Verify = v
	if !v.Registered {
		return res, errors.New("doğrulama: tablo hedefte Replicated olarak kayıtlı değil")
	}
	if v.ZKPath != plan.ZKPath {
		return res, fmt.Errorf("doğrulama: ZK yolu %s, beklenen %s", v.ZKPath, plan.ZKPath)
	}
	// v0.10.829 — seed: yolda TEK kayıtlı replika beklenir. 0 hata (tablo
	// Replicated ama Keeper kaydı yok), >1 yalnız not (DDL koştu, tablo
	// çalışıyor; bu arada bir eş katılmış olabilir) — başarılı onarımı okuma
	// yorumuyla "başarısız" göstermek 820'nin düzelttiği sınıf.
	if isSeedMode(plan.Mode) {
		note, nerr := seedReplicaCountNote(v.TotalReplicas)
		if nerr != nil {
			return res, nerr
		}
		if note != "" {
			res.Steps = append(res.Steps, "-- "+note)
		}
	}
	return res, nil
}

func (s *Store) verifyReplicaRepair(ctx context.Context, conn clickhouse.Conn, db, table string) (ReplicaRepairVerify, error) {
	var v ReplicaRepairVerify
	_ = conn.QueryRow(ctx, "SELECT engine FROM system.tables WHERE database = ? AND name = ? SETTINGS max_execution_time = 5", db, table).Scan(&v.Engine)
	// Tipler SQL'de sabitlenir (replica_consistency.go ile aynı disiplin):
	// total/active_replicas 24.3'ten sonra UInt32 — çıplak uint8 taraması
	// başarılı onarımı "başarısız" gösterirdi (inceleme 2026-09-19).
	rows, err := conn.Query(ctx, "SELECT zookeeper_path, replica_name, toUInt32(total_replicas), toUInt32(active_replicas), toUInt8(is_readonly) FROM system.replicas WHERE database = ? AND table = ? SETTINGS max_execution_time = 5", db, table)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var total, active uint32
		var ro uint8
		if err := rows.Scan(&v.ZKPath, &v.ReplicaName, &total, &active, &ro); err != nil {
			return v, err
		}
		v.Registered = true
		v.TotalReplicas, v.ActiveReplicas, v.ReadOnly = int(total), int(active), ro == 1
	}
	return v, rows.Err()
}

// CleanupReplicaRepair — `_fix` düşürme; motorlar DROP'tan hemen önce okunur.
func (s *Store) CleanupReplicaRepair(ctx context.Context, req ReplicaRepairRequest) (*ReplicaRepairResult, error) {
	req.Table, req.Host = strings.TrimSpace(req.Table), strings.TrimSpace(req.Host)
	if !chObjRe.MatchString(req.Table) || req.Host == "" {
		return nil, fmt.Errorf("geçersiz tablo adı %q / host", req.Table)
	}
	if !s.clusterMode() {
		return nil, errors.New("küme kipi değil")
	}
	var db string
	if err := s.conn.QueryRow(ctx, "SELECT currentDatabase()").Scan(&db); err != nil {
		return nil, err
	}
	addrs, _, err := s.resolveHostAddrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("system.clusters: %w", err)
	}
	addr := addrs[req.Host]
	if addr == "" {
		return nil, fmt.Errorf("%s adresi system.clusters'tan çözülemedi", req.Host)
	}
	conn, err := s.shardConn(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("hedef bağlantısı: %w", err)
	}
	res := &ReplicaRepairResult{Table: req.Table, Shard: req.Shard, Host: req.Host, Mode: replicaRepairModePlain, Steps: []string{}}
	fixName := req.Table + replicaRepairFixSuffix
	engineOf := func(name string) (string, error) {
		var e string
		err := conn.QueryRow(ctx, "SELECT engine FROM system.tables WHERE database = ? AND name = ? SETTINGS max_execution_time = 5", db, name).Scan(&e)
		if err != nil && strings.Contains(err.Error(), "no rows") {
			return "", nil
		}
		return e, err
	}
	engT, err := engineOf(req.Table)
	if err != nil {
		return res, fmt.Errorf("hedef system.tables: %w", err)
	}
	engF, err := engineOf(fixName)
	if err != nil {
		return res, fmt.Errorf("hedef system.tables: %w", err)
	}
	note, err := cleanupGate(engT, engF)
	if err != nil {
		return res, err
	}
	st := "DROP TABLE IF EXISTS " + qualifyCH(db, fixName) + " SYNC" + purgeGuard
	res.Steps = append(res.Steps, "-- "+note, st)
	ectx, cancel := context.WithTimeout(ctx, replicaRepairStepTimeout)
	err = conn.Exec(ectx, st)
	cancel()
	if err != nil {
		return res, fmt.Errorf("DROP: %w", err)
	}
	v, verr := s.verifyReplicaRepair(ctx, conn, db, req.Table)
	if verr == nil {
		res.Verify = v
	}
	return res, nil
}
