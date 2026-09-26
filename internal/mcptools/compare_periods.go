package mcptools

// compare_periods.go — v0.10.944 (CoSRE araştırma asistanı): "sorun
// penceresinde ne değişti?" sorusunun sunucu tarafı cevabı. Entegrasyonda
// ToolList'e comparePeriodsTool(d) olarak girer (yeni tool; REST eşi yok,
// salt-okunur, MinRole "" = viewer).
//
// Neden sunucuda: model iki ayrı get_service_health çağrısını yan yana
// koyup yüzdelik ortalıyor, ortamları birleştiriyor ve "trafik aynı" derken
// operasyon karışımının kaydığını göremiyordu. Burada her şey TEK kıyas
// okumasından gelir (chstore.ReadComparePeriods — giriş span'leri, tüm pencere
// tdigest; env/cluster/namespace yoksa ve pencereler MV kapsamındaysa
// spanmetrics_1m, değilse ham spans — karar ve gerekçe orada yazılı) ve
// dürüstlük notları sayılardan TÜRETİLİR, modele bırakılmaz:
//
//   - aynı adlı servis birden çok ortamda görülüyorsa env ZORUNLU (bad_args
//     ortamları listeler) — ortamlar asla birleştirilmez;
//   - düşük örnek (<50 giriş span'i), düşük kapsama (<%90 dolu 5 dk kova),
//     p95 oynarken karışım kayması → not;
//   - örnekleme notu HER cevapta (Coremetry'ye ulaşan span'ler ≠ tüm trafik).
//
// Kaynak hatası (erişilemedi/zaman aşımı/yetki) Go hatası DEĞİL, `sources`
// durumuyla BAŞARILI sonuçtur; yalnız argüman hataları ve iptal Go hatasıdır.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// Pencere sınırları — her pencere (sorun ve referans) için ayrı ayrı.
const (
	cpMinWindow     = 5 * time.Minute
	cpMaxWindow     = 24 * time.Hour
	cpDelayedWithin = 2 * time.Minute // pencere sonu bu kadar tazeyse geç gelen span eksik olabilir
	// cpWallSlack — v0.10.944: range_s penceresinin sonu duvar saatini bu
	// kadardan fazla aşarsa (istemci saati ileride, çıpa ≤5 dk gelecek kabul
	// ediliyor) pencere şimdiye kaydırılır. Çıpasız range_s'te to zaten
	// "şimdi"dir; aradaki fark yalnız çağrı gecikmesi.
	cpWallSlack = time.Second
)

// cpNoteSampling — spec'in her cevapta zorunlu notu (birebir).
const cpNoteSampling = "Sayılar Coremetry'ye ulaşan span'lerden hesaplanır; upstream örnekleme varsa toplam trafiğin kesin istatistiği değildir."

// cpRequestsDefinition — popülasyon ilanı (SQL'deki periodEntryKinds ile aynı).
const cpRequestsDefinition = "requests = giriş span'leri (kind server + consumer); istemci, üretici ve iç span'ler sayılmaz"

// cpPercentileMethod* — yüzdelik yöntemi okuma yoluna göre (v0.10.944):
// spanmetrics_1m hızlı yolu ya da ham spans (gerekçesiyle). İkisi de TÜM
// pencere; kova yüzdeliklerinin ortalaması hiçbir yolda yok.
const (
	cpPercentileMethodMV  = "p50/p95/p99 = spanmetrics_1m tdigest durumlarının TÜM pencere üzerinden birleşimi (quantilesTDigestMerge; giriş span'leri; yaklaşık, ~%1-2); kova yüzdeliklerinin ortalaması DEĞİL"
	cpPercentileMethodRaw = "p50/p95/p99 = ham spans üzerinden her pencerenin TÜM giriş span'lerine tek quantileTDigest (yaklaşık, ~%1-2); kova yüzdeliklerinin ortalaması DEĞİL"
)

// cpPercentileMethod — SAF: okuma yolu + gerekçe → modele giden yöntem metni.
func cpPercentileMethod(source, reason string) string {
	if source == chstore.PeriodSourceSpanmetrics {
		return cpPercentileMethodMV
	}
	switch reason {
	case chstore.PeriodRawReasonScope:
		return cpPercentileMethodRaw + " — ham yol çünkü env/cluster/namespace süzgeci bir MV boyutu değil"
	case chstore.PeriodRawReasonCoverage:
		return cpPercentileMethodRaw + " — ham yol çünkü pencerelerden biri spanmetrics_1m kapsamından (30 g TTL / MV kuruluşu) önce başlıyor"
	}
	return cpPercentileMethodRaw
}

type comparePeriodsArgs struct {
	Service    string `json:"service"`
	Env        string `json:"env,omitempty"`
	Cluster    string `json:"cluster,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Operation  string `json:"operation,omitempty"`
	FromISO    string `json:"from_iso,omitempty"`
	ToISO      string `json:"to_iso,omitempty"`
	RangeS     int    `json:"range_s,omitempty"`
	Reference  string `json:"reference,omitempty"`
	RefFromISO string `json:"ref_from_iso,omitempty"`
	RefToISO   string `json:"ref_to_iso,omitempty"`
}

// comparePeriodsReader — tool'un ihtiyaç duyduğu üç okuma. *chstore.Store
// karşılar; test sahte okuyucu verir (Deps.Store somut tip, arayüz değil).
// v0.10.944 — ortam keşfi iki pencere için TEK okuma (ReadPeriodEnvironments:
// service_env_summary_5m ya da ham); eskiden pencere başına iki ham tarama.
type comparePeriodsReader interface {
	ListServiceNames(ctx context.Context, pattern string, limit, offset int) ([]string, int, error)
	ReadPeriodEnvironments(ctx context.Context, service string, cur, ref chstore.PeriodWindow) ([]string, error)
	ReadComparePeriods(ctx context.Context, sc chstore.PeriodScope, cur, ref chstore.PeriodWindow) (chstore.PeriodCompareRaw, error)
}

// cpParseISO — SAF: RFC3339 (UTC'ye çevrilir).
func cpParseISO(field, v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s geçersiz: RFC3339 olmalı (ör. 2026-09-26T10:00:00Z), gelen %q", field, v)
	}
	return t.UTC(), nil
}

// cpCheckLen — SAF: pencere uzunluğu [5 dk, 24 saat].
func cpCheckLen(label string, w chstore.PeriodWindow) error {
	l := w.To.Sub(w.From)
	if l <= 0 {
		return fmt.Errorf("%s geçersiz: bitiş başlangıçtan sonra olmalı", label)
	}
	if l < cpMinWindow || l > cpMaxWindow {
		return fmt.Errorf("%s geçersiz: uzunluk 5 dakika ile 24 saat arasında olmalı (gelen %s)", label, l)
	}
	return nil
}

// cpProblemWindow — SAF (ctx yalnız çıpa için): from_iso+to_iso (ikisi
// birden) YA DA range_s (sohbet çıpasına dayalı, rangeWindow semantiği).
func cpProblemWindow(ctx context.Context, a comparePeriodsArgs) (chstore.PeriodWindow, error) {
	hasFrom, hasTo := strings.TrimSpace(a.FromISO) != "", strings.TrimSpace(a.ToISO) != ""
	if hasFrom != hasTo {
		return chstore.PeriodWindow{}, fmt.Errorf("pencere geçersiz: from_iso ve to_iso birlikte verilmeli (ikisi birden ya da hiçbiri)")
	}
	var w chstore.PeriodWindow
	if hasFrom {
		if a.RangeS != 0 {
			return w, fmt.Errorf("pencere geçersiz: from_iso/to_iso ile range_s birlikte verilemez — birini seç")
		}
		f, err := cpParseISO("from_iso", a.FromISO)
		if err != nil {
			return w, err
		}
		t, err := cpParseISO("to_iso", a.ToISO)
		if err != nil {
			return w, err
		}
		w = chstore.PeriodWindow{From: f, To: t}
	} else {
		if a.RangeS < 0 || (a.RangeS > 0 && (a.RangeS < int(cpMinWindow.Seconds()) || a.RangeS > int(cpMaxWindow.Seconds()))) {
			return w, fmt.Errorf("range_s geçersiz: 300 ile 86400 arasında olmalı (gelen %d)", a.RangeS)
		}
		f, t := rangeWindow(ctx, a.RangeS)
		w = chstore.PeriodWindow{From: f.UTC(), To: t.UTC()}
	}
	return w, cpCheckLen("sorun penceresi", w)
}

// cpReferenceWindow — SAF: referans penceresi aritmetiği.
//
//	previous    — eşit uzunluk, hemen önce: [from−L, from)
//	day_before  — aynı saatler bir gün önce: [from−24h, to−24h)
//	week_before — aynı saatler bir hafta önce: [from−7g, to−7g)
//	custom      — ref_from_iso + ref_to_iso (5 dk…24 saat, sorunla ÇAKIŞMAZ)
//
// Dönen string, uygulanan referans türüdür.
func cpReferenceWindow(a comparePeriodsArgs, cur chstore.PeriodWindow) (chstore.PeriodWindow, string, error) {
	kind := strings.ToLower(strings.TrimSpace(a.Reference))
	hasRef := strings.TrimSpace(a.RefFromISO) != "" || strings.TrimSpace(a.RefToISO) != ""
	if kind == "" {
		kind = "previous"
		if hasRef {
			kind = "custom"
		}
	}
	if hasRef && kind != "custom" {
		return chstore.PeriodWindow{}, kind, fmt.Errorf("referans geçersiz: ref_from_iso/ref_to_iso yalnız reference=custom ile kullanılır (gelen reference=%q)", kind)
	}
	var ref chstore.PeriodWindow
	switch kind {
	case "previous":
		ref = chstore.PeriodWindow{From: cur.From.Add(-cur.To.Sub(cur.From)), To: cur.From}
	case "day_before":
		ref = chstore.PeriodWindow{From: cur.From.Add(-24 * time.Hour), To: cur.To.Add(-24 * time.Hour)}
	case "week_before":
		ref = chstore.PeriodWindow{From: cur.From.Add(-7 * 24 * time.Hour), To: cur.To.Add(-7 * 24 * time.Hour)}
	case "custom":
		if strings.TrimSpace(a.RefFromISO) == "" || strings.TrimSpace(a.RefToISO) == "" {
			return ref, kind, fmt.Errorf("referans geçersiz: reference=custom için ref_from_iso ve ref_to_iso zorunlu")
		}
		f, err := cpParseISO("ref_from_iso", a.RefFromISO)
		if err != nil {
			return ref, kind, err
		}
		t, err := cpParseISO("ref_to_iso", a.RefToISO)
		if err != nil {
			return ref, kind, err
		}
		ref = chstore.PeriodWindow{From: f, To: t}
		if ref.From.Before(cur.To) && cur.From.Before(ref.To) {
			return ref, kind, fmt.Errorf("referans penceresi geçersiz: sorun penceresiyle çakışıyor — aynı span iki dönemde sayılırdı")
		}
	default:
		return ref, kind, fmt.Errorf("reference geçersiz: previous | day_before | week_before | custom olmalı (gelen %q)", a.Reference)
	}
	return ref, kind, cpCheckLen("referans penceresi", ref)
}

// cpResolveEnv — SAF: ortam ayrımı. requested boşken servis >1 ortamda
// görülüyorsa bad_args (ortamlar listelenir); requested verilmiş ama
// görülmemişse yine bad_args (sonuç sessizce boş dönerdi). Dönen not, env
// uygulanmadığında modele neyin dahil olduğunu söyler.
func cpResolveEnv(requested string, seen []string) (string, string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, e := range seen {
			if e == requested {
				return requested, "", nil
			}
		}
		if len(seen) == 0 {
			return "", "", fmt.Errorf("env geçersiz: servis bu pencerelerde deployment.environment etiketi taşımıyor — env argümanını kaldır")
		}
		return "", "", fmt.Errorf("env geçersiz: %q bu serviste bu pencerelerde görülmedi; görülen ortamlar: %s", requested, strings.Join(seen, ", "))
	}
	switch len(seen) {
	case 0:
		return "", "Servis bu pencerelerde env etiketi taşımıyor; env süzgeci uygulanmadı.", nil
	case 1:
		return "", fmt.Sprintf("env verilmedi; servis penceredeki tek ortamda (%s) görüldü — env süzgeci uygulanmadı, etiketsiz span'ler de dahil.", seen[0]), nil
	}
	return "", "", fmt.Errorf("env zorunlu: servis birden çok ortamda görülüyor (%s) — ortamlar birleştirilmez, birini env ile seç", strings.Join(seen, ", "))
}

// cpWindowJSON — SAF: pencere UTC ISO + unix ns (pivot için) + süre.
func cpWindowJSON(w chstore.PeriodWindow) map[string]any {
	return map[string]any{
		"from_iso": w.From.UTC().Format(time.RFC3339), "to_iso": w.To.UTC().Format(time.RFC3339),
		"from_unix_ns": w.From.UnixNano(), "to_unix_ns": w.To.UnixNano(),
		"duration_s": int64(w.To.Sub(w.From).Seconds()),
	}
}

// cpNoteInput — notları türeten gerçekler.
type cpNoteInput struct {
	Cmp           chstore.PeriodComparison
	EnvNote       string
	OperationSet  bool
	ClusterNote   string
	LengthsDiffer bool
	Delayed       bool
	// v0.10.944 — okuma yolu (raw.Source / SourceReason / VersionsSource) ve
	// okumanın pencere kayması notları (MV ızgarası, saat kayması).
	Source         string
	SourceReason   string
	VersionsSource string
	WindowNotes    []string
	// v0.10.944 — başarısız ikincil okumalar (cpSecondaryErrs; cpSources ile
	// AYNI liste). Hata yalnız `sources`ta kalırsa model onu görmüyordu: sonuç
	// 6000 rune'da kırpılır ve boş dependencies/pods "gerçek boş" okunurdu.
	SecondaryErrs []cpSecondaryErr
}

// cpSecondaryErr — bir ikincil okumanın (RED dışı) adı ve hatası.
type cpSecondaryErr struct {
	Name string
	Err  error
}

// cpSecondaryErrs — SAF (v0.10.944): yalnız BAŞARISIZ ikincil okumalar, sabit
// sırayla. cpNotes ve cpSources aynı listeyi kullanır — iki liste ayrışamaz.
func cpSecondaryErrs(raw chstore.PeriodCompareRaw) []cpSecondaryErr {
	var out []cpSecondaryErr
	for _, s := range []cpSecondaryErr{
		{"top_operations", raw.OpsErr}, {"dependencies", raw.DepsErr}, {"pods", raw.PodsErr}, {"versions", raw.VersionsErr},
	} {
		if s.Err != nil {
			out = append(out, s)
		}
	}
	return out
}

// cpNotes — SAF: sayılardan türeyen dürüstlük notları. Örnekleme notu DAİMA ilk.
func cpNotes(in cpNoteInput) []string {
	notes := []string{cpNoteSampling, cpPercentileMethod(in.Source, in.SourceReason)}
	// v0.10.944 — okunamayan alan "yok" değildir; not modelin gördüğü yerde.
	var depsErr error
	for _, se := range in.SecondaryErrs {
		n := fmt.Sprintf("%s okunamadı (%s) — problem/reference içindeki boş %s listesi ve 0 sayısı 'yok' demek DEĞİL; bu alan için kanıt yok.",
			se.Name, sourcestate.Classify(se.Err), se.Name)
		switch se.Name {
		case "top_operations":
			n += " traffic_mix_shift ve p95/karışım-kayması uyarısı değerlendirilemedi."
		case "dependencies":
			depsErr = se.Err
		}
		notes = append(notes, n)
	}
	pr, rf := in.Cmp.Problem, in.Cmp.Reference
	for _, p := range []struct {
		label string
		st    chstore.PeriodStats
	}{{"sorun", pr}, {"referans", rf}} {
		switch {
		case p.st.Requests == 0:
			// v0.10.944 — "istek yok" üç ayrı durumdur: yanlış yazılmış bir
			// operation süzgeci (bağımlılıklar servis geneli olduğundan onu
			// gizlerdi — ÖNCE bakılır), yalnız istemci/üretici/iç span üreten
			// bir servis (worker, zamanlanmış iş) ya da gerçekten veri yok.
			var calls uint64
			for _, d := range p.st.Dependencies {
				calls += d.Calls
			}
			switch {
			case in.OperationSet:
				notes = append(notes, fmt.Sprintf("%s penceresinde giriş span'i YOK — operation süzgeci hiçbir giriş span'iyle eşleşmemiş olabilir; adı list_operations ile doğrula (tam ad). Bu 'trafik yok' demek DEĞİL.", p.label))
			case calls > 0:
				fromEntry := "RED, yüzdelikler ve pods/versions giriş span'lerinden okunur, bu dönem için boş."
				if in.VersionsSource == chstore.PeriodSourceVersionsMV {
					fromEntry = "RED, yüzdelikler ve pods giriş span'lerinden okunur, bu dönem için boş (versions service_version_5m'den, tüm span'ler)."
				}
				notes = append(notes, fmt.Sprintf("%s penceresinde giriş span'i YOK ama servisin %d istemci/üretici çağrısı var — servis yalnız client/producer/internal span üretiyor olabilir (worker, zamanlanmış iş); 'servis kapalıydı' DEMEK DEĞİL. %s", p.label, calls, fromEntry))
			case depsErr != nil:
				// v0.10.944 — bağımlılık okunamadıysa "veri yok" kanıtlanmadı.
				notes = append(notes, fmt.Sprintf("%s penceresinde giriş span'i YOK; dependencies okunamadığı için servisin yalnız client/producer span'i üretip üretmediği bilinmiyor — 'istek yok / servis kapalı' DEME.", p.label))
			default:
				notes = append(notes, fmt.Sprintf("%s penceresinde giriş span'i YOK — istek alınmamış ya da veri yok; bu dönem için yüzdelik/oran kıyası yapılamaz.", p.label))
			}
			continue
		case p.st.LowSample:
			notes = append(notes, fmt.Sprintf("Düşük örnek: %s penceresinde %d giriş span'i (<%d) — yüzdelikler ve oranlar güvenilir değil.", p.label, p.st.Requests, chstore.PeriodLowSampleMin))
		}
		if p.st.Coverage < chstore.PeriodCoverageNoteMin {
			notes = append(notes, fmt.Sprintf("Kapsama düşük: %s penceresinin 5 dk kovalarının %%%.0f'inde veri var (%d/%d) — düşük trafik, gecikmeli veri ya da kesinti olabilir.", p.label, p.st.Coverage*100, p.st.BucketsWithData, p.st.BucketsTotal))
		}
	}
	d95 := in.Cmp.Deltas.P95Ms
	if !d95.NoData && d95.RelPct != nil && math.Abs(*d95.RelPct)/100 >= chstore.PeriodP95MovedMin &&
		math.Abs(in.Cmp.MaxMixShiftPP)/100 >= chstore.PeriodMixShiftMin {
		var sh chstore.PeriodMixShift
		for _, m := range in.Cmp.TrafficMixShift {
			if m.Operation == in.Cmp.MaxMixShiftOperation {
				sh = m
				break
			}
		}
		notes = append(notes, fmt.Sprintf("p95 %+.0f%% değişti VE trafik karışımı kaydı (en büyük kayma: %s payı %%%.0f → %%%.0f) — p95 farkının bir kısmı karışımdan gelebilir; top_operations'taki operasyon p95'lerini kıyasla.",
			*d95.RelPct, sh.Operation, sh.ShareReference*100, sh.ShareProblem*100))
	}
	if in.EnvNote != "" {
		notes = append(notes, in.EnvNote)
	}
	if in.OperationSet {
		notes = append(notes, "dependencies servis genelidir: operation süzgeci istemci span'lerine uygulanamaz (istemci span'i giriş operasyonunun adını taşımaz).")
	}
	if in.ClusterNote != "" {
		notes = append(notes, in.ClusterNote)
	}
	if in.LengthsDiffer {
		notes = append(notes, "Pencere uzunlukları farklı: requests/errors mutlak sayıları doğrudan kıyaslanamaz — rate_per_s ve error_rate_pct kıyaslanır.")
	}
	if in.Delayed {
		notes = append(notes, "Sorun penceresinin sonu son 2 dakika içinde: en son span'ler henüz yazılmamış olabilir (son kova eksik görünebilir; rate_per_s düşük, kapsama eksik görünür — bu KESİNTİ değil); bu bir gecikme ÖLÇÜMÜ değil, uyarıdır.")
	}
	if in.VersionsSource == chstore.PeriodSourceVersionsMV {
		notes = append(notes, "versions sayıları service_version_5m'den: servisin TÜM span'leri (yalnız giriş span'leri değil, sürüm anahtarı taşıyanlar), pencereler 5 dk ızgarasına yuvarlı — hangi sürümün koştuğunu gösterir; sayıları requests ile kıyaslama.")
	}
	notes = append(notes, in.WindowNotes...)
	return notes
}

// cpSourceOpts — kaynak durumunu etkileyen, okumanın dışındaki gerçekler.
type cpSourceOpts struct {
	OpSet       bool     // operation süzgeci dependencies'e uygulanamadı
	Delayed     bool     // sorun penceresinin sonu taze: "gecikmeli" rozeti
	ProblemNote []string // yalnız sorun penceresine ait notlar (saat kayması)
}

// cpSources — SAF: iki pencerenin kaynak durumu. İkincil okuma hataları ve
// uygulanamayan süzgeçler kısmi (partial) notu olarak İKİSİNE de yazılır
// (her okuma iki pencereyi birden tarar). v0.10.944 — Returned = giriş span'i
// + bağımlılık çağrıları: yalnız istemci span'i üreten bir servisin
// bağımlılık verisi olan penceresi "boş" değildir.
func cpSources(cmp chstore.PeriodComparison, raw chstore.PeriodCompareRaw, cur, ref chstore.PeriodWindow, o cpSourceOpts) []sourcestate.Status {
	secs := cpSecondaryErrs(raw)
	mk := func(label string, w chstore.PeriodWindow, ps chstore.PeriodStats, delayed bool, extra []string) sourcestate.Status {
		n := ps.Requests
		for _, d := range ps.Dependencies {
			n += d.Calls
		}
		st := sourcestate.Result("traces", "clickhouse", sourcestate.Outcome{Returned: int(n), Delayed: delayed}).
			WithWindow(w.From, w.To).WithNote(label, false)
		// v0.10.944 — pencere etiketi Detail'de: çip rozeti notları ve pencereyi
		// taşımaz (stepSourceState), aynı kaynaklı iki rozet ayırt edilemiyordu.
		st.Detail = label
		if ps.Requests == 0 && n > 0 {
			st = st.WithNote("giriş span'i yok; yalnız istemci/üretici span'leri okundu", false)
		}
		for _, x := range extra {
			st = st.WithNote(x, false)
		}
		for _, s := range secs {
			st = st.WithNote(fmt.Sprintf("%s okunamadı (%s)", s.Name, sourcestate.Classify(s.Err)), true)
		}
		if o.OpSet {
			st = st.WithNote("operation süzgeci dependencies okumasına uygulanamadı", true)
		}
		return st
	}
	return []sourcestate.Status{
		mk("sorun penceresi", cur, cmp.Problem, o.Delayed, o.ProblemNote),
		mk("referans penceresi", ref, cmp.Reference, false, nil),
	}
}

// cpShiftToWall — SAF (v0.10.944): range_s penceresinin sonu duvar saatini
// aşıyorsa (sohbet çıpası ≤5 dk gelecek kabul edilir — istemci saati
// ileride) pencere UZUNLUĞU KORUNARAK şimdiye kaydırılır: previous referansı
// ve rate_per_s tutarlı kalır, 5 dk alt sınırı bozulmaz. Kaydırma yoksa "".
func cpShiftToWall(cur chstore.PeriodWindow, wall time.Time) (chstore.PeriodWindow, string) {
	ahead := cur.To.Sub(wall)
	if ahead <= cpWallSlack {
		return cur, ""
	}
	l := cur.To.Sub(cur.From)
	shifted := chstore.PeriodWindow{From: wall.Add(-l).UTC(), To: wall.UTC()}
	return shifted, fmt.Sprintf("Çıpa duvar saatinin %s ilerisindeydi (istemci saat kayması); pencere sonu şimdiye çekildi.", ahead.Round(time.Second))
}

// cpGridNote — SAF: MV yolu pencereleri 1 dk ızgarasına yuvarladıysa okunan
// pencereleri söyler (hız ve kapsama bunlarla hesaplandı).
func cpGridNote(raw chstore.PeriodCompareRaw, cur, ref chstore.PeriodWindow) string {
	w := raw.Windows
	if w[0].To.IsZero() || (w[0].From.Equal(cur.From) && w[0].To.Equal(cur.To) && w[1].From.Equal(ref.From) && w[1].To.Equal(ref.To)) {
		return ""
	}
	iso := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	return fmt.Sprintf("spanmetrics_1m 1 dk ızgarası: okunan pencereler sorun %s–%s, referans %s–%s (uçlar dakikaya aşağı yuvarlandı; rate_per_s ve coverage bu pencerelerle).",
		iso(w[0].From), iso(w[0].To), iso(w[1].From), iso(w[1].To))
}

// cpEnvelope — v0.10.944: compare_periods zarfı; anahtarlar SABİT sırayla
// (marshalOrderedMap, trace_tools.go). Düz map alfabetik sıralanır: `sources`
// ve `notes` iri `problem`/`reference` nesnelerinin ARKASINA düşüyor, sohbetin
// 6000 rune'luk model kırpması (clampToolResultForModel) ikincil okuma
// hatalarını modelden saklıyordu. Durum + notlar önde, iri veri sonda;
// fail() yolu da aynı şekil.
type cpEnvelope map[string]any

var (
	cpEnvelopeFirst = []string{"service", "scope", "sources", "notes", "window", "reference_window",
		"read_source", "versions_source", "percentile_method", "requests_definition", "deltas",
		"operations_truncated", "dependencies_truncated"}
	cpEnvelopeLast = []string{"problem", "reference", "traffic_mix_shift"}
)

// MarshalJSON — sabit sıra; listelenmemiş anahtarlar alfabetik, ortada.
func (e cpEnvelope) MarshalJSON() ([]byte, error) {
	return marshalOrderedMap(e, cpEnvelopeFirst, cpEnvelopeLast)
}

// runComparePeriods — tool gövdesi; okuyucu arayüzden gelir (test edilebilir).
// wall — v0.10.944: duvar saati, YALNIZ doğrulama (gelecek/tazelik) için;
// pencereyi kurmaz. Handler wallNow() verir, test sabit bir an.
func runComparePeriods(ctx context.Context, d Deps, r comparePeriodsReader, a comparePeriodsArgs, wall time.Time) (any, error) {
	svc := strings.TrimSpace(a.Service)
	if svc == "" {
		return nil, fmt.Errorf("service zorunlu (list_services'teki tam ad)")
	}
	cur, err := cpProblemWindow(ctx, a)
	if err != nil {
		return nil, err
	}
	// v0.10.944 — gelecek/tazelik denetimi ÇIPADAN BAĞIMSIZ. Eskiden yalnız
	// çıpasız koşuyordu; ama chatAnchorTime 5 dk'ya kadar gelecek çıpayı kabul
	// ediyor ve çıpalı pencereler son dakikalarda bitebiliyor — sorun
	// penceresi yazılmamış zamanı kapsıyor, rate_per_s düşük çıkıyor ve
	// kapsama notu "kesinti" diyordu, uyarısız. Referans penceresi ancak bu
	// düzeltmeden SONRA hesaplanır (previous kayan pencereyi izler).
	var problemNotes []string
	if strings.TrimSpace(a.FromISO) != "" {
		if cur.To.After(wall.Add(time.Minute)) {
			return nil, fmt.Errorf("to_iso geçersiz: pencere sonu gelecekte olamaz")
		}
	} else if shifted, note := cpShiftToWall(cur, wall); note != "" {
		cur = shifted
		problemNotes = append(problemNotes, note)
	}
	delayed := cur.To.After(wall.Add(-cpDelayedWithin))
	ref, refKind, err := cpReferenceWindow(a, cur)
	if err != nil {
		return nil, err
	}
	refJSON := cpWindowJSON(ref)
	refJSON["kind"] = refKind
	out := cpEnvelope{
		"service": svc, "window": cpWindowJSON(cur), "reference_window": refJSON,
		"requests_definition": cpRequestsDefinition, "percentile_method": cpPercentileMethod("", ""),
	}
	fail := func(err error) (any, error) {
		if sourcestate.IsCancelled(err) {
			return nil, err
		}
		out["problem"], out["reference"] = nil, nil
		out["notes"] = []string{cpNoteSampling, "Trace kaynağı okunamadı — bu kıyas için kanıt YOK; diğer kaynaklarla devam et."}
		// v0.10.944 — pencere etiketi Detail önünde (rozet başlığı; Detail
		// stepSourceStatuses'ta 160 rune'a kırpılır, önek hep kalır).
		p := sourcestate.FromError("traces", "clickhouse", err).WithWindow(cur.From, cur.To)
		p.Detail = "sorun penceresi: " + p.Detail
		r := sourcestate.FromError("traces", "clickhouse", err).WithWindow(ref.From, ref.To)
		r.Detail = "referans penceresi: " + r.Detail
		out["sources"] = []sourcestate.Status{p, r}
		return out, nil
	}
	if r == nil {
		return fail(fmt.Errorf("trace deposu %w", sourcestate.ErrNotConfigured))
	}

	// v0.10.944 — varlık denetimi TÜM alt-dize eşleşmelerini kapsar.
	// ListServiceNames ILIKE '%svc%' ORDER BY service_name LIMIT 50: binlerce
	// servisli bir filoda kısa/genel bir adın ('checkout') ondan ÖNCE sıralanan
	// 50+ eşleşmesi olabilir ve var olan servis 50'lik sayfaya girmeyip
	// not_found alıyordu. total sayfadan büyükse eşleşmelerin tamamı (tavan
	// serviceCatalogueMax) okunur; alt-dize süzgeci okumayı ucuz tutar ve
	// ListServiceNames'in boş-MV ham yedeği (v0.8.234) korunur.
	names, total, err := r.ListServiceNames(ctx, svc, 50, 0)
	if err != nil {
		return fail(err)
	}
	if total > len(names) && !slices.Contains(names, svc) {
		lim := min(total, serviceCatalogueMax)
		if names, _, err = r.ListServiceNames(ctx, svc, lim, 0); err != nil {
			return fail(err)
		}
	}
	found := slices.Contains(names, svc)
	if !found {
		msg := fmt.Sprintf("service %q bulunamadı — list_services ile tam adı doğrula", svc)
		if all, _, ferr := r.ListServiceNames(ctx, "", serviceCatalogueMax, 0); ferr == nil {
			// Tam eşleşme katalogda varsa servis VARDIR (hata yok); "mı?"
			// ipucu yalnız harf-farkı / yakın ad için.
			if exact, cands := ResolveServiceAmong(svc, all, 8); exact == svc {
				found = true
			} else if exact != "" {
				msg = fmt.Sprintf("service %q bulunamadı — %q mı? tam adla tekrar çağır", svc, exact)
			} else if len(cands) > 0 {
				msg = fmt.Sprintf("service %q bulunamadı — adaylar: %s", svc, strings.Join(cands, ", "))
			}
		}
		if !found {
			return nil, fmt.Errorf("%s", msg)
		}
	}

	scope := chstore.PeriodScope{Service: svc, Namespace: strings.TrimSpace(a.Namespace), Operation: strings.TrimSpace(a.Operation)}
	clusterNote := ""
	if c := strings.TrimSpace(a.Cluster); c != "" {
		if cs, ok := clustersFor(d, c); ok && len(cs) > 0 {
			scope.Clusters = cs[0].SpanValues
			if len(scope.Clusters) == 0 {
				scope.Clusters = []string{cs[0].Name}
			}
		} else {
			scope.Clusters = []string{c}
			clusterNote = fmt.Sprintf("cluster %q Remote Cluster kaydında yok; span 'cluster' değeri olarak birebir uygulandı.", c)
		}
	}

	seen, err := r.ReadPeriodEnvironments(ctx, svc, cur, ref)
	if err != nil {
		return fail(err)
	}
	seen = slices.Clone(seen)
	slices.Sort(seen)
	seen = slices.Compact(seen)
	env, envNote, err := cpResolveEnv(a.Env, seen)
	if err != nil {
		return nil, err
	}
	scope.Env = env

	raw, err := r.ReadComparePeriods(ctx, scope, cur, ref)
	if err != nil {
		return fail(err)
	}
	cmp := chstore.BuildPeriodComparison(raw, cur, ref)
	opSet := scope.Operation != ""
	out["scope"] = map[string]any{
		"env": env, "envs_seen": seen, "cluster": strings.TrimSpace(a.Cluster), "cluster_values": scope.Clusters,
		"namespace": scope.Namespace, "operation": scope.Operation,
	}
	// v0.10.944 — okuma yolu ilanı: RED + top_operations'ın tablosu, sürüm
	// tablosu; yüzdelik yöntemi de buna göre.
	out["read_source"], out["versions_source"] = raw.Source, raw.VersionsSource
	out["percentile_method"] = cpPercentileMethod(raw.Source, raw.SourceReason)
	out["problem"], out["reference"] = cmp.Problem, cmp.Reference
	out["deltas"] = cmp.Deltas
	out["traffic_mix_shift"] = cmp.TrafficMixShift
	out["operations_truncated"], out["dependencies_truncated"] = cmp.OperationsTruncated, cmp.DependenciesTrunc
	winNotes := append([]string(nil), problemNotes...)
	if g := cpGridNote(raw, cur, ref); g != "" {
		winNotes = append(winNotes, g)
	}
	out["notes"] = cpNotes(cpNoteInput{
		Cmp: cmp, EnvNote: envNote, OperationSet: opSet, ClusterNote: clusterNote,
		LengthsDiffer: cur.To.Sub(cur.From) != ref.To.Sub(ref.From), Delayed: delayed,
		Source: raw.Source, SourceReason: raw.SourceReason, VersionsSource: raw.VersionsSource, WindowNotes: winNotes,
		SecondaryErrs: cpSecondaryErrs(raw),
	})
	rcur, rref := cur, ref
	if !raw.Windows[0].To.IsZero() && !raw.Windows[1].To.IsZero() {
		rcur, rref = raw.Windows[0], raw.Windows[1]
	}
	out["sources"] = cpSources(cmp, raw, rcur, rref, cpSourceOpts{OpSet: opSet, Delayed: delayed, ProblemNote: problemNotes})
	return out, nil
}

// comparePeriodsTool — compare_periods (v0.10.944, yeni).
func comparePeriodsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "compare_periods",
		ShortDescription: "Sorun vs referans pencere (önceki/dün/hafta): RED, pencere p95, operasyon karışımı, bağımlılık, pod, sürüm; çok ortamda env şart.",
		Description: "Compare ONE service between a problem window and a reference window and get, per period: requests (ENTRY spans only: kind server + consumer), rate_per_s, errors, error_rate_pct (percent, 0..100), " +
			"p50_ms/p95_ms/p99_ms computed over the WHOLE window — a tdigest merge of spanmetrics_1m states, or one quantileTDigest over raw spans (never an average of bucket percentiles; percentile_method + read_source say which), samples + low_sample (<50), " +
			"coverage (fraction of 5-minute buckets with data), top_operations (≤10: count, share, error_rate_pct, p95), dependencies (≤10 downstream targets seen from this service's client/producer spans: db / messaging / service, calls, error_rate_pct, p95 as seen by the caller), " +
			"pods (distinct + top 10) and versions (distinct + top 10; container image tag → service.version chain). Also: deltas (abs + rel_pct, null when the reference is 0), traffic_mix_shift (per-operation share delta in percentage points), " +
			"notes (sampling caveat always; low sample, coverage < 0.9, and a traffic-mix caveat when p95 moved while the operation mix shifted), sources (one status per window), window/reference_window in UTC ISO + unix ns. " +
			"requests/RED, top_operations and pods come from ENTRY spans only (versions too, except on the service_version_5m path, where they count all of the service's spans and a note says so); a service that emits only internal/producer spans (worker, scheduled job) shows requests=0 with dependencies still populated — that is not 'no traffic'. " +
			"Use it to answer 'what changed during the incident?' after get_service_health or list_problems names a service. " +
			"Window: from_iso+to_iso (RFC3339, both or neither) OR range_s (anchored to the chat's window end); each window 5 min … 24 h. " +
			"reference: previous (default — same length immediately before) | day_before | week_before | custom (ref_from_iso + ref_to_iso, must not overlap). " +
			"ENV: if the service exists in more than one deploy_env in these windows, env is REQUIRED and the call fails with bad_args listing the environments — same-named services in different environments are never merged. " +
			"Trace columns are fixed: service_name, deploy_env, cluster, k8s_namespace, k8s_pod; operation filters entry spans only (dependencies stay service-wide and the note says so). " +
			"COST: RED + top_operations read the spanmetrics_1m pre-aggregate (it carries kind, so the entry-span population is exact; tdigest merge of its states over the whole window) whenever no env/cluster/namespace filter is set and both windows start inside its coverage (30-day TTL); " +
			"otherwise they read raw spans because env/cluster/namespace is not an MV dimension (or the window predates MV coverage). Dependencies always read raw spans (no MV carries the target by caller); pods read the promoted k8s_pod column; " +
			"versions read service_version_5m when unfiltered, else raw spans. Up to 6 bounded queries with per-query time caps (a slow secondary read yields a partial source, not a failure); do not call it in a loop. " +
			"Counts come from spans that reached Coremetry: with upstream sampling they are not exact traffic totals.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":   map[string]any{"type": "string", "description": "Exact service name (from list_services)."},
				"env":       map[string]any{"type": "string", "description": "deploy_env value (int/uat/prep/prod style, from list_environments). Required when the service runs in more than one environment."},
				"cluster":   map[string]any{"type": "string", "description": "Remote Cluster id / name (list_clusters) or a raw span `cluster` value."},
				"namespace": map[string]any{"type": "string", "description": "Exact k8s namespace (k8s.namespace.name)."},
				"operation": map[string]any{"type": "string", "description": "Exact entry-span operation name (list_operations). Narrows RED/operations/pods/versions; dependencies stay service-wide."},
				"from_iso":  map[string]any{"type": "string", "description": "Problem window start, RFC3339 UTC (with to_iso)."},
				"to_iso":    map[string]any{"type": "string", "description": "Problem window end, RFC3339 UTC (with from_iso)."},
				"range_s": map[string]any{
					"type": "integer", "minimum": 300, "maximum": 86400,
					"description": "Problem window as seconds back from the chat's window end, when from_iso/to_iso are not given. Default 1800.",
				},
				"reference": map[string]any{
					"type": "string", "enum": []string{"previous", "day_before", "week_before", "custom"},
					"description": "Reference window. previous (default) = same length right before; day_before / week_before = same hours 1 day / 7 days earlier; custom = ref_from_iso + ref_to_iso.",
				},
				"ref_from_iso": map[string]any{"type": "string", "description": "Custom reference start, RFC3339 UTC (reference=custom)."},
				"ref_to_iso":   map[string]any{"type": "string", "description": "Custom reference end, RFC3339 UTC (reference=custom)."},
			},
			"required": []string{"service"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a comparePeriodsArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r comparePeriodsReader
			if d.Store != nil { // nil *Store'u arayüze koymak nil-olmayan arayüz üretirdi
				r = d.Store
			}
			return runComparePeriods(ctx, d, r, a, wallNow())
		},
	}
}
