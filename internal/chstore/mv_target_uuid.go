package chstore

// mv_target_uuid.go — v0.10.833: MV kapsama kartının GÖREMEDİĞİ hastalık —
// "ad doğru, NESNE uuid'si yanlış" ve onun aynası "nesne uuid'si doğru, ad
// beklenen değil".
//
// MV gizli iç tablosunu ADLA değil kendi metadata'sındaki NESNE uuid'siyle
// (`TO INNER UUID`) çözer. İki şekil de kartı yanıltıyordu:
//
//	(1) ad doğru (`.inner_id.<view uuid>`), nesne uuid'si BAŞKA → kapsama
//	    `ok` (yeşil) der; MV o tabloya YAZMAZ.
//	(2) nesne uuid'si DOĞRU ama tablonun adı beklenen değil → kapsama
//	    `dangling` (kırmızı) der ve CANLI, dolu, MV'nin gerçekten yazdığı
//	    tabloya İKİ danger düğme doğrultur ("Yeniden kur" → DROP … SYNC,
//	    "Öksüzü temizle"). Bu şekli v0.10.780–831 arası "Eşten kur" yolu
//	    üretiyordu; ingest çalıştığı için karşı sinyal YOK (spool büyümez).
//
// v0.10.832 öncesi FE runbook'u da operatöre iç tabloyu ADIN uuid'siyle
// kurdurtuyordu — şekilleri ÜRÜNÜN KENDİSİ öğretmiş olabilir; yedekten
// yükleme ve ATTACH de aynı sonucu verir.
//
// Bu sürüm YALNIZ SAPTAR: yeni yıkıcı düğme YOK, gerçek onarım ayrı sürüm.
// Yaptığı tek "koruma" eksiltici: hedefini ÇÖZEN bir satırda var olan yıkıcı
// düğmeleri KAPATIR.
//
// GERÇEK ÖLÇÜM (CH 24.8, inceleme turu): metadata uyuşmazlığının iki sonucu
// var ve ayrımı yalnız DAVRANIŞ verir — şekil-1'de `SELECT 1 FROM <mv>
// LIMIT 0` ve INSERT kod 60 ile düşer (ingest durur); şekil-2'de hedef BAŞKA
// ADLI var olan bir tabloya çözülür, SELECT de INSERT de BAŞARILI olur ve MV
// TOPLAR. "Uyuşmazlık = ingest düşüyor" varsayımı ölçülebilir şekilde
// yanlıştı; o yüzden sonuç serbest metin değil ALAN (TargetResolves).
//
// NEDEN AYRI DOSYA (yapısal): bu okuma
// show_table_uuid_in_table_create_query_if_not_nil'i 1 YAPAR, yani metin
// sınıflandırmanın (mv_coverage.go) beklediğinden BAŞKA biçimde gelir. Ayar=1
// metni sınıflandırmaya sızarsa v0.10.832'nin tam olarak düzelttiği hata geri
// gelir (her hücre `dangling`, "Yeniden kur" açık, DROP … SYNC canlı iç
// tabloyu götürür). İki kalkan:
//
//	(a) dosya ayrımı — mv_coverage.go `= 1` yazımından TEMİZ kalır; kapı
//	    (TestClassificationReadsPinTheUUIDSetting) PAKETİ tarar, tek dosyayı
//	    değil: okumayı başka dosyaya taşımak kapıyı yeşil geçmez;
//	(b) DÖNÜŞ TİPİ metin TAŞIMAZ — probe create_table_query'yi fonksiyon
//	    gövdesinde tüketir, dışarı yalnız uuid'ler + bir bayrak çıkar.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Hedef uuid kararı — DÖRT DEĞER. State (ok|plain|dangling|missing) ile
// KARIŞMAZ ve onu DEĞİŞTİRMEZ: State ADLA ilgili bir OLGUDUR
// (`.inner_id.<view uuid>` var mı, motoru ne), hedef uuid DİK bir sorudur.
// Ayrı tutmanın pratik sebebi FE'de: AdminClickhouse.tsx kapsamadan gelen
// `state !== 'ok'` HER hücreyi onarım satırına koyar ve DANGER düğme basar —
// CHMVState'e yeni bir değer eklemek "yıkıcı düğme yok" kararını SESSİZCE
// kırardı.
const (
	MVTargetOK         = "ok"         // iki uuid de okundu ve AYNI
	MVTargetMismatch   = "mismatch"   // iki uuid de okundu ve FARKLI → BULGU
	MVTargetByName     = "byname"     // to_inner_uuid Nil / TO'lu MV: hedef ADLA çözülür — bulgu DEĞİL
	MVTargetUnmeasured = "unmeasured" // ölçülemedi (probe koşmadı, kırpıldı, hata)
)

// chMVTargetProbeSettings — AYRI sabit: chInnerUUIDSettings'in tavanı 10 sn
// ve o TEK düğüme giden bir okuma içindir; bu okuma küme geneline yayılır ve
// envanterin 15'inden düşük bir tavan probe'u envanterden ÖNCE düşürürdü.
const chMVTargetProbeSettings = " SETTINGS show_table_uuid_in_table_create_query_if_not_nil = 1, max_execution_time = 15"

// reMVOwnUUID — MV'nin KENDİ uuid token'ı (`… MATERIALIZED VIEW db.x UUID '…'`).
// Varlığı tek işe yarar: ayarın BU HOST'TA uygulandığını kanıtlamak.
//
// NEDEN GEREKLİ: clusterAllReplicas + SETTINGS uzak düğümlere gönderilir ve
// orada uygulanır, AMA asimetrik — uzak (SECONDARY_QUERY) düğümde profil
// kısıtı varsa ayar SESSİZCE KIRPILIR (istisna atmaz), initiator'da kısıt
// varsa sorgunun TAMAMI hata verir. Yani bir host uuid'siz metin döndürebilir
// ve bu bir ARIZA DEĞİL, ÖLÇÜLEMEDİ'dir — host BAZINDA. Token hiç yoksa ayar
// uygulanmamıştır; token var ama `TO INNER UUID` yoksa ayar uygulanmış ve
// to_inner_uuid gerçekten Nil'dir.
var reMVOwnUUID = regexp.MustCompile(`(?is)^\s*(?:CREATE|ATTACH)(?:\s+OR\s+REPLACE)?\s+MATERIALIZED\s+VIEW\s+(?:IF\s+NOT\s+EXISTS\s+)?\S+\s+UUID\s+'[0-9a-fA-F-]{36}'`)

// mvUUIDTokenShown — SAF: ayar bu metinde UYGULANMIŞ mı.
func mvUUIDTokenShown(createQuery string) bool { return reMVOwnUUID.MatchString(createQuery) }

// mvTargetRead — BİR (host, view) ölçümü. METİN ALANI YOKTUR ve olmayacak
// (sızdırma yasağı, dosya başlığındaki (b)); TestMVTargetProbeCarriesNoDDLText
// alan KÜMESİNİ reflect ile çiviler.
type mvTargetRead struct {
	// Target — MV'nin `TO INNER UUID`'si; "" = metinde yok.
	Target innerObjectUUID
	// Shown — ayar bu host'ta uygulandı (metinde uuid token'ı var).
	Shown bool
}

// MVTargetSet — probe'un küme genelinde GÖRDÜĞÜ to_inner_uuid kümesi.
//
// Measured yoksa küme BOŞ DEĞİL, BİLİNMİYOR'dur: "hiçbir MV bu uuid'yi
// hedeflemiyor" ile "ölçemedik" aynı şey değildir ve ikincisinde öksüz
// kararı vermek CANLI bir iç tabloyu temizliğe gönderir
// ([[feedback-empty-set-vanishes-not-zero]] sınıfı).
//
// uuids unexported: harita dışarıdan doldurulamaz, yalnız probe üretir.
type MVTargetSet struct {
	Measured bool
	uuids    map[innerObjectUUID]bool
}

// Has — SAF: bu nesne uuid'si bir MV'nin ÖLÇÜLMÜŞ hedefi mi.
func (s MVTargetSet) Has(u string) bool {
	if !s.Measured {
		return false
	}
	return s.uuids[innerObjectUUID(strings.ToLower(strings.TrimSpace(u)))]
}

// mvTargetProbe — probe'un TÜM çıktısı. Err doluysa Read BOŞTUR: yarım bir
// harita, cevap veren host'ları "ölçüldü", vermeyenleri yanlış gerekçeyle
// "ölçülemedi" gösterirdi.
type mvTargetProbe struct {
	Err     string
	Read    map[string]map[string]mvTargetRead
	Targets MVTargetSet
}

// mvTargetUUIDs — küme geneli TEK okuma, yalnız MV satırları.
// db ÇAĞIRANDAN gelir (mvInventory onu zaten okudu): clusterAllReplicas'ta
// currentDatabase() UZAK düğümde başka bir veritabanına çözülür, o yüzden
// BAĞLI parametre şart — ama ikinci bir `SELECT currentDatabase()` açmak da
// bedava değil.
//
// Hata DÖNDÜRMEZ: okuma hatası kapsama sınıflarını DÜŞÜRMEZ, yalnız tüm
// hücreleri `unmeasured` yapar ve zarfta targetError olarak görünür
// (v0.10.820 sözleşmesi: okuma hatası ≠ iş başarısız).
func (s *Store) mvTargetUUIDs(ctx context.Context, db string) *mvTargetProbe {
	p := &mvTargetProbe{Read: map[string]map[string]mvTargetRead{}}
	fail := func(msg string) *mvTargetProbe {
		p.Err, p.Read, p.Targets = msg, nil, MVTargetSet{}
		return p
	}
	if strings.TrimSpace(db) == "" {
		return fail("veritabanı adı çözülemedi")
	}
	src := "system.tables"
	if s.clusterMode() {
		src = fmt.Sprintf("clusterAllReplicas('%s', system.tables)", strings.TrimSpace(s.cfg.ClusterName))
	}
	// create_table_query KIRPILMAZ: v0.10.832 öncesi 400 karakterlik substring
	// vardı ve ayar=1'de metin uzar — `TO INNER UUID` pencerenin dışına düşerdi.
	rows, err := s.conn.Query(ctx, `
		SELECT hostName(), name, create_table_query
		FROM `+src+`
		WHERE database = ? AND engine = 'MaterializedView'`+chMVTargetProbeSettings, db)
	if err != nil {
		return fail(err.Error())
	}
	defer rows.Close()
	targets := map[innerObjectUUID]bool{}
	for rows.Next() {
		var host, name, cq string
		if err := rows.Scan(&host, &name, &cq); err != nil {
			return fail(err.Error())
		}
		if p.Read[host] == nil {
			p.Read[host] = map[string]mvTargetRead{}
		}
		// cq BURADA tüketilir; dışarı yalnız uuid + bayrak çıkar.
		r := mvTargetRead{Target: innerObjectUUIDFromDDL(cq), Shown: mvUUIDTokenShown(cq)}
		p.Read[host][name] = r
		if validUUID(string(r.Target)) {
			targets[r.Target] = true
		}
	}
	if err := rows.Err(); err != nil {
		return fail(err.Error())
	}
	p.Targets = MVTargetSet{Measured: true, uuids: targets}
	return p
}

// mvTargetVerdict — SAF: (durum, iç tablonun KENDİ uuid'si, probe) → hedef
// kararı + MV'nin hedef uuid'si + operatör diliyle sebep.
//
// BOŞ ASLA UYUŞMAZLIK DEĞİLDİR: iki uuid'den biri okunamadıysa karar
// `unmeasured`'dır, `mismatch` DEĞİL. Yanlış yöne düşen bir karar burada
// kırmızı bir bulgu uydurur ve operatörü sağlam bir MV'yi elle sökmeye
// gönderir.
//
// Uyuşmazlık notu SONUCU İDDİA ETMEZ (gerçek CH ölçümü, inceleme turu):
// metadata farkı hem ingest'i düşüren şekli hem MV'nin çalışmaya devam
// ettiği şekli üretir. Hangisi olduğunu yalnız düğüm-yerel okuma söyler.
func mvTargetVerdict(state, innerUUID, host, view string, p *mvTargetProbe) (target, targetUUID, note string) {
	// Sorunun ÖN ŞARTI: çözülecek bir view. Yoksa sebep satırın KENDİ
	// durumudur, "probe cevap vermedi" değil.
	if state == MVStateMissing {
		return MVTargetUnmeasured, "", "MV bu host'ta yok — çözülecek bir hedef yok"
	}
	if p == nil {
		return MVTargetUnmeasured, "", "hedef uuid okuması hiç koşmadı"
	}
	if p.Err != "" {
		return MVTargetUnmeasured, "", "hedef uuid okuması düştü (" + p.Err + ") — kapsama sınıfları DURUYOR, yalnız hedef karşılaştırması ölçülemedi"
	}
	read, ok := p.Read[host][view]
	if !ok {
		return MVTargetUnmeasured, "", "bu host hedef uuid okumasına satır vermedi (düğüm cevap vermedi ya da view o an yoktu)"
	}
	tu := strings.ToLower(string(read.Target))
	if !validUUID(tu) {
		if !read.Shown {
			return MVTargetUnmeasured, "", "bu host'un metninde uuid token'ı HİÇ yok — ayar uzak düğümde sessizce kırpılmış olabilir (profil kısıtı istisna atmaz) ya da Ordinary DB; ölçülemedi"
		}
		return MVTargetByName, "", "MV'nin to_inner_uuid'si Nil — CH hedefi ADLA çözer (eski sürümde ya da ATTACH ile kurulmuş); bulgu değil"
	}
	if state == MVStateDangling {
		// Hedef uuid OKUNDU ama beklenen adda tablo yok. Bu, MV'nin hiçbir
		// şeye yazmadığı anlamına GELMEZ: hedef başka ADLI bir tabloya
		// çözülüyor olabilir (v0.10.780–831 "Eşten kur" yolunun bıraktığı
		// şekil). Kararı düğüm-yerel okuma verir.
		return MVTargetUnmeasured, tu, "beklenen adda (`.inner_id.<view uuid>`) iç tablo yok; MV " + tu + " nesnesini hedefliyor — o nesne BAŞKA ADLI var olan bir tablo olabilir (veri akıyor olabilir), düğüm-yerel okumayla ölçülür"
	}
	if !validUUID(innerUUID) {
		return MVTargetUnmeasured, tu, "iç tablonun KENDİ nesne uuid'si envanterde okunamadı — karşılaştırma yapılamadı"
	}
	if strings.EqualFold(innerUUID, tu) {
		return MVTargetOK, tu, ""
	}
	return MVTargetMismatch, tu, "ad doğru ama NESNE uuid'si yanlış: o adı taşıyan tablonun kendi uuid'si " + innerUUID +
		", MV ise " + tu + " hedefliyor — MV bu tabloya YAZMAZ; hedefin başka bir nesneye çözülüp çözülmediği düğüm-yerel okumayla ölçülür"
}

// mvApplyTargetVerdict — SAF: kapsama hücrelerine hedef kararını basar.
// Kapsamanın ZATEN karar verdiği hücreler (gizli iç tablosu olmayan MV'ler)
// EZİLMEZ.
func mvApplyTargetVerdict(cells []MVHostState, p *mvTargetProbe) {
	for i := range cells {
		c := &cells[i]
		if c.Target != "" {
			continue // kapsama kanıtla karar verdi (TO'lu MV / Ordinary DB)
		}
		c.Target, c.TargetUUID, c.TargetNote = mvTargetVerdict(c.State, c.InnerUUID, c.Host, c.View, p)
	}
}

// mvTargetResolved — SAF: hedef ÖLÇÜLEREK çözülüyor mu. nil (ölçülmedi) ve
// false (çözülmüyor) aynı kovada DEĞİL: yalnız kanıtlanmış "çözülüyor"
// yıkıcı eylemi kapatır.
func mvTargetResolved(c *MVHostState) bool { return c.TargetResolves != nil && *c.TargetResolves }

// storageResolves — SAF: hücre yoksa ÖLÇÜLMEDİ (nil), varsa ölçümü. Kapının
// girdisini nil-güvenli tutar; "satır yok" hâli zaten State koluyla reddedilir.
func storageResolves(c *MVHostState) *bool {
	if c == nil {
		return nil
	}
	return c.TargetResolves
}

// mvTargetNeedsCheck — SAF: bu hücrede düğüm-yerel sertleştirme ANLAMLI mı.
//
// İki hücre türü: (a) `mismatch` — hedef başka nesneye çözülüyor olabilir;
// (b) `dangling` + hedef uuid OKUNABİLMİŞ — tam da "nesne uuid'si doğru, ad
// beklenen değil" şekli, ve kart o satıra İKİ danger düğme doğrultuyor.
// Hedef uuid okunamadıysa ölçülecek bir şey yoktur.
func mvTargetNeedsCheck(state, target, targetUUID string) bool {
	if !validUUID(targetUUID) {
		return false
	}
	switch target {
	case MVTargetMismatch:
		return true
	case MVTargetUnmeasured:
		return state == MVStateDangling
	}
	return false
}

// Sertleştirme bütçesi. ÖLÇÜLDÜ (inceleme turu): kapaksız süpürme 6 host ×
// 21 kanonik MV = 126 hücrede en kötü 21 dk sürüyordu ve bedel üç yerde
// ödeniyordu; FE her onarımdan sonra yeniden taradığı için tıklama başına
// İKİ KEZ. Kapak + toplam bütçe + eylem yollarında ATLAMA üçü birlikte.
const (
	mvTargetCheckTimeout = 10 * time.Second // sorgu başına
	mvTargetCheckBudget  = 15 * time.Second // TOPLAM süpürme
	mvTargetCheckPerHost = 1                // host başına satır
	mvTargetCheckTotal   = 8                // toplam satır
)

// mvTargetCheckPlan — SAF: hangi hücreler ölçülür, hangileri kapağa takılır.
// Sıra girdinin sırası (kapsama view, host sıralı): karar deterministik.
func mvTargetCheckPlan(cells []MVHostState) (run, capped []int) {
	perHost := map[string]int{}
	for i := range cells {
		c := &cells[i]
		if !mvTargetNeedsCheck(c.State, c.Target, c.TargetUUID) {
			continue
		}
		// v0.10.835 — host kapağı `mismatch` satırlarını MUAF tutar.
		//
		// NEDEN (ölçüldü, 835 incelemesi): 833'te bu ölçüm yalnız bir
		// ETİKETTİ ve kapak zararsızdı. 835 EYLEMİ ölçüme bağladı
		// (ölçülmemiş satırda onarım koşmaz), yani kapak bir EYLEM KAPISINA
		// dönüştü. Host başına tek slot iki şekilde kilitliyordu: (a) aynı
		// host'taki ikinci `mismatch` satırı hiç ölçülmüyor ve plan
		// DETERMİNİSTİK olduğu için "Ölç" kaç kez basılırsa basılsın hep
		// aynısı seçiliyordu; (b) hedefini ÇÖZEN (şekil-2 — asla
		// onarılmayacak, kalıcı) bir satır o tek slotu SONSUZA DEK tutuyordu.
		// Muafiyet `mismatch` ile sınırlı: `dangling` satırlarının kartta
		// zaten eylemi var ve onlar kapakta kalır. Bedeli toplam satır tavanı
		// + toplam bütçe sınırlamaya devam ediyor.
		exempt := c.Target == MVTargetMismatch
		if len(run) >= mvTargetCheckTotal || (!exempt && perHost[c.Host] >= mvTargetCheckPerHost) {
			capped = append(capped, i)
			continue
		}
		perHost[c.Host]++
		run = append(run, i)
	}
	return run, capped
}

// mvTargetCappedNote — SAF: ölçülmeyen satırın notu. Metadata kanıtı KALIR,
// üstüne "doğrulanmadı" eklenir — sessizce şekil varsaymaz.
func mvTargetCappedNote(note string) string {
	const add = " · düğüm-yerel doğrulama KOŞULMADI (satır kapağı/bütçe) — karar yalnız metadata kanıtına dayanıyor"
	if strings.Contains(note, add) {
		return note
	}
	return note + add
}

// isCHUnknownTable — CH kod 60 (UNKNOWN_TABLE). errors.As deseni depoda var
// (replica_repair.go isCHTimeout, entity_store.go isUnknownTableOrColumn).
func isCHUnknownTable(err error) bool {
	if err == nil {
		return false
	}
	var ex *clickhouse.Exception
	if errors.As(err, &ex) {
		return ex.Code == 60
	}
	return strings.Contains(err.Error(), "code: 60,")
}

// mvTargetCheckNote — SAF: düğüm-yerel sertleştirme okumasının
// (`SELECT 1 FROM <view> LIMIT 0`) sonucundan ÜÇ HÂLLİ karar + metin.
//
//	true  → hedef ÇÖZÜLÜYOR: MV yazıyor, ingest akıyor (gerçek CH'de ölçüldü)
//	false → kod 60: MV hedefini ÇÖZEMİYOR, ingest bu host'ta düşüyor
//	nil   → okunamadı: bulgu OLDUĞU GİBİ kalır, şekil VARSAYILMAZ
func mvTargetCheckNote(state string, err error) (*bool, string) {
	yes, no := true, false
	switch {
	case err == nil && state == MVStateDangling:
		return &yes, "adı beklenen `.inner_id.<view uuid>` DEĞİL ama MV hedefini ÇÖZÜYOR — veri akıyor; yıkıcı eylemler bu satırda KAPALI (DROP bu host'un çalışan toplamasını götürürdü)"
	case err == nil:
		return &yes, "MV hedefini ÇÖZÜYOR: başka ADLI bir nesneye yazıyor ve TOPLUYOR; `.inner_id.<view uuid>` ÖLÜ KOPYA olabilir — hangisinin dolu olduğunu ölçmeden hiçbir şey düşürme"
	case isCHUnknownTable(err):
		return &no, "düğüm-yerel okuma kod 60 (UNKNOWN_TABLE) verdi: MV hedefini ÇÖZEMİYOR — bu host'ta INSERT kaskadı düşer ve Distributed spool büyür"
	}
	return nil, ""
}

// mvLeftoverStorageGate — SAF: kalıntıyı düşürmeden önce kanonik depolama
// adının O HOST'TA gerçekten çalıştığını sorar. "" = geçti.
//
// İKİ KOL, ÜÇÜNCÜSÜ YOK (inceleme turu, ölçülmüş): kapsama `ok` değilse
// reddet; `ok` ama hedef ÖLÇÜLEREK çözülmüyorsa reddet. Hedef çözülüyorsa
// ya da ölçülemediyse REDDETME — "uyuşmazlık = bozuk" varsayımı MEŞRU bir
// temizliği engelliyordu (şekil-2'de MV topluyor) ve ölçülemedi hâlinde
// v0.10.832 davranışını korumak bilinçli: bilmediğimiz şey yeni bir engel
// üretmez.
//
// Ret metni "Yeniden kur" düğmesinin adını yalnız o düğmenin GERÇEKTEN
// çizildiği dalda anar (durum `ok` değilse); hedef dalında durum `ok`
// olduğu için satırda düğme yoktur ve var olmayan bir düğmeyi işaret etmek
// v0.10.830'un düzelttiği sınıftır.
func mvLeftoverStorageGate(storage, state, target string, resolves *bool) string {
	if state != MVStateOK {
		why := "durum: " + state
		if state == "" {
			why = "kapsama raporunda satırı yok"
		}
		return fmt.Sprintf("%s bu host'ta sağlıklı değil (%s) — kalıntıyı düşürmek bu düğümün TEK toplamasını siler; önce MV onarımı ('Yeniden kur')", storage, why)
	}
	if target == MVTargetMismatch && resolves != nil && !*resolves {
		return fmt.Sprintf("%s bu host'ta duruyor ama MV hedefini ÇÖZEMİYOR (düğüm-yerel okuma kod 60 verdi) — hiçbir şey toplamıyor ve kalıntıyı düşürmek bu düğümü toplamasız bırakır. Kapsama sağlıklı göründüğü için satırda onarım düğmesi yoktur; MV kartındaki hedef-uuid runbook'unu izle", storage)
	}
	return ""
}

// mvCheckTargetResolution — düğüm-yerel SERTLEŞTİRME süpürmesi: iki uuid'nin
// farklı olması METADATA'dan gelen bir çıkarımdır; bu okuma DAVRANIŞI ölçer.
// Kapaklı ve bütçeli (yukarıdaki sabitler); kapağa takılan satırın notu
// metadata kanıtını KORUR.
//
// Adres çözülemiyorsa (ya da bağlantı kurulamıyorsa) o satır ATLANIR ve
// bulgu DÜŞMEZ: ölçemediğimiz şey bulguyu geçersiz kılmaz.
func (s *Store) mvCheckTargetResolution(ctx context.Context, cells []MVHostState, cluster string) {
	run, capped := mvTargetCheckPlan(cells)
	for _, i := range capped {
		cells[i].TargetNote = mvTargetCappedNote(cells[i].TargetNote)
	}
	if len(run) == 0 {
		return
	}
	bctx, cancelBudget := context.WithTimeout(ctx, mvTargetCheckBudget)
	defer cancelBudget()
	for n, i := range run {
		if bctx.Err() != nil {
			// Bütçe doldu: KALAN satırların notu metadata kanıtıyla kalır.
			for _, j := range run[n:] {
				cells[j].TargetNote = mvTargetCappedNote(cells[j].TargetNote)
			}
			return
		}
		s.mvHardenCell(bctx, &cells[i], cluster)
	}
}

// mvHardenCell — TEK hücrenin düğüm-yerel doğrulaması. Eylem yolları
// (RebuildMVOnHost / DropLeftoverMV) süpürmeyi ATLAR ve yalnız DOKUNACAKLARI
// satır için bunu çağırır: karar ölçülür ama 126 hücrelik tarama ödenmez.
func (s *Store) mvHardenCell(ctx context.Context, c *MVHostState, cluster string) {
	if !mvTargetNeedsCheck(c.State, c.Target, c.TargetUUID) || !chObjRe.MatchString(c.View) {
		return
	}
	conn := s.conn
	if cluster != "" {
		if c.Addr == "" {
			return
		}
		cc, err := s.shardConn(ctx, c.Addr)
		if err != nil {
			return
		}
		conn = cc
	}
	cctx, cancel := context.WithTimeout(ctx, mvTargetCheckTimeout)
	// LIMIT 0: hiçbir parça okunmaz, yalnız hedef ÇÖZÜMÜ denenir.
	err := conn.Exec(cctx, "SELECT 1 FROM `"+c.View+"` LIMIT 0 SETTINGS max_execution_time = 10")
	cancel()
	resolves, note := mvTargetCheckNote(c.State, err)
	if resolves == nil {
		return // ölçülemedi: bulgu OLDUĞU GİBİ kalır
	}
	c.TargetResolves, c.TargetNote = resolves, note
}
