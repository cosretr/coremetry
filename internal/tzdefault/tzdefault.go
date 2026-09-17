// Package tzdefault — sunucunun VARSAYILAN saat dilimi (v0.10.746, operatör:
// "default Europe/Istanbul olsun, imaj").
//
// Yalnız MODELE giden damgalar içindir: tarayıcı dilimi gelmeyen yollar
// (arka plan exception/problem açıklayıcıları, dilim göndermeyen eski
// istemci). Tarayıcı dilim gönderdiğinde o kazanır (explainOptions /
// chat context → chatLocationNamed); burası yalnız merdivenin son
// basamağıdır. time.Local'a DOKUNMAZ: ClickHouse bind arg'ları ve loglar
// UTC kalır — süreç dilimini değiştirmek o sözleşmeyi sessizce kırardı.
//
// Kaynak: COREMETRY_TZ (IANA adı). Boş → UTC; geçersiz → UTC + boot logu.
// İmaj varsayılanı Dockerfile'da ENV COREMETRY_TZ=Europe/Istanbul; chart
// ya da ortam env'i onu ezer. tzdata gömülü (alpine zoneinfo'suna bağlı
// değil, main.go ile aynı gerekçe).
package tzdefault

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

// EnvKey — okunan ortam değişkeni.
const EnvKey = "COREMETRY_TZ"

var (
	once sync.Once
	loc  = time.UTC
)

// Resolve — SAF: adı konuma çevirir. Boş → UTC, nil hata. Çözülemeyen ad
// → UTC + hata (çağıran loglar). "UTC" adı time.UTC'nin KENDİSİNİ döndürür
// (işaretçi eşitliği: eski testler `== time.UTC` diye pinler).
func Resolve(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "UTC") {
		return time.UTC, nil
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC, err
	}
	return l, nil
}

// Location — süreç boyunca sabit; ilk çağrıda COREMETRY_TZ'den çözülür.
func Location() *time.Location {
	once.Do(func() {
		l, err := Resolve(os.Getenv(EnvKey))
		if err != nil {
			log.Printf("tzdefault: %s=%q çözülemedi (%v) — UTC kullanılıyor", EnvKey, os.Getenv(EnvKey), err)
		}
		loc = l
	})
	return loc
}
