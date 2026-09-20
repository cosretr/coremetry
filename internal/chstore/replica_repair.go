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

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// ReplicaRepairRequest — (tablo, shard, host) üçlüsü; host = hostName().
type ReplicaRepairRequest struct {
	Table string `json:"table"`
	Shard int    `json:"shard"`
	Host  string `json:"host"`
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
	replicaRepairFixSuffix   = "_fix"
	replicaRepairStepTimeout = 2 * time.Minute
	// replicaRepairSyncTimeout — sürücü ReadTimeout 30 sn (store.go); SYNC
	// receive_timeout ile 25 sn'de sunucuda kesilir, zaman aşımı "sürüyor".
	replicaRepairSyncTimeout  = 30 * time.Second
	replicaRepairSyncReceiveS = 25
)

var (
	reReplicatedEngine = regexp.MustCompile(`(?s)ENGINE\s*=\s*(Replicated[A-Za-z]*MergeTree)\s*\(\s*'([^']*)'\s*,\s*'([^']*)'`)
	reCreateHeader     = regexp.MustCompile(`^\s*CREATE\s+TABLE\s+([^\s(]+)`)
	reMacro            = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
	rePartitionID      = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
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

// diskHeadroomOK — SAF: hedefin boş alanı klonlanacak eş verisi + yerel veri
// için yeter mi (%10 pay). free=0 (okunamadı) → karar verilmez (true, çağıran uyarır).
func diskHeadroomOK(free, peerBytes, localBytes uint64) bool {
	if free == 0 {
		return true
	}
	need := peerBytes + localBytes
	return free >= need+need/10
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
	for i := range rep.Tables {
		if rep.Tables[i].Table != req.Table {
			continue
		}
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

	// Eş: aynı shard'da sağlam Replicated replika.
	var peer *ReplicaState
	for i := range shard.Replicas {
		r := &shard.Replicas[i]
		if strings.HasPrefix(r.Engine, "Replicated") && !r.ReadOnly && !r.SessionExpired && r.ZKPath != "" {
			peer = r
			break
		}
	}
	if peer == nil {
		block("shard'da sağlam (Replicated, readonly olmayan) eş replika yok — kaynak DDL alınamaz")
		return plan, nil
	}
	plan.Peer, plan.ZKPath, plan.PeerReplica, plan.Engine = peer.Host, peer.ZKPath, peer.ReplicaName, peer.Engine
	if strings.Contains(peer.ZKPath, "{") {
		block("eşin ZK yolu makro taşıyor (%s) — beklenmedik; elle incele", peer.ZKPath)
	}

	// Adresler: hostName() → host:port (yoklama).
	addrs, notes, err := s.resolveHostAddrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("system.clusters: %w", err)
	}
	plan.Warnings = append(plan.Warnings, notes...)
	plan.targetAddr, plan.peerAddr = addrs[req.Host], addrs[peer.Host]
	if plan.targetAddr == "" {
		block("%s adresi system.clusters'tan çözülemedi (hostName() hiçbir satıra bağlanınca çıkmadı)", req.Host)
	}
	if plan.peerAddr == "" {
		block("eş %s adresi çözülemedi", peer.Host)
	}
	if len(plan.Blocked) > 0 {
		return plan, nil
	}
	check("adresler: hedef %s, eş %s", plan.targetAddr, plan.peerAddr)

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

	peerConn, err := s.shardConn(ctx, plan.peerAddr)
	if err != nil {
		return nil, fmt.Errorf("eş bağlantısı (%s): %w", peer.Host, err)
	}
	targetConn, err := s.shardConn(ctx, plan.targetAddr)
	if err != nil {
		return nil, fmt.Errorf("hedef bağlantısı (%s): %w", req.Host, err)
	}
	var ddl string
	if err := peerConn.QueryRow(ctx, "SHOW CREATE TABLE "+qualifyCH(rep.Database, req.Table)).Scan(&ddl); err != nil {
		return nil, fmt.Errorf("eşten DDL (%s): %w", peer.Host, err)
	}
	family, pathArg, replicaArg, ok := replicatedEngineArgs(ddl)
	if !ok {
		block("eşin DDL'inde Replicated motor argümanı yok (default_replica_path) — sihirbaz yalnız açık argümanlı motoru kopyalar")
		return plan, nil
	}
	if strings.Contains(pathArg, "{uuid}") {
		block("eşin yolu {uuid} taşıyor — uuid'li yol bu sihirbazın kapsamı dışında (elle: UUID '<eşin uuid'si>' ile CREATE)")
		return plan, nil
	}
	if !strings.HasPrefix(peer.Engine, family) && !strings.HasPrefix(family, peer.Engine) {
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
	if predicted, perr := expandMacros(pathArg, macros); perr != nil {
		plan.Warnings = append(plan.Warnings, "hedef makroları eşin yol argümanını çözemiyor ("+perr.Error()+"); yol literal yazıldı")
	} else if predicted != peer.ZKPath {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("hedefin {shard} makrosu eşin yolunu üretmiyor (%s ≠ %s); yol LİTERAL yazıldı — küme makroları düzeltilmeden sonraki küme-geneli DDL'ler yine ayrışır", predicted, peer.ZKPath))
	}
	check("motor %s · yol %s · hedef replika adı %s (eş: %s)", family, peer.ZKPath, targetReplica, peer.ReplicaName)

	// ZK: replicas altındaki znode'lar (bayat kayıt / çakışma).
	if names, zerr := zkChildren(ctx, peerConn, peer.ZKPath+"/replicas"); zerr == nil {
		for _, n := range names {
			if n == targetReplica {
				block("ZK'da %s/replicas/%s znode'u zaten var (bayat kayıt ya da çakışma) — eşte `%s` koşup (yalnız hedef adı için!) yeniden planla", peer.ZKPath, targetReplica, dropReplicaZkStmt(targetReplica, peer.ZKPath))
			}
		}
		check("ZK replicas: %s", strings.Join(names, ", "))
	} else {
		plan.Warnings = append(plan.Warnings, "system.zookeeper okunamadı: "+zerr.Error())
	}

	// Hedef DB motoru (EXCHANGE için Atomic şart).
	var dbEngine string
	if err := targetConn.QueryRow(ctx, "SELECT engine FROM system.databases WHERE name = ? SETTINGS max_execution_time = 5", rep.Database).Scan(&dbEngine); err != nil {
		return nil, fmt.Errorf("hedef system.databases: %w", err)
	}
	check("hedef DB motoru %s", dbEngine)
	if plan.Mode == replicaRepairModePlain && dbEngine != "Atomic" && dbEngine != "Replicated" {
		block("hedef DB motoru %s — EXCHANGE TABLES Atomic ister", dbEngine)
	}

	fixName := req.Table + replicaRepairFixSuffix
	// Eşin tablo boyutu: yeni replika (her iki kipte) eşin TÜM parçalarını
	// interserver'dan klonlar — hedefin boş alanı bunu karşılamalı.
	var peerBytes uint64
	if err := peerConn.QueryRow(ctx, "SELECT toUInt64(sum(bytes_on_disk)) FROM system.parts WHERE database = ? AND table = ? AND active SETTINGS max_execution_time = 10", rep.Database, req.Table).Scan(&peerBytes); err != nil {
		plan.Warnings = append(plan.Warnings, "eşin tablo boyutu okunamadı: "+err.Error())
	}
	plan.PeerBytes = peerBytes
	if plan.Mode == replicaRepairModePlain {
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
			// Ayarın ETKİN değeri ATTACH HEDEFİNDE (`_fix`, hedef host) geçerli
			// olandır — o yüzden targetConn. Sunucu varsayılanı okunur; eşin
			// DDL'i SETTINGS ile ezerse plan iyimser kalır (uyarı bunu söyler).
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
	// Her iki kipte: yeni replika eşin TÜM parçalarını klonlar (CREATE hemen
	// döner, çekme arka planda; interserver 9009 erişimi + disk yeri şart).
	plan.Warnings = append(plan.Warnings, fmt.Sprintf("tablo eşten klonlanır: eşte %d bayt, hedefte boş %d bayt (CREATE hemen döner, parçalar arka planda interserver'dan çekilir)", peerBytes, plan.TargetFreeBytes))
	if plan.TargetFreeBytes == 0 {
		plan.Warnings = append(plan.Warnings, "hedefin boş alanı okunamadı (system.disks) — disk yerini elle doğrula")
	} else if !diskHeadroomOK(plan.TargetFreeBytes, peerBytes, plan.TotalBytes) {
		block("hedefte boş alan yetersiz: %d bayt boş, en az %d bayt gerekir (eş verisi + yerel veri + %%10)", plan.TargetFreeBytes, (peerBytes+plan.TotalBytes)*11/10)
	}

	// Adımlar.
	createName := req.Table
	if plan.Mode == replicaRepairModePlain {
		createName = fixName
	}
	createDDL, err := rewriteReplicaDDL(ddl, rep.Database, req.Table, createName, peer.ZKPath)
	if err != nil {
		block("DDL yeniden yazılamadı: %v", err)
		return plan, nil
	}
	plan.Steps = append(plan.Steps, createDDL)
	if plan.Mode == replicaRepairModePlain {
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
