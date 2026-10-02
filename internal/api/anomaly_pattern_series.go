package api

// anomaly_pattern_series.go — v0.10.1060 (operatör, prod, log deseni
// anomalisi detayı: "Bunu doğru yakalamış ama artışın ne zaman başladığını
// göstermiyor. Elastic'e gidip bakınca barlardan net görüyorum.").
//
//   GET /api/anomalies/log-pattern-series?pattern=<desen adı>&from=<ns>[&to=<ns>]
//
// Desenin zaman içindeki eşleşme sayısı, anomali detay sayfasının bar
// grafiği için. Var olan okumalar bunu ifade edemiyordu: /api/logs/timeseries
// serbest metin araması (CH'de token-OR, dedektörün regex'i değil; "Java
// exceptions" deseninin "exception" token'ı her istisnayı sayardı) ve
// istemci desenin token'larını bilmiyor (olay yalnız desen ADINI taşır).
// Burada sunucu adı dedektörün kendi tanımına çevirir
// (anomaly.LogPatternSpecByName) ve logstore.PatternHistogram ile sayar —
// CountPatterns'ın yüklemi, iki arka uçta da (CH + ES).
//
// Rol kapısı YOK — salt okunur; viewer anomali sayfasını görür, grafiği de
// görmeli. Önbellek 60 s; anahtar TÜM girdileri taşır (desen, hizalı
// pencere, kova). Kardinalite sınırlı: desen küratörlü ad listesinden (≤~25,
// bilinmeyen ad 404 — anahtara hiç girmez), pencere kova sınırına hizalı,
// kova genişliği sabit basamaklardan, kova sayısı ≤120, pencere ≤7 gün.
//
// v0.10.1062 (operatör, prod ES: servissiz log deseni anomalisinde "Ne
// yapabilirim" kartı yalnız "servis adı yok" diyordu, operatör Kibana'ya elle
// gidiyordu) — cevap iki alan daha taşır, ikisi de yeni sorgu DEĞİL:
//   • logsQuery: desenin /logs arama metni (logstore.PatternSearchText —
//     dedektörün token'ları; istemci desenin yalnız ADINI bilir). Saf türetme.
//   • topServices: deseni en çok üreten ≤5 servis — ES'te aynı _search'ün
//     terms zinciri; CH boş (gerekçe CHStore.PatternHistogram).
// İkisi de anahtardaki girdilerden türer (desen + pencere); anahtarın sürüm
// öneki yalnız eski şekilli önbellek girdisi sunulmasın diye.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func init() {
	registerRoutesExtra("anomaly-pattern-series", (*Server).registerAnomalyPatternSeriesRoutes)
}

func (s *Server) registerAnomalyPatternSeriesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/anomalies/log-pattern-series", s.getLogPatternSeries)
}

// Kova basamakları (sn) — hepsi 86400'ü böler, yani gün sınırına da hizalı.
// En küçük basamak 1 dk: dedektör penceresi 5 dk, daha ince kova ES'e boşa
// kova sayısı yükler.
var patternSeriesRungs = []int{60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400}

const (
	patternSeriesMaxBuckets = 120
	patternSeriesMaxSpan    = 7 * 24 * time.Hour
	patternSeriesTTL        = 60 * time.Second
)

// snapPatternSeriesWindow — SAF. İstenen pencereyi önbellek-dostu ve
// sınırlı hâle getirir: to boş ya da gelecekteyse now; pencere en çok 7 gün
// (eski uç kırpılır); kova = hizalı kova sayısı ≤120 olan EN KÜÇÜK basamak;
// uçlar kovaya hizalanır (başlangıç aşağı, bitiş yukarı — pencere asla
// daralmaz). ok=false: pencere boş/ters.
func snapPatternSeriesWindow(from, to, now time.Time) (f, t time.Time, bucketSec int, ok bool) {
	if from.IsZero() {
		return time.Time{}, time.Time{}, 0, false
	}
	if to.IsZero() || to.After(now) {
		to = now
	}
	if to.Sub(from) > patternSeriesMaxSpan {
		from = to.Add(-patternSeriesMaxSpan)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, 0, false
	}
	for _, b := range patternSeriesRungs {
		bn := int64(b) * int64(time.Second)
		fs := floorDiv(from.UnixNano(), bn) * bn
		ts := -floorDiv(-to.UnixNano(), bn) * bn
		if (ts-fs)/bn <= patternSeriesMaxBuckets {
			return time.Unix(0, fs), time.Unix(0, ts), b, true
		}
	}
	// 7 günlük tavan en büyük basamakta 8 kovaya sığar; buraya düşülmez.
	return time.Time{}, time.Time{}, 0, false
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// fillPatternBuckets — SAF. Arka ucun seyrek noktalarını [from, to) ızgarasına
// oturtur: her kova bir nokta, boşu 0 ("sayıldı, eşleşme yok" — ölçülmemiş
// değil; pencerenin tamamı sorgulandı). Hizası kaymış nokta (CH sunucu saat
// dilimi vb.) içine düştüğü kovaya toplanır; ızgara dışı atılır.
func fillPatternBuckets(points []logstore.LogPoint, from, to time.Time, bucketSec int) []logstore.LogPoint {
	bn := int64(bucketSec) * int64(time.Second)
	if bn <= 0 || !to.After(from) {
		return []logstore.LogPoint{}
	}
	f := from.UnixNano()
	n := int((to.UnixNano() - f + bn - 1) / bn)
	out := make([]logstore.LogPoint, n)
	for i := range out {
		out[i].T = f + int64(i)*bn
	}
	for _, p := range points {
		if p.T < f {
			continue
		}
		if i := int((p.T - f) / bn); i < n {
			out[i].V += p.V
		}
	}
	return out
}

// logPatternSeriesKey — SAF; her girdi anahtarda (v0.5.187).
func logPatternSeriesKey(pattern string, from, to time.Time, bucketSec int) string {
	return fmt.Sprintf("anomaly-pattern-series:v2:p=%s:from=%d:to=%d:b=%d",
		pattern, from.UnixNano(), to.UnixNano(), bucketSec)
}

type logPatternSeriesResponse struct {
	Pattern   string              `json:"pattern"`
	BucketSec int                 `json:"bucketSec"`
	From      int64               `json:"from"` // ns, kovaya hizalı
	To        int64               `json:"to"`   // ns, kovaya hizalı (dahil değil)
	Points    []logstore.LogPoint `json:"points"`
	// Partial — ES yumuşak zaman aşımı / düşen shard: kovalar alt küme.
	Partial bool `json:"partial,omitempty"`
	// LogsQuery — v0.10.1062: /logs `q=` metni; token'sız desende boş.
	LogsQuery string `json:"logsQuery,omitempty"`
	// TopServices — v0.10.1062: penceredeki en çok ≤5 servis (yalnız ES).
	TopServices []logstore.PatternServiceHit `json:"topServices,omitempty"`
}

func (s *Server) getLogPatternSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("pattern"))
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "pattern parametresi zorunlu")
		return
	}
	spec, known := anomaly.LogPatternSpecByName(name)
	if !known {
		// Eski satır / yeniden adlandırılmış desen: sayılacak tanım yok.
		writeJSONError(w, http.StatusNotFound, "bilinmeyen log deseni")
		return
	}
	if s.logs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "log arka ucu yapılandırılmamış")
		return
	}
	// Snap anahtarın ÖNÜNDE: ham ns pencereler sınırsız ayrı girdi basardı.
	from, to, bucketSec, ok := snapPatternSeriesWindow(parseTime(q.Get("from")), parseTime(q.Get("to")), time.Now())
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from zorunlu ve to'dan önce olmalı")
		return
	}
	key := logPatternSeriesKey(name, from, to, bucketSec)
	s.serveCached(w, r, key, patternSeriesTTL, func(ctx context.Context) (any, error) {
		tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		res, err := s.logs.PatternHistogram(tctx, spec, from, to, bucketSec)
		if err != nil {
			// Yavaş / erişilemeyen arka uç Coremetry arızası değil: 502
			// (öz-gözlem error_rate'ini şişirmesin, v0.7.13). Sorgu hatası
			// olduğu gibi yüzeye çıkar.
			if mapped := logstore.MapBackendSlow(err, tctx, ctx); errors.Is(mapped, logstore.ErrBackendSlow) {
				log.Printf("[anomaly] pattern series degraded (backend=%s, pattern=%q): %v", s.logs.Backend(), name, err)
				return nil, fmt.Errorf("%w: log backend slow: %v", errUpstream, err)
			}
			return nil, err
		}
		var pts []logstore.LogPoint
		var top []logstore.PatternServiceHit
		partial := false
		if res != nil {
			pts, partial, top = res.Points, res.Partial, res.TopServices
		}
		return logPatternSeriesResponse{
			Pattern: name, BucketSec: bucketSec,
			From: from.UnixNano(), To: to.UnixNano(),
			Points:      fillPatternBuckets(pts, from, to, bucketSec),
			Partial:     partial,
			LogsQuery:   logstore.PatternSearchText(spec),
			TopServices: top,
		}, nil
	})
}
