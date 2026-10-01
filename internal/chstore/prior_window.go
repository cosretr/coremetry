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
func PriorWindow(from, to time.Time) (pFrom, pTo time.Time) {
	start := alignBucketStart(from)
	end := to.Truncate(time.Second)
	var n time.Duration
	if end.After(start) {
		// Tavan bölmesi: [start, end) aralığına dokunan kova sayısı.
		n = (end.Sub(start) + mvBucketWidth - 1) / mvBucketWidth
	}
	return start.Add(-n * mvBucketWidth), start
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
