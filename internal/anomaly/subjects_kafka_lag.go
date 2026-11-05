package anomaly

// subjects_kafka_lag.go — v0.10.1802 (Davis paritesi dilim 3): queue öznesinin
// VictoriaMetrics kaynaklı metriği `consumer_lag`. Bugüne dek Kafka lag'i
// yalnız statik kullanıcı kuralıyla (evaluator RuleTargetKafkaClient) izlenebiliyordu;
// burada mevsimsel/ardışık taban + yön kararı özne dedektörünün AYNI
// çekirdeğinden (decideSubjects → evaluateAnomaly) geçer.
//
// Özne: (sahip = tüketici servis, kimlik = chstore.QueueSubjectID("kafka", topic)).
// Seri: `max by (svc, topic) (max_over_time(<kafka.consumer.records_lag_max>[5m]))`
// — client_id × partition boyutu MAX ile katlanır (lag "en geride kalan
// partition" sorusudur, toplamı yanıltır).
//
// Seçim: `topk(N, max by (svc, topic) (avg_over_time(<lag>{topic!=""}[1h] @ A)))`
// — hacme değil son 1 sa'in ORTALAMA lag'ine göre üst-N (sistem tek: kafka,
// N = subjects.queue.topN). `@ A` seçimi SABİTLER: aralık sorgusunun her
// adımında ve her mevsimsel ofsette aynı küme (vmEndpointSelectExpr ikizi).
//
// Kova belleği / koşu planı / ofset birleşimi endpoint yolunun SAF parçaları
// (vmREDStore, vmRuns, vmGroupRuns, vmSplitForLen): tamamlanmış kova bir kez
// okunur; kararlı durumda tik başına ~2 aralık sorgusu (ardışığın yeni kovası +
// mevsimsel slot kenarı), ısınmada ~6-7, seçim 30 dk'da bir. Bütçe
// planSubjectBudget'ta: endpoint türü lag'in en az payını (kafkaLagVMMinQueries)
// bırakır.
//
// VM yapılandırılmamış ya da metrik yok (seçim boş) → sessiz atlama, gerekçe
// "vm_kafka_absent". messaging_summary_5m tabanlı queue metrikleri AYNEN kalır.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

const (
	// subjectMetricConsumerLag — özne metriği (olay deseni + terfi kapısı).
	subjectMetricConsumerLag = "consumer_lag"
	// kafkaLagOTelName — vmetrics/kafka.go kataloğu (gauge, agg max).
	kafkaLagOTelName   = "kafka.consumer.records_lag_max"
	kafkaLagTopicLabel = "topic"
	kafkaLagSystem     = "kafka"
	// kafkaLagVMMinQueries — türün bir tikte anlamlı iş yapması için en az
	// VM sorgusu: seçim + bir okuma grubu.
	kafkaLagVMMinQueries = 2
	// subjectSourceKafkaLag — subjectKindReq.Source / SubjectKindStats.Source.
	subjectSourceKafkaLag = "vm_kafka_lag"
	// subjectSkipVMKafkaAbsent — VM kapalı / lag metriği yok (sessiz atlama).
	subjectSkipVMKafkaAbsent = "vm_kafka_absent"
	// kafkaLagMinRecordsDefault — ayar okunamamış / sıfır ise taban.
	kafkaLagMinRecordsDefault = 1000
)

// kafkaLagSelector — SAF: lag metriğinin seçicisi (VM ad adayları + topic şartı).
func kafkaLagSelector() string {
	return "{" + vmetrics.NameSelector(vmetrics.NameCandidates("gauge", kafkaLagOTelName)) + ", " + kafkaLagTopicLabel + `!=""}`
}

// kafkaLagSelectExpr — SAF: üst-N (svc, topic), anchor anındaki 1 sa ortalama lag'e göre.
func kafkaLagSelectExpr(svcLabel string, topN int, anchor int64) string {
	if topN < 1 {
		topN = 1
	}
	return "topk(" + strconv.Itoa(topN) + ", max by (" + svcLabel + ", " + kafkaLagTopicLabel + ") (avg_over_time(" +
		kafkaLagSelector() + "[1h] @ " + strconv.FormatInt(anchor, 10) + ")))"
}

// kafkaLagExpr — SAF: tek ofsetin kova ifadesi, seçimle süzülmüş.
func kafkaLagExpr(svcLabel, sel string, off int64) string {
	by := svcLabel + ", " + kafkaLagTopicLabel
	return "(max by (" + by + ") (max_over_time(" + kafkaLagSelector() + fmt.Sprintf("[%ds]", vmStepS) + vmOffsetSuffix(off) +
		"))) and on (" + by + ") (" + sel + ")"
}

// kafkaLagQuery — SAF: ofsetlerin birleşik sorgusu (vmREDQuery ikizi, cm_off işaretli).
func kafkaLagQuery(svcLabel, sel string, offsets []int64) string {
	if len(offsets) == 0 || svcLabel == "" || sel == "" {
		return ""
	}
	parts := make([]string, 0, len(offsets))
	for _, off := range offsets {
		parts = append(parts, `label_set(`+kafkaLagExpr(svcLabel, sel, off)+`, "`+vmOffLabel+`", "`+strconv.FormatInt(off, 10)+`")`)
	}
	return strings.Join(parts, " or ")
}

// kafkaLagParseKey — SAF: grup anahtarı → depo anahtarı (servis + ayraç + topic) + ofset.
func kafkaLagParseKey(key []string, svcLabel string) (string, int64, bool) {
	k, off, ok := vmParseGroupKeyRoute(key, svcLabel, kafkaLagTopicLabel)
	return k, off, ok
}

// pickTopLagSubjects — SAF: seçim sonucunu (svc+topic → ortalama lag)
// deterministik üst-N'e indirir (VM topk eşitlikte fazla döndürebilir; iki pod
// aynı kümeyi seçsin). Sıfır lag'li özne seçime girmez (pickTopSubjects kuralı).
func pickTopLagSubjects(avg map[string]float64, topN int) map[string]bool {
	vols := make([]subjectVolume, 0, len(avg))
	for k, v := range avg {
		vols = append(vols, subjectVolume{Group: kafkaLagSystem, ID: k, Volume: v})
	}
	out := map[string]bool{}
	for _, v := range pickTopSubjects(vols, topN) {
		out[v.ID] = true
	}
	return out
}

// kafkaLagSource — lider belleği (tek goroutine: dedektör tiki).
type kafkaLagSource struct {
	store    *vmREDStore
	sel      vmEndpointSel
	selExpr  string
	svcLabel string
	backoff  time.Time
}

func newKafkaLagSource() *kafkaLagSource { return &kafkaLagSource{store: newVMREDStore()} }

// kafkaLagSvcLabel — SAF: servis etiketi (LabelMap rolü, yoksa service_name).
func kafkaLagSvcLabel(lm vmetrics.LabelMap) string {
	if l := lm.Get(vmetrics.RoleService); l != "" {
		return l
	}
	return "service_name"
}

// refreshSelection — üst-N (vmStatusTTL'de bir). Küme değişirse çapa yeni anı
// alır ve kovalar atılır (endpoint refreshSelection ikizi). Döner: sorgu
// sayısı, seçilen sayı (-1 okunamadı), hata.
func (s *kafkaLagSource) refreshSelection(ctx context.Context, vm REDVM, topN int, vnow time.Time) (int, int, error) {
	if s.sel.keys != nil && s.sel.topN == topN && s.selExpr != "" && vnow.Sub(s.sel.at) < vmStatusTTL {
		return 0, len(s.sel.keys), nil
	}
	anchor := vnow.Unix()
	qctx, cancel := context.WithTimeout(ctx, vmQueryTimeout)
	defer cancel()
	ser, err := vm.QueryPromQLInstant(qctx, kafkaLagSelectExpr(s.svcLabel, topN, anchor), vnow)
	if err != nil {
		if s.sel.keys != nil && s.selExpr != "" && vnow.Sub(s.sel.at) < vmMetaStale {
			return 1, len(s.sel.keys), nil // eski seçim geçerli (okunamadı ≠ yok)
		}
		s.sel, s.selExpr = vmEndpointSel{}, ""
		return 1, -1, err
	}
	avg := map[string]float64{}
	for _, sr := range ser {
		k, _, ok := kafkaLagParseKey(sr.GroupKey, s.svcLabel)
		if !ok || len(sr.Points) == 0 {
			continue
		}
		avg[k] = sr.Points[len(sr.Points)-1].Value
	}
	keys := pickTopLagSubjects(avg, topN)
	if !sameKeySet(keys, s.sel.keys) || s.sel.topN != topN || s.selExpr == "" {
		s.sel.anchor = anchor
		s.selExpr = kafkaLagSelectExpr(s.svcLabel, topN, anchor)
		s.store.flushFamily(0)
	}
	s.sel.keys, s.sel.at, s.sel.topN = keys, vnow, topN
	return 1, len(keys), nil
}

// planReads — SAF-ımsı (yalnız bellek okur): eksik kovaların sorguları, en yeni önce, ≤ limit.
func (s *kafkaLagSource) planReads(required []int64, limit int) []vmPlanned {
	if limit < 1 || s.selExpr == "" {
		return nil
	}
	miss := s.store.missing(0, vmKindCount, required)
	build := func(offs []int64) string { return kafkaLagQuery(s.svcLabel, s.selExpr, offs) }
	var out []vmPlanned
	for _, g := range vmGroupRuns(vmRuns(miss, vmMaxRunBuckets), vmMaxOffsetsPerQuery) {
		for _, sg := range vmSplitForLen(g, build) {
			if q := build(sg.Offsets); q != "" && len(q) <= vmMaxQueryLen {
				out = append(out, vmPlanned{kind: vmKindCount, group: sg, query: q})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].group.RefStart > out[j].group.RefStart })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// applyLagResult — sonucu kovalara yazar, sorgulanan kovaları "okundu" işaretler.
func (s *kafkaLagSource) applyLagResult(p vmPlanned, ser []chstore.SpanMetricSeries) {
	want := map[int64]bool{}
	var buckets []int64
	for _, off := range p.group.Offsets {
		for i := 0; i < p.group.Len; i++ {
			b := p.group.RefStart - off + int64(i*vmStepS)
			want[b] = true
			buckets = append(buckets, b)
		}
	}
	for _, sr := range ser {
		k, off, ok := kafkaLagParseKey(sr.GroupKey, s.svcLabel)
		if !ok {
			continue
		}
		for _, pt := range sr.Points {
			b := pt.Time/int64(time.Second) - off
			if want[b] && !math.IsNaN(pt.Value) {
				s.store.put(0, vmKindCount, b, k, pt.Value)
			}
		}
	}
	s.store.markHave(0, vmKindCount, buckets)
}

// lagSeries — SAF-ımsı: bir özne anahtarının ardışık + mevsimsel serisi.
// Hacim serisi = gözlem göstergesi (1 = kova okundu ve seri var): lag 0 bir
// GÖZLEMDİR (sessizlik değil); serisi olmayan kova fillDwellGrid'de (0, 0)
// dolgusu alır ve seyrek-seri kapısı (zBlockSparse) açılmayı keser.
func lagSeries(st *vmREDStore, key string, consec, seasonal []int64, upper int64, dwell int, class string, dayKey func(int64) int64, seasonalOK bool) subjectSeries {
	var vals, rates []float64
	var times []int64
	for _, b := range consec {
		if v, ok := st.get(0, vmKindCount, b, key); ok {
			vals, rates, times = append(vals, v), append(rates, 1), append(times, b)
		}
	}
	var out subjectSeries
	out.Values, out.Rates = fillDwellGrid(vals, rates, times, upper, dwell)
	if !seasonalOK {
		return out
	}
	days := map[int64]struct{}{}
	var seas, seasRates []float64
	for _, b := range seasonal {
		if v, ok := st.get(0, vmKindCount, b, key); ok {
			seas, seasRates = append(seas, v), append(seasRates, 1)
			days[seasonalDay(b, dayKey)] = struct{}{}
		}
	}
	if len(days) >= seasonalMinDaysFor(class) {
		out.Seasonal, out.SeasonalRates = seas, seasRates
	}
	return out
}

// collectKafkaLag — queue öznesinin consumer_lag serileri (bkz. dosya başı).
func (d *Detector) collectKafkaLag(ctx context.Context, now time.Time, sens chstore.AnomalySensitivityConfig,
	days, neighbor int, r subjectKindReq, vmLeft *int, out subjectTick, st *SubjectKindStats) {
	var vm REDVM
	if d.red != nil {
		if box := d.red.wire.Load(); box != nil && box.vm != nil && box.vm.Configured() {
			vm = box.vm
		}
	}
	if vm == nil || d.kafkaLag == nil {
		st.Skipped, st.Reason = true, subjectSkipVMKafkaAbsent
		return
	}
	plan := planSubjectBudget([]subjectKindReq{r}, *vmLeft)[0]
	if plan.Skipped {
		st.Skipped, st.Reason = true, plan.Reason
		return
	}
	src := d.kafkaLag
	vnow := now.Add(-vmIngestLag)
	if lbl := kafkaLagSvcLabel(vm.MetricLabelMap().Normalized()); lbl != src.svcLabel {
		src.svcLabel, src.sel, src.selExpr = lbl, vmEndpointSel{}, ""
		src.store.flushFamily(0)
	}
	bctx, cancel := context.WithTimeout(ctx, vmTickBudget)
	defer cancel()
	q, n, err := src.refreshSelection(bctx, vm, r.Cfg.TopN, vnow)
	st.Queries, st.Series = q, n
	defer func() { *vmLeft -= st.Queries }()
	switch {
	case err != nil:
		st.Skipped, st.Reason, st.LastError = true, subjectSkipNoSource, err.Error()
		return
	case n == 0:
		st.Skipped, st.Reason = true, subjectSkipVMKafkaAbsent
		return
	case n > r.Cfg.MaxSeriesPerTick:
		st.Skipped, st.Reason = true, subjectSkipSeriesCap
		return
	}
	loc := seasonalLocation(sens.SeasonalTimezone)
	upper := lastCompleteBucketStart(vnow).Unix()
	consec := vmConsecutiveBuckets(vnow)
	seasonal, class := vmSeasonalBucketsCal(vnow, days, neighbor, sens.Calendar, loc)
	req := vmRequiredSet(consec, seasonal)
	src.store.retain(req)
	required := make([]int64, 0, len(req))
	for b := range req {
		required = append(required, b)
	}
	sort.Slice(required, func(i, j int) bool { return required[i] > required[j] })
	if vnow.After(src.backoff) {
		for _, p := range src.planReads(required, plan.VMBudget-st.Queries) {
			from := time.Unix(p.group.RefStart, 0)
			to := from.Add(time.Duration(p.group.Len*vmStepS) * time.Second)
			qctx, qcancel := context.WithTimeout(bctx, vmQueryTimeout)
			ser, qerr := vm.QueryPromQLRange(qctx, p.query, from, to, vmStepS, vmMaxPoints)
			qcancel()
			st.Queries++
			if qerr != nil {
				st.LastError = qerr.Error()
				src.backoff = vnow.Add(vmKindBackoff)
				break
			}
			src.applyLagResult(p, ser)
		}
	}
	if len(src.store.missing(0, vmKindCount, consec)) > 0 {
		return // ısınıyor: ardışık pencere tam değil, karar yok (yarım taban yalan söyler)
	}
	seasonalOK := len(src.store.missing(0, vmKindCount, seasonal)) == 0
	keys := make([]string, 0, len(src.sel.keys))
	for k := range src.sel.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	dayKey := seasonalDayKey(loc)
	for _, k := range keys {
		svc, topic, ok := splitEndpointStoreKey(k)
		sid := chstore.QueueSubjectID(kafkaLagSystem, topic)
		if !ok || svc == "" || sid == "" {
			continue
		}
		ser := lagSeries(src.store, k, consec, seasonal, upper, sens.DwellBuckets, class, dayKey, seasonalOK)
		if len(ser.Values) == 0 {
			continue
		}
		sk := subjectKey{Kind: chstore.AnomalySubjectQueue, Owner: svc, ID: sid}.String()
		out.put(subjectMetricConsumerLag, sk, func(x *subjectSeries) { *x = ser })
	}
}

// ── karar + metin (SAF) ────────────────────────────────────────────────────

// lagMinRecordsOf — SAF: ayarın lag tabanı (0 / negatif → varsayılan).
func lagMinRecordsOf(c chstore.AnomalySubjectsConfig) float64 {
	if c.Queue.LagMinRecords < 1 {
		return kafkaLagMinRecordsDefault
	}
	return float64(c.Queue.LagMinRecords)
}

// consumerLagEventAllowed — SAF: açılan lag kararı olay üretir mi. Yalnız
// YUKARI yön (lag artışı; düşüş iyileşmedir) ve cari lag ≥ lagMinRecords.
func consumerLagEventAllowed(oc anomalyOutcome, minRecords float64) bool {
	return oc.Action == "open" && oc.EventDirection() == chstore.AnomalyDirectionUp && oc.Current >= minRecords
}

// formatIntTR — SAF: 48000 → "48.000" (yuvarlanmış, binlik nokta).
func formatIntTR(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "?"
	}
	n := int64(math.Round(v))
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// baselineKindTR — SAF: olay taban türünün Türkçe adı.
func baselineKindTR(kind string) string {
	switch kind {
	case chstore.AnomalyBaselineSeasonal:
		return "mevsimsel"
	case chstore.AnomalyBaselineConsecutive:
		return "ardışık"
	}
	return "taban yok"
}

// queueSubjectTopic — SAF: kuyruk öznesi kimliğinden hedef adı (çözülemezse kimlik).
func queueSubjectTopic(id string) string {
	if _, dest, ok := chstore.ParseQueueSubjectID(id); ok {
		return dest
	}
	return id
}

// consumerLagSample — SAF: olayın düz Türkçe örnek cümlesi.
//
//	orders-consumer · topic orders: lag 48.000 kayıt, taban 1.200 (mevsimsel, z=6,1, 3 kova)
func consumerLagSample(k subjectKey, oc anomalyOutcome) string {
	z := strings.Replace(strconv.FormatFloat(oc.Z, 'f', 1, 64), ".", ",", 1)
	return fmt.Sprintf("%s · topic %s: lag %s %s, taban %s (%s, z=%s, %d kova)",
		k.Owner, queueSubjectTopic(k.ID), formatIntTR(oc.Current), unitOf(subjectMetricConsumerLag),
		formatIntTR(oc.Median), baselineKindTR(oc.EventBaselineKind()), z, oc.Dwell)
}
