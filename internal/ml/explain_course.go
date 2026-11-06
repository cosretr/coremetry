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
// v0.10.1871 — log: yığılı hacim çubukları + gölge (beklenen) çubuk.
//   Taban = olay anı kaydının KENDİ serisi (çubuğun boyu dedektörün değeri);
//   varsayılan yığın mesaj kalıbı (ml_series `tpl|…` aileleri, ilk 2 +
//   "diğer"), ikinci seçenek seviye (`_error` + `_total − _error`). Gölge =
//   kova bandının üstü (q75; kart ve oynatma cümlesiyle aynı bant). ES'e
//   gitmez: aileler zaten ml_series'te (CH).

import (
	"math"
	"sort"
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
	Log   *LogCourse   `json:"log,omitempty"`
}

// LogStack — yığılı çubuk katmanı; Values Times ile hizalı.
type LogStack struct {
	Key    string    `json:"key"`
	Label  string    `json:"label"`
	New    bool      `json:"new,omitempty"`
	Values []float64 `json:"values"`
}

// LogCourse — log olayının yığılı hacim grafiği (kayıt serisinin kovaları).
type LogCourse struct {
	Times []int64 `json:"times"` // unix sn — olay anı kaydının seri zamanları
	// Shadow — kova başına beklenen üst değer (bant q75); bilinmeyen kova
	// null. Hiç bant yoksa alan yok (gölge çizilmez).
	Shadow    []*float64 `json:"shadow,omitempty"`
	ByPattern []LogStack `json:"byPattern"`
	ByLevel   []LogStack `json:"byLevel,omitempty"`
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
	switch in.Signal {
	case chstore.MLSignalTrace:
		return eventRoute(in) != ""
	case chstore.MLSignalLog:
		return len(in.Series) > 0 && logPartBase(in.Part)
	}
	return false
}

// courseSeriesMax — ml_series okuma penceresinin tavanı (kronik olayın
// kaydı günlere yayılabilir; satır sayısı sınırlı kalsın).
const courseSeriesMax = 48 * time.Hour

// CourseReadWindow — SAF: ml_series okuma penceresi. Kayıt serisi varsa
// onun kovaları (log yığını seriyle birebir hizalanır), yoksa CourseWindow;
// en çok 48 sa (sondan).
func CourseReadWindow(in CardInput) (from, to time.Time) {
	if n := len(in.Series); n > 0 {
		from, to = time.Unix(in.Series[0].T, 0), time.Unix(in.Series[n-1].T, 0).Add(in.bucket())
		if to.Sub(from) > courseSeriesMax {
			from = to.Add(-courseSeriesMax)
		}
		return from, to
	}
	return CourseWindow(in)
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

// logCourseTopPatterns — yığında adıyla gösterilen aile sayısı (mockup: 2 + diğer).
const logCourseTopPatterns = 2

// logPartBase — SAF: yığılabilen olay parçası (_error / _total).
func logPartBase(part string) bool { return part == partError || part == partTotal }

func pointAt(pts []chstore.MLPoint) map[int64]float64 {
	m := make(map[int64]float64, len(pts))
	for _, p := range pts {
		m[p.T] = p.V
	}
	return m
}

// BuildLogCourse — SAF: log olayının yığılı grafiği. Aile sayımları tüm
// seviyeleri içerebilir → yığın tabanı AŞAMAZ (kümülatif kelepçe), kalan
// "diğer" ≥ 0; çubuğun boyu her görünümde dedektörün değeridir (Seviye
// görünümü hariç: orada `_total`). Şablon dedektöründe (olayın kendi parçası
// bir aile) tek yığın; Seviye seçeneği yok.
func BuildLogCourse(in CardInput, series map[string][]chstore.MLPoint) *LogCourse {
	if in.Signal != chstore.MLSignalLog || len(in.Series) == 0 {
		return nil
	}
	n := len(in.Series)
	lc := &LogCourse{Times: make([]int64, n)}
	base := make([]float64, n)
	shadow := make([]*float64, n)
	banded := false
	for i, p := range in.Series {
		lc.Times[i], base[i] = p.T, math.Max(0, p.V)
		if b, ok := in.bandAt(p.T); ok {
			v := b.Hi
			shadow[i], banded = &v, true
		}
	}
	if banded {
		lc.Shadow = shadow
	}
	if strings.HasPrefix(in.Part, tplPartPrefix) {
		lc.ByPattern = []LogStack{{Key: in.Part, Label: strings.TrimPrefix(in.Part, tplPartPrefix), New: in.Detector == "rare_template", Values: base}}
		return lc
	}
	if !logPartBase(in.Part) {
		return nil
	}
	from, to := in.StartedAt.Unix(), cardEnd(in, cardOpen(in)).Unix()
	type cand struct {
		part string
		sum  float64
	}
	var cands []cand
	for part, pts := range series {
		if !strings.HasPrefix(part, tplPartPrefix) {
			continue
		}
		s := 0.0
		for _, p := range pts {
			if p.T >= from && p.T < to {
				s += p.V
			}
		}
		if s > 0 {
			cands = append(cands, cand{part, s})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].sum != cands[j].sum {
			return cands[i].sum > cands[j].sum
		}
		return cands[i].part < cands[j].part
	})
	if len(cands) > logCourseTopPatterns {
		cands = cands[:logCourseTopPatterns]
	}
	newText := ""
	if in.NewTemplate != nil {
		newText = strings.TrimSuffix(in.NewTemplate.Text, "…")
	}
	rest := append([]float64(nil), base...)
	for _, c := range cands {
		at := pointAt(series[c.part])
		vals := make([]float64, n)
		for i, t := range lc.Times {
			v := math.Min(math.Max(0, at[t]), rest[i])
			vals[i], rest[i] = v, rest[i]-v
		}
		ph := strings.TrimPrefix(c.part, tplPartPrefix)
		lc.ByPattern = append(lc.ByPattern, LogStack{Key: c.part, Label: ph, New: newText != "" && strings.HasPrefix(newText, ph), Values: vals})
	}
	other := "diğer mesajlar"
	switch {
	case len(cands) == 0 && in.Part == partError:
		other = "hata satırları"
	case len(cands) == 0:
		other = "log satırları"
	case in.Part == partError:
		other = "diğer hata mesajları"
	}
	lc.ByPattern = append(lc.ByPattern, LogStack{Key: "_rest", Label: other, Values: rest})
	if errPts, totPts := series[partError], series[partTotal]; len(errPts) > 0 && len(totPts) > 0 {
		ea, ta := pointAt(errPts), pointAt(totPts)
		ev, ov := make([]float64, n), make([]float64, n)
		for i, t := range lc.Times {
			ev[i] = math.Max(0, ea[t])
			ov[i] = math.Max(0, ta[t]-ea[t])
		}
		lc.ByLevel = []LogStack{{Key: partError, Label: "hata", Values: ev}, {Key: "_other", Label: "diğer seviyeler", Values: ov}}
	}
	return lc
}
