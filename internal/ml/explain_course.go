package ml

// explain_course.go — öğrenme döngüsü Faz 2 parti B (audit
// docs/anomaly/learning-loop-audit.md §7.2, §17; mockup
// docs/frontend/mockups/anomaly-detail.html görünüm 3–4): "Olayın seyri"
// bölümünün sinyale göre EK görselleri. Ana grafik (dedektörün kendi kaydı,
// MLEventPlayer) aynen kalır; burada yalnız onun yanına gelen katmanların
// SAF kurulumu var. Okumalar çağıranda (internal/api/ml_course.go).
//
// v0.10.1870 — trace: ısı haritası penceresi + "olay öncesi p95" çizgisi.
//   Isı haritası mevcut /api/spans/heatmap (ham span, örneklemeli) ile FE'de
//   çizilir; burada yalnız pencere (≤ 6 sa, kayıt penceresiyle aynı:
//   başlangıç − 1 sa … bitiş + 1 sa), süzgeç (servis + endpoint) ve referans
//   çizgisi. Çizgi = ml_series `<route>|p95` serisinin (VM RED, örneklenmemiş)
//   olaydan önceki kovalarının medyanı; seri yoksa çizgi YOK (uydurma yok).

import (
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// CourseLine — ısı haritası / nokta grafiğinin referans çizgisi (ms).
type CourseLine struct {
	Ms    float64 `json:"ms"`
	Label string  `json:"label"`
}

// TraceCourse — trace olayının ısı haritası katmanı.
type TraceCourse struct {
	View    string `json:"view"` // heatmap
	Service string `json:"service"`
	// Route — http.route süzgeci ("/" ile başlayan değer); boş = servisin
	// tüm giriş span'leri (rpc/diğer etiket span kolonuna eşlenemez).
	Route   string      `json:"route,omitempty"`
	FromNs  int64       `json:"fromNs"`
	ToNs    int64       `json:"toNs"`
	OnsetNs int64       `json:"onsetNs"`
	EndNs   int64       `json:"endNs"`
	Line    *CourseLine `json:"line,omitempty"`
}

// ExplainCourse — replay yanıtının `course` alanı; sinyale göre biri dolu.
type ExplainCourse struct {
	Trace *TraceCourse `json:"trace,omitempty"`
}

const (
	courseLead      = time.Hour     // olaydan önce gösterilen pay (kayıt penceresi)
	courseTail      = time.Hour     // bitişten sonra
	courseMaxWindow = 6 * time.Hour // /api/spans/heatmap bütçesi (≤ 6 sa)
)

// CourseWindow — SAF: [başlangıç − 1 sa, bitiş + 1 sa], şimdiyi aşmaz, en
// çok 6 sa (uzun olayda baştan 6 sa: ilk sapma görünür kalır).
func CourseWindow(in CardInput) (from, to time.Time) {
	open := cardOpen(in)
	end := cardEnd(in, open)
	from, to = in.StartedAt.Add(-courseLead), end.Add(courseTail)
	if !in.Now.IsZero() && to.After(in.Now) {
		to = in.Now
	}
	if to.Sub(from) > courseMaxWindow {
		to = from.Add(courseMaxWindow)
	}
	if !to.After(from) {
		to = from.Add(in.bucket())
	}
	return from, to
}

// CourseSeriesNeeded — SAF: bu olay için ml_series okuması gerekir mi
// (çağıran gereksiz CH okuması yapmasın).
func CourseSeriesNeeded(in CardInput) bool {
	return in.Signal == chstore.MLSignalTrace && eventRoute(in) != ""
}

// preOnsetMedian — SAF: [from, onset) kovalarının medyanı; nokta yoksa ok=false.
func preOnsetMedian(pts []chstore.MLPoint, from, onset time.Time) (float64, bool) {
	var xs []float64
	for _, p := range pts {
		if p.T >= from.Unix() && p.T < onset.Unix() && p.V > 0 {
			xs = append(xs, p.V)
		}
	}
	if len(xs) == 0 {
		return 0, false
	}
	m, _ := medianMAD(xs)
	return m, true
}

// msText — SAF: "1,1 sn" / "640 ms" (kartın değer biçimi).
func msText(ms float64) string {
	return valueFmt{unit: unitMs, scaleS: ms >= 1000}.one(ms, false)
}

// BuildTraceCourse — SAF: endpoint'li trace olayının ısı haritası katmanı.
// series = servisin ml_series parçaları (CourseWindow'da okunmuş); nil olur.
// Endpoint'siz trace dedektörleri (bağımlılık, aykırı pod) için nil: onların
// kaydı servisin giriş isteklerini anlatmaz.
func BuildTraceCourse(in CardInput, series map[string][]chstore.MLPoint) *TraceCourse {
	route := eventRoute(in)
	if in.Signal != chstore.MLSignalTrace || route == "" {
		return nil
	}
	from, to := CourseWindow(in)
	open := cardOpen(in)
	tc := &TraceCourse{View: "heatmap", Service: in.Service, FromNs: from.UnixNano(), ToNs: to.UnixNano(),
		OnsetNs: in.StartedAt.UnixNano(), EndNs: cardEnd(in, open).UnixNano()}
	if strings.HasPrefix(route, "/") {
		tc.Route = route
	}
	if m, ok := preOnsetMedian(series[tracePart(route, "p95")], from, in.StartedAt); ok {
		tc.Line = &CourseLine{Ms: m, Label: "olay öncesi p95 ≈ " + msText(m)}
	}
	return tc
}
