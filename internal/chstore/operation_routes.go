package chstore

// operation_routes.go — v0.10.1023. Operatör bildirimi: "Operation
// kısmında POST GET neden detail gözükmüyor, sonra trace'e girince
// çıkıyor." Servis sayfasının Operations sekmesi (Raw kip) satırlarını
// operation_summary_5m'den okur ve o MV'nin rota boyutu YOK (GROUP BY
// name): http.route'suz enstrümantasyonda span adı yalnız fiildir, satır
// çıplak "GET" / "POST" görünür. Traces listesi ise v0.10.756'dan beri kök
// span'ın http_route'unu ekleyip "GET /metrics" basıyor — aynı span iki
// sayfada iki farklı adla.
//
// Bu dosya Operations sekmesine ÖZEL bir zenginleştirme okuması: adı çıplak
// fiil olan span'leri spanmetrics_1m'den (fiil, rota) başına böler. Bundle'ın
// operations dilimi ve GetOperationSummary DEĞİŞMEZ — copilot, SpanDetail
// taban çizgisi, ProblemDetail, Overview OpsCard ve anomali incelemesi ham
// adı birebir eşliyor. Değiştirme (çıplak satır → rota satırları) tarayıcıda,
// yalnız tabloda yapılır (frontend pages/service/operationRoutes.ts). `name`
// gerçek span adı olarak kalır; rota ayrı `route` alanında taşınır.
//
// Neden operation_summary_5m'e rota boyutu DEĞİL: MV'ye boyut eklemek DROP +
// RECREATE (clickhouse-schema §5) ve operations okuyucularının hepsinde satır
// kimliğinin değişmesi demek. spanmetrics_1m rotayı zaten taşıyor; bedeli
// forward-only olması ve 30 günlük TTL — kapsamadığı pencerede bölme yapılmaz
// (covered=false), tablo bugünkü gibi görünür.
//
// Sayılar çıplak satırla karşılaştırılabilir olsun diye pencere ops MV
// okumasıyla (queryOperationsFromMV) AYNI hizalanır: alt uç 5 dk'ya aşağı,
// üst uç `to`'nun dakikası (ayrıntı planOperationRoutes'ta). Sparkline
// ızgarası da aynı sparklineGrid(winSec, 300) ve aynı köken — tablonun "All"
// satırı serileri eleman eleman topluyor ve tek ortak ızgara varsayıyor. Slot
// genişliği 300'ün katı ve köken 5 dk sınırında olduğundan bir 5 dk kovasının
// 1 dk kovaları SQL'deki aynı intDiv ile o kovanın slotuna düşer.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// BareHTTPMethods — çıplak HTTP fiili kümesinin Go tarafındaki TEK yazımı
// (v0.10.1023). Önceden üç kopya vardı: templater.httpMethods (ingest
// op_group), trace_health.go'nun SQL regex'i ve frontend
// lib/opDisplayName.ts. trace_health regex'i artık bu listeden türer;
// templater (chstore'u import ettiği için tersi olamaz) ve frontend kümesiyle
// eşitlik testle pinli: templater/http_methods_pin_test.go,
// operation_routes_test.go. Büyük harf, birebir: OTel HTTP fiillerini büyük
// harfle yazar; SQL'deki IN listesi de harf-duyarlı.
var BareHTTPMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "TRACE", "CONNECT"}

// BareVerbRouteLimit — (fiil, rota) satır tavanı. Çağıran (api) tam bu
// sayıda satırı "kesilmiş olabilir" sayar ve tarayıcı o yanıtla bölme yapmaz:
// yarım bir bölme "All" toplamını sessizce eksiltirdi.
const BareVerbRouteLimit = 300

// opRoutesPlan — bir pencerenin spanmetrics_1m okuma planı (SAF hesap).
type opRoutesPlan struct {
	useMV     bool      // ops okuması MV yolunda mı (operationsUseMV); değilse bölme yok
	start     time.Time // ops: bucketStart = winStart 5 dk'ya aşağı
	scanEnd   time.Time // 1 dk kovalar için DIŞLAYICI üst sınır = ceil1m(to saniyeye aşağı)
	bucketSec int64     // sparkline slot genişliği (300'ün katı)
	n         int       // slot sayısı
}

// planOperationRoutes — SAF: queryOperationsFromMV'nin pencere + ızgara
// hesabının spanmetrics_1m'ye çevrilmiş hâli.
//
//   - Uç SANİYEYE aşağı (v0.10.1023 inceleme R3): ops okuması winEnd'i
//     konumsal `?` ile bağlar ve clickhouse-go onu SANİYE hassasiyetinde
//     yazar (bindPositional → format(tz, Seconds, v) → value.Unix(), taban).
//     `to` = B + 400 ms (B 5 dk sınırı) iken ops `time_bucket < B` görür ve B
//     kovasını DIŞLAR; tam hassasiyetle hesaplayan plan onu içeri alıyordu.
//     Alt uç (bucketStart) zaten tam 5 dk sınırı — tabanlama onu değiştirmez.
//   - Tarama `to`'nun dakikasında BİTER (R4): ops `time_bucket < winEnd` ile
//     `to`'yu içeren 5 dk kovasını TAM alır, ama bundle o anda hesaplandı —
//     kovanın `to`'dan sonraki dakikaları o an boştu. Rota okuması sonradan
//     (sekme açılınca) koşar; 5 dk kovanın sonuna dek okusaydı bundle'dan
//     sonra gelen trafiği sayardı (15 dk pencerede %20-30'a varan şişme).
//     Bu yüzden 1 dk kovalardan yalnız başlangıcı `to`'dan ÖNCE olanlar:
//     [bucketStart, ceil1m(to)). ceil1m ≤ ceil5 olduğundan ops'un aldığı
//     kovaların dışına hiç taşmaz.
//   - Izgara ops'la birebir: winSec = bucketStart → to (saniye tabanlı; ops'un
//     `int64(winEnd.Sub(bucketStart).Seconds())` hesabıyla aynı tamsayı),
//     sparklineGrid(winSec, 300). Son slot kısmen dolu kalır — bundle'ın
//     kendi okumasında da koştuğu an öyleydi.
//
// Pencere 5 dk'dan kısaysa ops ham spans yoluna düşer (1 sn ızgara, hizasız
// köken) — o ızgarayı 1 dk kovalarla kurmak mümkün değil, bölme yok. Bu kapı
// ops'taki gibi TAM hassasiyetli süreye bakar (operationsUseMV Go'da koşar).
func planOperationRoutes(winStart, winEnd time.Time) opRoutesPlan {
	p := opRoutesPlan{useMV: operationsUseMV(winEnd.Sub(winStart), "")}
	if !p.useMV {
		return p
	}
	endSec := winEnd.Truncate(time.Second)
	p.start = winStart.Truncate(5 * time.Minute)
	p.scanEnd = endSec.Add(-time.Nanosecond).Truncate(time.Minute).Add(time.Minute)
	winSec := int64(endSec.Sub(p.start).Seconds())
	p.bucketSec, p.n = sparklineGrid(winSec, 300)
	return p
}

// OperationRoutesGridKey — SAF: planın önbellek anahtarı parçası. api'nin
// anahtarı pencere kovasının (cacheBucket, 30 sn) YANINDA bunu da taşır:
// aynı 30 sn hücresine düşen iki `to`, tam 5 dk sınırında ve hemen sonrasında
// olabilir; ikisi farklı ızgara (n, scanEnd) üretir ve biri ötekinin
// cevabını alırsa seriler ops satırlarının ızgarasından bir slot kayardı.
// Plandan türediği için saniye tabanlamasını (R3) da taşır: B ile B+400 ms
// aynı okumayı yapar, aynı anahtarı alır.
func OperationRoutesGridKey(winStart, winEnd time.Time) string {
	p := planOperationRoutes(winStart, winEnd)
	if !p.useMV {
		return "mv=0"
	}
	return fmt.Sprintf("mv=1:s=%d:e=%d:b=%d:n=%d", p.start.Unix(), p.scanEnd.Unix(), p.bucketSec, p.n)
}

// opRoutesCovered — SAF: spanmetrics_1m pencerenin başını kapsıyor mu
// (periodSpanmetricsGate'in kapsama yarısı). Sıfır kapsama = bilinmiyor →
// kapsamıyor; spanmetricsCoverageStart hata/boşta now() döner, o da
// kapsamıyor sayılır. Kapsamayan pencerede bölme YAPILMAZ: forward-only MV
// pencerenin başını görmüyorsa rota satırlarının toplamı çıplak satırdan
// eksik çıkardı.
func opRoutesCovered(coverage, need time.Time) bool {
	return !coverage.IsZero() && !need.Before(coverage)
}

// bareVerbINList — SAF: BareHTTPMethods'tan SQL IN listesi. Değerler sunucu
// sabiti (kullanıcı girdisi değil) ve testte ^[A-Z]+$ ile pinli — literal
// gömmek enjeksiyon-güvenli ve şekil testinde listenin kendisi görünür.
func bareVerbINList() string {
	q := make([]string, len(BareHTTPMethods))
	for i, m := range BareHTTPMethods {
		q[i] = "'" + m + "'"
	}
	return strings.Join(q, ", ")
}

// bareVerbRouteOpsSQL — SAF (şekil testi: operation_routes_test.go).
// İki seviye, GetEndpointsMV deseni: per_slot CTE'si (ad, rota, slot) başına
// sayaçları + slotun kendi yüzdeliklerini + birleştirilebilir tdigest
// durumunu çıkarır; dış SELECT pencere skalerlerini (gerçek pencere
// yüzdelikleri — slot yüzdeliklerinin maksimumu DEĞİL) ve slot dizilerini
// verir. Diziler Go'da sabit n uzunluğa yayılır (assembleRouteRows).
// kind ve status_code üzerinden birleşir: çıplak satır da (operation_summary_5m)
// ikisini ayırmıyor. tdigest 4 genişlikli (0.5, 0.9, 0.95, 0.99): indeks
// 1 = p50, 3 = p95, 4 = p99 (quantile_ordinal_test.go).
//
// Argüman sırası: slot kökeni (unix sn), slot genişliği, servis, alt sınır,
// üst sınır.
func bareVerbRouteOpsSQL(src string) string {
	return `
		WITH per_slot AS (
		  SELECT name,
		         http_route,
		         toInt64(intDiv(toInt64(toUnixTimestamp(time_bucket)) - ?, ?))      AS b,
		         countMerge(calls_state)                                            AS c,
		         countMerge(error_state)                                            AS e,
		         sumMerge(duration_sum_state)                                       AS dur_sum,
		         quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state)      AS q_slot,
		         quantilesTDigestMergeState(0.5, 0.9, 0.95, 0.99)(duration_q_state) AS q_state
		  FROM ` + src + `
		  WHERE service_name = ?
		    AND name IN (` + bareVerbINList() + `)
		    AND time_bucket >= ? AND time_bucket < ?
		  GROUP BY name, http_route, b
		)
		SELECT name,
		       http_route                                                                        AS route,
		       sum(c)                                                                            AS calls,
		       sum(e)                                                                            AS errors,
		       sum(dur_sum) / nullIf(sum(c), 0) / 1e6                                            AS avg_ms,
		       arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(q_state) AS q, 1) / 1e6 AS p50_ms,
		       arrayElement(q, 3) / 1e6                                                          AS p95_ms,
		       arrayElement(q, 4) / 1e6                                                          AS p99_ms,
		       groupArray(b)                                                                     AS slot_idx,
		       groupArray(c)                                                                     AS slot_calls,
		       groupArray(e)                                                                     AS slot_errors,
		       groupArray(dur_sum / 1e6)                                                         AS slot_sum_ms,
		       groupArray(arrayElement(q_slot, 1) / 1e6)                                         AS slot_p50,
		       groupArray(arrayElement(q_slot, 3) / 1e6)                                         AS slot_p95,
		       groupArray(arrayElement(q_slot, 4) / 1e6)                                         AS slot_p99
		FROM per_slot
		GROUP BY name, http_route
		ORDER BY calls DESC, name ASC, route ASC
		LIMIT ` + fmt.Sprint(BareVerbRouteLimit) + `
		SETTINGS max_execution_time = 15, ` + mvQuantileMemSettings
}

// routeRowRaw — bareVerbRouteOpsSQL'in bir satırı, taranmış hâli.
type routeRowRaw struct {
	name, route         string
	calls, errors       uint64
	avgMs, p50, p95     *float64
	p99                 *float64
	slotIdx             []int64
	slotCalls, slotErrs []uint64
	slotSumMs           []float64
	slotP50, slotP95    []float64
	slotP99             []float64
}

// finiteOr0 — slot dizilerindeki NaN/Inf'i (boş tdigest) JSON'a sokmaz.
func finiteOr0(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// assembleRouteRows — SAF: taranmış satırları OperationSummary'ye çevirir;
// slot dizilerini queryOperationsFromMV'nin taşıdığı AYNI serilere yayar
// (calls / errors / p99 her satırda; avg / p50 / p95 yalnız ilk latSparkCap
// satırda — satırlar calls DESC gelir, ops okumasının sözleşmesi). Izgara
// dışı slot (olmaması gerekir; savunma) düşer. Slot yüzdelikleri slotun
// birleşik tdigest'idir; ops okuması 5 dk kovalarının yüzdeliklerinin slot
// içi maksimumunu alıyor — slot = 5 dk kova olan pencerelerde (≤10 sa) ikisi
// aynı şeydir, daha geniş pencerede rota satırının çizgisi biraz daha
// düşük okuyabilir. Apdex okunmuyor (tabloda kolon yok, v0.9.69): sıfır.
func assembleRouteRows(raw []routeRowRaw, n int) []OperationSummary {
	out := make([]OperationSummary, 0, len(raw))
	for i, r := range raw {
		o := OperationSummary{
			Name:       r.name,
			Route:      r.route,
			SpanCount:  r.calls,
			ErrorCount: r.errors,
			AvgMs:      safeF(r.avgMs),
			P50Ms:      safeF(r.p50),
			P95Ms:      safeF(r.p95),
			P99Ms:      safeF(r.p99),
		}
		if r.calls > 0 {
			o.ErrorRate = float64(r.errors) / float64(r.calls) * 100
		}
		if n > 0 {
			wantLat := i < latSparkCap
			o.Sparkline = make([]uint64, n)
			o.ErrorsSparkline = make([]uint64, n)
			o.P99Sparkline = make([]float64, n)
			var sums []float64
			if wantLat {
				o.AvgSparkline = make([]float64, n)
				o.P50Sparkline = make([]float64, n)
				o.P95Sparkline = make([]float64, n)
				sums = make([]float64, n)
			}
			m := len(r.slotIdx)
			for _, l := range []int{len(r.slotCalls), len(r.slotErrs), len(r.slotSumMs), len(r.slotP50), len(r.slotP95), len(r.slotP99)} {
				if l < m {
					m = l
				}
			}
			for j := 0; j < m; j++ {
				b := r.slotIdx[j]
				if b < 0 || b >= int64(n) {
					continue
				}
				o.Sparkline[b] += r.slotCalls[j]
				o.ErrorsSparkline[b] += r.slotErrs[j]
				if v := round2(finiteOr0(r.slotP99[j])); v > o.P99Sparkline[b] {
					o.P99Sparkline[b] = v
				}
				if wantLat {
					sums[b] += finiteOr0(r.slotSumMs[j])
					if v := round2(finiteOr0(r.slotP95[j])); v > o.P95Sparkline[b] {
						o.P95Sparkline[b] = v
					}
					if v := round2(finiteOr0(r.slotP50[j])); v > o.P50Sparkline[b] {
						o.P50Sparkline[b] = v
					}
				}
			}
			if wantLat {
				for b := 0; b < n; b++ {
					if o.Sparkline[b] > 0 {
						o.AvgSparkline[b] = round2(sums[b] / float64(o.Sparkline[b]))
					}
				}
			}
		}
		out = append(out, o)
	}
	return out
}

// GetBareVerbRouteOperations — v0.10.1023: servisin çıplak HTTP fiili adlı
// operasyonlarının (fiil, http_route) kırılımı, spanmetrics_1m'den. Satır
// başına sayılar + gerçek pencere p50/p95/p99 + OperationSummary'nin ham
// satırlarda taşıdığı sparkline'lar, ops okumasının ızgarasında. Rota'sız
// span'ler `route: ""` artık satırı olarak gelir.
//
// covered=false (sorgu KOŞMADAN, nil satır): pencere 5 dk'dan kısa (ops ham
// yolda, ızgara eşleşmez) ya da spanmetrics_1m pencerenin başını kapsamıyor.
// env burada YOK: MV'de deploy_env boyutu yok; env'li istek api katmanında
// sorgusuz kısa devre olur.
func (s *Store) GetBareVerbRouteOperations(ctx context.Context, service string, from, to time.Time) ([]OperationSummary, bool, error) {
	if service == "" {
		return nil, false, fmt.Errorf("GetBareVerbRouteOperations: service required")
	}
	p := planOperationRoutes(from, to)
	if !p.useMV {
		return nil, false, nil
	}
	if !opRoutesCovered(s.spanmetricsCoverageStart(ctx), p.start) {
		return nil, false, nil
	}
	// telemetryReadConn: ops MV okumasıyla (repo.go) aynı havuz — aynı
	// tablonun iki yarısı farklı yoldan okunmasın.
	rows, err := s.telemetryReadConn().Query(ctx, bareVerbRouteOpsSQL(s.spanmetricsSourceFor("spanmetrics_1m")),
		p.start.Unix(), p.bucketSec, service, p.start, p.scanEnd)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	var raw []routeRowRaw
	for rows.Next() {
		var r routeRowRaw
		if err := rows.Scan(&r.name, &r.route, &r.calls, &r.errors,
			&r.avgMs, &r.p50, &r.p95, &r.p99,
			&r.slotIdx, &r.slotCalls, &r.slotErrs, &r.slotSumMs,
			&r.slotP50, &r.slotP95, &r.slotP99); err != nil {
			return nil, true, err
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, true, err
	}
	return assembleRouteRows(raw, p.n), true, nil
}
