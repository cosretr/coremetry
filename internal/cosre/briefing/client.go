package briefing

// client.go — kalem 2: istemci / sunucu-ailesi metrik farkı (briefing-audit
// "Tasarım önerisi" client_metrics_diff; Q9 Kafka Go yolu, Q11 Faz 1 = HTTP /
// gRPC SUNUCU aileleri — giden HTTP istemci metrikleri Faz 2). SAF: okuma api
// katmanında (Service → HTTP/GRPC sekmesinin readServiceHTTPRED'i; Infra
// Kafka panelinin runKafkaQuestions'ı), burada yalnız cur vs prev kıyası.

import (
	"fmt"
	"math"
)

// Aileler (pivot sekmesi: kafka → messaging, http/grpc → http).
const (
	FamilyKafka = "kafka"
	FamilyHTTP  = "http"
	FamilyGRPC  = "grpc"
)

// Ölçü türleri.
const (
	ClientErrPct    = "err_pct"     // sunucu 5xx / gRPC sunucu-hatası oranı (%)
	ClientLatency   = "latency_p95" // sunucu p95 (saniye)
	ClientSendErr   = "send_err"    // Kafka gönderim hatası (kayıt/sn)
	ClientLag       = "lag"         // Kafka tüketici lag maks. (kayıt)
	clientLatFloorS = 0.05          // p95 tabanı: 50 ms altı katlanması gürültü
	kafkaSendFloor  = 0.01          // ≈ 3 sa'te ~100 hatalı kayıt
	kafkaLagFloor   = 1000          // kayıt
)

// ClientRow — bir pencere çiftinin tek ölçüsü (okumadan aynen).
type ClientRow struct {
	Service, Family, Metric, Label string
	Now, Prev                      float64
	// HasNow / HasPrev — pencerede en az bir nokta okundu mu; yoksa kıyas YOK
	// (okunmamış pencere 0 sayılmaz — bulgu uydurulmaz).
	HasNow, HasPrev bool
	// VolNow / VolPrev — HTTP/gRPC pencere istek sayısı (rate ort. × pencere);
	// oran ölçüsü iki pencerede de ≥ MinSpans ister. Kafka'da kullanılmaz.
	VolNow, VolPrev float64
}

// SplitMeans — SAF: [ms, değer] noktaları → fromMs öncesi (prev) ve sonrası
// (cur) ortalaması. NaN/Inf atlanır; pencerede nokta yoksa has=false.
func SplitMeans(points [][2]float64, fromMs int64) (cur, prev float64, hasCur, hasPrev bool) {
	var sc, sp float64
	var nc, np int
	for _, p := range points {
		v := p[1]
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if int64(p[0]) >= fromMs {
			sc += v
			nc++
		} else {
			sp += v
			np++
		}
	}
	if nc > 0 {
		cur = sc / float64(nc)
	}
	if np > 0 {
		prev = sp / float64(np)
	}
	return cur, prev, nc > 0, np > 0
}

var familyLabelTR = map[string]string{FamilyKafka: "Kafka", FamilyHTTP: "HTTP", FamilyGRPC: "gRPC"}

func clientTab(family string) string {
	if family == FamilyKafka {
		return "messaging"
	}
	return "http"
}

func withLabel(text, label string) string {
	if label == "" {
		return text
	}
	return text + " · " + label
}

// ClientFindings — SAF: iki pencerede de okunmuş ölçüler → bulgu. Kat eşiği
// ClientX (tüm aileler); oran ölçüsü ayrıca ErrRateMinPct + MinSpans hacmi.
// Önceki pencerede 0 iken yükselen hata → "yeni" (prev OKUNMUŞ olmalı).
func ClientFindings(rows []ClientRow, th Thresholds, env string, windowS int) []Finding {
	var out []Finding
	for _, r := range rows {
		if !r.HasNow || !r.HasPrev || r.Service == "" {
			continue
		}
		fam := familyLabelTR[r.Family]
		if fam == "" {
			continue
		}
		link := ServiceURL(r.Service, clientTab(r.Family), env, windowS)
		rat := ratio(r.Now, r.Prev)
		f := Finding{Item: ItemClient, Kind: r.Family + "_" + r.Metric, Service: r.Service, Label: r.Label, Now: r.Now, Prev: r.Prev, Score: rat, PageURL: link}
		switch r.Metric {
		case ClientErrPct:
			if r.Family == FamilyKafka || r.VolNow < float64(th.MinSpans) || r.VolPrev < float64(th.MinSpans) || r.Now < th.ErrRateMinPct {
				continue
			}
			what := "5xx oranı"
			if r.Family == FamilyGRPC {
				what = "sunucu-hatası oranı"
			}
			f.Unit = "%"
			if r.Prev == 0 {
				f.Score = newErrScore
				f.Text = fmt.Sprintf("%s %s %s %s (önceki pencerede yoktu)", r.Service, fam, what, pct(r.Now))
			} else if rat >= th.ClientX {
				f.Text = fmt.Sprintf("%s %s %s %s (önceki %s, %s)", r.Service, fam, what, pct(r.Now), pct(r.Prev), xs(rat))
			} else {
				continue
			}
		case ClientLatency:
			if r.Family == FamilyKafka || r.Prev <= 0 || r.Now < clientLatFloorS || rat < th.ClientX {
				continue
			}
			f.Unit = "ms"
			f.Now, f.Prev = r.Now*1000, r.Prev*1000
			f.Text = fmt.Sprintf("%s %s p95 %s (önceki %s, %s)", r.Service, fam, ms(f.Now), ms(f.Prev), xs(rat))
		case ClientSendErr:
			if r.Family != FamilyKafka || r.Now < kafkaSendFloor {
				continue
			}
			f.Unit = "/s"
			if r.Prev == 0 {
				f.Score = newErrScore
				f.Text = withLabel(fmt.Sprintf("%s Kafka gönderim hatası %.2f/s (önceki pencerede yoktu)", r.Service, r.Now), r.Label)
			} else if rat >= th.ClientX {
				f.Text = withLabel(fmt.Sprintf("%s Kafka gönderim hatası %.2f/s (önceki %.2f/s, %s)", r.Service, r.Now, r.Prev, xs(rat)), r.Label)
			} else {
				continue
			}
		case ClientLag:
			if r.Family != FamilyKafka || r.Prev <= 0 || r.Now < kafkaLagFloor || rat < th.ClientX {
				continue
			}
			f.Unit = "kayıt"
			f.Text = withLabel(fmt.Sprintf("%s Kafka tüketici lag %.0f (önceki %.0f, %s)", r.Service, r.Now, r.Prev, xs(rat)), r.Label)
		default:
			continue
		}
		out = append(out, f)
	}
	return out
}
