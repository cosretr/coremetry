package chstore

// dangling_mv_admin.go — v0.10.762 SARKAN MV onarım sihirbazı (operatör:
// "sihirbaz yap"; prod olayı 2026-09-17: spans spool'u 509K dosya / 398 GiB,
// son hata "Target table `.inner_id.<uuid>` of view … doesn't exist").
//
// Combined bir MaterializedView'ın (TO'suz) verisi gizli `.inner_id.<view
// uuid>` tablosundadır. dropCombinedMV önce iç tabloyu (boyut kalkanı
// için, v0.8.190), sonra view nesnesini ON CLUSTER düşürür; ikinci adım
// bir node'da yarım kalırsa o node'da SARKAN view kalır: INSERT kaskadı
// hedef tabloyu bulamaz, INSERT reddedilir, Distributed spool'u o shard
// için büyür — trace'lerin yarısı aranamaz hâle gelir.
//
// Üç parça: (1) tespit — clusterAllReplicas(system.tables) üzerinden
// host başına "view var, .inner_id yok" (TO'lu MV'ler hariç); (2) onarım
// — YALNIZ o node'a bağlanıp (shardConn) view'ı düşür + kanonik DDL'i
// ON CLUSTER'sız yeniden kur (Replicated iç tablo ZK yoluna eş replika
// olarak katılır ve veriyi diğer replikadan çeker); (3) boot logu +
// dropCombinedMV sonrası artık temizliği (aynı sınıf bir daha sessiz
// kalmasın). Tek-node kipinde aynı mantık s.conn üzerinden.

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// DanglingMV — bir node'daki sarkan view.
type DanglingMV struct {
	Host    string `json:"host"`
	Addr    string `json:"addr,omitempty"` // host:port (native); boş = çözülemedi
	Shard   int    `json:"shard,omitempty"`
	Replica int    `json:"replica,omitempty"`
	View    string `json:"view"`
	// UUID — EKSİK iç tablonun ADINDAKİ uuid (`.inner_id.<uuid>`; ingest'in
	// "Target table … doesn't exist" hatasında geçen ad). Bu uuid VIEW'ın
	// uuid'sidir: CH iç tablonun adını DAİMA generateInnerTableName(view_id)
	// ile kurar — bkz. innerTableName (v0.10.832).
	//
	// v0.10.780 burada DDL'deki `TO INNER UUID '…'` değerini taşıyordu; o
	// değer iç tablonun NESNE uuid'sidir ve ADA hiç girmez. Nesne uuid'si
	// gerektiğinde onarım anında, ayar O SORGUDA açılarak okunur
	// (innerObjectUUIDOn) — envanterde taşınamaz, çünkü varsayılan ayarda
	// DDL metninde HİÇ görünmez. 780'in ViewUUID alanı artık UUID ile aynı
	// şeydi: kaldırıldı.
	UUID      string `json:"uuid"`
	Canonical bool   `json:"canonical"` // kanonik DDL var → yeniden kurulabilir
	// PeerHost/PeerAddr — aynı shard'da iç tablosu SAĞLAM bir eş replika;
	// varsa onarım iç tabloyu oradan AYNI UUID ile kurar (view düşmez,
	// tarihçe replikasyondan gelir). Yoksa view yeniden kurulur (tarihçe yok).
	PeerHost string `json:"peerHost,omitempty"`
	PeerAddr string `json:"peerAddr,omitempty"`
}

// mvTableRow — system.tables'tan okunan satır (saf tespit girdisi).
type mvTableRow struct {
	Host, Name, UUID, Engine, CreateQuery string
}

const zeroUUID = "00000000-0000-0000-0000-000000000000"

// reMVWithTO — "CREATE MATERIALIZED VIEW db.x TO db.t …": iç tablosu yok,
// sarkan sayılmaz.
//
// v0.10.832 — regex UUID-KÖRDÜ. `show_table_uuid_in_table_create_query_if_not_nil
// = 1` olan bir profilde CH araya bir token koyar:
//
//	CREATE MATERIALIZED VIEW db.span_links_reverse_mv UUID 'aaaa…' TO db.hedef …
//
// Eski kalıp `\S+\s+TO\s+` istediği için bu biçimle HİÇ eşleşmiyordu ve
// TO'lu MV "gizli iç tablosu var" sayılıp HER host'ta `dangling` çıkıyordu
// ("Yeniden kur" açık, doğrulama her denemede "iç tablo doğmadı"). Yani
// v0.10.832'nin ilk turu ayar-asılılığını combined MV'den TO'lu MV'ye
// TAŞIMIŞTI; bu kalıp + mvInventory'nin ayar sabitlemesi (kemer + askı) onu
// gerçekten keser.
//
// `TO INNER UUID '…'` DE bu kalıba uyar (öneki aynı) — ayrım mvHasInnerTable'da
// yapılır, nesne uuid'si disjunkt'u ÖNDE durur. Go RE2'de lookahead yok,
// o yüzden ayrım burada değil orada.
var reMVWithTO = regexp.MustCompile(`(?is)^\s*(?:CREATE|ATTACH)(?:\s+OR\s+REPLACE)?\s+MATERIALIZED\s+VIEW\s+(?:IF\s+NOT\s+EXISTS\s+)?\S+(?:\s+UUID\s+'[^']*')?\s+TO\s+`)

// chObjRe — SYSTEM/DDL'e girecek nesne adı (spool_ops.go ile aynı disiplin).
var chObjRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reInnerObjectUUID — MV DDL'indeki `TO INNER UUID '<x>'`: iç tablonun KENDİ
// NESNE uuid'si (StorageID'nin üçüncü alanı), ADI DEĞİL. MV hedefini Atomic
// DB'de bu uuid ile çözer ve CH `to_inner_uuid == view uuid` durumunu
// ("cannot point to itself") YASAKLAR — iki uuid asla eşit olamaz.
var reInnerObjectUUID = regexp.MustCompile(`(?i)TO\s+INNER\s+UUID\s+'([0-9a-fA-F-]{36})'`)

// innerTableName — SAF, TEK GÖVDE (v0.10.832): combined bir MV'nin gizli iç
// tablosunun ADI. Ad HER ZAMAN VIEW'ın uuid'sinden doğar — ClickHouse'un
// generateInnerTableName(view_id) gövdesi `.inner_id.` + view_id.uuid döner
// ve CREATE/ATTACH/RENAME/EXCHANGE/REFRESH dallarının HEPSİ onu VIEW'ın
// kimliğiyle çağırır (v20.3 → master, istisnasız).
//
// NEDEN ayrı bir gövde: v0.10.780'den beri ad, DDL metnindeki
// `TO INNER UUID '<x>'` değerinden kuruluyordu. O değer iç tablonun KENDİ
// NESNE uuid'sidir (StorageID'nin üçüncü alanı) ve view uuid'siyle ASLA eşit
// olamaz — CH `to_inner_uuid == view uuid` durumunu "cannot point to itself"
// ile yasaklar. Yani o adla tablo HİÇBİR ZAMAN yoktur: ayar
// show_table_uuid_in_table_create_query_if_not_nil = 1 olan bir profilde
// kapsama kartı her MV × her host'u `dangling`, artık kartı CANLI her iç
// tabloyu `oksuz` sınıflardı ve düğmeler canlı veriyi tarihçesiyle
// düşürürdü. Doğruluk BİZİM OLMAYAN bir ayara asılıydı; bu gövde onu keser.
//
// Boş / sıfır uuid kararı ÇAĞIRANIN: validUUID (purge.go) ile eler.
func innerTableName(viewUUID string) string {
	return innerTablePrefix + strings.ToLower(viewUUID)
}

// innerObjectUUID — iç tablonun KENDİ nesne uuid'si. AYRI BİR TİP, çünkü
// v0.10.780'in hatası "yanlış satır" değil "yanlış DEĞER TÜRÜ"ydü: ad kuran
// ifadeye nesne uuid'si geçiyordu. Tip ayrımıyla bu artık DERLENMEZ —
// `innerTableName(string)` bir innerObjectUUID kabul etmez. Grep kapısı
// (TestInnerNameNeverBuiltFromObjectUUID) ikincil kalır: o SATIR kapsamlı
// ve 780'in gerçek şekli iki satırdı (`iu := innerUUIDFor(…)` + sonraki
// satırda `innerTablePrefix+iu`), yani kapıyı yeşil geçerdi.
type innerObjectUUID string

// innerObjectUUIDFromDDL — SAF (v0.10.832): iç tablonun NESNE uuid'si, yoksa
// "". ADIN kaynağı DEĞİLDİR (ad innerTableName ile VIEW uuid'sinden doğar).
//
// UYARI: varsayılan ayarda (show_table_uuid_in_table_create_query_if_not_nil
// = 0) hem system.tables.create_table_query hem SHOW CREATE metni İKİ uuid'yi
// de siler — bu fonksiyon o profilde HER ZAMAN "" döner. Nesne uuid'si
// gerekiyorsa ayarı O SORGUDA açmak ŞARTTIR (innerObjectUUIDOn).
func innerObjectUUIDFromDDL(createQuery string) innerObjectUUID {
	if m := reInnerObjectUUID.FindStringSubmatch(createQuery); m != nil {
		return innerObjectUUID(strings.ToLower(m[1]))
	}
	return ""
}

// mvHasInnerTable — SAF, TEK GÖVDE (v0.10.832): bu MV'nin GİZLİ bir iç
// tablosu var mı. `TO <tablo>` biçimli MV'nin hedefi gerçek bir tablodur,
// gizli iç tablosu YOKTUR. Üç yüzey (sarkan tespiti, kapsama, sahip
// haritası) bu kararı ayrı ayrı yazıyordu ve ayrışmışlardı.
//
// Sol kol (`nesne uuid'si okunuyorsa iç tablo VARDIR`) SINIFLANDIRMA
// YOLUNDA ÖLÜDÜR ve öyle kalmalı: mvInventory/verifyMVRebuild artık ayarı
// 0'a sabitler, yani o metinlerde uuid HİÇ görünmez. Kol yine de duruyor
// çünkü (a) ayarı 1 yapan başka bir çağıran (innerObjectUUIDOn'un metni)
// buradan geçerse doğru cevabı almalı, (b) `TO INNER UUID '…'` reMVWithTO
// kalıbına DA uyar ve o metinde sağ kol yanlış cevap verirdi — sıra bu
// yüzden böyle.
func mvHasInnerTable(createQuery string) bool {
	return innerObjectUUIDFromDDL(createQuery) != "" || !reMVWithTO.MatchString(createQuery)
}

// injectTableUUID — SAF: bir CREATE TABLE metnine, `name` tablo adından hemen
// sonra `UUID '<objectUUID>'` ekler ki tablo o NESNE uuid'siyle doğsun ve
// MV'nin `to_inner_uuid` ile çözdüğü hedef GERÇEKTEN bu tablo olsun.
//
// v0.10.832 — iki hata düzeldi: (1) ad ile nesne uuid'si TEK parametreydi,
// yani tablo hangi uuid'den adlandırıldıysa nesne de onu alıyordu; (2)
// "zaten var" kararı `strings.Contains(ddl, "UUID '")` ile veriliyordu ve bu
// `TO INNER UUID '` ile DE eşleşir — böyle bir metinde ekleme sessizce
// ATLANIR ve tablo rastgele bir uuid ile doğardı. Karar artık YAPISAL:
// yalnız tablo adından hemen SONRA gelen `UUID '` sayılır.
func injectTableUUID(ddl, name string, objectUUID innerObjectUUID) string {
	q := "`" + name + "`"
	i := strings.Index(ddl, q)
	if i < 0 {
		return ddl
	}
	i += len(q)
	if strings.HasPrefix(strings.TrimLeft(ddl[i:], " \t\n\r"), "UUID '") {
		return ddl // tablo düzeyi uuid metinde ZATEN var
	}
	return ddl[:i] + " UUID '" + string(objectUUID) + "'" + ddl[i:]
}

// danglingFromRows — SAF: host başına view uuid'si için `.inner_id.<uuid>`
// var mı. Çıktı host, view sıralı.
//
// v0.10.825: iç tablo haritası artık MOTORU da taşır — eş replika adayı
// yalnız iç tablosu REPLICATED olan host olabilir. Düz (AggregatingMergeTree)
// bir eşten SHOW CREATE ile kurmak düz tabloyu ÇOĞALTIR: kaskad düzelir ama
// iki host birbirini hiç replike etmez ve kart bunu "onarıldı" sayardı.
// shardOf boşsa (tek düğüm ya da küme eşlemesi okunamadı) eş YOKTUR.
func danglingFromRows(rows []mvTableRow, shardOf map[string]int) []DanglingMV {
	inner := map[string]map[string]string{}
	var views []mvTableRow
	for _, r := range rows {
		if isInnerTable(r.Name) {
			if inner[r.Host] == nil {
				inner[r.Host] = map[string]string{}
			}
			inner[r.Host][r.Name] = r.Engine
			continue
		}
		if r.Engine == "MaterializedView" {
			views = append(views, r)
		}
	}
	out := []DanglingMV{}
	for _, v := range views {
		// v0.10.832 — "gizli iç tablosu var mı" kararı TEK GÖVDE
		// (mvHasInnerTable). Burası yalnız reMVWithTO'ya bakıyordu, kapsama
		// ise ikinci bir yükleme: iki yüzey ayrışmıştı ve TO'lu MV'ler ayar
		// 1 profilinde ikisinde de yanlış sınıflanıyordu.
		if !validUUID(v.UUID) || !mvHasInnerTable(v.CreateQuery) {
			continue
		}
		// Ad VIEW uuid'sinden doğar — DDL'deki nesne uuid'sinden DEĞİL.
		name := innerTableName(v.UUID)
		if _, has := inner[v.Host][name]; has {
			continue // iç tablo VAR (düz olabilir — o "plain", mv_coverage.go)
		}
		_, canon := canonicalMVForObject(v.Name)
		d := DanglingMV{Host: v.Host, View: v.Name, UUID: strings.ToLower(v.UUID), Canonical: canon}
		// Eş replika adayı: iç tablo başka bir host'ta REPLICATED duruyor mu
		// (shard eşleşmesi DanglingMVs'te adreslerle yapılır; burada yalnız
		// aday listesi). Deterministik: ada göre ilk.
		// Eş replika: AYNI shard'da iç tablosu REPLICATED olan host.
		// v0.10.825 — kural TEK GÖVDE (replicatedInnerPeer): DanglingMVs'in
		// içindeki ikiz döngü silindi, kapsama kartı da aynı gövdeyi okur.
		d.PeerHost = replicatedInnerPeer(inner, v.Host, name, shardOf)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].View < out[j].View
	})
	return out
}

// canonicalMVForObject — nesne adı → kanonik MV adı. Terfi edilmiş MV'ler
// kümede `<ad>_local` olarak yaşar (mvStorageName).
func canonicalMVForObject(obj string) (string, bool) {
	if canonicalMVDDL(obj) != "" {
		return obj, true
	}
	if base := strings.TrimSuffix(obj, "_local"); base != obj && canonicalMVDDL(base) != "" {
		return base, true
	}
	return "", false
}

// reOnCluster — adaptDDL'in eklediği ON CLUSTER parçası; node'a özel
// çalıştırmada sökülür (aksi hâlde DDL yine kümeye dağılır).
var reOnCluster = regexp.MustCompile("(?i)\\s+ON\\s+CLUSTER\\s+`?[A-Za-z0-9_.-]+`?")

// stripOnCluster — SAF.
func stripOnCluster(sql string) string { return reOnCluster.ReplaceAllString(sql, "") }

// DanglingMVs — küme geneli (ya da tek node) sarkan view listesi.
// Envanter okuması v0.10.825'te mvInventory'ye çıkarıldı: MV kapsama kartı
// (mv_coverage.go) AYNI satırları okur, iki kopya sorgu iki gerçek üretirdi.
func (s *Store) DanglingMVs(ctx context.Context) ([]DanglingMV, string, error) {
	in, cluster, err := s.mvInventory(ctx)
	if err != nil {
		return nil, cluster, err
	}
	// Shard ekseni ÖNCE: eş kararı saf gövdede verilir (replicatedInnerPeer),
	// burada yalnız adres eklenir. shard 0 = system.clusters'ta eşleşmedi →
	// haritaya girmez → o host eş olamaz (eski `Shard != 0` kuralı).
	shardOf := map[string]int{}
	byHost := map[string]clusterHostRow{}
	if cluster != "" {
		if hosts, _, herr := s.clusterHostRows(ctx); herr == nil {
			for _, h := range hosts {
				byHost[h.Host] = h
				if h.Shard != 0 {
					shardOf[h.Host] = h.Shard
				}
			}
		}
	}
	out := danglingFromRows(in, shardOf)
	for i := range out {
		if h, ok := byHost[out[i].Host]; ok {
			out[i].Addr, out[i].Shard, out[i].Replica = fmt.Sprintf("%s:%d", h.Host, h.Port), h.Shard, h.Replica
		}
		// Adresi çözülemeyen eş EŞ DEĞİLDİR: onarım ona bağlanamaz ve
		// "Eşten kur" sözü tutulamazdı (v0.10.825 incelemesi).
		if out[i].PeerHost == "" {
			continue
		}
		if ph, ok := byHost[out[i].PeerHost]; ok {
			out[i].PeerAddr = fmt.Sprintf("%s:%d", ph.Host, ph.Port)
		} else {
			out[i].PeerHost = ""
		}
	}
	return out, cluster, nil
}

// RepairDanglingMV — o node'da: view'ı düşür (iç tablo zaten yok), kanonik
// DDL'i ON CLUSTER'sız kur, iç tablonun doğduğunu doğrula. Koşulan
// ifadeler döner (audit + ekran).
//
// wantPeer — operatörün EKRANDA gördüğü eylem (v0.10.825 incelemesi):
// true ise kartta "Eşten kur" yazıyordu ve modal "view düşmez, tarihçeyi
// eşten çeker" sözünü verdi. O söz tutulamıyorsa (aynı shard'da Replicated
// eş çözülemedi) bu fonksiyon SESSİZCE DROP + kanonik CREATE'e DÜŞMEZ —
// hata döner, operatör "Yeniden kur"u bilerek seçer. Eski kod tam burada
// düşüyor ve tarihçeyi onay alınmamış bir eylemle yakıyordu.
func (s *Store) RepairDanglingMV(ctx context.Context, host, view string, wantPeer bool) ([]string, error) {
	if !chObjRe.MatchString(view) {
		return nil, fmt.Errorf("geçersiz nesne adı %q", view)
	}
	list, _, err := s.DanglingMVs(ctx)
	if err != nil {
		return nil, fmt.Errorf("tespit: %w", err)
	}
	var row *DanglingMV
	for i := range list {
		if list[i].Host == host && list[i].View == view {
			row = &list[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("%s/%s şu an sarkan değil ya da bulunamadı — yeniden Ölç", host, view)
	}
	conn := s.conn
	if s.clusterMode() {
		if row.Addr == "" {
			return nil, fmt.Errorf("%s adresi system.clusters'tan çözülemedi", host)
		}
		c, err := s.shardConn(ctx, row.Addr)
		if err != nil {
			return nil, fmt.Errorf("node bağlantısı: %w", err)
		}
		conn = c
	}
	// v0.10.780 — TERCİH: iç tabloyu eş replikadan kur. View düşmez, iç tablo
	// {uuid}'li Replicated yoluna katılır ve tarihçeyi eşten çeker; kaskad
	// anında düzelir. Eş yoksa eski yol (view'ı yeniden kur).
	if wantPeer && (row.PeerAddr == "" || !s.clusterMode()) {
		return nil, fmt.Errorf("aynı shard'da Replicated eş yok — 'Yeniden kur' ile devam et (tarihçe bu host'ta sıfırlanır)")
	}
	if wantPeer {
		return s.repairInnerFromPeer(ctx, conn, row)
	}
	name, ok := canonicalMVForObject(view)
	if !ok {
		return nil, fmt.Errorf("%s için kanonik DDL yok (migrations/*.sql MV'si) ve eş replika bulunamadı — elle onar", view)
	}
	// v0.10.825 — kanonik dal TEK GÖVDE: "Yeniden kur" (RebuildMVOnHost) ile
	// aynı adımlar ve aynı doğrulama. İkinci bir kopya ayrışırdı.
	return s.rebuildMVOnConn(ctx, conn, view, name)
}

// chInnerUUIDSettings — nesne uuid'sini metinde GÖRÜNÜR kılan ayar +
// okuma tavanı (v0.10.832). Varsayılan
// show_table_uuid_in_table_create_query_if_not_nil = 0'dır ve o profilde
// create_table_query HEM view'ın HEM iç tablonun uuid'sini siler
// (StorageSystemTables: ast_create->uuid = Nil; targets->resetInnerUUIDs()).
// Ayar bizim değil ve depo hiçbir yerde sabitlemiyor — nesne uuid'si
// gerektiğinde İSTEDİĞİMİZ sorguda AÇIKÇA açılır, metinden tahmin edilmez.
const chInnerUUIDSettings = " SETTINGS show_table_uuid_in_table_create_query_if_not_nil = 1, max_execution_time = 10"

// innerObjectUUIDOn — verilen NODE-YEREL bağlantıda `view`ın gizli iç
// tablosunun NESNE uuid'si (`TO INNER UUID`). Okunamıyorsa HATA: çağıran
// eylemi reddeder, view uuid'sine DÜŞMEZ (o, adın uuid'sidir ve nesne
// uuid'sine asla eşit olamaz).
func (s *Store) innerObjectUUIDOn(ctx context.Context, conn driver.Conn, view string) (innerObjectUUID, error) {
	var cq string
	if err := conn.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database = currentDatabase() AND name = ?"+chInnerUUIDSettings, view).Scan(&cq); err != nil {
		return "", err
	}
	u := innerObjectUUIDFromDDL(cq)
	if !validUUID(string(u)) {
		// v0.10.832 inceleme (KÜÇÜK): dördüncü ve en olası neden metinde
		// YOKTU — eski sürümde kurulmuş ya da ATTACH ile gelmiş bir MV'nin
		// `to_inner_uuid`'si Nil olabilir. Sonucu da söyle: o durumda iç
		// tablolar host'lar arasında ZATEN ayrışmıştır (her node kendi
		// adını/uuid'sini üretmiştir), yani eşten kurulum tarihçe GETİRMEZ.
		return "", fmt.Errorf("%s DDL'i TO INNER UUID taşımıyor — TO'lu MV, Ordinary DB, ayar sunucuda engelli ya da MV eski sürümde/ATTACH ile kurulduğu için to_inner_uuid Nil (bu son durumda iç tablolar host'lar arasında zaten ayrışmıştır, eşten kurulum tarihçe getirmez)", view)
	}
	return u, nil
}

// repairInnerFromPeer — "Eşten kur": eş bağlantısını çözer, işi
// repairInnerOn'a verir. Ayrım TEST İÇİN: eş bağlantısı gerçek I/O'dur ve
// onsuz dalın davranışı hiç ölçülemiyordu (iki anlamsal tersine çevirme tüm
// paketi yeşil bırakıyordu — v0.10.832 incelemesi).
func (s *Store) repairInnerFromPeer(ctx context.Context, conn driver.Conn, row *DanglingMV) ([]string, error) {
	peer, err := s.shardConn(ctx, row.PeerAddr)
	if err != nil {
		return nil, fmt.Errorf("eş replika bağlantısı (%s): %w", row.PeerHost, err)
	}
	return s.repairInnerOn(ctx, conn, peer, row)
}

// repairInnerOn — eksik iç tabloyu eş replikanın DDL'iyle, YEREL MV'nin
// hedeflediği NESNE uuid'siyle kurar ve gerçekten katıldığını doğrular.
//
// v0.10.832 — bu dal ÇALIŞMIYORDU: injectTableUUID'e ADIN uuid'si (row.UUID)
// nesne uuid'si olarak veriliyordu. MV hedefini `to_inner_uuid` ile çözdüğü
// için MV böyle kurulan tabloyu BULAMAZ. (Dahası aynı düğümde VIEW o uuid'yi
// zaten tutuyordur; CH `CREATE TABLE … UUID '<view uuid>'`'yi UUID collision
// ile reddeder.) Yol YIKICI DEĞİL, o yüzden fail-closed: uuid okunamıyorsa,
// eşinki tutmuyorsa ya da ZK yolu tutmuyorsa eylem REDDEDİLİR/hata döner ve
// operatör "Yeniden kur"u bilerek seçer.
func (s *Store) repairInnerOn(ctx context.Context, conn, peer driver.Conn, row *DanglingMV) ([]string, error) {
	inner := innerTableName(row.UUID)
	// 1. YEREL MV'nin hedefi. Tahmin yok — okunamazsa RET.
	want, err := s.innerObjectUUIDOn(ctx, conn, row.View)
	if err != nil {
		return nil, fmt.Errorf("%s@%s: iç tablonun nesne uuid'si okunamadı (%v) — yanlış uuid'li bir tablo kartı yeşile boyar ama MV onu BULAMAZ; 'Yeniden kur' ile devam et", row.View, row.Host, err)
	}
	// 2. Eşin iç tablosu: DDL + KENDİ nesne uuid'si (aynı okumada).
	var peerRaw, ddl string
	if err := peer.QueryRow(ctx, "SELECT toString(uuid), create_table_query FROM system.tables WHERE database = currentDatabase() AND name = ?"+chInnerUUIDSettings, inner).Scan(&peerRaw, &ddl); err != nil {
		return nil, fmt.Errorf("eş replikadan DDL (%s): %w", row.PeerHost, err)
	}
	// 3. Eşin nesne uuid'si YEREL MV'nin hedefiyle aynı olmalı.
	//
	// v0.10.832 incelemesi — gerekçe DÜZELTİLDİ: bu kapı ZK yolunu KORUMAZ.
	// Bu depoda iç tablonun Replicated argümanları AD tabanlıdır
	// (replicatedArgs: '<önek>/{shard}/<ad>', '{replica}'), içinde `{uuid}`
	// YOKTUR — nesne uuid'sinin ZK grubuna etkisi yok. Kapı yine de ŞART,
	// ama başka sebeple: MV hedefini NESNE uuid'siyle çözer; eşinki farklıysa
	// eşin DDL'ini kopyalamak bu host'ta MV'nin bulamayacağı bir tablo üretir
	// (yanlış gerekçe dokümanda durdukça uygulanır — v0.10.832).
	peerUUID := innerObjectUUID(peerRaw)
	if !strings.EqualFold(peerRaw, string(want)) {
		return nil, fmt.Errorf("eş %s'in iç tablosu %s nesne uuid'sini taşıyor, %s@%s ise %s hedefliyor — kopyalanan tabloyu bu host'taki MV BULAMAZ; 'Yeniden kur' ile devam et", row.PeerHost, peerUUID, row.View, row.Host, want)
	}
	ddl = injectTableUUID(ddl, inner, want)
	steps := []string{
		fmt.Sprintf("-- eş replika %s'ten; ad `%s` (VIEW uuid'si), nesne uuid'si %s (%s MV'sinin TO INNER UUID'si):", row.PeerHost, inner, want, row.View),
		ddl,
	}
	ectx, cancel := context.WithTimeout(ctx, mvRebuildStepTimeout)
	err = conn.Exec(ectx, ddl)
	cancel()
	if err != nil {
		return steps, fmt.Errorf("iç tablo kurulamadı: %w", err)
	}
	// 4. DOĞRULAMA: satırın uuid KOLONU istenen nesne uuid'si olmalı. Yalnız
	// count()>0 bakmak yalan söylüyordu — yanlış uuid'li bir tablo da bir
	// satırdır ve kaskad yine kırık kalırdı.
	var got string
	if err := conn.QueryRow(ctx, "SELECT toString(uuid) FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", inner).Scan(&got); err != nil {
		return steps, fmt.Errorf("doğrulama: iç tablo doğmadı (%v)", err)
	}
	if !strings.EqualFold(got, string(want)) {
		return steps, fmt.Errorf("doğrulama: `%s` nesne uuid'si %s, beklenen %s — MV hedefini bulamaz, elle düşürüp yeniden kur", inner, got, want)
	}
	return s.verifyPeerZKPath(ctx, conn, peer, row, inner, steps)
}

// verifyPeerZKPath — v0.10.832 incelemesi: TARİHÇENİN GELMESİ neye bağlıysa
// ONU ölç. ZK yolu `{shard}` makrosundan türer ve bu filoda "aynı shard,
// FARKLI ZooKeeper yolu" bilinen bir sınıftır (replicaVerdict'in
// `no_replication` kararı tam bu). Nesne uuid'si eşitliği bunu ÖLÇMEZ.
//
// Kanonik dal (verifyMVRebuild) zaten system.replicas'a bakıyordu, eşten-kur
// dalı uuid kolonunda duruyordu: iki dal iki sözleşme — birleştirildi.
// Okuma hatası UYARIDIR (DDL koştu; v0.10.820 dersi), yol FARKI hatadır.
func (s *Store) verifyPeerZKPath(ctx context.Context, conn, peer driver.Conn, row *DanglingMV, inner string, steps []string) ([]string, error) {
	const q = "SELECT zookeeper_path FROM system.replicas WHERE database = currentDatabase() AND table = ? SETTINGS max_execution_time = 10"
	var localZK, peerZK string
	if err := conn.QueryRow(ctx, q, inner).Scan(&localZK); err != nil {
		steps = append(steps, "-- uyarı: yerel system.replicas satırı okunamadı ("+err.Error()+") — DDL koştu, ZK yolu eşliği DOĞRULANMADI, kartı yeniden Ölç")
		return steps, nil
	}
	if err := peer.QueryRow(ctx, q, inner).Scan(&peerZK); err != nil {
		steps = append(steps, "-- uyarı: eşin system.replicas satırı okunamadı ("+err.Error()+") — DDL koştu, ZK yolu eşliği DOĞRULANMADI, kartı yeniden Ölç")
		return steps, nil
	}
	if localZK != peerZK {
		return steps, fmt.Errorf("doğrulama: `%s` bu host'ta %s yolunda, eş %s'te %s yolunda — AYRI ZooKeeper grupları, tarihçe eşten GELMEZ ({shard} makrosu host'lar arasında ayrışmış; Replika tutarlılığı kartındaki makro sorgusuna bak)", inner, localZK, row.PeerHost, peerZK)
	}
	steps = append(steps, fmt.Sprintf("-- doğrulandı: nesne uuid'si MV'nin hedefiyle aynı ve ZK yolu eşle AYNI (%s)", localZK))
	return steps, nil
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// dropLeftoverViewObjects — dropCombinedMV'nin ON CLUSTER view düşürmesi
// bir node'da yarım kaldıysa artık view'ı o node'da düşürür (best-effort,
// yüksek sesle log). Sonraki CREATE … IF NOT EXISTS ON CLUSTER o node'da
// artık sarkan adı görmez ve MV'yi taze kurar.
func (s *Store) dropLeftoverViewObjects(ctx context.Context, mv string) {
	if !s.clusterMode() {
		return
	}
	obj := s.mvStorageName(mv)
	list, _, err := s.DanglingMVs(ctx)
	if err != nil {
		log.Printf("[chstore] sarkan MV taraması düştü (%v) — %s için artık view kontrolü atlandı", err, obj)
		return
	}
	for _, d := range list {
		if d.View != obj {
			continue
		}
		if d.Addr == "" {
			log.Printf("[chstore] SARKAN VIEW %s@%s — adres çözülemedi, elle: DROP TABLE %s SYNC (o node'da)", d.View, d.Host, d.View)
			continue
		}
		c, cerr := s.shardConn(ctx, d.Addr)
		if cerr != nil {
			log.Printf("[chstore] SARKAN VIEW %s@%s — node bağlantısı: %v", d.View, d.Host, cerr)
			continue
		}
		if e := c.Exec(ctx, "DROP TABLE IF EXISTS `"+d.View+"` SYNC"); e != nil {
			log.Printf("[chstore] SARKAN VIEW %s@%s düşürülemedi: %v", d.View, d.Host, e)
			continue
		}
		log.Printf("[chstore] sarkan view %s@%s düşürüldü (ON CLUSTER drop o node'da yarım kalmıştı)", d.View, d.Host)
	}
}

// LogDanglingMVs — boot sonrası: sarkan view varsa yüksek sesle log.
// Onarım sihirbazdan (Admin → ClickHouse), boot'ta DDL koşmaz.
func (s *Store) LogDanglingMVs(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, _, err := s.DanglingMVs(pctx)
	if err != nil {
		log.Printf("[chstore] sarkan MV taraması düştü: %v", err)
		return
	}
	for _, d := range list {
		log.Printf("[chstore] SARKAN MV: %s@%s (uuid %s) — INSERT kaskadı bu node'da DÜŞER, Distributed spool büyür; Admin → ClickHouse → MV onarımı", d.View, d.Host, d.UUID)
	}
}
