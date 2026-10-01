package chstore

import (
	"math"
	"time"
)

// prior_window.go — "önceki eşit pencere"nin TEK türetimi (v0.10.1025).
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// /databases ve /messaging listelerinin ?compare=prior okuması prior
// pencereyi `[from − (to − from), from)` diye kuruyordu ve iki okuma da
// 5 dk'lık MV kovalarından geliyor. Current okuma alt sınırı kovaya
// İNDİRİYOR (`time_bucket >= floor5(from)`, alignBucketStart), prior
// okumanın üst sınırı ise HİZASIZ `from`'du (`time_bucket < from`). from
// bir kova sınırına oturmadığında (10:03) 10:00 kovası İKİ pencereye de
// giriyordu: aynı beş dakikalık trafik hem "şimdi" hem "önce" sayılıyor,
// delta sıfıra doğru sulanıyordu. 15 dk'lık bir pencerede bu, pencerenin
// üçte biri demek. Fark yalnız hizasız `from`'da çıkar — hizalı pencerede
// iki sınır çakışmaz, o yüzden gözden kaçtı.
//
// Emsal düzeltmeler repoda zaten vardı ama paylaşılmıyordu:
// GetOperationSummaryCompared (v0.9.64, `priorEnd := winStart.Truncate(5m)…`)
// ve compare_periods.go `periodGrid`. Bu dosya kuralı TEK yere koyuyor ve
// liste (db + messaging) ile /database detayı aynı fonksiyonu çağırıyor.
//
// ── KURAL ───────────────────────────────────────────────────────────────
//
// Current okuma şu kova etiketlerini alır: floor5(from) ≤ L < to. Prior
// pencere bu kümenin HEMEN ÖNCESİNDEKİ aynı sayıda kovadır:
//
//	pTo   = floor5(from)            (current'ın ilk kovası prior'a GİRMEZ)
//	pFrom = pTo − N × 5dk           (N = current'ın kova sayısı)
//
// İki uç da hizalı, yani prior okuması `time_bucket >= pFrom AND
// time_bucket < pTo` ile TAM N kova okur. Ortak kova yok, kova sayısı eşit.
//
// Neden `to − from` değil de N × 5dk: hizasız iki uçlu bir pencere
// (10:02–11:02) 13 kovaya dokunur (10:00 … 11:00), 60 dakikalık geri kayma
// ise 12 kova verir — prior bir kova KISA kalır ve sayaç deltası sahte bir
// "+%8" basar. Eşit kova sayısı, sayaç deltasının oran deltasına eşit
// kalmasının şartı.
//
// `to` SANİYEYE inerek sayılır: clickhouse-go konumsal `?` bağında
// time.Time'ı saniye hassasiyetinde yazar (bind.go, Seconds ölçeği;
// v0.10.1023 kaydının emsali). to = 11:00:00.400 iken SQL `< 11:00:00`
// görür ve 11:00 kovası current'a girmez; sayım bunu taklit etmezse prior
// bir kova FAZLA okurdu.
//
// KAPSAM: yalnız 5 dk MV kovasından okuyan yüzeyler. Ham spans okuyan yol
// (/databases env süzgeci, getDatabasesRaw) kova ızgarası taşımaz; onun
// prior'u birebir süre kaydırmasıdır ve çağıranda seçilir
// (api_databases.go dbListPriorWindow).
//
// v0.10.1028 — gövde PriorWindowGrid'e taşındı; bu fonksiyon onun 5 dk
// ızgarasıdır (tek uygulama, TestPriorWindowIsGrid5m).
func PriorWindow(from, to time.Time) (pFrom, pTo time.Time) {
	return PriorWindowGrid(from, to, mvBucketWidth)
}

// PriorWindowGrid — PriorWindow kuralının ızgara-parametreli hâli
// (v0.10.1028). Okuma `time_bucket >= floorB(from) AND time_bucket < to`
// biçimindeyse (B = bucket) prior pencere:
//
//	pTo   = floorB(from)
//	pFrom = pTo − N × B   (N = ceil((sec(to) − floorB(from)) / B))
//
// Aynı üç özellik: ortak kova yok, kova sayısı eşit, `to` saniyeye iner
// (clickhouse-go bağı). v0.10.1025 kaydının kalan yüzeyleri her zaman
// 5 dk ızgarasında değil: /endpoints MV yolu alt sınırı DAKİKAYA indiriyor
// (spanmetrics_1m / _10s). O okuyucu bunu EndpointsPriorWindow üzerinden
// kullanır; 5 dk kuralını orada zorlamak prior'u bir dakikaya kadar
// kaydırırdı.
//
// bucket ≤ 0 = ızgarasız (ham) okuma: birebir süre kaydırması — ham
// sınırlar kova taşımaz, dbListPriorWindow'un ham dalıyla aynı kural.
//
// GİRDİ KISITLARI (v0.10.1028 inceleme R5):
//   - bucket 24 saati (86400 sn) TAM bölmeli. Go'nun Truncate'i ızgarayı
//     sıfır zamana (1. yıl) göre kurar, ClickHouse ise Unix epoch'a göre;
//     ikisi arası 719162 TAM gündür, yani yalnız günü bölen kovalarda iki
//     ızgara çakışır (10 sn, 1 dk, 5 dk, 1 sa, 1 g …). Bölmeyen kova (7 sn)
//     ızgarası bilinmeyen okuma sayılır: birebir süre kaydırması.
//   - Pencere boyu time.Duration taşma aralığının (~292 yıl) çok altında
//     varsayılır; `to.Sub(from)` ve `n × bucket` orada doyar.
func PriorWindowGrid(from, to time.Time, bucket time.Duration) (pFrom, pTo time.Time) {
	if bucket <= 0 || (24*time.Hour)%bucket != 0 {
		return from.Add(-to.Sub(from)), from
	}
	start := from.Truncate(bucket)
	end := to.Truncate(time.Second)
	var n time.Duration
	if end.After(start) {
		// Tavan bölmesi: [start, end) aralığına dokunan kova sayısı.
		n = (end.Sub(start) + bucket - 1) / bucket
	}
	return start.Add(-n * bucket), start
}

// ── OKUYUCUYA ÖZGÜ UYARLAMALAR (v0.10.1028) ─────────────────────────────
//
// v0.10.1025 kusuru (prior'un `[from − dur, from)` diye kurulması) dört
// yüzeyde daha vardı. İkisi düz 5 dk okuyucu (dbstmt detayı, /services MV
// yolu — doğrudan PriorWindow). İkisinin okuyucusu farklı sınır kuruyor;
// kuralı onların SQL'ine göre uyarlayan saf fonksiyonlar aşağıda. Her biri
// okuyucusunun sınır gerçeğini kaynak pini ile taşır
// (prior_window_sites_test.go): okuyucu değişirse test kırmızıya döner.

// endpointsMVFloor / endpointsMVFineGrain — GetEndpointsMV'nin iki sınır
// gerçeği: alt sınır İKİ katmanda da dakikaya iner (`q.From.Truncate(
// time.Minute)`); ince katman spanmetrics_10s'in greni 10 sn
// (endpointsSparkGrid). Kalın katman (spanmetrics_1m) greni = dakika.
const (
	endpointsMVFloor     = time.Minute
	endpointsMVFineGrain = 10 * time.Second
)

// EndpointsPriorWindow — /endpoints ?compare=prior'un prior penceresi
// (v0.10.1028). SAF. Dönen çift doğrudan ikinci GetEndpoints çağrısının
// From/To'sudur.
//
// Yol, GetEndpoints'in AYNI yüklemiyle seçilir (q.forcesRaw):
//
//   - Ham yol (cluster / env süzgeci, getEndpointsRaw: `time >= from AND
//     time <= to`, ızgara yok) → birebir süre kaydırması.
//
//   - MV yolu (GetEndpointsMV) → alt sınır floor1m(from), üst `< to`.
//     Okuyucu pencere boyuna ve yaşına göre KATMAN seçer: 2 saatten kısa ve
//     son 24 saatteki pencereler spanmetrics_10s (10 sn kova), geri kalanı
//     spanmetrics_1m. Prior aynı alt sınır kuralıyla okunur (okuyucu
//     prior'un From'unu da dakikaya indirir), bu yüzden:
//
//     pFrom = floor1m(from) − n1 × 1dk     (dakika hizalı, okuyucu oynatmaz)
//     pTo   = pFrom + n10 × 10sn          (≤ floor1m(from))
//
//     n1 = current'ın 1 dk kova sayısı, n10 = 10 sn kova sayısı. Her iki
//     katmanda da ortak kova yok ve kova sayısı eşit: 10 sn katmanında prior
//     tam n10 kova okur; 1 dk katmanında ceil(n10/6) = n1 kova okur (iç içe
//     tavan özdeşliği). Bedeli: 10 sn katmanında prior ile current arasında
//     en çok 50 sn'lik okunmayan bir aralık kalabilir (bitişiklik değil,
//     eşit kova sayısı tercih edildi — sayaç deltası kova sayısına
//     bağlı). Düz PriorWindowGrid(…, 1dk) varsayılan /endpoints yolunda
//     (1 sa = 10 sn katmanı) prior'u 50 sn'ye kadar UZUN okurdu.
func EndpointsPriorWindow(q EndpointsQuery) (pFrom, pTo time.Time) {
	if q.forcesRaw() {
		return PriorWindowGrid(q.From, q.To, 0)
	}
	pFrom, start := PriorWindowGrid(q.From, q.To, endpointsMVFloor)
	fineFrom, _ := PriorWindowGrid(start, q.To, endpointsMVFineGrain)
	return pFrom, pFrom.Add(start.Sub(fineFrom))
}

// TopologyPriorWindow — /topology/service ve /servicegraph ?compare=prior
// için ReadServiceTopologyAgg / ReadServiceTopologyAggForFocus ARGÜMANLARI
// (v0.10.1028). SAF.
//
// Okuyucu pencereyi İKİ uçta da kovaya DIŞA yuvarlar:
// `time_bucket >= toStartOfFiveMinute(from) AND time_bucket <
// toStartOfFiveMinute(to) + 5 dk` — `to`'nun kovası (hizalı `to`'da bile)
// okunur. Eski prior argümanı (from − dur, from) bu yüzden floor5(from)
// kovasını HER pencerede, hizalı from dahil, iki tarafa da sayıyordu.
//
// Current kovaları: floor5(from) … floor5(to), yani PriorWindow'un
// [from, floor5(to) + 5dk) penceresi. Prior o kümenin hemen önündeki aynı
// sayıda kova; okuyucu üst ucu kendi kovasını da alarak yuvarladığı için
// dönen pTo prior'un SON KOVASININ etiketidir (= floor5(from) − 5 dk),
// dışlayıcı bir son değil. Okuyucu bu çifti [pFrom, floor5(from)) olarak
// okur.
func TopologyPriorWindow(from, to time.Time) (pFrom, pTo time.Time) {
	pFrom, end := PriorWindow(from, alignBucketStart(to).Add(mvBucketWidth))
	return pFrom, end.Add(-mvBucketWidth)
}

// ── CANLI KENAR (v0.10.1025 inceleme düzeltmesi R1) ─────────────────────
//
// Her hazır aralıkta `to = now`. Current okumanın SON kovası henüz
// doluyor (10:17:30'da 10:15 kovası yalnız 2,5 dk veri taşır), PriorWindow
// ise N TAM kova döndürür. Düz bir iş yükünde bile sayaç deltası bu yüzden
// AŞAĞI kayar: ortalamada 5 dk pencerede −%25, 15 dk'da −%12,5, 1 sa'te
// −%3,8. 15 dk'lık gerçek bir +%20 hata artışı ~+%5 ya da gri bir ↓
// olarak okunabiliyordu. Oranlar (hata oranı, gecikme) bundan ETKİLENMEZ;
// yalnız SAYAÇLAR (çağrı, hata, üretim/tüketim) ölçeklenir.

// minPriorCoverage — PriorCoverage'ın alt sınırı (saniye cinsinden dolu
// süre). Sıfır kapsama "prior'u yok say" demek olurdu; pencere tamamen
// gelecekteyse bile ölçek pozitif kalır ve ScalePriorCount'un tabanı
// devreye girer.
const minPriorCoverage = time.Second

// PriorCoverage — prior pencerenin uzunluğunun ne kadarını current okuma
// GERÇEKTEN doldurmaya vakit buldu: `(min(now, end5) − floor5(from)) /
// (N × 5dk)`; end5, current'ın dokunduğu son kovanın (dışlayıcı) sonu.
// (0, 1] aralığında; son kovası tamamlanmış her pencerede (geçmiş
// pencereler) TAM 1. SAF.
//
// Sınır: MV'ye yazım gecikmesi (async insert + MV tetiklemesi) hesaba
// katılmaz — `now`a kadar olan verinin okunur olduğu varsayılır. Gecikme
// saniyeler mertebesinde; dakikalarca gecikmiş bir ingest current'ı yine
// biraz eksik okur.
func PriorCoverage(from, to, now time.Time) float64 {
	pFrom, pTo := PriorWindow(from, to)
	length := pTo.Sub(pFrom) // N × 5dk
	if length <= 0 {
		return 1
	}
	end5 := pTo.Add(length) // pTo = floor5(from); son kovanın sonu
	if !now.Before(end5) {
		return 1
	}
	filled := now.Sub(pTo)
	if filled < minPriorCoverage {
		filled = minPriorCoverage
	}
	if filled >= length {
		return 1
	}
	return float64(filled) / float64(length)
}

// RawPriorCoverage — ham spans yolunun (kova ızgarası yok, current
// birebir [from, to]) karşılığı: `(min(now, to) − from) / (to − from)`,
// (0, 1]. Hazır aralıklarda to ≤ now olduğundan pratikte 1; yalnız
// geleceğe uzanan özel aralıkta < 1. SAF.
func RawPriorCoverage(from, to, now time.Time) float64 {
	length := to.Sub(from)
	if length <= 0 || !now.Before(to) {
		return 1
	}
	filled := now.Sub(from)
	if filled < minPriorCoverage {
		filled = minPriorCoverage
	}
	if filled >= length {
		return 1
	}
	return float64(filled) / float64(length)
}

// ScalePriorCount — bir prior SAYACI kapsama oranıyla ölçekler (en yakın
// tamsayı). SAF.
//
// TABAN 1 (bilinçli): ölçülmüş, sıfır olmayan bir prior asla 0'a
// yuvarlanmaz. Sıfır bu yüklerde bir İDDİA — detayda "önce 0" ("önceki
// pencerede 0'dı") cümlesini, listede ise omitempty ile ALANIN YOKLUĞUNU
// (satır eşleşmemiş görünür, bütün rozetleri kaybolur) üretir. Beklenen
// değer 0,5'in altına düşse de (yalnız tek kovalık çok kısa canlı
// pencerelerde olur) "1" bir tahmin fazlasıdır ama "0" bir YALAN olurdu.
// Bedeli: o kısa pencerelerde tek olaylı bir prior'a karşı delta biraz
// İYİMSER okunur (artışı hafifçe küçük gösterir).
func ScalePriorCount(n uint64, scale float64) uint64 {
	if n == 0 || scale >= 1 || scale <= 0 || math.IsNaN(scale) {
		return n
	}
	v := math.Round(float64(n) * scale)
	if v < 1 {
		return 1
	}
	return uint64(v)
}

// PriorReadable — prior penceresi okunduğu tablonun saklama ufku içinde
// mi? horizonDays: o tablonun TTL'i (gün); 0 = bilinmiyor → OKUNMAZ
// (doğrulanamayan bir kıyas çizilmez). SAF (v0.10.1025 R3).
//
// Bir günlük pay: TTL `toDate(ts) + INTERVAL n DAY` — satır kendi gününün
// GECE YARISINDAN n gün sonra silinmeye uygun olur, tam n×24 sa sonra
// değil. pFrom ≥ now − (n−1) gün ise kovasının gece yarısı + n gün her saat
// diliminde now'dan sonradır.
//
// YÖN: kısmen silinmiş (ya da hiç dolmamış) bir prior EKSİK sayar; current
// buna karşı FAZLA görünür — sahte bir KÖTÜLEŞME (kırmızı ↑), iyileşme
// değil. Boş prior penceresi (pFrom == pTo) de okunmaz.
func PriorReadable(pFrom, pTo, now time.Time, horizonDays int) bool {
	if !pTo.After(pFrom) || horizonDays <= 0 {
		return false
	}
	horizon := now.Add(-time.Duration(horizonDays-1) * 24 * time.Hour)
	return !pFrom.Before(horizon)
}
