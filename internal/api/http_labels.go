package api

// http_labels.go — v0.10.1350: HTTP durum-kodu etiketinin VERİDEN
// çözümü; Service Overview metrik RED'i (service_metric_red.go) kullanır.
// v0.10.1361 — ikinci tüketici /api/endpoints/metric silindi.
//
// Operator-reported (prod): /endpoint?service=acm-order-quote-allocation-
// prod&… "Kaynak: metrik" → "Hata BİLİNMİYOR: metrikte durum-kodu etiketi yok"
// derken Service → HTTP Metrics sekmesi (VM) AYNI servis için 200/400
// kodlarını çiziyordu. Kök neden: etiket varlığı src.MetricPresentKeys ile
// soruluyordu — VM'de TEK, filo genelinde, 7 günlük /api/v1/labels taraması;
// her hata nil'e çöküyor, isPartial düşüyordu. Prod boyutunda zaman aşımı /
// kısmi cevap "etiket yok" diye okundu, 5xx sorgusu hiç atılmadı.
//
// Yeni sözleşme — VERİ GÜDÜMLÜ yoklama: çağrı sorgusunun GroupBy'ına aday
// durum anahtarı eklenir. Dikişler yokluğu "" döner (vmetrics seriesGroupKey,
// chstore groupKeyExprMetric). Herhangi bir boş-olmayan değer ⇒ VAR; hepsi
// boş ⇒ sıradaki aday (ikinci sorgu yalnız ıskada); ikisi de boş ⇒ YOK;
// sorgu hatası / satır tavanı ⇒ BİLİNMİYOR (gerekçesiyle) — "yok" DEĞİL.
// Çağrı = tüm durumların toplamı; hata = ^5\d\d$ (4xx hata sayılmaz —
// HTTP Metrics sekmesinin ayrı 4xx oranıyla tutarlı).
//
// İzleme (kuyruk, ŞİMDİ DEĞİL): MetricPresentKeys sertleştirmesi — servis
// kapsamı + kısmi cevap ⇒ bilinmiyor. Diğer tüketicisi (service_metric_
// throughput presentKeys teşhisi) hâlâ eski yoldan okur.
// v0.10.1358 — YAPILDI: chstore.PresentKeys (known|unknown + Reason +
// service|fleet Scope), pencere ≤24 sa; throughput zarfı presentKeysState taşır.

import (
	"errors"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// httpStatusKeyCandidates — sırayla: OTel semconv (≥1.23) sonra eski yazım.
// VM'de promLabel ile http_response_status_code / http_status_code olur;
// frontend lib/httpMetrics.ts HTTP_LABELS.status ile eşli (pin testi).
var httpStatusKeyCandidates = []string{"http.response.status_code", "http.status_code"}

// statusLabelState — durum-kodu etiketinin üç hâli.
type statusLabelState string

const (
	statusLabelPresent statusLabelState = "present"
	statusLabelAbsent  statusLabelState = "absent"
	statusLabelUnknown statusLabelState = "unknown"
)

// http5xxRe — hata tanımı: tam üç haneli 5xx kodu.
var http5xxRe = regexp.MustCompile(`^5[0-9][0-9]$`)

// isHTTP5xx — SAF: durum değeri hata mı (4xx DEĞİL).
func isHTTP5xx(v string) bool { return http5xxRe.MatchString(v) }

// resolveStatusLabel — SAF: `key` ile gruplanmış serilerde groupIdx
// konumunda boş-olmayan bir değer varsa etiket VAR (key döner); yoksa YOK.
// Konum dışı / kısa tuple "" sayılır (dikiş sözleşmesi).
func resolveStatusLabel(series []chstore.SpanMetricSeries, groupIdx int, key string) (statusLabelState, string) {
	for _, s := range series {
		if groupIdx >= 0 && groupIdx < len(s.GroupKey) && s.GroupKey[groupIdx] != "" {
			return statusLabelPresent, key
		}
	}
	return statusLabelAbsent, ""
}

// statusProbe — yoklama sonucu. Series: son BAŞARILI sorgunun serileri
// (present'ta durum kırılımlı; absent'ta durum sütunu boş — yine geçerli
// çağrı serisi; unknown'da ilk aday hata verdiyse nil → çağıran durumsuz
// sorguya düşer).
type statusProbe struct {
	State  statusLabelState
	Key    string
	Reason string
	Series []chstore.SpanMetricSeries
}

// errStatusRowsCapped — durum kırılımlı TEK bir sorgu chstore.SpanMetricRowCap
// tavanına çarptı: kırpılmış kırılım sessizce eksik toplam üretirdi, bu
// yüzden yoklama "bilinmiyor" der ve çağıran durumsuz sorguya düşer.
var errStatusRowsCapped = errors.New("durum kırılımı satır tavanına çarptı")

// statusRowsChecked — query sonucunu sorgu başına tavan denetiminden geçirir.
func statusRowsChecked(ser []chstore.SpanMetricSeries, err error) ([]chstore.SpanMetricSeries, error) {
	if err != nil {
		return nil, err
	}
	if chstore.SeriesRowsCapped(ser) {
		return nil, errStatusRowsCapped
	}
	return ser, nil
}

// probeStatusLabel — adayları sırayla dener; query(key) çağrı sorgusunu
// GroupBy'a `key` ekleyerek koşar (tavan denetimi query'nin işi:
// statusRowsChecked). Hata ⇒ unknown, "yok" DEĞİL.
func probeStatusLabel(query func(key string) ([]chstore.SpanMetricSeries, error), groupIdx int) statusProbe {
	return probeStatusLabelKeys(query, groupIdx, httpStatusKeyCandidates)
}

// probeStatusLabelKeys — v0.10.1429: aday anahtarlar AİLEDEN (HTTP:
// httpStatusKeyCandidates; gRPC: rpc.grpc.status_code / rpc.response.status_code,
// red_family.go). Davranış probeStatusLabel ile aynı.
func probeStatusLabelKeys(query func(key string) ([]chstore.SpanMetricSeries, error), groupIdx int, keys []string) statusProbe {
	var last []chstore.SpanMetricSeries
	for _, k := range keys {
		ser, err := query(k)
		if err != nil {
			return statusProbe{State: statusLabelUnknown, Reason: err.Error(), Series: last}
		}
		if st, key := resolveStatusLabel(ser, groupIdx, k); st == statusLabelPresent {
			return statusProbe{State: st, Key: key, Series: ser}
		}
		last = ser
	}
	return statusProbe{State: statusLabelAbsent, Series: last}
}

// statusLabelNote — SAF: notun hata cümlesi (baştaki boşlukla; durum
// çözülmediyse — satır yok — boş). subject "Hata" | "Failure rate";
// absentTail yok-hâlinin kuyruğu. VM'de anahtar VM etiket yazımıyla
// okunur (HTTP Metrics sekmesiyle aynı ad).
func statusLabelNote(state statusLabelState, key, reason, backend, subject, absentTail string) string {
	return statusLabelNoteFamily(state, key, reason, backend, subject, absentTail, redFamilies[0])
}

// statusLabelNoteFamily — v0.10.1429: aileye göre hata tanımı ve aday listesi
// (HTTP cümleleri bayt bayt eski).
func statusLabelNoteFamily(state statusLabelState, key, reason, backend, subject, absentTail string, fam redFamily) string {
	switch state {
	case statusLabelPresent:
		if backend == metricSourceVM {
			key = vmetrics.PromLabel(key)
		}
		return " " + subject + " = " + key + " " + fam.errorNoteTR() + "."
	case statusLabelAbsent:
		return " " + subject + " BİLİNMİYOR: metrikte durum-kodu etiketi yok (" +
			strings.Join(fam.StatusKeys, " / ") + ")" + absentTail + "."
	case statusLabelUnknown:
		return " " + subject + " BİLİNMİYOR: durum sorgusu başarısız (" + reason + ") — etiket yok DEĞİL."
	}
	return ""
}
