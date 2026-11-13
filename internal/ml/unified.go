package ml

// unified.go — v0.10.2051: /anomalies TEK listesinin birleştirmesi SUNUCUDA.
//
// v0.10.2027 listeyi tarayıcıda kuruyordu (unifiedRows.ts): ML olayları
// (tavan 200) + eski anomaly_events (tavan 200) iki uçtan gelip istemcide
// katlanıyordu; sayfalama yoktu, yoğun pencerede satırlar SESSİZCE
// düşüyordu ("Table > 100 rows" kuralının ihlali). Kural birebir taşındı:
//
//  1. ML olayları okuma-anı D2 grubuna (GroupIndex) katlanır; grup kimliği
//     yoksa olay kendi satırı ("ev:<id>"). Grubun aralığı ve sinyalleri
//     grubun TÜM üyelerinden (GroupMLEvents), durumu listedeki üyelerden.
//  2. ML grupları + eski olaylar AYNI D2 kuralıyla (ChainWindows: aynı
//     servis, boşluk ≤ teyit penceresi, zincirleme) tek satıra iner. Boş
//     servisli eski olaylar kendi aralarında zincirlenir (TS ile aynı).
//  3. Durum: Sürüyor > İzleniyor > Bitti > Değil dendi; açık bölüm durum
//     sonra başlangıç (yeni önce), biten bölüm başlangıç (yeni önce).
//
// Cümle (Ne oldu) BURADA KURULMAZ: satır yalnız "lider" üyeyi taşır, metin
// tarayıcıdaki mevcut işlevlerle (mlRowText / legacyHeadline) aynı kalır.

import (
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Unified durumları — TS MLGroupStatus ile aynı dizgeler.
const (
	UnifiedRunning  = "running"
	UnifiedWatching = "watching"
	UnifiedDone     = "done"
	UnifiedRejected = "rejected"
)

var unifiedSignalOrder = []string{chstore.MLSignalTrace, chstore.MLSignalLog, chstore.MLSignalMetric}

// UnifiedInput — birleştirme girdisi.
type UnifiedInput struct {
	ML      []chstore.MLEvent        // liste satırları (kararlarla)
	Groups  map[string]*MLGroup      // olay id → okuma-anı grubu (nil = grup yok)
	Shadows map[string]GroupDecision // grup id → en son gölge karar
	Legacy  []chstore.AnomalyEvent   // eski dedektör olayları (kararlarla)
	Now     time.Time
	Slack   time.Duration
}

// UnifiedRow — birleşik liste satırı. Üyeler girdideki indeksler.
type UnifiedRow struct {
	Key     string
	Service string
	Start   int64
	End     int64
	Signals []string
	Status  string
	MLLead  int   // ML lideri (UnifiedInput.ML indeksi); -1 = yok
	LegLead int   // eski olay lideri (UnifiedInput.Legacy indeksi); -1 = yok
	ML      []int // ML üyeleri
	Legacy  []int // eski üyeler
}

// UnifiedSignalOf — SAF: TS signalKindOf aynası (dedektör + sunucu sinyali → tür).
func UnifiedSignalOf(detector, signal string) string {
	if strings.HasPrefix(detector, "metric_") {
		return chstore.MLSignalMetric
	}
	for _, n := range chstore.MLTraceDetectorNames {
		if n == detector {
			return chstore.MLSignalTrace
		}
	}
	for _, n := range chstore.MLLogDetectorNames {
		if n == detector {
			return chstore.MLSignalLog
		}
	}
	if signal == chstore.MLSignalMetric || signal == chstore.MLSignalTrace {
		return signal
	}
	return chstore.MLSignalLog
}

// LegacySignal — SAF: TS legacySignal aynası (eski olay türü → sinyal).
func LegacySignal(kind string) string {
	switch {
	case strings.HasPrefix(kind, "log_") || kind == "elastic_ml":
		return chstore.MLSignalLog
	case strings.HasPrefix(kind, "trace_op") || kind == "op_latency":
		return chstore.MLSignalTrace
	case strings.HasPrefix(kind, "ml:"):
		return chstore.MLSignalLog
	}
	return chstore.MLSignalMetric
}

// unifiedMemberOpen — SAF: TS memberOpen; kural chstore.MLEventOpen'da
// (v0.10.2080: eski okuyucuların erişicisi ListAnomalyFeed de onu okur).
func unifiedMemberOpen(e chstore.MLEvent, nowNs int64) bool { return chstore.MLEventOpen(e, nowNs) }

func unifiedLegacyStatus(e chstore.AnomalyEvent) string {
	if e.Verdict == chstore.VerdictNotAnomaly {
		return UnifiedRejected
	}
	if e.Status == "active" {
		return UnifiedWatching
	}
	return UnifiedDone
}

// UnifiedStatusRank — SAF: süren önce.
func UnifiedStatusRank(s string) int {
	switch s {
	case UnifiedRunning:
		return 3
	case UnifiedWatching:
		return 2
	case UnifiedDone:
		return 1
	}
	return 0
}

// UnifiedOpen — SAF: üst tablo mu ("Bitenler" değil).
func UnifiedOpen(s string) bool { return s == UnifiedRunning || s == UnifiedWatching }

// mlLeadLess — TS leadMember: skor, sonra son görülme (büyük önce), sonra kimlik.
func mlLeadLess(a, b chstore.MLEvent) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.LastSeen != b.LastSeen {
		return a.LastSeen > b.LastSeen
	}
	return a.ID < b.ID
}

// legacyLeadLess — TS leadLegacy: tepe oran, sayım, son görülme, kimlik.
func legacyLeadLess(a, b chstore.AnomalyEvent) bool {
	if a.PeakRatio != b.PeakRatio {
		return a.PeakRatio > b.PeakRatio
	}
	if a.CurrentCount != b.CurrentCount {
		return a.CurrentCount > b.CurrentCount
	}
	if a.LastSeen != b.LastSeen {
		return a.LastSeen > b.LastSeen
	}
	return a.ID < b.ID
}

type mlFold struct {
	key     string
	members []int
	lead    int
	start   int64
	end     int64
	signals map[string]bool
	status  string
}

// foldML — SAF: TS groupRows aynası (olay → grup satırı, girdi sırasıyla).
func foldML(in UnifiedInput) []*mlFold {
	byKey := map[string]*mlFold{}
	var order []*mlFold
	for i, e := range in.ML {
		k := "ev:" + e.ID
		if g := in.Groups[e.ID]; g != nil && g.ID != "" {
			k = g.ID
		}
		f := byKey[k]
		if f == nil {
			f = &mlFold{key: k, lead: i}
			byKey[k] = f
			order = append(order, f)
		}
		f.members = append(f.members, i)
		if mlLeadLess(e, in.ML[f.lead]) {
			f.lead = i
		}
	}
	nowNs := in.Now.UnixNano()
	for _, f := range order {
		lead := in.ML[f.lead]
		g := in.Groups[lead.ID]
		f.signals = map[string]bool{}
		minStart, maxEnd := lead.StartedAt, lead.LastSeen
		for _, m := range f.members {
			minStart = min(minStart, in.ML[m].StartedAt)
			maxEnd = max(maxEnd, in.ML[m].LastSeen)
		}
		f.start, f.end = minStart, maxEnd
		if g != nil {
			if g.Start != 0 {
				f.start = g.Start
			}
			if g.End != 0 {
				f.end = g.End
			}
			for _, s := range g.Signals {
				if s != chstore.MLSignalTrace && s != chstore.MLSignalLog && s != chstore.MLSignalMetric {
					s = chstore.MLSignalLog
				}
				f.signals[s] = true
			}
			for _, d := range g.Detectors {
				f.signals[UnifiedSignalOf(d, "")] = true
			}
		}
		for _, m := range f.members {
			f.signals[UnifiedSignalOf(in.ML[m].Detector, in.ML[m].Signal)] = true
		}
		f.status = mlGroupStatus(in, f.members, g, nowNs)
	}
	return order
}

// mlGroupStatus — SAF: TS groupStatus aynası.
func mlGroupStatus(in UnifiedInput, members []int, g *MLGroup, nowNs int64) string {
	real, rejected, open := false, false, false
	for _, m := range members {
		e := in.ML[m]
		switch e.Verdict {
		case chstore.VerdictAnomaly:
			real = true
		case chstore.VerdictNotAnomaly:
			rejected = true
		}
		if unifiedMemberOpen(e, nowNs) {
			open = true
		}
	}
	if !real && rejected {
		return UnifiedRejected
	}
	if open {
		// Grubun gölge kararı tüm üyelerde aynı (grup id'sine bağlı).
		if g != nil {
			if d, ok := in.Shadows[g.ID]; ok && d.WouldOpen {
				return UnifiedRunning
			}
		}
		return UnifiedWatching
	}
	return UnifiedDone
}

// BuildUnified — SAF: iki kaynak → birleşik satırlar, sıralama YOK (Sort*
// işlevleri bölüme göre sıralar). Girdi değiştirilmez.
func BuildUnified(in UnifiedInput) []UnifiedRow {
	folds := foldML(in)
	// Pencere kimliği: ML grubu "m<i>", eski olay "l<i>" (Idx ile geri dönülür);
	// sıralama eşitliğinde TS ile aynı anahtar (grup anahtarı / olay kimliği).
	ws := make([]Window, 0, len(folds)+len(in.Legacy))
	for i, f := range folds {
		ws = append(ws, Window{Key: in.ML[f.lead].Service, ID: f.key, Start: f.start, End: f.end, Idx: i})
	}
	superseded := legacySupersededByML(in.ML)
	for i, e := range in.Legacy {
		if superseded[e.ID] {
			continue
		}
		ws = append(ws, Window{Key: e.Service, ID: e.ID, Start: e.StartedAt, End: e.LastSeen, Idx: len(folds) + i})
	}
	chains := ChainWindows(ws, in.Slack)
	out := make([]UnifiedRow, 0, len(chains))
	for _, c := range chains {
		r := UnifiedRow{Service: c.Key, MLLead: -1, LegLead: -1, Start: c.Start}
		sig := map[string]bool{}
		var statuses []string
		r.End = c.End
		for _, w := range c.Members {
			if w.Idx < len(folds) {
				f := folds[w.Idx]
				r.ML = append(r.ML, f.members...)
				for s := range f.signals {
					sig[s] = true
				}
				statuses = append(statuses, f.status)
				if r.MLLead < 0 || mlLeadLess(in.ML[f.lead], in.ML[r.MLLead]) {
					r.MLLead = f.lead
				}
				continue
			}
			li := w.Idx - len(folds)
			e := in.Legacy[li]
			r.Legacy = append(r.Legacy, li)
			sig[LegacySignal(e.Kind)] = true
			statuses = append(statuses, unifiedLegacyStatus(e))
			if r.LegLead < 0 || legacyLeadLess(e, in.Legacy[r.LegLead]) {
				r.LegLead = li
			}
		}
		if r.MLLead >= 0 {
			key := in.ML[r.MLLead].ID
			for _, w := range c.Members {
				if w.Idx < len(folds) && folds[w.Idx].lead == r.MLLead {
					key = folds[w.Idx].key
				}
			}
			r.Key = "ml:" + key
		} else {
			r.Key = "ae:" + in.Legacy[r.LegLead].ID
		}
		for _, s := range unifiedSignalOrder {
			if sig[s] {
				r.Signals = append(r.Signals, s)
			}
		}
		r.Status = combineUnifiedStatus(statuses)
		out = append(out, r)
	}
	return out
}

// legacySupersededByML — SAF (v0.10.2069): ML'e taşınan eski olayların
// kimlikleri. Eski log_template_new dedektörü silinene dek AYNI kalıp hem
// eski satır hem ML new_template olayı üretir; ML olayı eski satırın
// kimliğini kanıtında taşır (evidence.legacyId) ve listede eski satır
// bastırılır — kalıp bir kez görünür. Seçim: ML satırını değil ESKİ satırı
// düşürmek, çünkü ML olayı gruplara / ≥ 2 sinyal Problem kuralına giren
// kayıttır. ML olayı listede yoksa (ML kapalı, katalog dışı, kesildi)
// eski satır aynen görünür.
func legacySupersededByML(ml []chstore.MLEvent) map[string]bool {
	return chstore.MLSupersededLegacyIDs(ml) // v0.10.2080: tek gövde (ListAnomalyFeed de okur)
}

func combineUnifiedStatus(ss []string) string {
	best := UnifiedRejected
	for _, s := range ss {
		if UnifiedStatusRank(s) > UnifiedStatusRank(best) {
			best = s
		}
	}
	return best
}

// SortUnifiedOpen — SAF: durum (süren önce), sonra başlangıç (yeni önce), sonra anahtar.
// TS: üst tablonun varsayılan sırası (status desc = rank·1e19 + start).
func SortUnifiedOpen(rows []UnifiedRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ra, rb := UnifiedStatusRank(a.Status), UnifiedStatusRank(b.Status); ra != rb {
			return ra > rb
		}
		if a.Start != b.Start {
			return a.Start > b.Start
		}
		return a.Key < b.Key
	})
}

// SortUnifiedDone — SAF: başlangıç (yeni önce); eşitlikte TS'nin kararlı
// sırası (durum, sonra anahtar). TS: "Bitenler" tablosu started desc.
func SortUnifiedDone(rows []UnifiedRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Start != b.Start {
			return a.Start > b.Start
		}
		if ra, rb := UnifiedStatusRank(a.Status), UnifiedStatusRank(b.Status); ra != rb {
			return ra > rb
		}
		return a.Key < b.Key
	})
}

// HasSignal — SAF: sinyal süzgeci (boş = hepsi).
func (r UnifiedRow) HasSignal(sig string) bool {
	if sig == "" {
		return true
	}
	for _, s := range r.Signals {
		if s == sig {
			return true
		}
	}
	return false
}
