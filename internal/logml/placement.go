// Package logml — ES log ML katmanı (docs/audits/log-ml-audit.md).
// Elastic ML job'larının karşılığı: ayrı alt sistem, kendi goroutine'i,
// kendi Redis kilidi, kendi tik bütçesi; evaluator/notifier/LLM drain'i
// ile aynı süreçte olsa da onları bloklamaz. Dilim 0: iskelet + self-obs;
// dedektörler sonraki dilimlerde Detector arayüzüyle takılır.
package logml

// Roles — pod'un taşıdığı rol bayrakları (main.go runMode'un ilgili kısmı;
// main.go tipi dışarı çıkmaz, döngü yok).
type Roles struct {
	Worker bool
	Sre    bool
}

// Placement — SAF: alt sistem bu pod'da koşmalı mı? Ayar rolü "sre"
// (varsayılan) ya da "worker"; pod o rolü taşıyorsa koşar. `all` modu iki
// rolü de taşır → koşar. Dönen etiket /admin/stats `placement` alanı.
func Placement(enabled bool, role string, r Roles) (run bool, label string) {
	if !enabled {
		return false, "off"
	}
	switch role {
	case "worker":
		return r.Worker, "worker"
	default:
		return r.Sre, "sre"
	}
}
