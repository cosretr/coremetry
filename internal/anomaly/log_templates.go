package anomaly

// v0.6.27 — new-template detector. Complements log_patterns.go
// (curated regex against ~20 known production failure shapes)
// with an OPEN-ENDED signal: when the Drain log templater
// (internal/templater) first observes a shape it's never seen
// before, that's an anomaly worth surfacing — operators don't
// know what tomorrow's log line looks like, but they want a
// pinned "this just started happening" feed.
//
// Spike detection (a known template firing 10× more than
// baseline) is intentionally deferred — log_templates rows
// store cumulative total_count + first_seen + last_seen, no
// per-window count column, so spike math needs a schema
// addition. Ship "new" first; revisit spike when the operator
// asks.
//
// Fired anomalies flow through the same UpsertAnomalyEvent
// path as log_pattern + trace_op detections; the /anomalies
// page renders them for free.
//
// v0.10.1030 — "yeni" artık KİMLİK değil AİLE demek. Operatör (prod, ekran
// görüntüsüyle): "Çok fazla problem geliyor." — tek servis için on+
// `log_template_new` satırı, hepsi aynı saniyede doğmuş, hepsi "no signal",
// desenleri aynı uzun JSON önekiyle başlıyor. Kök neden templater'da: puller
// her tikte ~1000 satırlık örnekten ağacı SOĞUK kurar ve küme kimliği her
// inceltmede yeniden hesaplanır; aynı satır ailesi tikten tike farklı `<*>`
// konumlarıyla, yani farklı kimlikle defterde first_seen = şimdi olan yeni
// bir satır olarak doğar (ayrıntı: templater/family.go). Dedektör artık bir
// adayı, servisinin pencereden ÖNCE doğmuş bir şablonuyla Drain'in kendi
// kümeleme kuralına göre aynı aileyse (templater.SameTemplateFamily)
// bastırır, ve aynı tikte aile başına yalnız BİR aday çıkarır.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/templater"
)

// LogTemplateAnomaly is one Drain template that crossed into
// existence within `window`. The same fingerprint (template ID)
// surfaces every tick until the row falls outside the window —
// UpsertAnomalyEvent dedupes via ReplacingMergeTree(version).
type LogTemplateAnomaly struct {
	TemplateID  string
	Template    string
	Service     string // v0.10.1030: lexicographically smallest of the template's services (emittedService)
	FirstSeenNs int64
	LastSeenNs  int64
	TotalCount  uint64
	Sample      string
}

// logTemplateLister — dedektörün tek depo bağımlılığı (*chstore.Store
// karşılar). v0.10.1030: bilinen-okuma hatasının "hiçbir aday çıkmaz" yönü
// sahte depoyla DAVRANIŞ olarak test edilebilsin diye arayüz.
type logTemplateLister interface {
	ListLogTemplates(ctx context.Context, f chstore.ListLogTemplatesFilter) ([]chstore.LogTemplate, error)
}

const (
	// v0.9.47 tabanı — aşağıdaki newTemplateCandidates yorumu. v0.10.1030:
	// bilinen şablonlara da uygulanır (bir-iki kez görülmüş blip "bilinen"
	// olup kendi inceltilmiş, gerçek ≥3 şablonunu 7 gün bastırmasın).
	newTemplateMinCount = 3
	// v0.10.1030 — aday okumasının tavanı okuyucunun azamisi (500; eskiden
	// 100). Sel anında pencere içi doğumlar 100'ü aşabiliyordu.
	newTemplateCandidateLimit = 500
	// v0.10.1030 — bilinen şablonlar: son 7 günde GÖRÜLMÜŞ (last_seen) ve
	// pencereden ÖNCE doğmuş (first_seen) şablonlar, en son görülen önce.
	// 7 gün susup geri dönen bir aile "yeni" sayılır (geri dönüş de haber).
	knownTemplatesHorizon = 7 * 24 * time.Hour
	// ListLogTemplates'in KENDİ tavanı 500: daha büyük bir Limit sessizce
	// 100'e düşer (chstore/log_templates.go), o yüzden 5000 gibi "cömert" bir
	// değer yazmak bütçeyi KÜÇÜLTÜRDÜ. 500 satır SQL'de adayların
	// servisleri, belirteç sayıları, sayı tabanı ve first_seen < pencere
	// başıyla daraltılmış satırlara harcanır.
	knownTemplatesLimit = 500
)

// DetectNewLogTemplates returns Drain templates whose first_seen
// landed inside [now-window, now]. The templater puller writes
// these rows; we just query them.
//
// Window guidance: the templater puller runs every 5min by
// default (see templater/puller.go), so window=10min covers the
// last two puller cycles — enough to absorb any clock drift
// between puller + recorder. The anomaly_event dedupe collapses
// repeated detections.
//
// v0.10.1030 — pencerede doğmak artık yetmez: aday, servisinin bilinen bir
// şablonunun Drain-ailesindense (varyant) bastırılır; aynı tikte aile başına
// tek aday çıkar (filterNewTemplateFamilies). Tik başına en çok İKİ okuma:
// aday okuması + aday varsa TEK bilinen-şablon okuması. İkisi AYNI sinceNs
// ile kurulur ve tam tümleyendir: aday = first_seen >= sinceNs, bilinen =
// first_seen < sinceNs (ns kesin bind).
func DetectNewLogTemplates(ctx context.Context, store *chstore.Store, window time.Duration) ([]LogTemplateAnomaly, error) {
	return detectNewLogTemplates(ctx, store, window, time.Now())
}

func detectNewLogTemplates(ctx context.Context, store logTemplateLister, window time.Duration, now time.Time) ([]LogTemplateAnomaly, error) {
	if window <= 0 {
		window = 10 * time.Minute
	}
	sinceNs := now.Add(-window).UnixNano()
	tmpls, err := store.ListLogTemplates(ctx, candidateTemplatesFilter(sinceNs))
	if err != nil {
		return nil, err
	}
	out := []LogTemplateAnomaly{}
	cands := parseLogTemplates(newTemplateCandidates(tmpls, sinceNs))
	if len(cands) == 0 {
		// Aday yoksa bilinen okuması da yok — boş tik CH'ye ikinci kez
		// dokunmaz.
		return out, nil
	}
	known, err := store.ListLogTemplates(ctx, knownTemplatesFilter(cands, now, sinceNs))
	if err != nil {
		// FAIL-CLOSED (bilinçli yön, aiops §5 "yumuşak-hata yönü"): bilinen
		// küme okunamadıysa "hepsi yeni" diye çıkarmak tam da v0.10.1030'un
		// kapattığı sel olurdu. Kaçırılan bir "yeni şablon" notu selden çok
		// daha ucuz; gerçekten yeni bir aile pencerede (10 dk) kaldıkça
		// sonraki tiklerde yine aday olur. Tik DÜŞMEZ: kayıtçı bu hatayı tik
		// başına bir kez yazar ve öbür dedektörlere geçer.
		return nil, fmt.Errorf("known templates read failed, %d candidates held back this tick (fail-closed): %w", len(cands), err)
	}
	emit, st := filterNewTemplateFamilies(cands, parseLogTemplates(usableKnownTemplates(known, sinceNs)))
	if st.VariantOfKnown > 0 || st.SameTickDup > 0 {
		// Tik başına tek özet satırı — yalnız sayılar, şablon metni YOK.
		log.Printf("[anomaly-recorder] new log templates: candidates=%d known=%d (limit %d) emitted=%d suppressed_known_variant=%d suppressed_same_tick=%d",
			st.Candidates, st.Known, knownTemplatesLimit, st.Emitted, st.VariantOfKnown, st.SameTickDup)
	}
	for _, t := range emit {
		out = append(out, logTemplateAnomalyFrom(t))
	}
	return out, nil
}

// candidateTemplatesFilter — v0.10.1030: aday okuması. Doğum penceresi
// first_seen ile SQL'de (eskiden last_seen >= sinceNs + Go'da first_seen
// süzgeci: puller örneği en yeniden eskiye okuduğu için bir kümenin
// FirstSeen'i LastSeen'inden SONRA olabilir — first_seen pencerede ama
// last_seen değil olan satır ne aday ne bilinen kümesine giriyordu). Sayı
// tabanı da SQL'de; en ERKEN doğanlar önce ki aile temsilcileri tavanı
// atlatsın.
func candidateTemplatesFilter(sinceNs int64) chstore.ListLogTemplatesFilter {
	return chstore.ListLogTemplatesFilter{
		FirstSeenSinceNs: sinceNs,
		MinTotalCount:    newTemplateMinCount,
		SortBy:           "first_seen_asc",
		Limit:            newTemplateCandidateLimit,
	}
}

// newTemplateCandidates — v0.10.1030'da SAF işleve taşındı; seçim v0.6.27 /
// v0.9.47 ile AYNI: pencerede doğmuş ve TotalCount >= 3. v0.10.1030'dan beri
// ikisi de SQL'de; burası emniyet kemeri.
func newTemplateCandidates(tmpls []chstore.LogTemplate, sinceNs int64) []chstore.LogTemplate {
	out := make([]chstore.LogTemplate, 0, len(tmpls))
	for _, t := range tmpls {
		// Filter to templates whose first_seen is in the window —
		// "born in the window" specifically (sınır dahil: first_seen ==
		// sinceNs adaydır, bilinen DEĞİL).
		if t.FirstSeen < sinceNs {
			continue
		}
		// v0.9.47 — >= 3 tabanı: bir-iki kez görülen taze template
		// anlık blip'tir (operatör isteği; new_error/new-pattern'ın
		// 3 tabanıyla simetrik). Gerçek yeni hata hattı dakikalar
		// içinde 3'ü geçer ve bir sonraki turda yakalanır.
		if t.TotalCount < newTemplateMinCount {
			continue
		}
		out = append(out, t)
	}
	return out
}

// usableKnownTemplates — bilinen okumasının emniyet kemeri (SQL'deki
// first_seen < sinceNs ve total_count >= 3'ün Go aynası): sınırdaki ya da
// blip bir satır, depo ne döndürürse döndürsün bilinen SAYILMAZ.
func usableKnownTemplates(known []chstore.LogTemplate, sinceNs int64) []chstore.LogTemplate {
	out := make([]chstore.LogTemplate, 0, len(known))
	for _, k := range known {
		if k.FirstSeen >= sinceNs || k.TotalCount < newTemplateMinCount {
			continue
		}
		out = append(out, k)
	}
	return out
}

// parsedLogTemplate — defter satırı + bir kez ayrılmış şablonu. Tik başına
// her şablon BİR kez ayrılır; aday × bilinen çiftleri yeniden ayırmaz.
type parsedLogTemplate struct {
	chstore.LogTemplate
	shape templater.ParsedTemplate
}

func parseLogTemplates(in []chstore.LogTemplate) []parsedLogTemplate {
	out := make([]parsedLogTemplate, len(in))
	for i, t := range in {
		out[i] = parsedLogTemplate{LogTemplate: t, shape: templater.ParseTemplate(t.Template)}
	}
	return out
}

// knownTemplatesFilter — v0.10.1030: tik başına TEK bilinen-şablon okumasının
// süzgeci. SAF. Bilinen = first_seen pencere başından ÖNCE (adayların tam
// tümleyeni, aynı sinceNs), total_count >= 3, last_seen son 7 günde; en son
// görülen önce, 500 satır. LIMIT ilgili satırlara harcansın diye SQL'de iki
// kesin ön süzgeç daha:
//   - servis: adayların servisleri (hasAny); servissiz bir aday varsa
//     servissiz satırlar da (OR empty(services)) — servissiz bir aday, servis
//     adı taşımayan satırlardan doğar ve önceki varyantları da servissizdir;
//     filoyu toptan açmaya gerek yok.
//   - belirteç sayısı: adayların belirteç sayıları (Drain'de farklı sayı asla
//     aynı küme değil, bu süzgeç sonucu değiştirmez, yalnız bütçeyi korur).
func knownTemplatesFilter(cands []parsedLogTemplate, now time.Time, sinceNs int64) chstore.ListLogTemplatesFilter {
	svcs, serviceless := candidateServices(cands)
	return chstore.ListLogTemplatesFilter{
		SinceNs:            now.Add(-knownTemplatesHorizon).UnixNano(),
		FirstSeenBeforeNs:  sinceNs,
		MinTotalCount:      newTemplateMinCount,
		SortBy:             "last_seen",
		Limit:              knownTemplatesLimit,
		AnyServices:        svcs,
		IncludeServiceless: serviceless,
		TokenCounts:        candidateTokenCounts(cands),
	}
}

// candidateServices — adayların servis birleşimi, sıralı (deterministik
// bind) + servissiz aday var mı.
func candidateServices(cands []parsedLogTemplate) ([]string, bool) {
	seen := map[string]bool{}
	serviceless := false
	for _, c := range cands {
		if len(c.Services) == 0 {
			serviceless = true
		}
		for _, s := range c.Services {
			seen[s] = true
		}
	}
	if len(seen) == 0 {
		return nil, serviceless
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, serviceless
}

// candidateTokenCounts — adayların farklı belirteç sayıları, sıralı.
func candidateTokenCounts(cands []parsedLogTemplate) []int {
	seen := map[int]bool{}
	for _, c := range cands {
		if n := c.shape.TokenCount(); n > 0 {
			seen[n] = true
		}
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// familyFilterStats — tik özet satırının sayıları.
type familyFilterStats struct {
	Candidates     int
	Known          int
	Emitted        int
	VariantOfKnown int // bilinen bir şablonun Drain-ailesi → bastırıldı
	SameTickDup    int // aynı tikte aynı ailenin ikinci+ adayı → bastırıldı
}

// filterNewTemplateFamilies — v0.10.1030 "yeni"nin kararı. SAF (ClickHouse
// yok), girdi sırasından bağımsız.
//
//  1. Aday, kendisiyle en az bir servisi paylaşan (aday servissizse: HERHANGİ)
//     bir bilinen şablonla Drain-ailesindense (templater.SameTemplateFamily)
//     VARYANTTIR → bastırılır. Yüklem Drain'in kendi kümeleme kuralı: Drain o
//     iki şablonu aynı kümeye koyacaksa, aday yeni bir biçim değil, eski bir
//     biçimin bu tikteki inceltme düzeyidir.
//  2. Kalan adaylar arasında aile başına TEK aday çıkar: EN ERKEN first_seen,
//     eşitlikte ID. Temsilci kayıtçı tikleri arasında KARARLI kalmalı: olay
//     satırının parmak izi şablon kimliğidir; temsilci değişirse aynı aile
//     ikinci bir satır açar. first_seen yapışkan; total_count ise her puller
//     tikinde o tikin örnek sayısıyla ÜZERİNE yazılır (birikimli değil), o
//     yüzden sıralamaya HİÇ girmez. Erken doğan pencereden çıkınca bilinen
//     olur ve kardeşlerini (1) ile bastırır — aile tek satır kalır.
func filterNewTemplateFamilies(cands, known []parsedLogTemplate) ([]chstore.LogTemplate, familyFilterStats) {
	st := familyFilterStats{Candidates: len(cands), Known: len(known)}
	ordered := append([]parsedLogTemplate(nil), cands...)
	sort.SliceStable(ordered, func(i, j int) bool { return familyRepLess(ordered[i].LogTemplate, ordered[j].LogTemplate) })

	emitted := make([]parsedLogTemplate, 0, len(ordered))
	for _, c := range ordered {
		if variantOfKnown(c, known) {
			st.VariantOfKnown++
			continue
		}
		if dupOfEmitted(c, emitted) {
			st.SameTickDup++
			continue
		}
		emitted = append(emitted, c)
	}
	out := make([]chstore.LogTemplate, len(emitted))
	for i, e := range emitted {
		out[i] = e.LogTemplate
	}
	st.Emitted = len(out)
	return out, st
}

// familyRepLess — aile temsilcisinin TAM sıralaması (karıştırılmış girdi aynı
// çıktıyı versin): first_seen ↑, ID ↑, şablon ↑. TotalCount BİLEREK yok
// (tik başına üzerine yazılır — filterNewTemplateFamilies yorumu).
func familyRepLess(a, b chstore.LogTemplate) bool {
	if a.FirstSeen != b.FirstSeen {
		return a.FirstSeen < b.FirstSeen
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Template < b.Template
}

func variantOfKnown(c parsedLogTemplate, known []parsedLogTemplate) bool {
	for _, k := range known {
		// Emniyet: iki okuma atomik değil; depo adayın KENDİSİNİ bilinen
		// diye döndürürse aday kendini bastırmasın.
		if k.ID == c.ID {
			continue
		}
		if servicesOverlap(c.Services, k.Services) && c.shape.SameFamily(k.shape) {
			return true
		}
	}
	return false
}

func dupOfEmitted(c parsedLogTemplate, emitted []parsedLogTemplate) bool {
	for _, e := range emitted {
		if e.ID == c.ID {
			return true
		}
		if servicesOverlap(c.Services, e.Services) && c.shape.SameFamily(e.shape) {
			return true
		}
	}
	return false
}

// servicesOverlap — adayın servis listesi öbürüyle en az bir ortak servis
// paylaşıyor mu. Servissiz aday her şeyle karşılaştırılır (tasarım kuralı);
// servisli adayı servissiz bir şablon bastıramaz (ilgisiz bir kaynağın
// biçimi bir servisin yeni satırını gizlemesin).
func servicesOverlap(cand, other []string) bool {
	if len(cand) == 0 {
		return true
	}
	for _, a := range cand {
		for _, b := range other {
			if a == b {
				return true
			}
		}
	}
	return false
}

// emittedService — v0.10.1030: olayın servisi şablonun servisleri içinde
// SÖZLÜK SIRASINDA EN KÜÇÜK olan. Services[0] varış sırasını izler (puller
// her tik soğuk kurar) ve tikten tike değişebilir → farklı parmak izi →
// aynı şablona ikinci satır. Boş liste → "".
func emittedService(services []string) string {
	svc := ""
	for _, s := range services {
		if s == "" {
			continue
		}
		if svc == "" || s < svc {
			svc = s
		}
	}
	return svc
}

func logTemplateAnomalyFrom(t chstore.LogTemplate) LogTemplateAnomaly {
	// Template body is the cluster signature with placeholders
	// (e.g. "User <*> logged in from <*>"). Truncate the
	// human-facing pattern field for the anomaly_event ID +
	// rendering — anomaly_events.pattern is a String we don't
	// want unbounded. (v0.10.1030: aile kararı KESİLMEMİŞ tam
	// şablonla verilir; kesme yalnız burada, çıktıda.)
	return LogTemplateAnomaly{
		TemplateID:  t.ID,
		Template:    truncTemplate(t.Template, 160),
		Service:     emittedService(t.Services),
		FirstSeenNs: t.FirstSeen,
		LastSeenNs:  t.LastSeen,
		TotalCount:  t.TotalCount,
		Sample:      t.Sample,
	}
}

// truncTemplate truncates the Drain template at the nearest word
// boundary past max bytes; appends an ellipsis when cut.
func truncTemplate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndexAny(s[:max], " \t")
	if cut < max/2 {
		// no good boundary; hard cut
		cut = max
	}
	return s[:cut] + "…"
}
