package chstore

import (
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// v0.10.822 — Distributed OKUMA tarafında replika seçimini belirlemek için
// iki env anahtarı (operatör 2026-09-19, test kümesi: aynı sorgu her
// yenilemede farklı sayı döndürüyor).
//
// Kök neden ORADA veri ıraksaması; bu anahtarlar onu DÜZELTMEZ, yalnız
// okumayı DETERMİNİSTİK yapar: her sorgu aynı replikaya gider, dolayısıyla
// sayı yenilemeden yenilemeye zıplamaz. Iraksamanın kendisi Admin →
// ClickHouse → "Replika tutarlılığı" ile ölçülür, "Replika onarımı"
// sihirbazıyla giderilir. Anahtarları açıp orayı atlamak, bozuk veriyi
// sabit bir yalanla örtmektir.
//
// DETERMİNİZM İÇİN İKİSİ BİRLİKTE GEREKİR: in_order + 0.
// prefer_localhost_replica ClickHouse'ta VARSAYILAN 1'dir ve koordinatörün
// kendi barındırdığı her shard için load_balancing'i DEVRE DIŞI bırakır —
// sorgu planı o shard'ı yerelden okumayı, load_balancing'e hiç bakmadan
// seçer. Okuma havuzu RoundRobin olduğundan (store.go, v0.9.496) ardışık
// SELECT'leri FARKLI düğümler koordine eder; her biri kendi shard'ını
// kendinden cevaplar ve sayı yine zıplar. Yalnız in_order vermek bu yüzden
// yetmez; prefer_localhost_replica=0 ile yerel kestirme kapatılmalıdır.
//
// INGEST HAVUZUNA UYGULANMAZ (store.go: yalnız ana bağlantı + okuma havuzu).
// Her iki ayar Distributed INSERT'te de replika seçer: internal_replication
// açıkken sink hedef replikayı aynı load_balancing önceliğiyle seçer ve
// yerel-yazma kestirmesini yalnız prefer_localhost_replica=1 iken alır.
// in_order + 0 ingest'e de uygulansaydı tüm yazmalar shard başına İLK
// replikaya hunilenir ve yerel sink kaybolurdu — okuma determinizmi için
// yazma dağılımı feda edilmez.
const (
	chSettingLoadBalancing   = "load_balancing"
	chSettingPreferLocalhost = "prefer_localhost_replica"
)

// chLoadBalancingAllowed — izin listesi TEK gövdede burada yaşar; config
// paketi değeri yalnız taşır (ikiz bir liste sapardı).
var chLoadBalancingAllowed = []string{
	"in_order",         // her sorgu listedeki ilk erişilebilir replikaya — deterministik
	"first_or_random",  // ilki düşerse rastgele eşe
	"random",           // CH varsayılanı: her sorgu rastgele replika
	"nearest_hostname", // ad benzerliğine göre en yakın
	"round_robin",      // sırayla
}

// chReadBalancingSettings, COREMETRY_CH_READ_LOAD_BALANCING ve
// COREMETRY_CH_READ_PREFER_LOCALHOST değerlerini CH ayarlarına çevirir.
// Boş değer = ayar GÖNDERİLMEZ (CH varsayılanı kalır: load_balancing=random,
// prefer_localhost_replica=1). Geçersiz değer = ayar gönderilmez + uyarı.
// Hiçbir durumda panik yok. Saf: tek girdi, tek çıktı, yan etki yok.
func chReadBalancingSettings(loadBalancing, preferLocalhost string) (clickhouse.Settings, []string) {
	out := clickhouse.Settings{}
	var warns []string

	if lb := strings.ToLower(strings.TrimSpace(loadBalancing)); lb != "" {
		ok := false
		for _, a := range chLoadBalancingAllowed {
			if lb == a {
				ok = true
				break
			}
		}
		if ok {
			out[chSettingLoadBalancing] = lb
		} else {
			warns = append(warns, fmt.Sprintf(
				"COREMETRY_CH_READ_LOAD_BALANCING=%q geçersiz — izinli: %s; yok sayıldı, ClickHouse varsayılanı geçerli",
				loadBalancing, strings.Join(chLoadBalancingAllowed, ", ")))
		}
	}

	// Üç durumlu: ayarsız/boş = gönderme (CH varsayılanı 1), 0 = yerel
	// kestirmeyi kapat, 1 = aç. Değer int gönderilir (CH tarafında UInt64).
	if pl := strings.ToLower(strings.TrimSpace(preferLocalhost)); pl != "" {
		v := -1
		switch pl {
		case "1", "true", "on", "yes":
			v = 1
		case "0", "false", "off", "no":
			v = 0
		default:
			warns = append(warns, fmt.Sprintf(
				"COREMETRY_CH_READ_PREFER_LOCALHOST=%q geçersiz — 0/false/off/no ya da 1/true/on/yes bekleniyor; yok sayıldı",
				preferLocalhost))
		}
		if v >= 0 {
			out[chSettingPreferLocalhost] = v
		}
	}
	return out, warns
}

// chReadBalancingEffective — boot logu ETKİN değeri yazsın diye: ayar
// gönderiliyorsa değeri, gönderilmiyorsa (boş ya da geçersiz env) CH
// varsayılanının adı. Geçersiz bir değer "uygulanmış" gibi loglanmaz.
func chReadBalancingEffective(rb clickhouse.Settings) (lb, prefer string) {
	lb, prefer = "random (CH varsayılanı)", "1 (CH varsayılanı)"
	if v, ok := rb[chSettingLoadBalancing]; ok {
		lb = fmt.Sprint(v)
	}
	if v, ok := rb[chSettingPreferLocalhost]; ok {
		prefer = fmt.Sprint(v)
	}
	return lb, prefer
}

// applyReadBalancing — ayarları TEK bir bağlantı seçeneğine ekler. Çağrı
// yerleri bilinçli olarak ana bağlantı ve okuma havuzu; ingest havuzu
// dokunulmadan kalır (gerekçe: dosya başlığı).
func applyReadBalancing(o *clickhouse.Options, rb clickhouse.Settings) {
	if o == nil || len(rb) == 0 {
		return
	}
	if o.Settings == nil {
		o.Settings = clickhouse.Settings{}
	}
	for k, v := range rb {
		o.Settings[k] = v
	}
}
