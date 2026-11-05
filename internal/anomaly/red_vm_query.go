package anomaly

// red_vm_query.go — v0.10.1478 (operatör: "Servislerin kendi içerisinde
// haftalık olağan akışlarını takip etse ve gerçek bir anomaly öyle yakalasa").
// SAF: VM RED ailesi kataloğu + 5 dk kova PromQL kurucuları + sorgu planı.
//
// NEDEN: metrik dedektörü (anomaly.go) ve davranış motoru (behavior*.go)
// RED'i span MV'sinden (service_summary_5m) okuyordu. Prod trace'leri
// gateway'de kuyruk örnekleniyor: hatalar hep tutulur + ~%5 taban → span
// error_rate yukarı yanlı ve örnekleme politikasıyla oynuyor, istek hızı %5'lik
// gürültülü bir örnek. VictoriaMetrics OTel collector'ın ÖRNEKLENMEMİŞ
// HTTP/gRPC sunucu histogramını taşıyor (CoSRE servis sağlığı aynı aileyi
// okuyor — api/red_family.go). Taban mantığı (mevsimsel 14 g, 168 kova,
// dwell, tabanlar, terfi) DEĞİŞMEZ; yalnız seri kaynağı değişir.
//
// Aile kataloğu api/red_family.go'nun ALT KÜMESİ (api → anomaly importu var,
// tersi döngü olurdu); eşitlik api tarafında pinli
// (TestAnomalyREDFamiliesMatchAPI). Kurallar aynı: aileler ASLA toplanmaz
// (HTTP birincil, sonra gRPC legacy, sonra stable), gecikme ölçeği ailenin
// beyan ettiği birimden, hata kümesi pinli.
//
// Sorgu şekli (kova = [t, t+5m), 5 dk ızgara, UTC):
//
//	count: sum by (svc) (increase({<count adları>, <env>}[5m] offset Os))
//	err  : (sum by (svc) (increase({…, <durum>=~"<hata>"}[5m] offset Os)) or …)
//	p99  : histogram_quantile(0.99, sum by (svc, le) (increase({<bucket>}[5m] offset Os))) * ölçek
//
// Her alt ifade label_set(…, "cm_off", "<O>") ile işaretlenir ve aynı
// uzunluktaki koşular TEK sorguda `or` ile birleşir (geçmiş günlerin aynı
// slotu, geçmiş haftaların aynı saati): sorgu sayısı gün/hafta sayısıyla
// değil, koşu UZUNLUĞU sayısıyla büyür. Tavanlar: koşu ≤ vmMaxRunBuckets,
// sorgu başına ≤ vmMaxOffsetsPerQuery ofset, sorgu metni ≤ vmMaxQueryLen.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// vmREDFamily — bir RED metrik ailesinin SAF tanımı (api/red_family.go alt kümesi).
type vmREDFamily struct {
	ID    string
	Label string // kanıt metni ("HTTP" | "gRPC")
	// Probe — OTel adı: etiket keşfi (MetricLabelNamesIn / ValuesIn).
	Probe string
	// Names — seçici adayları için girdi: HTTP'de OTel adı (yazım
	// alternasyonu, api ile bayt bayt), gRPC'de TAM VM tabanı (ms ↔ s
	// ailesi tek alternasyonda toplanmasın).
	Names        string
	Unit         string   // "s" | "ms" — p99 ölçeği buradan (ms'ye çevrilir)
	StatusLabels []string // VM yazımı, tercih sırası
	ServerErr    string   // hata kümesi regex gövdesi (PİNLİ)
}

// vmREDFamilies — sıra = seçim sırası (HTTP birincil; legacy stable'dan önce).
// vmFamN — aile sayısı (dizi boyutları; görünümün hazırlık tabloları).
const vmFamN = 3

var vmREDFamilies = [vmFamN]vmREDFamily{
	{ID: "http", Label: "HTTP", Probe: "http.server.request.duration", Names: "http.server.request.duration", Unit: "s",
		StatusLabels: []string{"http_response_status_code", "http_status_code"}, ServerErr: "5.."},
	{ID: "grpc_legacy", Label: "gRPC", Probe: "rpc.server.duration", Names: "rpc_server_duration_milliseconds", Unit: "ms",
		StatusLabels: []string{"rpc_grpc_status_code"}, ServerErr: "2|4|8|12|13|14|15"},
	{ID: "grpc_stable", Label: "gRPC", Probe: "rpc.server.call.duration", Names: "rpc_server_call_duration_seconds", Unit: "s",
		StatusLabels: []string{"rpc_response_status_code"}, ServerErr: "2|4|8|12|13|14|15"},
}

// REDFamilyPin — api tarafının pin testi için dışa açık özet.
type REDFamilyPin struct {
	ID, Probe, Names, Unit, ServerErr string
	StatusLabels                      []string
}

// REDFamilyPins — katalog özeti (TestAnomalyREDFamiliesMatchAPI).
func REDFamilyPins() []REDFamilyPin {
	out := make([]REDFamilyPin, 0, len(vmREDFamilies))
	for _, f := range vmREDFamilies {
		out = append(out, REDFamilyPin{ID: f.ID, Probe: f.Probe, Names: f.Names, Unit: f.Unit, ServerErr: f.ServerErr,
			StatusLabels: append([]string(nil), f.StatusLabels...)})
	}
	return out
}

// vmREDKind — bir kovanın okunan büyüklüğü.
type vmREDKind int

const (
	vmKindCount vmREDKind = iota // istek sayısı (5 dk artışı)
	vmKindErr                    // sunucu-hatası sayısı
	vmKindP99                    // p99 gecikme (ms)
	vmKindN
)

func (k vmREDKind) String() string {
	switch k {
	case vmKindCount:
		return "count"
	case vmKindErr:
		return "err"
	case vmKindP99:
		return "p99"
	}
	return "?"
}

// Tavanlar — sorgu maliyetini sınırlar (DECISIONS v0.10.1478).
const (
	vmStepS = bucketSeconds // 5 dk ızgara: MV kovasıyla aynı
	// vmMaxRunBuckets — tek koşunun en fazla kova sayısı (1 sa). Isınmada
	// kova histogramının filo geneli okuması bu pencereyle sınırlı kalır.
	vmMaxRunBuckets = 12
	// vmMaxOffsetsPerQuery — tek sorguda birleşen ofset (koşu) sayısı.
	vmMaxOffsetsPerQuery = 6
	// vmMaxQueryLen — sorgu metni tavanı (GET URL'i; ara katman 8 KiB'ı
	// geçmesin, VM -search.maxQueryLen varsayılanı 16 KiB). Aşan grup ikiye
	// bölünür (vmSplitForLen).
	vmMaxQueryLen = 6000
	// vmMaxEnvMatcherLen — ortam eşleştiricisinin tavanı; aşan kapsam
	// "çözülemedi" sayılır (span yedeği).
	vmMaxEnvMatcherLen = 1000
	// vmOffLabel — ofset işaret etiketi (alt ifadeleri ayırır).
	vmOffLabel = "cm_off"
)

// vmQueryScope — ailenin çözülmüş etiketleri (filo geneli keşif).
type vmQueryScope struct {
	SvcLabel     string   // servis etiketi (LabelMap.Service / service_name / keşif)
	EnvMatcher   string   // "" = ortam süzgeci yok
	StatusLabels []string // kümede BULUNAN durum etiketleri (ailenin sırasıyla)
	// RouteLabel / Select — v0.10.1791 (Davis F1.3, endpoint özneleri):
	// RouteLabel doluysa gruplama `by (svc, route)` olur ve her ifade Select
	// (servis başına üst-N rota seçimi, vmEndpointSelectExpr) ile `and on`
	// süzülür. İkisi boşken (servis düzeyi) ifadeler bayt bayt eski.
	RouteLabel string
	Select     string
}

// vmByLabels — SAF: gruplama etiketleri (servis; endpoint kipinde + rota).
func vmByLabels(sc vmQueryScope) string {
	if sc.RouteLabel == "" {
		return sc.SvcLabel
	}
	return sc.SvcLabel + ", " + sc.RouteLabel
}

// vmWithSelect — SAF: endpoint kipinde ifadeyi seçimle süzer; aksi aynen.
func vmWithSelect(e string, sc vmQueryScope) string {
	if sc.RouteLabel == "" || sc.Select == "" {
		return e
	}
	return "(" + e + ") and on (" + vmByLabels(sc) + ") (" + sc.Select + ")"
}

// vmSelector — SAF: {__name__ adayları, env, ek}.
func vmSelector(f vmREDFamily, nameKind string, sc vmQueryScope, extra string) string {
	parts := []string{vmetrics.NameSelector(vmetrics.NameCandidates(nameKind, f.Names))}
	if m := strings.TrimSpace(sc.EnvMatcher); m != "" {
		parts = append(parts, m)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// vmOffsetSuffix — SAF: " offset Ns" (0 → "").
func vmOffsetSuffix(off int64) string {
	if off <= 0 {
		return ""
	}
	return " offset " + strconv.FormatInt(off, 10) + "s"
}

// vmREDExpr — SAF: tek ofsetin ifadesi (label_set'siz). ok=false: tür bu
// kapsamda ölçülemez (ör. durum etiketi yok → hata sayısı YOK; 0 sanılmasın).
func vmREDExpr(f vmREDFamily, k vmREDKind, sc vmQueryScope, off int64) (string, bool) {
	win := fmt.Sprintf("[%ds]%s", vmStepS, vmOffsetSuffix(off))
	by := vmByLabels(sc) // v0.10.1791 — endpoint kipinde (svc, route)
	switch k {
	case vmKindCount:
		return vmWithSelect("sum by ("+by+") (increase("+vmSelector(f, "count", sc, "")+win+"))", sc), true
	case vmKindErr:
		if len(sc.StatusLabels) == 0 {
			return "", false
		}
		parts := make([]string, 0, len(sc.StatusLabels))
		for _, l := range sc.StatusLabels {
			parts = append(parts, "sum by ("+by+") (increase("+vmSelector(f, "count", sc, l+`=~"`+f.ServerErr+`"`)+win+"))")
		}
		// `or`: aynı servis iki yazımı birden taşıyorsa (semconv geçişi) sol
		// kazanır — iki kez sayılmaz.
		return vmWithSelect("("+strings.Join(parts, " or ")+")", sc), true
	case vmKindP99:
		q := "histogram_quantile(0.99, sum by (" + by + ", le) (increase(" + vmSelector(f, "bucket", sc, "") + win + ")))"
		if f.Unit != "ms" {
			q = "(" + q + ") * 1000" // s → ms (p99_ms birimi)
		}
		return vmWithSelect(q, sc), true
	}
	return "", false
}

// vmEndpointSelectExpr — SAF (v0.10.1791, karar 1-2): servis başına en
// yoğun topN rota, `anchor` anındaki 1 sa'lik istek artışına göre. `@ anchor`
// SEÇİMİ SABİTLER: aralık sorgusunun her adımında ve her ofsetinde aynı küme
// (adım başına topk üyeliği kayar, mevsimsel ofsette geçmişin üst-N'i olurdu).
// Rota etiketi boş seriler dışarıda — rotasız servis servis düzeyinde kalır.
func vmEndpointSelectExpr(f vmREDFamily, sc vmQueryScope, topN int, anchor int64) string {
	if topN < 1 {
		topN = 1
	}
	inner := vmSelector(f, "count", sc, sc.RouteLabel+`!=""`)
	return "topk by (" + sc.SvcLabel + ") (" + strconv.Itoa(topN) + ", sum by (" + sc.SvcLabel + ", " + sc.RouteLabel +
		") (increase(" + inner + "[1h] @ " + strconv.FormatInt(anchor, 10) + ")))"
}

// vmRouteLabels — SAF: OTel http.route'un VM yazımı adayları (tercih sırası).
var vmRouteLabels = []string{"http_route", "route"}

// vmParseGroupKeyRoute — SAF: endpoint kipinde grup anahtarı → depo anahtarı
// (endpointStoreKey: servis + ayraç + rota) + ofset. Rota boş → ok=false.
func vmParseGroupKeyRoute(key []string, svcLabel, routeLabel string) (string, int64, bool) {
	svc, off, ok := vmParseGroupKey(key, svcLabel)
	if !ok {
		return "", 0, false
	}
	route := ""
	for _, kv := range key {
		i := strings.IndexByte(kv, '=')
		if i <= 0 || kv[:i] != routeLabel {
			continue
		}
		raw := kv[i+1:]
		v, err := strconv.Unquote(raw)
		if err != nil {
			v = strings.Trim(raw, `"`)
		}
		route = v
	}
	if route == "" {
		return "", 0, false
	}
	return endpointStoreKey(svc, route), off, true
}

// vmREDQuery — SAF: ofsetlerin birleşik sorgusu. Her alt ifade cm_off ile
// işaretli; dönen noktanın GERÇEK kovası = nokta kovası − ofset.
func vmREDQuery(f vmREDFamily, k vmREDKind, sc vmQueryScope, offsets []int64) (string, bool) {
	if len(offsets) == 0 || sc.SvcLabel == "" {
		return "", false
	}
	parts := make([]string, 0, len(offsets))
	for _, off := range offsets {
		e, ok := vmREDExpr(f, k, sc, off)
		if !ok {
			return "", false
		}
		parts = append(parts, `label_set(`+e+`, "`+vmOffLabel+`", "`+strconv.FormatInt(off, 10)+`")`)
	}
	return strings.Join(parts, " or "), true
}

// vmStatusServicesQuery — SAF: son 1 sa'te durum etiketi TAŞIYAN servisler
// (anlık sorgu). Etiketi olmayan servisin hata oranı VM'den okunmaz: "seri
// yok" orada 0 hata değil, ölçülemez demek.
func vmStatusServicesQuery(f vmREDFamily, sc vmQueryScope) (string, bool) {
	if len(sc.StatusLabels) == 0 || sc.SvcLabel == "" {
		return "", false
	}
	parts := make([]string, 0, len(sc.StatusLabels))
	for _, l := range sc.StatusLabels {
		parts = append(parts, "group by ("+sc.SvcLabel+") (last_over_time("+vmSelector(f, "count", sc, l+`!=""`)+"[1h]))")
	}
	return strings.Join(parts, " or "), true
}

// vmRun — ardışık kova koşusu (Start = ilk kova başı, unix sn).
type vmRun struct {
	Start int64
	Len   int
}

// vmRuns — SAF: eksik kova başlarını (sırasız, tekrar olabilir) ardışık
// koşulara böler; her koşu ≤ maxLen.
func vmRuns(missing []int64, maxLen int) []vmRun {
	if len(missing) == 0 || maxLen < 1 {
		return nil
	}
	ts := append([]int64(nil), missing...)
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	var out []vmRun
	for i, t := range ts {
		if i > 0 && t == ts[i-1] {
			continue
		}
		if n := len(out); n > 0 {
			last := &out[n-1]
			if t == last.Start+int64(last.Len)*vmStepS && last.Len < maxLen {
				last.Len++
				continue
			}
		}
		out = append(out, vmRun{Start: t, Len: 1})
	}
	return out
}

// vmQueryGroup — tek range sorgusu: [RefStart, RefStart+Len·5dk) penceresi,
// her ofset bir koşu (koşu başı = RefStart − ofset).
type vmQueryGroup struct {
	RefStart int64
	Len      int
	Offsets  []int64 // artan
}

// vmGroupRuns — SAF: aynı uzunluktaki koşuları ≤ maxOffsets'lik gruplara
// toplar. Referans = gruptaki EN YENİ koşu (ofsetler ≥ 0). Sıra: en yeni
// koşulu grup önce (bütçe kesilirse taze kovalar kazanır).
func vmGroupRuns(runs []vmRun, maxOffsets int) []vmQueryGroup {
	if maxOffsets < 1 {
		maxOffsets = 1
	}
	byLen := map[int][]vmRun{}
	var lens []int
	for _, r := range runs {
		if _, ok := byLen[r.Len]; !ok {
			lens = append(lens, r.Len)
		}
		byLen[r.Len] = append(byLen[r.Len], r)
	}
	var out []vmQueryGroup
	for _, l := range lens {
		rs := byLen[l]
		sort.Slice(rs, func(i, j int) bool { return rs[i].Start > rs[j].Start })
		for i := 0; i < len(rs); i += maxOffsets {
			end := i + maxOffsets
			if end > len(rs) {
				end = len(rs)
			}
			chunk := rs[i:end]
			g := vmQueryGroup{RefStart: chunk[0].Start, Len: l}
			for _, r := range chunk {
				g.Offsets = append(g.Offsets, chunk[0].Start-r.Start)
			}
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RefStart > out[j].RefStart })
	return out
}

// vmSplitForLen — SAF: sorgu metni tavanı aşan grubu ofsetleri ikiye bölerek
// küçültür (tek ofset tavanı hâlâ aşıyorsa olduğu gibi döner; çağıran düşürür).
func vmSplitForLen(g vmQueryGroup, build func([]int64) string) []vmQueryGroup {
	if len(g.Offsets) <= 1 || len(build(g.Offsets)) <= vmMaxQueryLen {
		return []vmQueryGroup{g}
	}
	mid := len(g.Offsets) / 2
	a := vmQueryGroup{RefStart: g.RefStart, Len: g.Len, Offsets: g.Offsets[:mid]}
	b := vmQueryGroup{RefStart: g.RefStart, Len: g.Len, Offsets: g.Offsets[mid:]}
	return append(vmSplitForLen(a, build), vmSplitForLen(b, build)...)
}

// vmParseGroupKey — SAF: QueryPromQLRange'in GroupKey'i (`k="v"` sıralı)
// → servis + ofset. ok=false: servis etiketi yok.
func vmParseGroupKey(key []string, svcLabel string) (svc string, off int64, ok bool) {
	for _, kv := range key {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k, raw := kv[:i], kv[i+1:]
		v, err := strconv.Unquote(raw)
		if err != nil {
			v = strings.Trim(raw, `"`)
		}
		switch k {
		case svcLabel:
			svc, ok = v, v != ""
		case vmOffLabel:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				off = n
			}
		}
	}
	return svc, off, ok
}
