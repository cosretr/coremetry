package anomaly

// red_vm_select.go — v0.10.1478: servis × metrik başına RED kaynağı seçimi +
// span toplu okumasının ÜSTÜNE VM serisinin bindirilmesi + kanıt satırı.
//
// SÖZLEŞME (aiops — tek kaynak kuralı): bir (servis, metrik) çiftinin ardışık
// serisi, hacim serisi, mevsimsel tabanı ve mevsimsel hacmi AYNI kaynaktan
// gelir. VM seçildiyse span'in o çiftteki dört serisi de SİLİNİR/EZİLİR —
// örneklenmiş span error_rate'i VM'in tabanına ya da cari değerine ASLA
// karışmaz (TestOverlayNeverMixesSampledSpanErrorRate). VM'in mevsimsel tabanı
// yetersizse (gün çeşitliliği) span'inkine değil, VM'in ardışık penceresine
// düşülür (chooseBaseline'ın bugünkü yedeği).
//
// Seçim (pickREDSource, SAF, tablo testli):
//
//	kaynak ayarı span             → span ("kaynak ayarı span")
//	VM yapılandırılmamış          → span
//	servisin HTTP/gRPC ailesi yok → span
//	aile okunamadı / ısınıyor     → span (gerekçeli)
//	metrik ölçülemiyor            → span (ör. durum etiketi yok → error_rate)
//	VM serisi yetersiz            → span (yeni enstrümantasyon: span geçmişi uzun)
//	aksi                          → VM
//
// Batch yük kapısı / dwell / tabanlar / terfi kapısı DEĞİŞMEZ: kapılar
// bindirilmiş seriyi okur (hacim VM'de gerçek istek/sn).

import (
	"math"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	redSourceVM   = "vm"
	redSourceSpan = "span"
)

// Gerekçe metinleri (kanıt + durum ucu).
const (
	redReasonSpanSetting   = "kaynak ayarı span"
	redReasonNotConfigured = "VictoriaMetrics yapılandırılmamış"
	redReasonNoFamily      = "OTel HTTP/gRPC sunucu histogramı VictoriaMetrics'te yok"
	redReasonNoService     = "servisin HTTP/gRPC sunucu histogramı serisi yok"
	redReasonVMError       = "VictoriaMetrics okunamadı (okunamadı ≠ yok)"
	redReasonWarming       = "VictoriaMetrics geçmişi ısınıyor"
	redReasonNoStatus      = "durum kodu etiketi yok — hata oranı VM'den ölçülemez"
	redReasonShort         = "VictoriaMetrics serisi yetersiz (taban + dwell kovası yok)"
)

// vmStrictMetric — VM'de serisi olan serviste örneklenmiş span'in HİÇ karar
// vermediği metrik (operatör 2026-10-24: "VM serisi varken örneklenmiş span
// karar kaynağı olmasın"). Kuyruk örneklemesi hata oranını politikayla
// oynatır (hatalar hep tutulur + ~%5 taban); VM okunamadığında / ısınırken
// span'e düşmek, kaynağın tik tik VM ↔ span arasında salınması ve rampanın
// örneklenmiş seride kaybolması demekti. Böyle bir tikte çift BEKLETİLİR
// (Hold): yeni karar yok, açık satır yalnız tazelenir. Yalnız servis VM'de
// durum etiketi TAŞIMIYORSA (hata oranı VM'den ölçülemez) span karar verir.
const vmStrictMetric = "error_rate"

// vmStrict — v0.10.1534 (Problem standardı R6, D12: "VM-karar kuralı p99_ms /
// request_rate'e genişler"). VM'in bildiği serviste örneklenmiş span üç RED
// metriğinin HİÇBİRİNDE karar vermez: VM o tik karar veremiyorsa (ısınma,
// okuma hatası, kısa seri) çift bekletilir (Hold). p99 kuyruk örneklemesinde
// yavaş istekleri tutar (hatalar gibi), istek hızı örnekleme oranıyla oynar —
// error_rate'in gerekçesinin aynısı. Tek istisna yalnız error_rate'te: servisin
// VM'de durum etiketi yoksa hata oranı VM'den ölçülemez → span.
func vmStrict(metric string) bool {
	switch metric {
	case vmStrictMetric, "p99_ms", "request_rate":
		return true
	}
	return false
}

// redSourceUsed — çiftin kaynağı (kanıt).
type redSourceUsed struct {
	VM bool
	// Hold — VM bu servisi biliyor ama bu tik karar veremiyor (ısınma /
	// okuma hatası / kısa seri): span'e DÜŞÜLMEZ (vmStrictMetric), karar yok.
	Hold   bool
	Family string // VM: "HTTP" | "gRPC"
	Reason string // span: neden VM değil ("" = VM hiç denenmedi değil, ayar)
}

// redSourceEvidenceTR — SAF: Problem açıklamasına / olay ayrıntısına eklenen
// kaynak satırı. Operatörün prod'da arayacağı metin budur. v0.10.1523 (Problem
// standardı R1): işaret dizgileri chstore'da TEK tanım — StandardizeProblem
// aynı sabitlerle kaynağı geri okur (chstore.ParseEvidenceSource).
func redSourceEvidenceTR(u redSourceUsed) string {
	if u.Hold {
		return chstore.REDEvidenceVMHold + u.Reason
	}
	if u.VM {
		s := chstore.REDEvidenceVM
		if u.Family != "" {
			s += " · " + u.Family + " sunucu histogramı"
		}
		return s
	}
	s := chstore.REDEvidenceSpan
	if r := shownSourceReason(u.Reason); r != "" {
		s += " — VM kullanılmadı: " + r
	}
	return s
}

// shownSourceReason — SAF (operatör 2026-10-24): VM HİÇ yapılandırılmamış
// kurulumda "VictoriaMetrics yapılandırılmamış" gerekçesi gürültüdür — her
// kanıt satırında / rozette tekrarlanan, operatörün bilinçli tercihi. Yalnız
// O gerekçe gizlenir; "kaynak: span (örneklenmiş)" etiketi kalır. VM
// yapılandırılmış ama kullanılamamışsa (okunamadı, seri yok, aile yok, kaynak
// ayarı span …) gerekçe GÖRÜNÜR kalır — orada operatörün bilmesi gereken bir
// sapma var. Durum ucu (REDSourceStats / AlertVMStats) gerekçeyi aynen taşır.
func shownSourceReason(reason string) string {
	if reason == redReasonNotConfigured {
		return ""
	}
	return reason
}

// withREDEvidence — SAF: açıklamaya kaynak satırı (boşsa aynen).
func withREDEvidence(desc, ev string) string {
	if ev == "" {
		return desc
	}
	return desc + " " + ev + "."
}

// vmPickInput — seçim kararının girdileri.
type vmPickInput struct {
	SpanSetting  bool
	Configured   bool
	FamilyFound  bool   // servis bir ailede VAR (ardışık pencerede istek)
	FamilyReason string // aile bulunamadıysa en anlamlı gerekçe
	MetricOK     bool
	MetricReason string
	Enough       bool // enoughHistory(VM serisi)
}

// pickREDSource — SAF: VM mi span mi + gerekçe.
func pickREDSource(in vmPickInput) (bool, string) {
	switch {
	case in.SpanSetting:
		return false, redReasonSpanSetting
	case !in.Configured:
		return false, redReasonNotConfigured
	case !in.FamilyFound:
		if in.FamilyReason == "" {
			return false, redReasonNoService
		}
		return false, in.FamilyReason
	case !in.MetricOK:
		return false, in.MetricReason
	case !in.Enough:
		return false, redReasonShort
	}
	return true, ""
}

// vmREDView — tikin VM görünümü (prepare üretir; okuma tik goroutine'inde).
type vmREDView struct {
	mode          string
	configured    bool
	reason        string
	store         *vmREDStore
	meta          []vmFamMeta
	vnow          time.Time
	upper         int64
	consec        []int64
	seasonal      []int64
	seasonalClass string
	behavior      []int64
	readyDet      [vmFamN][vmKindN]bool
	// readyConsec — yalnız ardışık 24 sa kovaları hazır (mevsimsel eksik
	// olabilir). vmStrictMetric bununla VM'de kalır: mevsimsel taban yoksa VM'in
	// ardışık penceresi kullanılır, span'inki DEĞİL.
	readyConsec [vmFamN][vmKindN]bool
	readyBeh    [vmFamN][vmKindN]bool
	failed      [vmFamN][vmKindN]bool // son okuma hata verdi (geri çekilme sürüyor)
	// ölçüm
	queries, queryErrs, bucketsFetched, pending int
	lastErr                                     string
}

// vmSeries — bir çiftin VM serileri (span toplu okumasıyla aynı şekil).
type vmSeries struct {
	Family                  string
	Values, Rates           []float64
	Seasonal, SeasonalRates []float64
}

// redSeriesProvider — overlay'in kaynağı (görünüm ya da test sahtesi).
type redSeriesProvider interface {
	seriesFor(service, metric string, dwell int) (vmSeries, redSourceUsed)
}

// famFor — servisin ailesi: sıradaki ilk aile ki kapsamı kullanılabilir,
// istek türü hazır ve servisin ardışık pencerede en az bir isteği var.
// Aileler ASLA toplanmaz. ok=false → gerekçe.
func (v *vmREDView) famFor(service string) (int, string) { return v.famForMode(service, false) }

// famForMode — consecOnly: istek türünün yalnız ARDIŞIK kovaları hazır olsa
// yeter (vmStrictMetric; mevsimsel ısınması VM'den çıkarmaz).
func (v *vmREDView) famForMode(service string, consecOnly bool) (int, string) {
	reason := ""
	for fi := range vmREDFamilies {
		if fi >= len(v.meta) {
			break
		}
		m := v.meta[fi]
		if !m.ok {
			if reason == "" && m.reason != "" && m.reason != redReasonNoFamily {
				reason = m.reason
			}
			continue
		}
		ready := v.readyDet[fi][vmKindCount]
		if consecOnly {
			ready = v.readyConsec[fi][vmKindCount]
		}
		if !ready {
			reason = v.notReadyReason(fi, vmKindCount)
			continue
		}
		for _, b := range v.consec {
			if c, ok := v.store.get(fi, vmKindCount, b, service); ok && c > 0 {
				return fi, ""
			}
		}
		if reason == "" {
			reason = redReasonNoService
		}
	}
	if reason == "" {
		reason = redReasonNoFamily // hiçbir aile VM'de yok
	}
	return -1, reason
}

// vmKnows — VM bu servisi biliyor mu: kullanılabilir bir ailede, BELLEKTEKİ
// ardışık kovalardan birinde isteği var (hazır olmasa da). vmStrictMetric'in
// "span'e düşme" kapısı: ısınma / okuma hatası sırasında bile servisin VM'de
// olduğu bellekten görülebiliyorsa karar span'e devredilmez.
func (v *vmREDView) vmKnows(service string) bool {
	for fi := range vmREDFamilies {
		if fi >= len(v.meta) || !v.meta[fi].ok {
			continue
		}
		for _, b := range v.consec {
			if c, ok := v.store.get(fi, vmKindCount, b, service); ok && c > 0 {
				return true
			}
		}
	}
	return false
}

// vmServices — SAF-ımsı: son ardışık pencerede kullanılabilir bir ailede
// isteği olan servisler (sıralı). scan bunları span MV'sinin aktif servis
// listesine BİRLEŞTİRİR: VM'de olup span'de olmayan (ya da span adı farklı)
// servis önceden HİÇ değerlendirilmiyordu.
func (v *vmREDView) vmServices() []string {
	if v == nil || v.mode == "span" || !v.configured || v.store == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for fi := range vmREDFamilies {
		if fi >= len(v.meta) || !v.meta[fi].ok || !v.readyConsec[fi][vmKindCount] {
			continue
		}
		for _, b := range v.consec {
			for _, svc := range v.store.servicesAt(fi, vmKindCount, b) {
				if !seen[svc] {
					seen[svc] = true
					out = append(out, svc)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// mergeServices — SAF: span listesine VM-yalnız servisleri ekler (sıra: span
// listesi aynen, sonra eksikler). Kaç tane eklendiği döner (log / durum).
func mergeServices(span, vm []string) ([]string, int) {
	if len(vm) == 0 {
		return span, 0
	}
	have := make(map[string]bool, len(span))
	for _, s := range span {
		have[s] = true
	}
	out := append([]string(nil), span...)
	added := 0
	for _, s := range vm {
		if !have[s] {
			out = append(out, s)
			added++
		}
	}
	return out, added
}

// metricOK — metriğin bu ailede ölçülebilirliği.
func (v *vmREDView) metricOK(fi int, service, metric string) (bool, string) {
	need := []vmREDKind{vmKindCount}
	switch metric {
	case "error_rate":
		m := v.meta[fi]
		if len(m.scope.StatusLabels) == 0 {
			return false, redReasonNoStatus
		}
		if m.statusSvc == nil {
			return false, redReasonVMError + ": durum etiketi kümesi"
		}
		if !m.statusSvc[service] {
			return false, redReasonNoStatus
		}
		need = append(need, vmKindErr)
	case "p99_ms":
		need = append(need, vmKindP99)
	}
	for _, k := range need {
		ready := v.readyDet[fi][k]
		if vmStrict(metric) { // v0.10.1534 — mevsimsel ısınma VM'den çıkarmaz
			ready = v.readyConsec[fi][k]
		}
		if !ready {
			return false, v.notReadyReason(fi, k)
		}
	}
	return true, ""
}

// notReadyReason — hazır olmayan türün gerekçesi: okuma hatası mı ısınma mı.
func (v *vmREDView) notReadyReason(fi int, k vmREDKind) string {
	if v.failed[fi][k] {
		return redReasonVMError + " (" + k.String() + ")"
	}
	return redReasonWarming + " (" + k.String() + ")"
}

// vmBucketValue — SAF: kovanın metrik değeri + hacmi; ok=false = satır yok
// (span MV'sinde GROUP BY satırı olmayan kovanın ikizi: istek yok).
func (v *vmREDView) vmBucketValue(fi int, service, metric string, b int64) (val, rate float64, ok bool) {
	c, ok := v.store.get(fi, vmKindCount, b, service)
	if !ok || c <= 0 {
		return 0, 0, false
	}
	rate = c / float64(bucketSeconds)
	switch metric {
	case "error_rate":
		e, _ := v.store.get(fi, vmKindErr, b, service) // seri yok = 0 hata (durum etiketi servisçe var)
		if e < 0 {
			e = 0
		}
		return math.Min(e/c*100, 100), rate, true
	case "request_rate":
		return rate, rate, true
	case "p99_ms":
		p, ok := v.store.get(fi, vmKindP99, b, service)
		if !ok {
			return 0, 0, false
		}
		return p, rate, true
	}
	return 0, 0, false
}

// seriesFor — redSeriesProvider.
func (v *vmREDView) seriesFor(service, metric string, dwell int) (vmSeries, redSourceUsed) {
	in := vmPickInput{SpanSetting: v.mode == "span", Configured: v.configured}
	if in.SpanSetting || !in.Configured {
		_, r := pickREDSource(in)
		return vmSeries{}, redSourceUsed{Reason: r}
	}
	strict := vmStrict(metric) // v0.10.1534 (D12)
	fi, freason := v.famForMode(service, strict)
	in.FamilyFound, in.FamilyReason = fi >= 0, freason
	if !in.FamilyFound {
		_, r := pickREDSource(in)
		if strict && v.vmKnows(service) {
			return vmSeries{}, redSourceUsed{Hold: true, Reason: r}
		}
		return vmSeries{}, redSourceUsed{Reason: r}
	}
	in.MetricOK, in.MetricReason = v.metricOK(fi, service, metric)
	var s vmSeries
	if in.MetricOK {
		s = v.buildSeries(fi, service, metric, dwell)
	}
	in.Enough = enoughHistory(len(s.Values), dwell)
	useVM, r := pickREDSource(in)
	if !useVM {
		// Durum etiketi yoksa hata oranı VM'den ÖLÇÜLEMEZ — span'in tek dürüst
		// kullanım yeri. Diğer her gerekçe (ısınma, okuma hatası, kısa seri)
		// bekletir.
		if strict && r != redReasonNoStatus {
			return vmSeries{}, redSourceUsed{Hold: true, Reason: r}
		}
		return vmSeries{}, redSourceUsed{Reason: r}
	}
	return s, redSourceUsed{VM: true, Family: vmREDFamilies[fi].Label}
}

// buildSeries — span okumalarının VM ikizi: ardışık seri (istekli kovalar,
// artan zaman, kuyruk sessizliği + dwell ızgarası sıfırla — fillDwellGrid,
// v0.10.1585) + mevsimsel
// örnekler (gün çeşitliliği budaması fetchAllSeasonal ile aynı).
func (v *vmREDView) buildSeries(fi int, service, metric string, dwell int) vmSeries {
	s := vmSeries{Family: vmREDFamilies[fi].Label}
	var times []int64
	for _, b := range v.consec {
		val, rate, ok := v.vmBucketValue(fi, service, metric, b)
		if !ok {
			continue
		}
		s.Values = append(s.Values, val)
		s.Rates = append(s.Rates, rate)
		times = append(times, b)
	}
	s.Values, s.Rates = fillDwellGrid(s.Values, s.Rates, times, v.upper, dwell)
	days := map[int64]struct{}{}
	var seas, seasRates []float64
	for _, b := range v.seasonal {
		val, rate, ok := v.vmBucketValue(fi, service, metric, b)
		if !ok {
			continue
		}
		seas = append(seas, val)
		seasRates = append(seasRates, rate)
		days[b/86400] = struct{}{}
	}
	if len(days) >= seasonalMinDaysFor(v.seasonalClass) && v.seasonalReady(fi, metric) {
		s.Seasonal, s.SeasonalRates = seas, seasRates
	}
	return s
}

// seasonalReady — metriğin türlerinin mevsimsel kovaları da tam mı. Tam
// değilse mevsimsel taban KULLANILMAZ (eksik slotlu taban yalan söyler);
// overlay span'in mevsimselini de siler → VM ardışık penceresi.
func (v *vmREDView) seasonalReady(fi int, metric string) bool {
	need := []vmREDKind{vmKindCount}
	switch metric {
	case "error_rate":
		need = append(need, vmKindErr)
	case "p99_ms":
		need = append(need, vmKindP99)
	}
	for _, k := range need {
		if !v.readyDet[fi][k] {
			return false
		}
	}
	return true
}

// overlayREDSeries — SAF (sağlayıcı verilince): span toplu okumasının
// haritalarına VM serisini bindirir. Döner: "servis|metrik" → kaynak.
// seasonalRates YALNIZ batch gecikme metriği için taşınır (fetchAllSeasonal
// sözleşmesi).
func overlayREDSeries(p redSeriesProvider, services, tracked []string, dwell int,
	buckets, seasonal, rates, seasonalRates map[string]map[string][]float64) map[string]redSourceUsed {
	used := make(map[string]redSourceUsed, len(services)*len(tracked))
	ensure := func(m map[string]map[string][]float64, metric string) map[string][]float64 {
		if m[metric] == nil {
			m[metric] = map[string][]float64{}
		}
		return m[metric]
	}
	for _, svc := range services {
		for _, metric := range tracked {
			s, u := p.seriesFor(svc, metric, dwell)
			used[svc+"|"+metric] = u
			if u.Hold {
				// Bekletilen çift: span serileri SİLİNİR (karar verilmez; scan
				// açık satırı yalnız tazeler).
				for _, m := range []map[string]map[string][]float64{buckets, rates, seasonal, seasonalRates} {
					if m[metric] != nil {
						delete(m[metric], svc)
					}
				}
				continue
			}
			if !u.VM {
				continue
			}
			ensure(buckets, metric)[svc] = s.Values
			ensure(rates, metric)[svc] = s.Rates
			if s.Seasonal != nil {
				ensure(seasonal, metric)[svc] = s.Seasonal
			} else if seasonal[metric] != nil {
				delete(seasonal[metric], svc) // span tabanı VM cari değerine karışmasın
			}
			if metric == batchLatencyMetric && s.SeasonalRates != nil {
				ensure(seasonalRates, metric)[svc] = s.SeasonalRates
			} else if seasonalRates[metric] != nil {
				delete(seasonalRates[metric], svc)
			}
		}
	}
	return used
}

// behaviorRowsFor — davranış motorunun VM satırları: üç tür de hazır, servis
// durum etiketi taşıyor (hata oranı ölçülebilir) ve aile seçilmiş olmalı.
// ok=false → span satırları kalır.
func (v *vmREDView) behaviorRowsFor(service string) ([]behaviorRow, redSourceUsed, bool) {
	if v == nil || v.mode == "span" || !v.configured || len(v.behavior) == 0 {
		return nil, redSourceUsed{}, false
	}
	fi, _ := v.famFor(service)
	if fi < 0 {
		return nil, redSourceUsed{}, false
	}
	m := v.meta[fi]
	if len(m.scope.StatusLabels) == 0 || m.statusSvc == nil || !m.statusSvc[service] {
		return nil, redSourceUsed{}, false
	}
	for k := vmREDKind(0); k < vmKindN; k++ {
		if !v.readyBeh[fi][k] {
			return nil, redSourceUsed{}, false
		}
	}
	var rows []behaviorRow
	for _, b := range v.behavior {
		c, ok := v.store.get(fi, vmKindCount, b, service)
		spans := uint64(math.Round(c))
		if !ok || spans == 0 {
			continue
		}
		e, _ := v.store.get(fi, vmKindErr, b, service)
		errs := uint64(math.Round(math.Max(e, 0)))
		if errs > spans {
			errs = spans
		}
		p99, _ := v.store.get(fi, vmKindP99, b, service)
		rows = append(rows, behaviorRow{Unix: b, HOW: hourOfWeek(time.Unix(b, 0)), Spans: spans, Errs: errs, P99Ms: p99})
	}
	if len(rows) == 0 {
		return nil, redSourceUsed{}, false
	}
	return rows, redSourceUsed{VM: true, Family: vmREDFamilies[fi].Label}, true
}
