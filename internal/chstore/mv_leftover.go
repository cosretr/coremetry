package chstore

// mv_leftover.go — v0.10.830 "MV artığı": terfi öncesi ÇIPLAK MV (kalıntı)
// ve SAHİPSİZ iç tablo (öksüz). Operatör onayı 2026-09-20 ("3 için de kontrol
// ve onay"); kart AdminClickhouse.tsx DanglingMVPanel.
//
// Olay: v0.10.825'in MV onarımı sihirbazı "MV'ler sağlıklı · 21 MV × 4 host"
// derken Replika tutarlılığı kartı `.inner_id.<uuid>` satırlarında KALICI
// kırmızı gösteriyordu (v0.10.824 etiketi: "MV iç tablosu · view:
// db_summary_5m") ve satırda EYLEM yoktu. İki kart da kendi ölçtüğü şey için
// haklıydı; ölçülmeyen bir sınıf vardı.
//
// Kök neden — çıplak bir MV kümede YAPISAL olarak kalıcıdır:
//   - mvStorageName (cluster.go): highVolume bir MV'nin küme depolama adı
//     `<mv>_local`; db_summary_5m highVolumeTables'ta.
//   - promoteCombinedMVs (cluster.go) çıplak→_local RENAME'i YALNIZ dört ad
//     için koşar (store.go, spanmetrics_* + operation_group_summary_5m).
//   - ensureDistributedWrappers (cluster.go) çıplak→_local taşımasını YALNIZ
//     `Replicated*` motorlu tablolarda yapar; combined bir MV
//     engine='MaterializedView' bildirir → o dal hiç çalışmaz.
//   - v0.10.825 kapsaması kanonik DEPOLAMA adına (`mvStorageName`) kilitli ve
//     kanonik olmayan view'ları düşürür → çıplak kalıntıyı GÖRMEZ.
//   - DanglingMVs kalıntıyı yalnız iç tablosu KAYIPSA görür.
//
// Sonuç: is_local kör bir host'ta (küme IP ile tanımlı → ON CLUSTER DDL o
// host'a hiç ulaşmamış) çıplak MV kendi `.inner_id.<uuid>`'siyle sonsuza dek
// durur, hiçbir yüzey onu taşımaz ve replika kartı uuid'yi kalıcı kırmızı
// çizer. Bu dosya sınıfı ÖLÇER (mvInventory — skip_unavailable_shards YOK,
// erişilemeyen host HATA'dır, boşluk değil) ve iki düğüm-yerel eylem verir.
//
// Sahip haritası TEK GÖVDE: mvOwnersByHost'u hem bu sınıflandırma hem
// v0.10.824'ün innerTableViews'ü okur — ikiz harita ayrışır, kimse fark etmez
// (v0.9.1358 dersi).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MV artık sınıfları — FE rozetleri bunlara göre (AdminClickhouse.tsx).
const (
	// MVLeftoverBare — sahibi VAR ama o ad kanonik depolama adı DEĞİL:
	// terfi öncesi çıplak MV. Kendi gizli iç tablosuyla birlikte durur ve
	// `<mv>_local` ile YAN YANA yaşarsa aynı insert'i iki kez toplar.
	MVLeftoverBare = "artik"
	// MVLeftoverOrphan — KÜME GENELİNDE hiçbir MV bu uuid'yi adreslemiyor:
	// sahibi düşmüş, iç tablosu kalmış. Kimse yazmaz, kimse okumaz; yalnız
	// disk tutar ve replika kartında kalıcı kırmızı satır üretir.
	MVLeftoverOrphan = "oksuz"
)

// MVLeftover — bir HOST'taki bir artık.
type MVLeftover struct {
	Host string `json:"host"`
	Addr string `json:"addr,omitempty"` // host:port (native); boş = çözülemedi
	Kind string `json:"kind"`           // artik | oksuz
	// Inner — gizli iç tablonun adı (`.inner_id.<uuid>`).
	Inner       string `json:"inner"`
	UUID        string `json:"uuid"`
	InnerEngine string `json:"innerEngine,omitempty"`
	// View — YALNIZ artik: o host'taki çıplak view adı (düşürülecek nesne).
	View string `json:"view,omitempty"`
	// Storage — YALNIZ artik: kanonik depolama adı (`<base>_local`). Kalıntı
	// düşerken bu MV çalışmaya DEVAM eder; eylem kapısı onun sağlığını arar.
	Storage string `json:"storage,omitempty"`
	// Rows/Bytes — iç tablonun aktif parçaları (modal "ne siliniyor" der).
	// Okunamazsa 0 kalır: boyut süstür, sınıflandırma ona bağlı DEĞİL —
	// ama EYLEM tarafı boyutu TAZE ve hata denetimli okur (veri kapısı).
	Rows  uint64 `json:"rows,omitempty"`
	Bytes uint64 `json:"bytes,omitempty"`
	// StorageInner/StorageRows/StorageBytes — v0.10.830 (inceleme): kanonik
	// `<base>_local`'in iç tablosu ve boyutu. Modal "ne siliniyor" kadar "ne
	// HAYATTA KALIYOR" da demeli; kapı da bunu ölçer.
	StorageInner string `json:"storageInner,omitempty"`
	StorageRows  uint64 `json:"storageRows,omitempty"`
	StorageBytes uint64 `json:"storageBytes,omitempty"`
	// Blocked — doluysa bu satırın EYLEMİ YOKTUR (FE düğme çizmez) ve neden
	// budur. Sunucu aynı metinle reddeder: tek gövde.
	Blocked string `json:"blocked,omitempty"`
}

// leftoverGuardedReason — SAF: guarded MV kalıntısı neden temizlenemez.
// FE'deki düğmesiz satırın açıklaması ile sunucunun reddi TEK metin.
func leftoverGuardedReason(storage string) string {
	return fmt.Sprintf("kaynak kolonu yok — boot bu MV'yi bilerek kurmuyor, kanonik %s bu kurulumda hiç doğmaz; kalıntı ancak elle düşürülebilir", storage)
}

// distributedWrapperStmt — SAF: adaptDDL çıktısındaki Distributed sarmalayıcı
// ifadesi, DÜĞÜM-YEREL hâle getirilmiş (ON CLUSTER sökülü). Yoksa "".
// Metin ÜRETİLMEZ: adaptDDL'in high-volume dalında zaten kurduğu ifade
// alınır, böylece sarmalayıcının shard anahtarı/küme adı tek gövdeden gelir.
func distributedWrapperStmt(frags []string) string {
	for _, f := range frags {
		if strings.Contains(f, "ENGINE = Distributed(") {
			return stripOnCluster(f)
		}
	}
	return ""
}

// leftoverDropStmts — SAF: kalıntı temizliğinin koşulacak İKİ ifadesi.
//
// Eylem YIKICI DEĞİL TAMAMLAYICIDIR (v0.10.830 incelemesi, KRİTİK): `artik`
// sınıfı yapı gereği yalnız highVolume adlarıdır, yani ürünün SORGULADIĞI
// çıplak ad (db_summary_5m, trace_summary_5m, metric_catalog…). Küme kipinde
// o adın Distributed sarmalayıcı olması gerekir; kalıntı host'unda onu şu an
// MV dolduruyor. Tek başına DROP, adı O HOST'TA YOK ederdi ve is_local-kör
// bir host'ta sarmalayıcıyı kuran her yol ON CLUSTER taşıdığı için ad BİR
// DAHA DOĞMAZDI → o düğüm koordinatör olduğunda UNKNOWN_TABLE (kod 60) ve
// /api/databases/trends gibi uçlar 500.
//
// Sarmalayıcı ifadesi üretilemiyorsa nil döner: yıkıcı yarıyı onarıcı yarısı
// olmadan KOŞMAYIZ, eylem reddedilir.
func leftoverDropStmts(view, wrapper string) []string {
	if strings.TrimSpace(view) == "" || strings.TrimSpace(wrapper) == "" {
		return nil
	}
	return []string{
		"DROP TABLE `" + view + "` SYNC" + purgeGuard,
		wrapper,
	}
}

// leftoverDataGate — SAF: kalıntı DOLU ve kanonik `_local` BOŞ ise temizlik
// o düğümün TEK dolu toplamasını yok eder. Varlık + motor ailesi kapısı
// (kapsama `ok`) bunu görmez: yeni kurulmuş bir `_local` 0 satırla da
// "sağlıklı"dır. Bu düğme veri TAŞIMAZ, o yüzden reddeder.
//
// Boş dönüş = izin. Kalıntı boşsa silinecek veri yoktur; ikisi de doluysa
// kanonik toplama zaten çalışıyordur.
func leftoverDataGate(view, storage string, leftoverRows, storageRows uint64) string {
	if leftoverRows > 0 && storageRows == 0 {
		return fmt.Sprintf("%s kalıntısı %d satır taşıyor, %s ise BOŞ (0 satır) — önce veri taşınmalı, bu düğme veriyi TAŞIMAZ", view, leftoverRows, storage)
	}
	return ""
}

// innerOwner — bir iç tablonun sahibi (view adı) ve sahibin bulunduğu host'lar.
type innerOwner struct {
	View  string
	Hosts []string
}

// mvOwnersByHost — SAF, TEK GÖVDE: host → `.inner_id.<uuid>` → o host'ta o
// iç tabloyu adresleyen MV'nin adı.
//
// uuid kaynağı innerUUIDFor: Atomic DB'de MV DDL'i `TO INNER UUID '…'` taşır,
// eski biçimde view'ın kendi uuid'si (v0.10.780 dersi — view uuid'sine bakan
// 762 prod'daki sarkan view'ı hiç göremedi). `TO <tablo>` biçimli MV'nin
// gizli iç tablosu YOKTUR, haritaya girmez.
func mvOwnersByHost(rows []mvTableRow) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, r := range rows {
		if r.Engine != "MaterializedView" || r.UUID == "" || r.UUID == zeroUUID {
			continue
		}
		if !reInnerUUID.MatchString(r.CreateQuery) && reMVWithTO.MatchString(r.CreateQuery) {
			continue // TO <tablo>: hedefi gerçek tablo
		}
		iu := innerUUIDFor(r.CreateQuery, r.UUID)
		if iu == "" || iu == zeroUUID {
			continue
		}
		if out[r.Host] == nil {
			out[r.Host] = map[string]string{}
		}
		name := innerTablePrefix + strings.ToLower(iu)
		// Aynı host'ta aynı uuid'yi iki view adresleyemez; yine de
		// deterministik ol (ada göre küçük olan).
		if cur, ok := out[r.Host][name]; !ok || r.Name < cur {
			out[r.Host][name] = r.Name
		}
	}
	return out
}

// innerTableOwners — SAF: küme geneli iç tablo → sahibi + sahibin host'ları.
// mvOwnersByHost'un TEK GÖVDESİNDEN türer.
func innerTableOwners(rows []mvTableRow) map[string]innerOwner {
	byHost := mvOwnersByHost(rows)
	hosts := make([]string, 0, len(byHost))
	for h := range byHost {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	out := map[string]innerOwner{}
	for _, h := range hosts {
		for inner, view := range byHost[h] {
			o := out[inner]
			if o.View == "" || view < o.View {
				o.View = view
			}
			o.Hosts = append(o.Hosts, h)
			out[inner] = o
		}
	}
	for inner, o := range out {
		sort.Strings(o.Hosts)
		out[inner] = o
	}
	return out
}

// mvLeftoversFromRows — SAF sınıflandırma, HOST başına. Üç sonuç:
//
//	GÜNCEL  — sahip bu host'ta VAR ve adı kanonik depolama adı (storage(base)
//	          == view): bulgu değil.
//	KALINTI — sahip bu host'ta VAR, kanonik katalogda çözülüyor, küme kipi ve
//	          storage(base) != view: terfi öncesi çıplak MV.
//	ÖKSÜZ   — HİÇBİR host'taki hiçbir MV bu uuid'yi adreslemiyor. Küme geneli
//	          kontrol ŞART: sahibi BAŞKA host'ta duran bir iç tablo burada
//	          öksüz DEĞİLDİR (o host onu GÜNCEL iç tablosu olarak kullanır;
//	          düşürmek Replicated ZK yolunu onun altından çeker).
//
// storage: s.mvStorageName. Tek düğümde çıplak ad ZATEN depolama adıdır →
// storage(base) == view → kalıntı sınıfı o kurulumda üretilemez; cluster
// bayrağı ikinci kemer.
//
// guarded: s.mvGuardedOff. Kaynak kolonu olmayan kurulumda boot o MV'yi
// BİLEREK kurmaz (operation_group_summary_5m / db_statement_summary_5m) —
// kanonik `_local` HİÇ doğmayacağı için "_local sağlıklı" kapısı asla
// geçmez. Bulgu görünür kalır ama DÜĞMESİZDİR (Blocked): eylem gösterip
// sonra 409 atmak, var olmayan bir düğmeye yollayan bir metin üretiyordu.
func mvLeftoversFromRows(rows []mvTableRow, cluster bool, storage func(string) string, guarded func(string) bool) []MVLeftover {
	owners := mvOwnersByHost(rows)
	referenced := map[string]bool{}
	for _, byInner := range owners {
		for inner := range byInner {
			referenced[inner] = true
		}
	}
	// Ters harita: host'ta hangi iç tablo KANONİK depolama adının hedefi.
	// Kalıntının yanında yaşayan `_local`'in boyutunu okumak için gerekir
	// (veri kapısı: kalıntı dolu + kanonik boş = temizlik veriyi yok eder).
	ownerInner := map[string]map[string]string{} // host → view → iç tablo
	for h, byInner := range owners {
		ownerInner[h] = map[string]string{}
		for inner, view := range byInner {
			ownerInner[h][view] = inner
		}
	}
	out := []MVLeftover{}
	for _, r := range rows {
		if !isInnerTable(r.Name) {
			continue
		}
		uuid := strings.TrimPrefix(r.Name, innerTablePrefix)
		view, has := owners[r.Host][r.Name]
		if !has {
			if referenced[r.Name] {
				// Sahibi başka host'ta: bu host'un derdi "eksik MV"dir ve o
				// kapsama kartının işi. Buradan DÜŞÜRÜLMEZ.
				continue
			}
			out = append(out, MVLeftover{Host: r.Host, Kind: MVLeftoverOrphan,
				Inner: r.Name, UUID: uuid, InnerEngine: r.Engine})
			continue
		}
		if !cluster {
			continue
		}
		base, ok := canonicalMVForObject(view)
		if !ok {
			continue // migrations/*.sql MV'si: kanonik depolama adı yok, yargılayamayız
		}
		st := storage(base)
		if st == view {
			continue // GÜNCEL
		}
		l := MVLeftover{Host: r.Host, Kind: MVLeftoverBare,
			Inner: r.Name, UUID: uuid, InnerEngine: r.Engine, View: view, Storage: st,
			StorageInner: ownerInner[r.Host][st]}
		if guarded != nil && guarded(base) {
			l.Blocked = leftoverGuardedReason(st)
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Inner < out[j].Inner
	})
	return out
}

// MVLeftovers — küme geneli (ya da tek node) artık listesi. Envanter
// mvInventory'den: MV kapsaması ve sarkan liste ile AYNI satırlar, üç kart
// üç farklı gerçek üretmesin.
func (s *Store) MVLeftovers(ctx context.Context) ([]MVLeftover, string, error) {
	rows, cluster, err := s.mvInventory(ctx)
	if err != nil {
		return nil, cluster, fmt.Errorf("system.tables: %w", err)
	}
	out := mvLeftoversFromRows(rows, s.clusterMode(), s.mvStorageName, s.mvGuardedOff)
	if len(out) == 0 {
		return out, cluster, nil
	}
	if cluster != "" {
		if addrs, _, aerr := s.resolveHostAddrs(ctx); aerr == nil {
			for i := range out {
				out[i].Addr = addrs[out[i].Host]
			}
		}
	}
	// LİSTEDE boyut SÜStür: okunamazsa bulgular kalır, modal "boyut
	// okunamadı" der. EYLEM tarafı aynı okumayı TAZE ve hata denetimli
	// yapar — sessiz bir 0, veri kapısını kandırırdı.
	if sizes, serr := s.innerTableSizes(ctx, cluster); serr == nil {
		for i := range out {
			if sz, ok := sizes[out[i].Host][out[i].Inner]; ok {
				out[i].Rows, out[i].Bytes = sz[0], sz[1]
			}
			if si := out[i].StorageInner; si != "" {
				if sz, ok := sizes[out[i].Host][si]; ok {
					out[i].StorageRows, out[i].StorageBytes = sz[0], sz[1]
				}
			}
		}
	}
	return out, cluster, nil
}

// innerTableSizes — host → `.inner_id.*` → {satır, disk baytı} (aktif parçalar).
func (s *Store) innerTableSizes(ctx context.Context, cluster string) (map[string]map[string][2]uint64, error) {
	src := "system.parts"
	if cluster != "" {
		src = fmt.Sprintf("clusterAllReplicas('%s', system.parts)", cluster)
	}
	rows, err := s.conn.Query(ctx, `
		SELECT hostName(), table, toUInt64(sum(rows)), toUInt64(sum(bytes_on_disk))
		FROM `+src+`
		WHERE database = currentDatabase() AND active AND startsWith(table, '`+innerTablePrefix+`')
		GROUP BY hostName(), table
		SETTINGS max_execution_time = 15`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string][2]uint64{}
	for rows.Next() {
		var host, table string
		var n, b uint64
		if err := rows.Scan(&host, &table, &n, &b); err != nil {
			return nil, err
		}
		if out[host] == nil {
			out[host] = map[string][2]uint64{}
		}
		out[host][table] = [2]uint64{n, b}
	}
	return out, rows.Err()
}

// mvUUIDRe — 36 karakterlik onaltılık/çizgi uuid. İstemciden `.inner_id.…`
// ADI ASLA kabul edilmez (chObjRe noktayı reddeder, iç tablo adı ondan
// geçemez): istemci uuid verir, ad SUNUCUDA kurulur.
var mvUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// mvLeftoverStepTimeout — adım başına tavan (mv_coverage.go ile aynı).
const mvLeftoverStepTimeout = 2 * time.Minute

// DropLeftoverMV — terfi öncesi çıplak MV'yi YALNIZ o host'ta TAMAMLAR:
// MV'yi düşürür (kaskadla gizli iç tablosu da gider) ve AYNI adım listesinde,
// AYNI düğüm-yerel bağlantıda çıplak adı Distributed sarmalayıcı olarak geri
// kurar. Gerekçe leftoverDropStmts'te: tek başına DROP o adı host'ta yok
// eder ve is_local-kör bir host'ta bir daha hiç doğmaz.
//
// Kapılar TAZE ölçümle, hepsi SUNUCUDA (FE satırına güvenilmez — ölçümle tık
// arasında host onarılmış olabilir):
//  1. küme kipi — tek düğümde çıplak ad zaten kanonik depolama adıdır,
//     sınıf orada üretilemez;
//  2. storage(base) != view — kanonik depolama adını düşürmek MV'yi öldürür;
//  3. guarded MV değil — kanonik `_local` o kurulumda hiç doğmaz (Blocked);
//  4. Distributed sarmalayıcı ifadesi üretilebiliyor — üretilemezse eylem
//     REDDEDİLİR, yıkıcı yarı tek başına koşmaz;
//  5. nesnenin O HOST'TAKİ motoru MaterializedView — Distributed
//     sarmalayıcıya ya da gerçek bir tabloya asla dokunulmaz;
//  6. `<base>_local` O HOST'TA var ve kapsaması ok (varlık + motor);
//  7. VERİ kapısı — kalıntı dolu, kanonik boş ise REDDET (bu düğme veri
//     taşımaz). Boyutlar TAZE ve hata denetimli okunur.
func (s *Store) DropLeftoverMV(ctx context.Context, host, view string) ([]string, error) {
	host, view = strings.TrimSpace(host), strings.TrimSpace(view)
	if !chObjRe.MatchString(view) {
		return nil, fmt.Errorf("geçersiz nesne adı %q", view)
	}
	if !s.clusterMode() {
		return nil, fmt.Errorf("küme kipi değil — çıplak ad bu kurulumda kanonik depolama adıdır, kalıntı sınıfı YOK")
	}
	base, ok := canonicalMVForObject(view)
	if !ok {
		return nil, fmt.Errorf("%s kanonik katalogda yok (migrations/*.sql MV'si) — elle", view)
	}
	storage := s.mvStorageName(base)
	if storage == view {
		return nil, fmt.Errorf("%s bu kurulumda KANONİK depolama adı — kalıntı değil, düşürülmez", view)
	}
	if s.mvGuardedOff(base) {
		return nil, errors.New(leftoverGuardedReason(storage))
	}
	// Sarmalayıcı ifadesi ÖNCE: üretilemiyorsa hiçbir şey düşürmeyiz.
	wrapper := distributedWrapperStmt(s.adaptDDL(canonicalMVDDL(base)))
	if wrapper == "" {
		return nil, fmt.Errorf("%s için Distributed sarmalayıcı ifadesi üretilemedi — DROP tek başına çıplak adı bu host'ta yok ederdi (sonraki okumalar UNKNOWN_TABLE); elle", view)
	}
	list, cluster, err := s.MVLeftovers(ctx)
	if err != nil {
		return nil, fmt.Errorf("tespit: %w", err)
	}
	var row *MVLeftover
	for i := range list {
		if list[i].Host == host && list[i].View == view && list[i].Kind == MVLeftoverBare {
			row = &list[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("%s@%s şu an kalıntı değil — yeniden Ölç", view, host)
	}
	if row.Blocked != "" {
		return nil, errors.New(row.Blocked)
	}
	cov, _, cerr := s.MVCoverage(ctx)
	if cerr != nil {
		return nil, fmt.Errorf("kapsama: %w", cerr)
	}
	localOK := false
	for _, c := range cov {
		if c.Host == host && c.View == storage && c.State == MVStateOK {
			localOK = true
			break
		}
	}
	if !localOK {
		return nil, fmt.Errorf("%s bu host'ta sağlıklı değil — kalıntıyı düşürmek bu düğümün TEK toplamasını siler; önce MV onarımı ('Yeniden kur')", storage)
	}
	// VERİ kapısı (v0.10.830 incelemesi, MAJOR): kapsama `ok` yalnız VARLIK
	// ve motor ailesini ölçer — yeni kurulmuş boş bir `_local` de "ok"tur.
	// Boyutlar burada TAZE ve HATA DENETİMLİ okunur; sessiz bir 0 kapıyı
	// kandırırdı.
	sizes, serr := s.innerTableSizes(ctx, cluster)
	if serr != nil {
		return nil, fmt.Errorf("boyut okunamadı (%w) — veri kapısı ölçülmeden kalıntı düşürülmez", serr)
	}
	leftoverRows := sizes[host][row.Inner][0]
	storageInner := row.StorageInner
	if storageInner == "" {
		return nil, fmt.Errorf("%s bu host'ta hangi iç tabloya yazıyor çözülemedi — veri kapısı ölçülemedi", storage)
	}
	storageRows := sizes[host][storageInner][0]
	if msg := leftoverDataGate(view, storage, leftoverRows, storageRows); msg != "" {
		return nil, errors.New(msg)
	}
	if row.Addr == "" {
		return nil, fmt.Errorf("%s adresi system.clusters'tan çözülemedi", host)
	}
	conn, err := s.shardConn(ctx, row.Addr)
	if err != nil {
		return nil, fmt.Errorf("node bağlantısı: %w", err)
	}
	var engine string
	if err := conn.QueryRow(ctx, "SELECT engine FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", view).Scan(&engine); err != nil {
		return nil, fmt.Errorf("motor okunamadı (%s@%s): %w", view, host, err)
	}
	if engine != "MaterializedView" {
		return nil, fmt.Errorf("%s bu host'ta engine=%s — yalnız MaterializedView düşürülür (Distributed sarmalayıcıya/tabloya dokunulmaz)", view, engine)
	}
	stmts := leftoverDropStmts(view, wrapper)
	if len(stmts) != 2 {
		return nil, fmt.Errorf("%s: adım listesi kurulamadı (sarmalayıcı yok) — düşürme yok", view)
	}
	steps := append([]string{
		fmt.Sprintf("-- %s@%s · terfi öncesi çıplak MV; DROP kaskadla gizli iç tablosunu (%s, %d satır) götürür, sonra çıplak ad AYNI adımda Distributed sarmalayıcı olarak geri kurulur; %s (%d satır) çalışmaya devam eder",
			view, host, row.Inner, leftoverRows, storage, storageRows),
	}, stmts...)
	for _, st := range stmts {
		ectx, cancel := context.WithTimeout(ctx, mvLeftoverStepTimeout)
		err = conn.Exec(ectx, st)
		cancel()
		if err != nil {
			return steps, fmt.Errorf("%s: %w", firstWords(st, 4), err)
		}
	}
	// Doğrulama: gizli iç tablo GİTTİ ve çıplak ad artık Distributed.
	var innerLeft uint64
	var bareEngine string
	if err := conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", row.Inner).Scan(&innerLeft); err != nil {
		steps = append(steps, "-- uyarı: doğrulama okunamadı ("+err.Error()+") — adımlar koştu, kartı yeniden Ölç")
		return steps, nil
	}
	if innerLeft != 0 {
		return steps, fmt.Errorf("doğrulama: %s hâlâ duruyor", row.Inner)
	}
	if err := conn.QueryRow(ctx, "SELECT engine FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", view).Scan(&bareEngine); err != nil {
		steps = append(steps, "-- uyarı: sarmalayıcı doğrulaması okunamadı ("+err.Error()+") — adımlar koştu, kartı yeniden Ölç")
		return steps, nil
	}
	if bareEngine != "Distributed" {
		return steps, fmt.Errorf("doğrulama: %s bu host'ta engine=%q — Distributed sarmalayıcı doğmadı, ad okunamaz durumda (elle: sarmalayıcıyı kur)", view, bareEngine)
	}
	steps = append(steps, "-- doğrulandı: gizli iç tablo yok, çıplak ad Distributed sarmalayıcı")
	return steps, nil
}

// DropOrphanInner — sahipsiz iç tabloyu YALNIZ o host'ta düşürür.
//
// Kapılar TAZE ölçümle:
//  1. uuid 36 karakter onaltılık — ad sunucuda kurulur, istemci adı veremez;
//  2. KÜME GENELİNDE sıfır referans (MVLeftovers, skip_unavailable_shards
//     YOK) — sahibi başka host'ta duran bir uuid öksüz değildir;
//  3. hiçbir Distributed tablonun DDL'i bu adı anmıyor;
//  4. motor Replicated* ise: bu host o ZK yolunun SON kayıtlı replikası
//     OLMAMALI — son replikayı düşürmek yolu siler ve aynı uuid'yi GÜNCEL
//     iç tablosu olarak kullanan bir host'un altını boşaltır.
func (s *Store) DropOrphanInner(ctx context.Context, host, uuid string) ([]string, error) {
	host, uuid = strings.TrimSpace(host), strings.ToLower(strings.TrimSpace(uuid))
	if !mvUUIDRe.MatchString(uuid) {
		return nil, fmt.Errorf("geçersiz uuid %q", uuid)
	}
	inner := innerTablePrefix + uuid // ad SUNUCUDA kurulur
	list, cluster, err := s.MVLeftovers(ctx)
	if err != nil {
		return nil, fmt.Errorf("tespit: %w", err)
	}
	var row *MVLeftover
	for i := range list {
		if list[i].Host == host && list[i].Inner == inner && list[i].Kind == MVLeftoverOrphan {
			row = &list[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("%s@%s şu an öksüz değil (bir MV onu adresliyor ya da host ölçüme girmedi) — yeniden Ölç", inner, host)
	}
	refs, rerr := s.distributedRefs(ctx, cluster, inner)
	if rerr != nil {
		return nil, fmt.Errorf("sarmalayıcı (Distributed) denetimi: %w", rerr)
	}
	if refs > 0 {
		return nil, fmt.Errorf("%s bir Distributed tablonun DDL'inde geçiyor (%d satır) — düşürülmez", inner, refs)
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
	if strings.HasPrefix(row.InnerEngine, "Replicated") {
		var zk string
		var total uint32
		if err := conn.QueryRow(ctx, "SELECT zookeeper_path, toUInt32(total_replicas) FROM system.replicas WHERE database = currentDatabase() AND table = ? SETTINGS max_execution_time = 10", inner).Scan(&zk, &total); err != nil {
			return nil, fmt.Errorf("%s Replicated ama system.replicas satırı okunamadı (%v) — son replika olup olmadığı bilinmeden düşürülmez", inner, err)
		}
		if total <= 1 {
			return nil, fmt.Errorf("%s bu ZK yolunun (%s) SON kayıtlı replikası — düşürmek yolu siler ve aynı uuid'yi GÜNCEL iç tablo olarak kullanan bir host'un altını boşaltır; elle", inner, zk)
		}
	}
	steps := []string{
		fmt.Sprintf("-- %s@%s · sahipsiz iç tablo (küme genelinde hiçbir MV bu uuid'yi adreslemiyor); yalnız bu host", inner, host),
		"DROP TABLE `" + inner + "` SYNC" + purgeGuard,
	}
	ectx, cancel := context.WithTimeout(ctx, mvLeftoverStepTimeout)
	err = conn.Exec(ectx, steps[1])
	cancel()
	if err != nil {
		return steps, fmt.Errorf("%s: %w", firstWords(steps[1], 4), err)
	}
	var n uint64
	if err := conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", inner).Scan(&n); err != nil {
		steps = append(steps, "-- uyarı: doğrulama okunamadı ("+err.Error()+") — DROP koştu, kartı yeniden Ölç")
		return steps, nil
	}
	if n != 0 {
		return steps, fmt.Errorf("doğrulama: %s hâlâ duruyor", inner)
	}
	steps = append(steps, "-- doğrulandı: iç tablo bu host'ta yok")
	return steps, nil
}

// distributedRefs — verilen adı DDL'inde anan Distributed tablo sayısı
// (küme geneli). Öksüz kapısının ikinci yarısı: bir sarmalayıcı hâlâ o adı
// işaret ediyorsa tablo sahipsiz DEĞİLDİR.
func (s *Store) distributedRefs(ctx context.Context, cluster, name string) (uint64, error) {
	src := "system.tables"
	if cluster != "" {
		src = fmt.Sprintf("clusterAllReplicas('%s', system.tables)", cluster)
	}
	var n uint64
	err := s.conn.QueryRow(ctx, `
		SELECT count() FROM `+src+`
		WHERE database = currentDatabase() AND engine = 'Distributed'
		  AND position(create_table_query, ?) > 0
		SETTINGS max_execution_time = 10`, name).Scan(&n)
	return n, err
}
