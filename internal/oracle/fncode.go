package oracle

// fncode.go — v0.10.1000 — özne çözücünün FONKSİYON KODU basamağı (operatör
// teyidi 2026-10-01: Oracle hata satırının kod alanı — özel SQL kipinde
// FUNCTIONCODE — span'lerdeki FUNCTION_CODE attribute'u ile AYNI değerdir;
// "zaman içinde öğrendikçe güncellersin").
//
// Oracle satırının operasyon adı (DIGITAL_PAYMENT_EFT gibi) trace'lerde yok,
// ama satır fonksiyon kodunu da taşıyor ve o kod span'lerde var. Trace kimliği
// olmayan / Coremetry'de bulunmayan satırlar bu köprüyle servise bağlanır:
//
//	satır (operasyon, fonksiyon kodu) → o kodu taşıyan span'lerin servisi
//
// Kaynak başına AÇILIR (SourceConfig.FunctionCodeMatch; varsayılan kapalı —
// mevcut kaynakların öznesi değişmez). Açıkken:
//
//   - Observe her poll'da bu tikin kodlarını ÖNBELLEKTEN okur; önbellekte
//     olmayan / bayatlamış (10 dk) kodlar tek CH sorgusuyla tazelenir
//     (chstore.FunctionCodeServices: geniş rollup → terfi kolonu).
//   - TAZE okunan her (operasyon, kod) çifti, kodun servisi belliyse öğrenilmiş
//     haritaya BİR oy verir (trace ve pod oyu olmayan operasyonda). Harita
//     böylece zamanla onaylanır (≥3 teyit, ≥%70), servis değişirse döner,
//     30 gün teyitsiz kalırsa düşer — subject.go'nun mevcut kuralları.
//   - Resolve'da son basamak: trace → pod → öğrenilmiş → FONKSİYON KODU →
//     bilinmiyor. Yani harita henüz onaysızken de servis bulunur.
//
// Servis seçimi (PickFunctionService): kodu taşıyan HATA span'lerinin (≥3
// ise; yoksa tüm span'lerin) ≥%70'i tek servisteyse o servis. Kod çağrı
// zinciri boyunca birden çok serviste taşınıyorsa çoğunluk çıkmaz → servis
// uydurulmaz, not adayları söyler.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// SubjectSourceFunctionCode — ExternalSubjectResolution.Source değeri.
	SubjectSourceFunctionCode = "function_code"

	fnMinShare       = 0.70
	fnMinSpans       = 3
	fnFactTTL        = 10 * time.Minute // bu yaştan sonra kod yeniden okunur
	fnFactMaxAge     = 30 * time.Minute // Resolve bundan eski dağılıma güvenmez
	fnLookupWindow   = 30 * time.Minute
	fnLookupMaxCodes = 200
	fnCacheMax       = 2000
)

// isFunctionCodeColumn — SAF: kolon adı bir fonksiyon kodu kolonu mu
// (FUNCTIONCODE, FUNCTION_CODE, APP_ERR_FUNCTIONCODE…). Harf ve alt çizgi
// duyarsız; ad "FUNCTIONCODE" ile biter.
func isFunctionCodeColumn(name string) bool {
	n := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(name)), "_", "")
	return strings.HasSuffix(n, "FUNCTIONCODE")
}

// CodeIsFunctionCode — SAF: kaynağın `code` alanı (error.code) bir fonksiyon
// kodu kolonuna mı eşlenmiş (v0.10.902 eşlemesi: code ← FUNCTIONCODE). Değilse
// (v0.10.1001 — operatörün güncel sorgusu: code ← ERRORCODE, FUNCTIONCODE ayrı
// kolon) fonksiyon kodu satırın ATTRIBUTE'larından okunur.
func CodeIsFunctionCode(src SourceConfig) bool {
	return isFunctionCodeColumn(src.Columns[FieldCode])
}

// FunctionCodeOf — SAF (tablo testli): satırın fonksiyon kodu. Önce `code`
// alanı fonksiyon kodu kolonuna eşlenmişse o; değilse tüketilmeyen kolonlardan
// (attribute) adı fonksiyon kodu olan ilki. Kırpılmış; yoksa "".
func FunctionCodeOf(src SourceConfig, row chstore.OracleErrorRow) string {
	if CodeIsFunctionCode(src) {
		return strings.TrimSpace(row.ErrorCode) // sayaçla aynı kırpma (Oracle CHAR dolgusu)
	}
	for i, k := range row.AttrKeys {
		if i < len(row.AttrValues) && isFunctionCodeColumn(k) {
			return strings.TrimSpace(row.AttrValues[i])
		}
	}
	return ""
}

// FunctionCodeLookup — chstore.Store.FunctionCodeServices.
type FunctionCodeLookup func(ctx context.Context, codes []string, from, to time.Time) (chstore.FunctionCodeFacts, error)

// fnFact — bir kodun önbellekteki servis dağılımı.
type fnFact struct {
	dist []chstore.FunctionCodeService
	at   time.Time
}

// FnPick — dağılımdan seçim. Service boşsa çoğunluk yok / kanıt az; Best o
// durumda en güçlü adaydır (not için).
type FnPick struct {
	Service  string
	Best     string
	Hits     uint64
	Total    uint64
	Services int
	OnErrors bool // oylar hata span'lerinden (false = tüm span'ler)
}

// PickFunctionService — SAF (tablo testli): servis dağılımından özne.
// Hata span'i toplamı ≥ fnMinSpans ise ağırlık hata span'i, değilse tüm
// span'ler. Seçim: toplam ≥ fnMinSpans VE en büyük pay ≥ fnMinShare.
func PickFunctionService(dist []chstore.FunctionCodeService) FnPick {
	var errs uint64
	for _, d := range dist {
		errs += d.Errors
	}
	p := FnPick{OnErrors: errs >= fnMinSpans}
	for _, d := range dist {
		w := d.Spans
		if p.OnErrors {
			w = d.Errors
		}
		if w == 0 || d.Service == "" {
			continue
		}
		p.Services++
		p.Total += w
		if w > p.Hits || (w == p.Hits && d.Service < p.Best) {
			p.Best, p.Hits = d.Service, w
		}
	}
	if p.Total >= fnMinSpans && float64(p.Hits)/float64(p.Total) >= fnMinShare {
		p.Service = p.Best
	}
	return p
}

// MergeFunctionDists — SAF: birden çok kodun dağılımını servis başına toplar
// (küme Problem'i: operasyonun bu tikteki tüm kodları).
func MergeFunctionDists(dists ...[]chstore.FunctionCodeService) []chstore.FunctionCodeService {
	by := map[string]*chstore.FunctionCodeService{}
	for _, dist := range dists {
		for _, d := range dist {
			m := by[d.Service]
			if m == nil {
				m = &chstore.FunctionCodeService{Service: d.Service}
				by[d.Service] = m
			}
			m.Spans += d.Spans
			m.Errors += d.Errors
		}
	}
	out := make([]chstore.FunctionCodeService, 0, len(by))
	for _, m := range by {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// fnUnit — not metninde oy birimi.
func fnUnit(onErrors bool) string {
	if onErrors {
		return "hata span'i"
	}
	return "span"
}

// SetFunctionCodeLookup — fonksiyon kodu okuyucusunu bağlar (main.go). nil =
// basamak kapalı (kaynak ayarı açık olsa da).
func (r *SubjectResolver) SetFunctionCodeLookup(fn FunctionCodeLookup) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fnLookup = fn
}

// observeFunctionCodes — Observe'un parçası (r.mu tutulur): bu tikin (op, kod)
// çiftlerini toplar, eksik/bayat kodları tazeler, TAZE okunan çiftlerden
// öğrenme oyu üretir. Hata poll'u düşürmez; dönen dizge ObserveResult.Error'a
// eklenir.
func (r *SubjectResolver) observeFunctionCodes(ctx context.Context, src SourceConfig, rows []chstore.OracleErrorRow, to time.Time, tf *tickFacts, now time.Time) string {
	if !src.FunctionCodeMatch || r.fnLookup == nil {
		delete(r.fnFacts, src.ID)
		delete(r.fnPath, src.ID)
		r.fnOn[src.ID] = false
		return ""
	}
	r.fnOn[src.ID] = true
	r.fnFromCode[src.ID] = CodeIsFunctionCode(src)
	cache := r.fnFacts[src.ID]
	if cache == nil {
		cache = map[string]fnFact{}
		r.fnFacts[src.ID] = cache
	}
	seenPair, seenCode := map[string]bool{}, map[string]bool{}
	var need []string
	for _, row := range rows {
		code := FunctionCodeOf(src, row)
		if code == "" {
			continue
		}
		if pk := row.OperationCode + "\x00" + code; !seenPair[pk] {
			seenPair[pk] = true
			tf.opCodes[row.OperationCode] = append(tf.opCodes[row.OperationCode], code)
		}
		if seenCode[code] {
			continue
		}
		seenCode[code] = true
		if f, ok := cache[code]; (!ok || now.Sub(f.at) >= fnFactTTL) && len(need) < fnLookupMaxCodes {
			need = append(need, code)
		}
	}
	if len(need) == 0 {
		return ""
	}
	facts, err := r.fnLookup(ctx, need, to.Add(-fnLookupWindow), to.Add(subjectLookupPad))
	if err != nil {
		if !r.fnErr[src.ID] {
			log.Printf("[oracle/subject] %s: fonksiyon kodu araması: %v", src.Name, err)
		}
		r.fnErr[src.ID] = true
		return "fonksiyon kodu araması: " + err.Error()
	}
	r.fnErr[src.ID] = false
	r.fnPath[src.ID] = facts.Source
	if facts.Source == "" {
		return "" // okuma yolu yok (rollup da terfi kolonu da yok) — önbelleğe yazılmaz
	}
	fresh := make(map[string]bool, len(need))
	for _, code := range need {
		cache[code] = fnFact{dist: facts.ByCode[code], at: now} // boş dağılım da yazılır (her tik yeniden sorulmasın)
		fresh[code] = true
	}
	pruneFnCache(cache, fnCacheMax)
	// Öğrenme oyu: yalnız BU TİKTE taze okunan kodlar (aynı okuma üç tikte üç
	// "teyit" sayılmasın — teyit, bağımsız bir okumadır).
	for op, codes := range tf.opCodes {
		if op == "" {
			continue
		}
		for _, code := range codes {
			if !fresh[code] {
				continue
			}
			pick := PickFunctionService(cache[code].dist)
			if pick.Service == "" {
				continue
			}
			if tf.fnVotes[op] == nil {
				tf.fnVotes[op] = map[string]int{}
			}
			tf.fnVotes[op][pick.Service]++
			tf.fnTotals[op]++
		}
	}
	return ""
}

// pruneFnCache — SAF: tavan aşılırsa en eski okunanlar düşer.
func pruneFnCache(cache map[string]fnFact, max int) {
	if len(cache) <= max {
		return
	}
	type ca struct {
		code string
		at   time.Time
	}
	order := make([]ca, 0, len(cache))
	for c, f := range cache {
		order = append(order, ca{c, f.at})
	}
	sort.Slice(order, func(i, j int) bool {
		if !order[i].at.Equal(order[j].at) {
			return order[i].at.Before(order[j].at)
		}
		return order[i].code < order[j].code
	})
	for _, o := range order[:len(cache)-max] {
		delete(cache, o.code)
	}
}

// resolveByFunctionCode — Resolve'un son basamağı (r.mu tutulur). Serinin
// kodu (values[1] = error.code) YALNIZ kaynakta `code` alanı fonksiyon koduna
// eşlenmişse kullanılır; değilse (code ← ERRORCODE) ya da çağıran yalnız
// operasyonu verdiyse (küme Problem'i) operasyonun bu tikteki tüm fonksiyon
// kodları birleştirilir.
// Service boş + Note dolu = "denendi, şu yüzden çıkmadı".
func (r *SubjectResolver) resolveByFunctionCode(sourceID string, values []string, now time.Time) anomaly.ExternalSubjectResolution {
	if !r.fnOn[sourceID] {
		return anomaly.ExternalSubjectResolution{}
	}
	var codes []string
	if len(values) > 1 && r.fnFromCode[sourceID] {
		// Seri: kendi kodu. Boş / "-" / tavan serisi ("diğer") kod değildir.
		if c := values[1]; c != "" && c != counterQualifierNone && c != counterOtherCode {
			codes = []string{c}
		}
	} else if tf := r.tick[sourceID]; tf != nil && len(values) > 0 {
		codes = tf.opCodes[values[0]]
	}
	if len(codes) == 0 {
		return anomaly.ExternalSubjectResolution{}
	}
	if r.fnPath[sourceID] == "" {
		if r.fnErr[sourceID] {
			return anomaly.ExternalSubjectResolution{Note: "fonksiyon kodu araması düştü"}
		}
		return anomaly.ExternalSubjectResolution{Note: "fonksiyon kodu okunamıyor (geniş rollup / terfi kolonu yok)"}
	}
	cache := r.fnFacts[sourceID]
	dists := make([][]chstore.FunctionCodeService, 0, len(codes))
	for _, c := range codes {
		if f, ok := cache[c]; ok && now.Sub(f.at) <= fnFactMaxAge {
			dists = append(dists, f.dist)
		}
	}
	label := codes[0]
	if len(codes) > 1 {
		label = fmt.Sprintf("%s +%d", codes[0], len(codes)-1)
	}
	pick := PickFunctionService(MergeFunctionDists(dists...))
	switch {
	case pick.Service != "":
		return anomaly.ExternalSubjectResolution{Service: pick.Service, Source: SubjectSourceFunctionCode,
			Note: fmt.Sprintf("fonksiyon kodundan %s→%s (%d/%d %s)", label, pick.Service, pick.Hits, pick.Total, fnUnit(pick.OnErrors))}
	case pick.Total == 0:
		return anomaly.ExternalSubjectResolution{Note: fmt.Sprintf("fonksiyon kodu %s span'lerde görülmedi", label)}
	case pick.Services > 1:
		return anomaly.ExternalSubjectResolution{Note: fmt.Sprintf("fonksiyon kodu %s %d serviste, çoğunluk yok (en çok %s %d/%d %s)",
			label, pick.Services, pick.Best, pick.Hits, pick.Total, fnUnit(pick.OnErrors))}
	}
	return anomaly.ExternalSubjectResolution{Note: fmt.Sprintf("fonksiyon kodu %s için kanıt az (%s %d %s)", label, pick.Best, pick.Total, fnUnit(pick.OnErrors))}
}
