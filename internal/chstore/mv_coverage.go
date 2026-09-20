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
	UUID        string `json:"uuid,omitempty"` // iç tablonun uuid'si
	// PeerHost/PeerAddr — yalnız dangling'de ve yalnız eşin iç tablosu
	// REPLICATED ise: düz bir eşten kopyalamak düz tabloyu çoğaltır.
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
	inner := map[string]map[string]string{}     // host → `.inner_id.<uuid>` → engine
	views := map[string]map[string]mvTableRow{} // host → view adı → satır
	for _, r := range rows {
		if isInnerTable(r.Name) {
			if inner[r.Host] == nil {
				inner[r.Host] = map[string]string{}
			}
			inner[r.Host][r.Name] = r.Engine
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
			eng, has := inner[host][name]
			switch {
			case !has:
				st.State = MVStateDangling
				st.PeerHost = replicatedInnerPeer(inner, host, name, shardOf)
			case !replicatedInner || strings.HasPrefix(eng, "Replicated"):
				st.State = MVStateOK
			default:
				st.State, st.InnerEngine = MVStatePlain, eng
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
func replicatedInnerPeer(inner map[string]map[string]string, self, innerName string, shardOf map[string]int) string {
	sh, ok := shardOf[self]
	if !ok {
		return ""
	}
	best := ""
	for h, set := range inner {
		if h == self || !strings.HasPrefix(set[innerName], "Replicated") {
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
func (s *Store) mvInventory(ctx context.Context) ([]mvTableRow, string, error) {
	cluster := ""
	src := "system.tables"
	if s.clusterMode() {
		cluster = strings.TrimSpace(s.cfg.ClusterName)
		src = fmt.Sprintf("clusterAllReplicas('%s', system.tables)", cluster)
	}
	var db string
	if err := s.conn.QueryRow(ctx, "SELECT currentDatabase()").Scan(&db); err != nil {
		return nil, cluster, fmt.Errorf("currentDatabase: %w", err)
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
		return nil, cluster, err
	}
	defer rows.Close()
	var out []mvTableRow
	for rows.Next() {
		var r mvTableRow
		if err := rows.Scan(&r.Host, &r.Name, &r.UUID, &r.Engine, &r.CreateQuery); err != nil {
			return nil, cluster, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, cluster, err // yarım envanter = sahte "eksik MV"
	}
	return out, cluster, nil
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

// MVCoverage — kanonik MV kataloğu × erişilebilir host: hücre başına durum.
// Adresler resolveHostAddrs ile (v0.10.820): küme tanımı IP ile yazılmışsa
// system.clusters.host_name hostName() ile UYUŞMAZ, tek güvenilir yol her
// satıra bağlanıp hostName() sormaktır. Adres çözülemezse Addr boş kalır ve
// FE düğmeyi "adres çözülemedi" ile kapatır.
func (s *Store) MVCoverage(ctx context.Context) ([]MVHostState, string, error) {
	rows, cluster, err := s.mvInventory(ctx)
	if err != nil {
		return nil, cluster, fmt.Errorf("system.tables: %w", err)
	}
	hosts, err := s.mvHostRoster(ctx, cluster)
	if err != nil {
		return nil, cluster, fmt.Errorf("host rosteri: %w", err)
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
	if cluster == "" {
		return out, cluster, nil
	}
	addrs, _, aerr := s.resolveHostAddrs(ctx)
	if aerr != nil {
		return out, cluster, nil // adres yok: satırlar kalır, eylem kapanır
	}
	for i := range out {
		out[i].Addr = addrs[out[i].Host]
		if out[i].PeerHost != "" {
			out[i].PeerAddr = addrs[out[i].PeerHost]
		}
	}
	return out, cluster, nil
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
	cov, cluster, err := s.MVCoverage(ctx)
	if err != nil {
		return nil, fmt.Errorf("tespit: %w", err)
	}
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
	if row.State == MVStateOK {
		return nil, fmt.Errorf("%s/%s zaten sağlıklı — yeniden Ölç", host, view)
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
