package chstore

// mv_target_repair.go — v0.10.835: v0.10.833'ün SAPTADIĞI "ad doğru, NESNE
// uuid'si yanlış" arızasının ONARIMI. 833 yalnız ölçüyordu ve runbook'u elle
// altı adımdı; bu sürüm o merdiveni ürünün içine alır — ama YALNIZ ŞEKİL-1
// için.
//
// İKİ ŞEKİL, TEK ONARIM (gerçek CH 24.8'de ölçüldü, v0.10.833):
//
//	ŞEKİL-1 (TargetResolves == false) — MV'nin hedeflediği NESNE uuid'si
//	    HİÇBİR tabloya çözülmüyor. Düğüm-yerel `SELECT 1 FROM <view> LIMIT 0`
//	    kod 60 (UNKNOWN_TABLE) veriyor; INSERT kaskadı bu host'ta düşüyor ve
//	    Distributed spool büyüyor. ONARIM BURADA.
//	ŞEKİL-2 (TargetResolves == true) — hedef uuid BAŞKA ADLI var olan bir
//	    tabloya çözülüyor. MV çalışıyor, TOPLUYOR, ingest ayakta;
//	    `.inner_id.<view uuid>` adındaki tablo ölü kopya OLABİLİR. Burada
//	    yıkıcı eylem YASAK — 833 o satırda bütün düğmeleri zaten kapatıyor.
//
// Bu yüzden uygunluk kapısı FAIL-CLOSED ve ÜÇ HÂLLİ bir işaretçiye bakar:
// `nil` (ölçülmedi) ASLA yeterli değildir, `true` KESİN REDDİR. "Bilmiyoruz"
// ile "bozuk" aynı kovaya düşerse bu düğme CANLI bir toplamayı siler.
//
// AYNALI KURAL TEK GÖVDE ister (v0.9.1358): iç tabloyu eşten kuran merdiven
// v0.10.832'de zaten yazıldı ve sözleşmeyi taşıyor — eşin DDL'i +
// injectTableUUID(ddl, ad, NESNE uuid'si) + doğrulama uuid KOLONUNDAN +
// verifyPeerZKPath. Bu dosya İKİNCİ bir merdiven yazmaz, `repairInnerOn`u
// ÇAĞIRIR; kendine yalnız 833'ün ölçtüğü şekle özgü iki şeyi ekler:
//
//	(a) ADI TUTAN TABLO kapısı — şekil-1'de `.inner_id.<view uuid>` adında
//	    bir tablo ZATEN vardır (kendi uuid'siyle; MV ona yazmaz). O tablo
//	    MV'nin YAZMADIĞI bir nesnedir ama içeriği VERİDİR: doluysa eylem
//	    REDDEDİLİR, boşsa DROP AYRI ve AÇIKÇA onaylanan bir adımdır
//	    (dropEmpty). Ölçülemiyorsa da reddedilir — ölçmeden hiçbir şey
//	    düşürülmez ([[feedback-bug-repro-discipline]] duruşu).
//	(b) İKİ ŞARTLI doğrulama — metadata eşitliği DAVRANIŞI kanıtlamaz.
//	    Kurulumdan sonra hem hedef uuid BEKLENEN ADA çözülmeli HEM düğüm-yerel
//	    okuma BAŞARILI olmalı. İkisi birden tutmazsa sonuç "YARIM"dır ve yeşil
//	    denmez.
//
// v0.10.835 inceleme turu (çelişkili okuma) üç şeyi değiştirdi ve üçü de
// "ölçüm yalan söyleyebilir" ailesinden:
//
//	1. "BOŞ" TEK BİR SORGUYLA KANITLANMAZ. `system.parts` DETACHED parçaları
//	   GÖSTERMEZ ve MergeTree olmayan bir motor orada HİÇ satır üretmez;
//	   ikisinde de aggregate 0 döner ve DROP tablo dizinini `detached/` ile
//	   birlikte götürürdü. Artık motor + detached sayısı da sorulur ve
//	   ikisinde de RET. Ayrıca adı tutan tablonun KENDİ uuid'si BAŞKA bir
//	   MV'nin ölçülmüş hedefiyse (833'ün `MVTargetSet`i bunu zaten biliyor)
//	   RET; küme ölçülmediyse de RET.
//	2. purgeGuard BU DROP'TAN ÇIKARILDI. `max_table_size_to_drop = 0` sınırı
//	   KALDIRIR; gerçekten boş bir tablo varsayılan eşiğe zaten takılmaz, yani
//	   o eşik SADECE ölçüm yanlışken ısırır — tam da bu kapının tek hata
//	   modunda. CH'nin reddi burada istenen şeydir, engel değil.
//	3. YIKICI ADIM ÖN-DENETLENEBİLİR OKUMALARIN ARDINDA. Eşten kurulumun ilk
//	   üç işi saf okumadır (prepareInnerFromPeer); eskiden DROP onlardan önce
//	   koşuyor ve okuma düşünce ad boş kalıyordu.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// mvTargetRepairEligible — SAF KAPI: bu hücre hedef-uuid onarımı alır mı.
// "" = uygun, dolu metin = operatör diliyle RET sebebi (FE düğmeyi baştan
// çizmez, sunucu ikinci katman olarak aynı metni döner).
//
// SIRA ÖNEMLİ: önce ÖLÇÜM (üç hâl), sonra şekil. Ölçülmemiş bir satırda
// "hangi şekil" sorusunun cevabı yoktur ve kapının en kolay bozulma biçimi
// `nil`i "uygun" saymaktır — v0.10.833'ün TargetResolves'i tam olarak bu
// ayrımı taşısın diye işaretçidir.
func mvTargetRepairEligible(c *MVHostState, guarded bool) string {
	if c == nil {
		return "kapsama raporunda satırı yok — kartı yeniden Ölç"
	}
	if guarded {
		// Boot bu MV'yi BİLEREK kurmuyor (kaynak kolonu yok, v0.8.186): onu
		// kurmak insert-trigger'ı kod 16 ile düşürür ve TÜM ingest'i bloklar.
		//
		// BUGÜN ULAŞILAMAZ ve asıl sözleşme budur (v0.10.835 incelemesi):
		// mvCoverageReport kanonik listeyi kurarken guarded adları ZATEN
		// eliyor, yani guarded bir MV için hiç HÜCRE doğmaz ve çağıran daha
		// önce "kapsama raporunda yok" der. Kol ikinci katman olarak duruyor
		// — kapsama bir gün guarded satır üretirse yıkıcı eylem varsayılan
		// olarak KAPALI olsun diye. Asıl sözleşme
		// TestMVCoverageNeverEmitsGuardedRows ile çivili.
		return "bu MV bu kurulumda bilerek kurulmuyor (kaynak kolonu yok) — hedef uuid onarımı burada TANIMSIZ"
	}
	switch r := storageResolves(c); {
	case r == nil:
		return "hedefin çözülüp çözülmediği ÖLÇÜLMEDİ — ölçülmemiş bir satırda onarım koşmaz (düğüm-yerel okuma kapağa/bütçeye takılmış ya da adres çözülememiş olabilir); kartı yeniden Ölç"
	case *r:
		return "MV hedefini ÇÖZÜYOR: başka adlı var olan bir nesneye yazıyor ve TOPLUYOR — bu satırda onarım CANLI veriyi siler, eylem KAPALI (ölü kopya temizliği için karttaki runbook)"
	}
	if c.Target != MVTargetMismatch {
		why := fmt.Sprintf("hedef kararı %q — bu onarım YALNIZ `mismatch` şeklini (ad doğru, NESNE uuid'si yanlış) kapatır", c.Target)
		if c.State == MVStateDangling {
			why += "; burada `.inner_id.<view uuid>` adı HİÇ yok, o satırın onarımı kartın 'Eşten kur' / 'Yeniden kur' düğmesidir"
		}
		return why
	}
	// SATIR BAŞINA TEK EYLEM (v0.10.825 duruşu, 835 incelemesi): kapsaması
	// `ok` OLMAYAN bir hücre onarım tablosunda ZATEN bir yıkıcı düğme taşır
	// ("Yeniden kur"; `plain + mismatch` hücresi tam olarak bu). Aynı satıra
	// ikinci bir yıkıcı düğme koymak operatöre iki farklı söz veren iki yol
	// sunar ve hangisinin koştuğu audit'ten okunur hâle gelir.
	if c.State != MVStateOK {
		return fmt.Sprintf("kapsama durumu %q — bu satırın onarımı üstteki tabloda ('Eşten kur' / 'Yeniden kur') ve o yol hedef uuid'sini de düzeltir; satır başına TEK yıkıcı eylem", c.State)
	}
	if !validUUID(c.TargetUUID) {
		return "MV'nin hedef uuid'si okunamadı — kurulacak nesnenin uuid'si TAHMİN EDİLEMEZ (adın uuid'sine düşmek v0.10.780'in hatasıdır)"
	}
	return ""
}

// chMergeTreeFamily — SAF: bu motor parça (part) tutan bir MergeTree ailesi
// mi. `Replicated*MergeTree` de kapsanır.
//
// v0.10.835: "system.parts 0 satır verdi" ile "tablo boş" AYNI ŞEY DEĞİL.
// Log/StripeLog/Memory/Set/Buffer o tabloda HİÇ parça üretmez ve aggregate
// yine 0 döner; o adı tutan şey combined bir MV'nin iç tablosu değilse
// doluluğunu bu yoldan ölçemeyiz ve düşürmeyiz.
func chMergeTreeFamily(engine string) bool { return strings.Contains(engine, "MergeTree") }

// innerNameState — `.inner_id.<view uuid>` ADINI tutan nesnenin DÜĞÜM-YEREL
// ölçümü. Tek bir "satır sayısı" yetmiyor (v0.10.835 incelemesi): motor da,
// DETACHED parçalar da, nesnenin KENDİ uuid'si de kararın parçası.
type innerNameState struct {
	Exists   bool
	Engine   string // motor ailesi kararı (parça tutmayan motorda "0 satır" YALAN)
	UUID     string // o adı tutan nesnenin KENDİ uuid'si (TAZE)
	Rows     uint64 // aktif parçalardaki satır
	Detached uint64 // system.parts'ta GÖRÜNMEYEN parçalar
}

// mvTargetOccupancyGate — SAF: `.inner_id.<view uuid>` adını TUTAN tablo için
// karar. "" = DROP edilebilir.
//
// needsName — bu dal o ADI gerçekten kullanacak mı. YALNIZ eşten kurulum
// kullanır. Kanonik dal view'ı düşürüp yeniden kurar, view YENİ bir uuid alır
// ve iç tablonun adı ONDAN doğar (verifyMVTargetRepair'in okuduğu ad da
// odur) — yani o dalda eski ad HİÇ kullanılmaz, "düşürmezsek kod 57 alırız"
// gerekçesi YANLIŞTIR ve dolu bir ad yüzünden ingest'i düşmüş bir host'u
// onarımsız bırakmak GEREKSİZDİR. Eski ad o dalda öksüz kalır ve v0.10.830'un
// "sahipsiz iç tablo" satırı olarak karta düşer — doğru sahibi orasıdır.
//
// Kollar (hepsi RET, sırası mesaj kalitesi için): motor ailesi → detached →
// dolu → kendi uuid'si okunamadı → hedef kümesi ölçülmedi → BAŞKA bir MV'nin
// hedefi → onay yok. Doluyken dropEmpty kapıyı AÇMAZ: "boş" ölçülmüş bir
// olgudur, onay bir niyettir.
func mvTargetOccupancyGate(inner string, needsName bool, st innerNameState, dropEmpty bool, targets MVTargetSet) string {
	if !needsName || !st.Exists {
		return ""
	}
	if !chMergeTreeFamily(st.Engine) {
		return fmt.Sprintf("`%s` adını %s motorlu bir nesne tutuyor — o motor parça (part) üretmez, yani \"0 satır\" doluluk KANITI DEĞİLDİR ve bu ad combined bir MV'nin iç tablosu değildir. Eylem reddedildi; nesneyi elle incele", inner, st.Engine)
	}
	if st.Detached > 0 {
		return fmt.Sprintf("`%s` %d DETACHED parça taşıyor — `system.parts` onları GÖSTERMEZ, yani aktif satır sayısı 0 olsa da tabloda VERİ vardır (karantinaya alınmış parça ya da elle ayrılmış tarihçe) ve DROP tablo dizinini `detached/` ile birlikte siler. Önce `system.detached_parts`'ı incele; geri dönüşü yok", inner, st.Detached)
	}
	if st.Rows > 0 {
		return fmt.Sprintf("`%s` duruyor ve %d satır taşıyor — MV bu tabloya YAZMIYOR ama içeriği VERİDİR. Önce bu tablonun verisiyle ne yapılacağına karar ver (RENAME ile kenara al, gerekiyorsa MV'nin gerçek hedefine taşı), sonra onarımı tekrarla; bu düğme veri TAŞIMAZ", inner, st.Rows)
	}
	// Boş görünen tablo BAŞKA bir MV'nin CANLI hedefi olabilir: 833'ün probe'u
	// küme genelindeki `to_inner_uuid` kümesini zaten okuyor. Bilinmiyorsa da
	// reddederiz — "ölçemedik" bu kapıda "uygun" demek değildir.
	if !validUUID(st.UUID) {
		return fmt.Sprintf("`%s` duruyor ama KENDİ nesne uuid'si okunamadı — bu nesnenin bir MV'nin hedefi olup olmadığı kanıtlanamıyor, eylem reddedildi", inner)
	}
	if !targets.Measured {
		return fmt.Sprintf("hedef uuid kümesi ÖLÇÜLEMEDİ — `%s` başka bir MV'nin canlı hedefi olabilir ve bunu çürütemiyoruz; kartı yeniden Ölç", inner)
	}
	if targets.Has(st.UUID) {
		return fmt.Sprintf("`%s` boş görünüyor ama KENDİ nesne uuid'si (%s) kümede BİR MV'NİN HEDEFİ — o MV bu nesneye yazıyor; düşürmek onun toplamasını götürür", inner, st.UUID)
	}
	if !dropEmpty {
		return fmt.Sprintf("`%s` duruyor (0 satır, 0 detached parça ölçüldü) ve doğru nesne uuid'siyle kurulacak tablo tam olarak bu ADI ister — düşürülmeden kurulum kod 57 (TABLE_ALREADY_EXISTS) alır. DROP AYRI bir onaydır: modaldaki 'boş adı düşür' kutusunu işaretle", inner)
	}
	return ""
}

// innerTableStateOn — DÜĞÜM-YEREL ölçüm (SALT OKUMA). Hata YUTULMAZ:
// okunamayan bir sayım 0 sayılırsa dolu bir tablo "boş" görünür ve tek yıkıcı
// adımın bütün dayanağı çöker ([[feedback-empty-set-vanishes-not-zero]]
// sınıfının yıkıcı hâli).
//
// ÜÇ okuma, üçü de ayrı bir körlüğü kapatıyor: (1) varlık + motor + nesne
// uuid'si tek aggregate'te (satır yoksa da bir satır döner, "bulunamadı"
// hatası ile "tablo yok" karışmaz); (2) aktif parçalardaki satır; (3)
// DETACHED parçalar — `system.parts` onları HİÇ göstermez.
//
// Sayısal kolonlar SQL'de sabitlenir (toUInt64): system.* kolon tipleri
// sürümle değişiyor ve çıplak tarama sağlam bir okumayı hataya çeviriyordu.
func (s *Store) innerTableStateOn(ctx context.Context, conn driver.Conn, inner string) (innerNameState, error) {
	var st innerNameState
	var n uint64
	if err := conn.QueryRow(ctx, "SELECT toUInt64(count()), any(toString(uuid)), any(engine) FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", inner).Scan(&n, &st.UUID, &st.Engine); err != nil {
		return st, err
	}
	if n == 0 {
		return st, nil
	}
	st.Exists = true
	if err := conn.QueryRow(ctx, "SELECT toUInt64(sum(rows)) FROM system.parts WHERE database = currentDatabase() AND table = ? AND active SETTINGS max_execution_time = 10", inner).Scan(&st.Rows); err != nil {
		return st, err
	}
	if err := conn.QueryRow(ctx, "SELECT toUInt64(count()) FROM system.detached_parts WHERE database = currentDatabase() AND table = ? SETTINGS max_execution_time = 10", inner).Scan(&st.Detached); err != nil {
		return st, err
	}
	return st, nil
}

// RepairMVTargetOnHost — ŞEKİL-1'in onarımı, YALNIZ o host'ta (ON CLUSTER
// YOK). Durum TAZE ölçülür ve dokunulacak satır DÜĞÜM-YEREL olarak
// sertleştirilir: FE satırına güvenilmez, ölçümle tık arasında host onarılmış
// ya da şekil değişmiş olabilir.
//
// wantPeer — operatörün EKRANDA gördüğü dal (v0.10.825 duruşu): söz
// tutulamıyorsa SESSİZCE öteki dala DÜŞMEYİZ. İki yön de korunur:
//   - "eşten" yazıyordu ama eş çözülemiyor → RET (tarihçe onaysız yanmaz);
//   - "kanonik (tarihçe sıfır)" yazıyordu ama artık sağlam bir eş VAR → RET
//     (daha yıkıcı olanı sessizce koşmak da bir sürprizdir).
func (s *Store) RepairMVTargetOnHost(ctx context.Context, host, view string, wantPeer, dropEmpty bool) ([]string, error) {
	host, view = strings.TrimSpace(host), strings.TrimSpace(view)
	if !chObjRe.MatchString(view) {
		return nil, fmt.Errorf("geçersiz nesne adı %q", view)
	}
	// Süpürme ATLANIR (harden=false): 126 hücrelik tarama tıklama başına iki
	// kez ödenmez — gereken tek ölçüm DOKUNULACAK satırındır (v0.10.833 D).
	rep, err := s.mvCoverageReport(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("tespit: %w", err)
	}
	var row *MVHostState
	for i := range rep.Rows {
		if rep.Rows[i].Host == host && rep.Rows[i].View == view {
			row = &rep.Rows[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("%s/%s kapsama raporunda yok — yeniden Ölç", host, view)
	}
	s.mvHardenCell(ctx, row, rep.Cluster)
	guarded := false
	if base, ok := canonicalMVForObject(view); ok {
		guarded = s.mvGuardedOff(base)
	}
	if why := mvTargetRepairEligible(row, guarded); why != "" {
		return nil, fmt.Errorf("%s/%s: %s", host, view, why)
	}
	conn := s.conn
	if rep.Cluster != "" {
		// Düğüm-yerel iş: adresi çözülemeyen host'ta eylem YOK.
		if row.Addr == "" {
			return nil, fmt.Errorf("%s adresi system.clusters'tan çözülemedi — bu onarım YALNIZ o düğümde koşar", host)
		}
		c, cerr := s.shardConn(ctx, row.Addr)
		if cerr != nil {
			return nil, fmt.Errorf("node bağlantısı: %w", cerr)
		}
		conn = c
	}
	// Eş bağlantısı BURADA çözülür (repairInnerFromPeer yerine): dal reddi
	// bağlantıdan ÖNCE verilmeli ve repairMVTargetOn'un iki bağlantıyı da
	// PARAMETRE alması davranışsal testin tek yolu (v0.10.832 dersi: saf
	// çekirdek yeşil, çağrıldığı yer pinsizse düzeltme kendini iptal eder).
	// Onarım MERDİVENİ yine tek gövdedir: repairInnerOn.
	var peer driver.Conn
	switch {
	case wantPeer && (rep.Cluster == "" || row.PeerAddr == ""):
		return nil, fmt.Errorf("%s/%s: aynı shard'da iç tablosu Replicated bir eş ÇÖZÜLEMEDİ — ekranda 'Eşten onar' yazıyordu, sessizce tarihçesiz kanonik kuruluma düşmeyiz; kartı yeniden Ölç", host, view)
	case wantPeer:
		p, perr := s.shardConn(ctx, row.PeerAddr)
		if perr != nil {
			return nil, fmt.Errorf("eş replika bağlantısı (%s): %w", row.PeerHost, perr)
		}
		peer = p
	case rep.Cluster != "" && row.PeerAddr != "":
		// Mesaj VAR OLAN bir düğmeyi işaret eder: kartta tek düğme var
		// ("Hedefi onar") ve dalı kendisi seçer (v0.10.830 dersi — var
		// olmayan bir düğmeye yollamak operatörü kilitler).
		return nil, fmt.Errorf("%s/%s: ekranda tarihçesiz KANONİK kurulum yazıyordu ama artık aynı shard'da iç tablosu Replicated bir eş var (%s) — daha yıkıcı olanı sessizce koşmayız. Kartı yeniden Ölç; 'Hedefi onar' bu kez eşten kurulum dalını seçer ve tarihçe korunur", host, view, row.PeerHost)
	}
	return s.repairMVTargetOn(ctx, conn, peer, row, dropEmpty, rep.Targets)
}

// repairMVTargetOn — onarımın BAĞLANTIYLA konuşan yarısı; iki bağlantı da
// PARAMETRE (bkz. yukarıdaki gerekçe). peer == nil → kanonik dal.
//
// Sıra (eşten dalı): ölç → kapı → HAZIRLA → adı boşalt → uygula → İKİ ŞARTLI
// doğrula. Yıkıcı adım ön-denetlenebilir HER okumanın ardındadır. Adım listesi
// hata yolunda da döner (audit + ekran): yarım kalan bir onarımın nerede
// durduğu operatörün tek kanıtıdır.
//
// targets — 833'ün küme geneli `to_inner_uuid` kümesi; adı tutan boş tablonun
// başka bir MV'nin canlı hedefi olup olmadığını ancak o söyler.
func (s *Store) repairMVTargetOn(ctx context.Context, conn, peer driver.Conn, row *MVHostState, dropEmpty bool, targets MVTargetSet) ([]string, error) {
	if !validUUID(row.UUID) {
		return nil, fmt.Errorf("%s@%s: view'ın uuid'si okunamadı/sıfır — Ordinary DB'de iç tablo `.inner.<ad>` olarak yaşar ve bu onarım o kipte TANIMLI DEĞİL", row.View, row.Host)
	}
	// Ad DAİMA VIEW uuid'sinden doğar (v0.10.832, innerTableName); nesne
	// uuid'si AYRI bir değerdir ve repairInnerOn onu MV'nin kendi
	// metadata'sından okur.
	inner := innerTableName(row.UUID)
	// needsName — YALNIZ eşten kurulum `.inner_id.<ESKİ view uuid>` adını
	// kullanır. Kanonik dal view'ı yeniden kurar, yeni uuid yeni ad doğurur:
	// o dalda ne ölçüm ne DROP gerekir (bkz. mvTargetOccupancyGate).
	needsName := peer != nil
	var steps []string
	if !needsName {
		// Kanonik dal: tarihçe bu host'ta SIFIRLANIR (modal açıkça söyler).
		// Eski ad öksüz kalır ve v0.10.830'un "sahipsiz iç tablo" satırı
		// olarak karta düşer — doğru sahibi orasıdır, burada DOKUNULMAZ.
		name, ok := canonicalMVForObject(row.View)
		if !ok {
			return nil, fmt.Errorf("%s için kanonik DDL yok (migrations/*.sql MV'si) ve Replicated eş de bulunamadı — elle onar", row.View)
		}
		st, rerr := s.rebuildMVOnConn(ctx, conn, row.View, name)
		steps = append(steps, st...)
		if rerr != nil {
			return steps, rerr
		}
		return s.verifyMVTargetRepair(ctx, conn, row.View, steps)
	}
	// 1. ÖLÇ (düğüm-yerel, salt okuma).
	st, err := s.innerTableStateOn(ctx, conn, inner)
	if err != nil {
		return nil, fmt.Errorf("`%s` ölçülemedi (%v) — ÖLÇMEDEN hiçbir şey düşürülmez, eylem reddedildi", inner, err)
	}
	// 2. KAPI (saf).
	if why := mvTargetOccupancyGate(inner, needsName, st, dropEmpty, targets); why != "" {
		return nil, errors.New(why)
	}
	// 3. HAZIRLA (salt okuma; eşe bağlanır). YIKICI ADIMDAN ÖNCE: eşin
	// uuid'si tutmazsa ya da okuma düşerse hiçbir şey değişmemiş olur.
	// TEK GÖVDE: merdiven v0.10.832'nin sözleşmesini taşır.
	dm := &DanglingMV{
		Host: row.Host, Addr: row.Addr, View: row.View, UUID: row.UUID,
		PeerHost: row.PeerHost, PeerAddr: row.PeerAddr, Canonical: true,
	}
	plan, perr := s.prepareInnerFromPeer(ctx, conn, peer, dm)
	if perr != nil {
		return nil, perr
	}
	// 4. YIKICI ADIM — artık yalnızca KESİN kurulabilecek bir CREATE'in
	// önünde. purgeGuard BİLEREK YOK (v0.10.835 incelemesi): o ayar
	// max_table_size_to_drop sınırını KALDIRIR ve gerçekten boş bir tablo o
	// eşiğe zaten takılmaz — eşik SADECE ölçümümüz yanlışken ısırır, yani tam
	// da istediğimiz fren. Takılırsa DROP koşmaz ve doğru olan budur.
	if st.Exists {
		drop := "DROP TABLE IF EXISTS `" + inner + "` SYNC"
		steps = append(steps, fmt.Sprintf("-- v0.10.835: `%s` 0 satır + 0 detached parça ölçüldü, motoru %s ve hiçbir MV'nin hedefi değil — ad boşaltılıyor (operatör ayrıca onayladı)", inner, st.Engine), drop)
		ectx, cancel := context.WithTimeout(ctx, mvRebuildStepTimeout)
		derr := conn.Exec(ectx, drop)
		cancel()
		if derr != nil {
			return steps, fmt.Errorf("boş iç tablo düşürülemedi: %w — max_table_size_to_drop'a takıldıysa ÖLÇÜM YANLIŞTIR (tabloda veri var), sınırı kaldırma", derr)
		}
	}
	// 5. UYGULA.
	ast, aerr := s.applyInnerFromPeer(ctx, conn, peer, dm, plan)
	steps = append(steps, ast...)
	if aerr != nil {
		return steps, aerr
	}
	return s.verifyMVTargetRepair(ctx, conn, row.View, steps)
}

// verifyMVTargetRepair — İKİ ŞART, ikisi de tutmazsa "YARIM".
//
//  1. METADATA, TERS YÖNDEN — MV'nin hedef uuid'si HANGİ ADA çözülüyor?
//     Beklenen ad `.inner_id.<view uuid>` olmalı. Merdiven kurduğu tabloyu
//     ADIYLA arayıp uuid'sini doğruluyordu; aynı yönü burada tekrar okumak
//     merdivenin KENDİ İDDİASININ tekrarı olurdu (v0.10.835 incelemesi). Bu
//     okuma ters soruyu sorar — uuid → ad — ve "hedef HİÇBİR şeye çözülmüyor"
//     (şekil-1 sürüyor) ile "BAŞKA ada çözülüyor" (şekil-2 doğdu) hâllerini
//     ayırt eder. (Kanonik dalda view yeniden doğduğu için hedef uuid de
//     yenidir; o yüzden istek anındaki değer değil ŞU ANKİ değer okunur —
//     iki dal tek doğrulama gövdesi kullanabilsin.)
//  2. DAVRANIŞ — `SELECT 1 FROM <view> LIMIT 0` artık BAŞARILI. Metadata
//     davranışı KANITLAMAZ: v0.10.833'te ölçüldü ki aynı uuid ilişkisinin iki
//     farklı gözlenebilir sonucu var.
//
// Doğrulama okuması düşerse de YARIM'dır: DDL koştu ama sonucu bilmiyoruz.
// v0.10.820'nin "okuma hatası ≠ iş başarısız" dersi TESPİT okumaları içindir;
// burada okuma doğrulamanın KENDİSİDİR ve yeşil demek yalan olurdu.
func (s *Store) verifyMVTargetRepair(ctx context.Context, conn driver.Conn, view string, steps []string) ([]string, error) {
	half := func(format string, a ...any) ([]string, error) {
		return steps, fmt.Errorf("YARIM — "+format, a...)
	}
	want, err := s.innerObjectUUIDOn(ctx, conn, view)
	if err != nil {
		return half("kurulum koştu ama MV'nin hedef uuid'si okunamadı (%v) — iki şarttan hiçbiri doğrulanamadı, kartı yeniden Ölç", err)
	}
	var viewUUID string
	if e := conn.QueryRow(ctx, "SELECT toString(uuid) FROM system.tables WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 10", view).Scan(&viewUUID); e != nil {
		return half("kurulum koştu ama view'ın uuid'si okunamadı (%v) — iç tablonun ADI ondan doğar, doğrulanamadı", e)
	}
	if !validUUID(viewUUID) {
		return half("view'ın uuid'si sıfır/okunamaz (Ordinary DB) — iç tablonun adı doğrulanamaz")
	}
	inner := innerTableName(viewUUID)
	// TERS YÖN: uuid → ad. any() boş kümede '' döner, yani "hiçbir tabloya
	// çözülmüyor" bir HATA değil bir CEVAPTIR ve öyle raporlanır.
	var resolved string
	if e := conn.QueryRow(ctx, "SELECT any(name) FROM system.tables WHERE database = currentDatabase() AND uuid = toUUID(?) SETTINGS max_execution_time = 10", string(want)).Scan(&resolved); e != nil {
		return half("1. şart ÖLÇÜLEMEDİ: MV'nin hedef uuid'sinin hangi tabloya çözüldüğü okunamadı (%v)", e)
	}
	if resolved == "" {
		return half("1. şart TUTMADI: MV %s hedefliyor ama o nesne uuid'si bu host'ta HİÇBİR tabloya çözülmüyor — şekil-1 sürüyor, ingest ayağa kalkmadı", want)
	}
	if !strings.EqualFold(resolved, inner) {
		return half("1. şart TUTMADI: MV'nin hedefi (%s) `%s` adlı BAŞKA bir tabloya çözülüyor, beklenen ad `%s` — bu bir şekil-2'dir: hiçbir şey düşürme, kartı yeniden Ölç", want, resolved, inner)
	}
	pctx, cancel := context.WithTimeout(ctx, mvTargetCheckTimeout)
	perr := conn.Exec(pctx, "SELECT 1 FROM `"+view+"` LIMIT 0 SETTINGS max_execution_time = 10")
	cancel()
	if perr != nil {
		return half("2. şart TUTMADI: düğüm-yerel okuma hâlâ düşüyor (%v) — uuid'ler tutuyor ama MV hedefini çözemiyor, ingest bu host'ta ayağa KALKMADI", perr)
	}
	return append(steps, fmt.Sprintf("-- doğrulandı (İKİ ŞART): MV'nin hedefi %s `%s` adına çözülüyor (beklenen ad) VE `SELECT 1 FROM %s LIMIT 0` başarılı", want, resolved, view)), nil
}
