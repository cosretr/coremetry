package chstore

// mv_coverage.go — v0.10.825 "MV onarımı" sihirbazının veri yarısı
// (spec onayı 2026-09-20 "Onay"; kart AdminClickhouse.tsx DanglingMVPanel).
//
// v0.10.762'nin sarkan MV kartı TEK bir hastalığı tanıyordu: view VAR, gizli
// iç tablosu (`.inner_id.<uuid>`) YOK. Operatörün test kümesinde kanonik
// spanmetrics_hist_5m iki BAŞKA hastalıkta duruyordu — bir host'ta view var
// ama iç tablo DÜZ AggregatingMergeTree (eşlerle replike olmaz, o host
// yalnız kendine düşeni tutar), öteki host'ta view HİÇ YOK. İkisi de
// "view var, iç tablo yok" değil; kart "sarkan MV yok" diyordu ve iki host
// da sessizce ıraksıyordu. Kök neden replica_consistency.go'nun
// blindHosts uyarısıyla aynı: küme tanımı IP ile yazılmış, host'lar
// kendilerini is_local görmüyor, ON CLUSTER DDL o host'lara hiç ulaşmamış.
//
// Bu dosya kapsamayı DURUM olarak ölçer: kanonik MV kataloğu × erişilebilir
// host rosteri → ok | plain | dangling | missing. Sarkan liste
// (dangling_mv_admin.go) bu kümenin bir DİLİMİDİR ve kalır (kanonik olmayan,
// migrations/*.sql MV'lerini yalnız o taşır); kart ikisini birleştirir,
// aynı MV'yi iki kez çizmez.
//
// Onarım TEK GÖVDE: rebuildMVOnConn'u hem "Yeniden kur" (RebuildMVOnHost)
// hem sarkan onarımın kanonik dalı (RepairDanglingMV) çağırır — "aynalı
// kural tek gövde ister" (v0.9.1358): iki kopya ayrışır, kimse fark etmez.
//
// skip_unavailable_shards BİLEREK YOK (dangling_mv_admin.go ile aynı
// duruş): sessizce düşen bir host hem "eksik MV" satırını hem rosterdeki
// yerini kaybeder ve kart tam da DDL'in ulaşmadığı host için "sağlıklı"
// der. Erişilemeyen host bu kartta HATA'dır, boşluk değil.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// MV kapsama durumları — FE rozetleri bunlara göre (AdminClickhouse.tsx).
const (
	MVStateOK       = "ok"       // view var + iç tablo var (küme kipinde Replicated)
	MVStatePlain    = "plain"    // view var, iç tablo var ama Replicated DEĞİL
	MVStateDangling = "dangling" // view var, iç tablo YOK (v0.10.762 sınıfı)
	MVStateMissing  = "missing"  // view o host'ta hiç yok
)

// MVHostState — bir (kanonik MV × host) hücresinin durumu.
type MVHostState struct {
	Host string `json:"host"`
	Addr string `json:"addr,omitempty"` // host:port (native); boş = çözülemedi
	View string `json:"view"`           // küme depolama adı (mvStorageName)
	// State — ok | plain | dangling | missing.
	State string `json:"state"`
	// InnerEngine — yalnız plain'de dolu (operatöre "neden düz" der).
	InnerEngine string `json:"innerEngine,omitempty"`
	UUID        string `json:"uuid,omitempty"` // iç tablonun ADINDAKİ uuid = VIEW'ın uuid'si
	// v0.10.833 — ÜÇ uuid var ve üçü AYRI şeydir; adları bilerek uzun:
	//
	//	UUID       → view'ın uuid'si; iç tablonun ADI bundan doğar (v0.10.832)
	//	InnerUUID  → o adı taşıyan tablonun KENDİ nesne uuid'si (envanterden BEDAVA)
	//	TargetUUID → MV'nin `TO INNER UUID`'si, yani hedef olarak ÇÖZDÜĞÜ nesne
	//
	// InnerUUID ≠ TargetUUID ise ad doğru, hedef yanlış: kart `ok` der, MV
	// hedefini bulamaz, ingest "Target table … doesn't exist" demeye devam eder.
	InnerUUID  string `json:"innerUUID,omitempty"`
	TargetUUID string `json:"targetUUID,omitempty"`
	// Target — ok | mismatch | byname | unmeasured (mv_target_uuid.go). State'e
	// DİK bir karar: State'i ne değiştirir ne de ondan türer.
	Target string `json:"target"`
	// TargetResolves — düğüm-yerel ÖLÇÜM: MV hedefini gerçekten çözüyor mu.
	// ÜÇ HÂL ve üçü de farklı (bu yüzden işaretçi): true = çözüyor, veri
	// akıyor; false = kod 60, ingest bu host'ta düşüyor; nil = ölçülmedi.
	//
	// Metadata uyuşmazlığının SONUCU buradan okunur, Target'tan DEĞİL: gerçek
	// CH 24.8'de aynı uyuşmazlık hem ingest'i düşüren hem MV'nin toplamaya
	// devam ettiği şekli üretiyor. YIKICI EYLEM UYGUNLUĞU bu alana bağlı —
	// hedefini çözen bir satırda düğme çizilmez (mvRebuildAllowed).
	TargetResolves *bool `json:"targetResolves,omitempty"`
	// TargetNote — ölçülememe ya da bulgu sebebi, operatör diliyle.
	TargetNote string `json:"targetNote,omitempty"`
	// PeerHost/PeerAddr — aynı shard'da iç tablosu REPLICATED olan eş host
	// (düz bir eşten kopyalamak düz tabloyu çoğaltır, başka shard'daki eş
	// başka veriyi tutar). v0.10.835'e kadar YALNIZ dangling satırında
	// doluydu; hedef uuid onarımı adı VAR olan bir hücrede koştuğu için artık
	// iç tablosu okunabilen her hücrede dolar. "Eşten kur" düğmesinin kapısı
	// FE'de hâlâ state === 'dangling'dir — düğme yayılmaz.
	PeerHost string `json:"peerHost,omitempty"`
	PeerAddr string `json:"peerAddr,omitempty"`
}

// mvCoverageFromRows — SAF: envanter + kanonik katalog + host rosteri →
// hücre başına durum. Çıktı view, host sıralı; ok satırları DA döner (kart
// "N MV × M host sağlıklı" diyebilsin, FE süzer).
//
// replicatedInner: küme kipinde iç tablonun Replicated olması BEKLENİR.
// Tek düğümde kanonik DDL zaten düz AggregatingMergeTree kurar — o kipte
// "düz" bir hastalık DEĞİL, normaldir; parametre olmasaydı tek düğümlü her
// kurulum kataloğun tamamını kırmızı görürdü.
//
// shardOf: hostName() → shard. Eş replika AYNI shard'da olmak ZORUNDA
// (DanglingMVs ile aynı kural). Bu harita olmadan kart başka shard'daki bir
// host'u eş gösterir, operatör "Eşten kur"un yeşil sözünü (view düşmez,
// tarihçe eşten gelir) okur ve onarım sessizce DROP + kanonik CREATE'e
// düşerdi — tarihçe yanar. İki taraftan biri eşlenemiyorsa eş YOKTUR.
func mvCoverageFromRows(rows []mvTableRow, canonical []string, hosts []string, replicatedInner bool, shardOf map[string]int) []MVHostState {
	canon := map[string]bool{}
	for _, n := range canonical {
		if n = strings.TrimSpace(n); n != "" {
			canon[n] = true
		}
	}
	inner := map[string]map[string]mvInnerTable{} // host → `.inner_id.<uuid>` → motor + KENDİ uuid'si
	views := map[string]map[string]mvTableRow{}   // host → view adı → satır
	for _, r := range rows {
		if isInnerTable(r.Name) {
			if inner[r.Host] == nil {
				inner[r.Host] = map[string]mvInnerTable{}
			}
			// v0.10.833 — uuid ARTIK ATILMIYOR. Karşılaştırmanın bir tarafı
			// envanterde zaten geliyordu (`toString(uuid)`) ve tam burada
			// düşürülüyordu; ikinci bir sorgu açmadan bedava.
			inner[r.Host][r.Name] = mvInnerTable{Engine: r.Engine, UUID: r.UUID}
			continue
		}
		// Kanonik olmayan view'lar kapsama EKSENİNE girmez: onların "eksik"
		// olması diye bir şey yok (katalogda yoklar). Sarkan hâlleri
		// danglingFromRows'tan gelir.
		if r.Engine != "MaterializedView" || !canon[r.Name] {
			continue
		}
		if views[r.Host] == nil {
			views[r.Host] = map[string]mvTableRow{}
		}
		views[r.Host][r.Name] = r
	}
	names := make([]string, 0, len(canon))
	for n := range canon {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	hs := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h = strings.TrimSpace(h); h != "" && !seen[h] {
			seen[h] = true
			hs = append(hs, h)
		}
	}
	sort.Strings(hs)

	out := []MVHostState{}
	for _, view := range names {
		for _, host := range hs {
			st := MVHostState{Host: host, View: view}
			v, ok := views[host][view]
			if !ok {
				st.State = MVStateMissing
				out = append(out, st)
				continue
			}
			// Gizli iç tablosu OLMAYAN MV'ler: `TO <tablo>` biçimi (hedefi
			// gerçek bir tablo) ve uuid'si okunamayanlar (Ordinary DB:
			// iç tablo `.inner.<ad>`, bu envanterde yok). View duruyorsa
			// kanıtlanabilir bir hastalık yok → ok.
			if !validUUID(v.UUID) || !mvHasInnerTable(v.CreateQuery) {
				st.State = MVStateOK
				// v0.10.833 — hedef uuid sorusu bu hücrede GEÇERSİZ: `TO
				// <tablo>` biçimli MV'nin hedefi gerçek bir tablodur, Ordinary
				// DB'de iç tablo `.inner.<ad>` olarak yaşar — ikisinde de CH
				// hedefi ADLA çözer. Karar BURADA verilir (kanıt burada) ve
				// probe geçişi onu EZMEZ; aksi hâlde TO'lu her kanonik MV
				// kalıcı "ölçülemedi" sayılır, yeşil rozet hiç dönmezdi.
				st.Target = MVTargetByName
				st.TargetNote = "gizli iç tablo yok (TO'lu MV ya da Ordinary DB) — CH hedefi ADLA çözer"
				out = append(out, st)
				continue
			}
			// v0.10.832 — ad VIEW uuid'sinden doğar. Burası DDL'deki
			// `TO INNER UUID` değerini kullanıyordu; o NESNE uuid'sidir ve
			// öyle adlandırılmış bir tablo HİÇ yoktur — ayar açık bir
			// profilde HER hücre `dangling` çıkıyor, "Yeniden kur" kapısı
			// (State == ok değil) açılıyor ve DROP … SYNC + purgeGuard
			// CANLI iç tabloyu tarihçesiyle götürüyordu.
			name := innerTableName(v.UUID)
			st.UUID = strings.ToLower(v.UUID)
			it, has := inner[host][name]
			// v0.10.833 — karşılaştırmanın ENVANTER tarafı: o adı taşıyan
			// tablonun KENDİ nesne uuid'si. Kararı mvTargetVerdict verir.
			st.InnerUUID = strings.ToLower(it.UUID)
			// v0.10.835 — eş adayı ARTIK yalnız `dangling` satırında değil:
			// hedef uuid onarımı (şekil-1) ADI VAR OLAN bir hücrede koşar ve
			// tarihçeyi eşten getirebilmek için aynı eşe ihtiyaç duyar. Eşi
			// yalnız o dalda hesaplamak, onarımı sessizce tarihçesiz kanonik
			// dala mahkûm ederdi. Hesap TEK GÖVDE (replicatedInnerPeer) ve
			// kapıları aynı: düz eş aday değil, başka shard aday değil.
			// FE'nin "Eşten kur" düğmesi state === 'dangling' istediği için o
			// düğmenin çizildiği satırlar DEĞİŞMEZ.
			st.PeerHost = replicatedInnerPeer(inner, host, name, shardOf)
			switch {
			case !has:
				st.State = MVStateDangling
			case !replicatedInner || strings.HasPrefix(it.Engine, "Replicated"):
				st.State = MVStateOK
			default:
				st.State, st.InnerEngine = MVStatePlain, it.Engine
			}
			out = append(out, st)
		}
	}
	return out
}

// replicatedInnerPeer — SAF: AYNI SHARD'da iç tablosu REPLICATED olan başka
// bir host (deterministik: ada göre ilk). İki kapı:
//
//	düz eş aday DEĞİL — ondan SHOW CREATE ile kurmak düz tabloyu çoğaltır;
//	başka shard'daki eş aday DEĞİL — onun iç tablosu BAŞKA veriyi tutar,
//	aynı ZK yoluna katılmaz ve "tarihçeyi eşten çeker" sözü yalan olur.
//
// shardOf nil ya da taraflardan biri eşlenemiyorsa eş YOKTUR: "bilinmiyor"
// bu kartta "uygun" demek değildir (v0.10.825 incelemesi).
func replicatedInnerPeer(inner map[string]map[string]mvInnerTable, self, innerName string, shardOf map[string]int) string {
	sh, ok := shardOf[self]
	if !ok {
		return ""
	}
	best := ""
	for h, set := range inner {
		if h == self || !strings.HasPrefix(set[innerName].Engine, "Replicated") {
			continue
		}
		if psh, pok := shardOf[h]; !pok || psh != sh {
			continue
		}
		if best == "" || h < best {
			best = h
		}
	}
	return best
}

// mvInventory — küme geneli (ya da tek node) MV + iç tablo envanteri.
// v0.10.825'te DanglingMVs'in içinden ÇIKARILDI: kapsama ölçümü ve sarkan
// tespiti aynı satırları okur, iki kopya sorgu iki farklı gerçek üretirdi.
// db DA döner (v0.10.833): hedef uuid probe'u aynı adı BAĞLI parametre olarak
// ister ve ikinci bir `SELECT currentDatabase()` açmak bedava değil.
func (s *Store) mvInventory(ctx context.Context) ([]mvTableRow, string, string, error) {
	cluster := ""
	src := "system.tables"
	if s.clusterMode() {
		cluster = strings.TrimSpace(s.cfg.ClusterName)
		src = fmt.Sprintf("clusterAllReplicas('%s', system.tables)", cluster)
	}
	var db string
	if err := s.conn.QueryRow(ctx, "SELECT currentDatabase()").Scan(&db); err != nil {
		return nil, cluster, "", fmt.Errorf("currentDatabase: %w", err)
	}
	// v0.10.832 — ayar BİZ SABİTLİYORUZ. Sınıflandırma create_table_query'nin
	// BİÇİMİNE bakar (`TO <tablo>` var mı); profil
	// show_table_uuid_in_table_create_query_if_not_nil = 1 ise CH araya
	// `UUID '…'` token'ı koyar ve aynı metin başka bir şeye benzer. Doğruluğun
	// bizim olmayan bir ayara asılı kalmaması için okuma onu 0'a çiviler:
	// hangi profilde koşarsak koşalım metin AYNI gelir.
	rows, err := s.conn.Query(ctx, `
		SELECT hostName(), name, toString(uuid), engine, substring(create_table_query, 1, 400)
		FROM `+src+`
		WHERE database = ?
		  AND (engine = 'MaterializedView' OR name LIKE '.inner_id.%')
		SETTINGS max_execution_time = 15, show_table_uuid_in_table_create_query_if_not_nil = 0`, db)
	if err != nil {
		return nil, cluster, db, err
	}
	defer rows.Close()
	var out []mvTableRow
	for rows.Next() {
		var r mvTableRow
		if err := rows.Scan(&r.Host, &r.Name, &r.UUID, &r.Engine, &r.CreateQuery); err != nil {
			return nil, cluster, db, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, cluster, db, err // yarım envanter = sahte "eksik MV"
	}
	return out, cluster, db, nil
}

// mvHostRoster — kapsamanın HOST ekseni: küme kipinde cevap veren her düğüm
// (system.one, replica_consistency.go ile aynı kaynak), tek düğümde yalnız
// bağlı düğüm.
func (s *Store) mvHostRoster(ctx context.Context, cluster string) ([]string, error) {
	q := "SELECT hostName() SETTINGS max_execution_time = 10"
	if cluster != "" {
		q = fmt.Sprintf("SELECT hostName() FROM clusterAllReplicas('%s', system.one) SETTINGS max_execution_time = 10", cluster)
	}
	rows, err := s.conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// MVCoverageReport — kapsama + hedef uuid okumasının sonucu (v0.10.833).
// TargetError AYRI bir alan, hata DEĞİL: hedef uuid okuması düşerse kapsama
// sınıfları (ok|plain|dangling|missing) AYNEN durur, yalnız her hücrenin
// hedef kararı `unmeasured` olur — okuma hatası ≠ iş başarısız (v0.10.820).
type MVCoverageReport struct {
	Rows        []MVHostState `json:"rows"`
	Cluster     string        `json:"cluster"`
	TargetError string        `json:"targetError,omitempty"`
	// Targets — probe'un gördüğü to_inner_uuid kümesi. Artık kartı (öksüz
	// kararı) BU kümeye bakar: nesne uuid'si burada olan bir iç tablo CANLI
	// bir MV'nin hedefidir, öksüz DEĞİLDİR. İkinci bir sorgu açmamak için
	// kapsamadan taşınır.
	Targets MVTargetSet `json:"-"`
}

// MVCoverage — kanonik MV kataloğu × erişilebilir host: hücre başına durum.
// Adresler resolveHostAddrs ile (v0.10.820): küme tanımı IP ile yazılmışsa
// system.clusters.host_name hostName() ile UYUŞMAZ, tek güvenilir yol her
// satıra bağlanıp hostName() sormaktır. Adres çözülemezse Addr boş kalır ve
// FE düğmeyi "adres çözülemedi" ile kapatır.
func (s *Store) MVCoverage(ctx context.Context) (MVCoverageReport, error) {
	return s.mvCoverageReport(ctx, true)
}

// mvCoverage — harden=false: düğüm-yerel doğrulama SÜPÜRMESİ koşmaz.
// Eylem yolları (RebuildMVOnHost / DropLeftoverMV) bunu kullanır ve yalnız
// DOKUNACAKLARI satırı mvHardenCell ile ölçer: karar yine ölçülür ama 126
// hücrelik tarama tıklama başına İKİ KEZ ödenmez (FE onarımdan sonra yeniden
// tarar). Ölçüldü: kapaksız süpürme 6 host × 21 MV'de en kötü 21 dk.
func (s *Store) mvCoverageReport(ctx context.Context, harden bool) (MVCoverageReport, error) {
	snap, err := s.mvInventorySnap(ctx)
	if err != nil {
		return MVCoverageReport{Rows: []MVHostState{}, Cluster: snap.cluster}, fmt.Errorf("system.tables: %w", err)
	}
	return s.mvCoverageReportFrom(ctx, snap, harden, s.hostAddrsOnce(ctx))
}

// mvCoverageReportFrom — v0.10.848: envanter ve adres çözücü ÇAĞIRANDAN
// (mv_admin_report.go tek istekte üç okuyucuya aynı snapshot'ı verir).
func (s *Store) mvCoverageReportFrom(ctx context.Context, snap mvInventorySnap, harden bool, addrsOf func() (map[string]string, error)) (MVCoverageReport, error) {
	rep := MVCoverageReport{Rows: []MVHostState{}}
	rows, cluster, db := snap.rows, snap.cluster, snap.db
	rep.Cluster = cluster
	hosts, err := s.mvHostRoster(ctx, cluster)
	if err != nil {
		return rep, fmt.Errorf("host rosteri: %w", err)
	}
	names := canonicalMVNames()
	canonical := make([]string, 0, len(names))
	for _, n := range names {
		// Boot'ta BİLEREK atlanan MV (kaynak kolonu yok) "eksik" DEĞİLDİR:
		// onu kurmak insert-trigger'ı kod 16 ile düşürür ve TÜM ingest'i
		// bloklar (v0.8.186 / v0.8.375). Karar migrate() ile TEK GÖVDE.
		if s.mvGuardedOff(n) {
			continue
		}
		canonical = append(canonical, s.mvStorageName(n))
	}
	// Shard ekseni: system.clusters, eşleşmeyen host'lar için {shard} makrosu
	// (replica_consistency.shardRefFor) — küme tanımı IP ile yazılmışsa
	// host_name hostName() ile uyuşmaz ve TEK kaynak makrolardır. Okunamazsa
	// harita BOŞ kalır ve hiçbir satır eş göstermez (sessiz düşüş yerine
	// "Yeniden kur").
	shardOf := map[string]int{}
	if cluster != "" {
		if m, merr := s.hostShardMap(ctx, cluster, hosts); merr == nil {
			shardOf = m
		}
	}
	out := mvCoverageFromRows(rows, canonical, hosts, cluster != "", shardOf)
	// v0.10.833 — hedef uuid okuması: KÜME GENELİ TEK sorgu, yalnız MV
	// satırları, AYARI 1 YAPAN tek yer (mv_target_uuid.go). Buraya konuldu
	// çünkü mvInventory bir kart yenilemesinde ÜÇ KEZ koşuyor (DanglingMVs +
	// MVCoverage + MVLeftovers) — okumayı oraya koymak +1 değil +3 fan-out
	// ederdi.
	probe := s.mvTargetUUIDs(ctx, db)
	rep.TargetError, rep.Targets = probe.Err, probe.Targets
	mvApplyTargetVerdict(out, probe)
	rep.Rows = out
	if cluster == "" {
		// Tek düğümde adres yok; sertleştirme s.conn üzerinden koşar.
		if harden {
			s.mvCheckTargetResolution(ctx, rep.Rows, cluster)
		}
		return rep, nil
	}
	addrs, aerr := addrsOf()
	if aerr != nil {
		return rep, nil // adres yok: satırlar kalır, eylem kapanır
	}
	for i := range out {
		out[i].Addr = addrs[out[i].Host]
		if out[i].PeerHost != "" {
			out[i].PeerAddr = addrs[out[i].PeerHost]
		}
	}
	// Sertleştirme adresleri ÇÖZÜLDÜKTEN sonra: düğüm-yerel bağlantı ister.
	if harden {
		s.mvCheckTargetResolution(ctx, rep.Rows, cluster)
	}
	return rep, nil
}

// mvRebuildAllowed — SAF ALLOWLIST + ÖLÇÜLMÜŞ VETO (v0.10.833).
//
// Allowlist: eskiden kapı DENYLIST'ti ("durum ok ise reddet") ve State'e
// eklenecek HER yeni değer varsayılan olarak YIKICI eylemi (DROP … SYNC +
// kanonik CREATE) açardı. Artık yeni bir durum varsayılan olarak REDDEDİLİR.
//
// Veto: `dangling` bir satır CANLI olabilir. v0.10.780–831 arası "Eşten kur"
// yolunun bıraktığı şekilde iç tablo MV'nin hedeflediği NESNE uuid'siyle VAR
// ve MV ona YAZIYOR, yalnız ADI `.inner_id.<view uuid>` değil. Durum ADLA
// ilgili bir OLGUDUR ve `dangling` kalır (sessizce `ok`'a çevirmek kartı
// yalan söyletirdi) — ama hedefin ÇÖZÜLDÜĞÜ ÖLÇÜLDÜYSE DROP o düğümün
// çalışan tek toplamasını götürür. nil (ölçülmedi) veto DEĞİLDİR: yalnız
// kanıt kapıyı kapatır.
func mvRebuildAllowed(state string, targetResolves *bool) bool {
	if targetResolves != nil && *targetResolves {
		return false
	}
	switch state {
	case MVStatePlain, MVStateDangling, MVStateMissing:
		return true
	default:
		return false
	}
}

// mvRebuildStepTimeout — adım başına tavan (replica_repair.go ile aynı).
const mvRebuildStepTimeout = 2 * time.Minute

// RebuildMVOnHost — bir host'ta kanonik MV'yi yeniden kurar: view (varsa)
// düşer, kanonik DDL ON CLUSTER'sız koşar (küme kipinde Replicated iç tablo
// eş ZK yoluna katılır). Üç durumu da kapsar: plain (düz iç tablo), dangling
// (iç tablo yok), missing (view yok).
//
// Durum TAZE ölçülür — FE satırına güvenilmez (ölçümle tık arasında host
// onarılmış olabilir; "zaten sağlıklı" bir MV'yi düşürmek tarihçeyi bedava
// yakar).
func (s *Store) RebuildMVOnHost(ctx context.Context, host, view string) ([]string, error) {
	host, view = strings.TrimSpace(host), strings.TrimSpace(view)
	if !chObjRe.MatchString(view) {
		return nil, fmt.Errorf("geçersiz nesne adı %q", view)
	}
	name, ok := canonicalMVForObject(view)
	if !ok {
		return nil, fmt.Errorf("%s için kanonik DDL yok (migrations/*.sql MV'si) — elle", view)
	}
	// Süpürme ATLANIR (harden=false): bu yolda gereken tek ölçüm DOKUNULACAK
	// satırındır ve onu aşağıda tek tek yaparız — 126 hücrelik tarama
	// tıklama başına iki kez ödenmez.
	rep, err := s.mvCoverageReport(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("tespit: %w", err)
	}
	cov, cluster := rep.Rows, rep.Cluster
	var row *MVHostState
	for i := range cov {
		if cov[i].Host == host && cov[i].View == view {
			row = &cov[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("%s/%s kapsama raporunda yok — yeniden Ölç", host, view)
	}
	// v0.10.833 — kapının ikinci kolu ÖLÇÜLÜR: satır `dangling` görünse bile
	// MV hedefini çözüyor olabilir (ad beklenen değil ama tablo CANLI).
	// Ölçüm yalnız BU satır için koşar.
	s.mvHardenCell(ctx, row, cluster)
	// TEK KAPI ve ALLOWLIST (mvRebuildAllowed). Mesaj kozmetik, kararı
	// allowlist + ölçülmüş veto verir.
	if !mvRebuildAllowed(row.State, row.TargetResolves) {
		switch {
		case mvTargetResolved(row):
			return nil, fmt.Errorf("%s/%s: iç tablonun ADI beklenen değil ama MV hedefini ÇÖZÜYOR (düğüm-yerel okuma başarılı) — veri akıyor, DROP bu host'un çalışan tek toplamasını götürürdü. MV kartındaki hedef-uuid runbook'unu izle", host, view)
		case row.State == MVStateOK:
			return nil, fmt.Errorf("%s/%s zaten sağlıklı — yeniden Ölç", host, view)
		}
		return nil, fmt.Errorf("%s/%s durumu %q için yeniden kurulum TANIMLI DEĞİL (izinli: düz · sarkan · eksik) — kartı yeniden Ölç", host, view, row.State)
	}
	conn := s.conn
	if cluster != "" {
		if row.Addr == "" {
			return nil, fmt.Errorf("%s adresi system.clusters'tan çözülemedi", host)
		}
		c, cerr := s.shardConn(ctx, row.Addr)
		if cerr != nil {
			return nil, fmt.Errorf("node bağlantısı: %w", cerr)
		}
		conn = c
	}
	return s.rebuildMVOnConn(ctx, conn, view, name)
}

// rebuildMVOnConn — TEK GÖVDE (RebuildMVOnHost + RepairDanglingMV kanonik
// dalı): verilen NODE-YEREL bağlantıda view'ı düşür (kaskadla iç tablosunu
// da götürür) ve kanonik DDL'i ON CLUSTER'sız kur. Koşulan ifadeler hata
// yolunda da döner (audit + ekran).
//
// DROP purgeGuard taşır: düz iç tablo GiB'lerce olabilir ve
// max_table_size_to_drop varsayılanı DROP'u reddederdi (v0.10.110 dersi).
func (s *Store) rebuildMVOnConn(ctx context.Context, conn driver.Conn, view, name string) ([]string, error) {
	steps := []string{"DROP TABLE IF EXISTS `" + view + "` SYNC" + purgeGuard}
	for _, st := range s.adaptDDL(canonicalMVDDL(name)) {
		steps = append(steps, stripOnCluster(st))
	}
	for _, st := range steps {
		ectx, cancel := context.WithTimeout(ctx, mvRebuildStepTimeout)
		err := conn.Exec(ectx, st)
		cancel()
		if err != nil {
			return steps, fmt.Errorf("%s: %w", firstWords(st, 4), err)
		}
	}
	return s.verifyMVRebuild(ctx, conn, view, steps)
}

// verifyMVRebuild — aynı bağlantıda doğrulama: view doğdu mu, iç tablosu
// var mı, küme kipinde Replicated mi ve Keeper'a kayıtlı mı.
//
// system.replicas OKUNAMAZSA ya da satır vermezse bu bir UYARIDIR, hata
// değil: DDL koştu, başarılı bir onarımı okuma hatası yüzünden "başarısız"
// göstermek v0.10.820'nin düzelttiği sınıftır (ReplicaRepairResult.VerifyError).
func (s *Store) verifyMVRebuild(ctx context.Context, conn driver.Conn, view string, steps []string) ([]string, error) {
	// Ayar mvInventory ile AYNI şekilde 0'a çivili: iki yüzey aynı metni
	// görmezse aynı MV'yi farklı sınıflar (v0.10.832).
	var uuid, cq string
	if err := conn.QueryRow(ctx, "SELECT toString(uuid), substring(create_table_query, 1, 400) FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10, show_table_uuid_in_table_create_query_if_not_nil = 0", view).Scan(&uuid, &cq); err != nil {
		return steps, fmt.Errorf("doğrulama (view): %w", err)
	}
	if !mvHasInnerTable(cq) {
		return steps, nil // TO'lu MV: gizli iç tablosu yok
	}
	// v0.10.832 incelemesi (KÜÇÜK): innerTableName'in ilan ettiği validUUID
	// sözleşmesi BURADA YOKTU. Ordinary DB'de MV uuid'si sıfırdır ve iç tablo
	// `.inner.<ad>` olarak yaşar; `.inner_id.0000…` aranınca BAŞARILI bir
	// kurulum "iç tablo doğmadı" diye raporlanıyordu. Sıfır uuid HATA değil,
	// DOĞRULANAMAZ demektir — sessizce de geçilmez, adımda yazar.
	if !validUUID(uuid) {
		return append(steps, "-- uyarı: view'ın uuid'si okunamadı/sıfır (Ordinary DB: iç tablo `.inner.<ad>`) — DDL koştu, iç tablo doğrulaması atlandı"), nil
	}
	// Ad VIEW uuid'sinden (system.tables.uuid kolonu), DDL metnindeki nesne
	// uuid'sinden DEĞİL: yanlış adı arayan doğrulama taze kurulmuş SAĞLAM bir
	// MV'yi "iç tablo doğmadı" diye reddederdi.
	innerName := innerTableName(uuid)
	var innerEngine string
	if err := conn.QueryRow(ctx, "SELECT engine FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", innerName).Scan(&innerEngine); err != nil || innerEngine == "" {
		return steps, fmt.Errorf("doğrulama: iç tablo doğmadı (%v)", err)
	}
	if !s.clusterMode() {
		return steps, nil
	}
	if !strings.HasPrefix(innerEngine, "Replicated") {
		return steps, fmt.Errorf("doğrulama: iç tablo Replicated değil (%s) — kanonik DDL küme kipinde Replicated motor kurmalıydı", innerEngine)
	}
	// Tipler SQL'de sabit (replica_repair.go ile aynı disiplin): total/
	// active_replicas 24.3'ten sonra UInt32, çıplak uint8 taraması sağlam
	// bir onarımı "kayıtsız" gösterirdi.
	var zk, replica string
	var total, active uint32
	var ro uint8
	err := conn.QueryRow(ctx, "SELECT zookeeper_path, replica_name, toUInt32(total_replicas), toUInt32(active_replicas), toUInt8(is_readonly) FROM system.replicas WHERE database = currentDatabase() AND table = ? SETTINGS max_execution_time = 10", innerName).Scan(&zk, &replica, &total, &active, &ro)
	switch {
	case err != nil:
		steps = append(steps, "-- uyarı: iç tablonun system.replicas satırı okunamadı ("+err.Error()+") — DDL koştu, kartı yeniden Ölç")
	case ro == 1:
		steps = append(steps, fmt.Sprintf("-- uyarı: iç tablo readonly (%s, %d/%d replika) — Keeper bağlantısına bak", zk, active, total))
	default:
		steps = append(steps, fmt.Sprintf("-- doğrulandı: %s · %s · replika %s · %d/%d aktif", innerEngine, zk, replica, active, total))
	}
	return steps, nil
}
