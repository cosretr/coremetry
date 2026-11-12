package templater

// severity.go — v0.10.2029 (onaylı takip dilimi, v0.10.2028 gürültü
// tabanının seviye yarısı): yeni log kalıbı kararına seviye girer.
//
// Puller kalıbı çıkardığı örnek satırların seviyesini zaten görür
// (logstore.Record.Severity / SeverityText). Şema DEĞİŞMEDEN, kümenin
// ERROR+ satır sayacı süreç içi bir kayda yazılır (kalıp kimliği → örneklemde
// ERROR+ / toplam satır); yeni kalıp dedektörü kararı verirken buradan okur.
//
// Kural: örneklemdeki satırların ÇOĞUNLUĞU ERROR ve üstüyse kalıp ERROR
// sayılır (tek bir ERROR satırı yetmez). Seviyesi bilinmeyen ("other")
// satır ERROR sayılmaz ama paydadadır — çoğunluğu "other" olan kalıp ERROR
// değildir (batch'te olay üretmez; bugünkü güvenli davranış).
//
// Sınır: kayıt SÜREÇ İÇİ. Puller (templater lideri) ile anomali kayıtçısı
// (anomaly lideri) farklı pod'lara düşerse kayıtçı seviyeyi bilmez → batch
// serviste yeni kalıp olayı yok (güvenli yön). Kalıcı çözüm log_templates'e
// kolon (ayrı onay).

import (
	"strings"
	"sync"
	"time"
)

// severityTTL — kayıttaki bir kalıbın seviye bilgisinin ömrü.
const severityTTL = 6 * time.Hour

type sevTally struct {
	errPlus, total uint64
	at             time.Time
}

var sevReg = struct {
	mu sync.Mutex
	m  map[string]sevTally
}{m: map[string]sevTally{}}

// IsErrorSeverity — SAF: OTel seviye numarası ≥ 17 (ERROR) ya da seviye
// metni ERROR ve üstü. İkisi de yoksa / tanınmıyorsa false ("other").
func IsErrorSeverity(num uint8, text string) bool {
	if num >= 17 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "error", "err", "fatal", "critical", "crit", "severe", "emerg", "emergency", "alert", "panic":
		return true
	}
	return false
}

// MajorityErrorLevel — SAF: örneklemdeki ERROR+ satırlar çoğunluktaysa
// "ERROR", değilse "" (bilinmiyor / ERROR değil).
func MajorityErrorLevel(errPlus, total uint64) string {
	if total > 0 && errPlus*2 > total {
		return "ERROR"
	}
	return ""
}

// RecordTemplateSeverity — puller tikinde kümenin sayaçları (küme ömrü
// boyunca birikmiş değerler; üzerine yazılır).
func RecordTemplateSeverity(id string, errPlus, total uint64, now time.Time) {
	if id == "" {
		return
	}
	sevReg.mu.Lock()
	defer sevReg.mu.Unlock()
	sevReg.m[id] = sevTally{errPlus: errPlus, total: total, at: now}
	for k, v := range sevReg.m {
		if now.Sub(v.at) > severityTTL {
			delete(sevReg.m, k)
		}
	}
}

// TemplateLevel — kalıbın örneklem seviyesi ("ERROR" | ""). Kayıtta yoksa
// ya da bayatsa "" (bilinmiyor).
func TemplateLevel(id string) string {
	sevReg.mu.Lock()
	defer sevReg.mu.Unlock()
	v, ok := sevReg.m[id]
	if !ok || time.Since(v.at) > severityTTL {
		return ""
	}
	return MajorityErrorLevel(v.errPlus, v.total)
}

// NoteErrorPlus — Add'in döndürdüğü kümenin ERROR+ satır sayacı.
func (d *Drain) NoteErrorPlus(c *Cluster) {
	if c == nil {
		return
	}
	d.mu.Lock()
	c.ErrPlus++
	d.mu.Unlock()
}
