package api

// logs_patterns_compare.go — v0.10.1897 (Log Search Faz 4 dilim 4.4; audit
// §3.2 "İki aralık", §6 PatternCompare).
//
//   GET /api/logs/patterns/compare?<Desenler süzgeci>&from=&to=&bfrom=&bto=
//
// İki zaman aralığının desenlerini karşılaştırır: YENİ (A'da var, B'de yok),
// ARTAN (iki aralıkta da var, A'daki payı B'dekinin ≥ 1,8 katı — oran ifadesi
// merdiveninde "yaklaşık 2 kat"ın eşiği) ve KAYBOLAN (B'de var, A'da yok).
// A = sayfanın penceresi, B = kullanıcının seçtiği karşılaştırma aralığı.
//
// "Yeni" ve "kaybolan" örneklemden çıkan ADAYLARDIR; gerçek sayımla
// doğrulanır: yeni adayın B'de, kaybolan adayın A'da desene uyan satır var mı
// (PatternsExist, tek `_msearch`, size:0 + terminate_after:1, timeout 2s).
// Satır bulunursa aday "örneklemde yoktu ama aralıkta var" diye işaretlenir,
// yeni/kaybolan DENMEZ. Kısmi / zaman aşımı → "doğrulanamadı".
//
// ES İŞİ (1877 kuralı): iki aralık × TEK sayfa (500 satır) örnekleme + bir
// `_msearch` = 3 istek; mevcut Desenler sorgusu en çok 4 sayfa (2000 satır).
// Yalnız kullanıcı ikinci aralığı SEÇİNCE istemci ister (sekme açılışında
// değil); serveCached 30 sn; toplam bütçe 12 sn. CH log yolunda doğrulama
// yok ("unsupported"; örnekleme iki yolda da çalışır).
//
// Neden ayrı uç (Desenler'e `?compare=` değil): yanıt şekli farklı (üç liste)
// ve önbellek anahtarı iki pencere taşır; api.go büyümez (TestApiGoDoesNotGrow).
// Rol kapısı YOK — salt okuma, viewer da görür.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func init() {
	registerRoutesExtra("logs_patterns_compare", (*Server).registerLogsPatternsCompareRoutes)
}

func (s *Server) registerLogsPatternsCompareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs/patterns/compare", s.getLogsPatternsCompare)
}

const (
	patternsCompareSample    = 500 // aralık başına TEK ES sayfası
	patternsCompareRows      = 20  // liste başına satır
	patternsCompareVerifyMax = 10  // tür başına doğrulama (toplam ≤ PatternInsightsMax)
	patternsCompareRiseRatio = 1.8
	patternsCompareRiseMin   = 3 // artan: A'da en az bu kadar satır (gürültü tabanı)
	patternsCompareMaxSpan   = 31 * 24 * time.Hour
	patternsCompareBudget    = 12 * time.Second
)

// patternsCompareRow — bir desenin iki aralıktaki yeri.
type patternsCompareRow struct {
	Hash         string  `json:"hash"`
	Template     string  `json:"template"`
	Query        string  `json:"query"`
	Sample       string  `json:"sample"`
	Severity     uint8   `json:"severity"`
	SeverityText string  `json:"severityText,omitempty"`
	CountA       int64   `json:"countA"`
	CountB       int64   `json:"countB"`
	ShareA       float64 `json:"shareA"` // örneklemdeki pay (0..1)
	ShareB       float64 `json:"shareB"`
	Ratio        float64 `json:"ratio,omitempty"` // ShareA / ShareB (artan)
	// Verified — yalnız yeni / kaybolan: "yes" = öbür aralıkta gerçek sayım 0;
	// "no" = öbür aralıkta satır var (örneklem kaçırdı); "unknown" =
	// doğrulanamadı; "" = doğrulanmadı (ilk patternsCompareVerifyMax dışında).
	Verified string `json:"verified,omitempty"`
}

type patternsCompareWindow struct {
	FromNs    int64 `json:"fromNs"`
	ToNs      int64 `json:"toNs"`
	Sampled   int   `json:"sampled"`
	Total     int   `json:"total"`
	Truncated bool  `json:"truncated"`
	Distinct  int   `json:"distinct"`
}

type patternsCompareResult struct {
	A      patternsCompareWindow `json:"a"`
	B      patternsCompareWindow `json:"b"`
	New    []patternsCompareRow  `json:"new"`
	Rising []patternsCompareRow  `json:"rising"`
	Gone   []patternsCompareRow  `json:"gone"`
	// Verify — ok | partial | error | unsupported | none (doğrulanacak aday yok).
	Verify   string `json:"verify"`
	Degraded bool   `json:"degraded,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func compareWindowOf(f logstore.Filter, r *logstore.PatternsResult) patternsCompareWindow {
	w := patternsCompareWindow{FromNs: f.From.UnixNano(), ToNs: f.To.UnixNano()}
	if r != nil {
		w.Sampled, w.Total, w.Truncated, w.Distinct = r.Sampled, r.Total, r.Truncated, r.Distinct
	}
	return w
}

func compareRowOf(g logstore.SignatureGroup) patternsCompareRow {
	return patternsCompareRow{Hash: g.Hash, Template: g.Template, Query: g.Query, Sample: g.Sample,
		Severity: g.Severity, SeverityText: g.SeverityText}
}

// comparePatternGroups — SAF: iki örneklemin hash birleşimi → yeni / artan /
// kaybolan, her biri en çok max satır. Paylar örnek boyuna göre (iki aralığın
// örneklemi farklı dolabilir). Sıra: yeni ve kaybolan sayıya göre azalan,
// artan orana göre azalan; eşitlikte hash (deterministik).
func comparePatternGroups(a, b *logstore.PatternsResult, max int) (newRows, rising, gone []patternsCompareRow) {
	if a == nil || b == nil {
		return nil, nil, nil
	}
	share := func(n int64, sampled int) float64 {
		if sampled <= 0 {
			return 0
		}
		return float64(n) / float64(sampled)
	}
	inB := make(map[string]logstore.SignatureGroup, len(b.Groups))
	for _, g := range b.Groups {
		inB[g.Hash] = g
	}
	inA := make(map[string]bool, len(a.Groups))
	for _, g := range a.Groups {
		inA[g.Hash] = true
		row := compareRowOf(g)
		row.CountA, row.ShareA = g.Count, share(g.Count, a.Sampled)
		bg, ok := inB[g.Hash]
		if !ok {
			newRows = append(newRows, row)
			continue
		}
		row.CountB, row.ShareB = bg.Count, share(bg.Count, b.Sampled)
		if row.ShareB > 0 && g.Count >= patternsCompareRiseMin {
			row.Ratio = row.ShareA / row.ShareB
			if row.Ratio >= patternsCompareRiseRatio {
				rising = append(rising, row)
			}
		}
	}
	for _, g := range b.Groups {
		if inA[g.Hash] {
			continue
		}
		row := compareRowOf(g)
		row.CountB, row.ShareB = g.Count, share(g.Count, b.Sampled)
		gone = append(gone, row)
	}
	byCount := func(rows []patternsCompareRow, a bool) {
		sort.SliceStable(rows, func(i, j int) bool {
			ci, cj := rows[i].CountB, rows[j].CountB
			if a {
				ci, cj = rows[i].CountA, rows[j].CountA
			}
			if ci != cj {
				return ci > cj
			}
			return rows[i].Hash < rows[j].Hash
		})
	}
	byCount(newRows, true)
	byCount(gone, false)
	sort.SliceStable(rising, func(i, j int) bool {
		if rising[i].Ratio != rising[j].Ratio {
			return rising[i].Ratio > rising[j].Ratio
		}
		return rising[i].Hash < rising[j].Hash
	})
	trim := func(r []patternsCompareRow) []patternsCompareRow {
		if r == nil {
			return []patternsCompareRow{}
		}
		if len(r) > max {
			return r[:max]
		}
		return r
	}
	return trim(newRows), trim(rising), trim(gone)
}

// compareVerifyProbes — SAF: doğrulanacak adaylar → yoklamalar. Yeni aday B'de,
// kaybolan aday A'da aranır; sorgusu boş olan atlanır. idx[i] = {liste, satır}.
func compareVerifyProbes(newRows, gone []patternsCompareRow, fa, fb logstore.Filter, perKind int) ([]logstore.PatternProbe, [][2]int) {
	var probes []logstore.PatternProbe
	var idx [][2]int
	add := func(list int, rows []patternsCompareRow, f logstore.Filter) {
		n := 0
		for i, r := range rows {
			if n >= perKind {
				break
			}
			if r.Query == "" {
				continue
			}
			probes = append(probes, logstore.PatternProbe{Query: r.Query, From: f.From, To: f.To})
			idx = append(idx, [2]int{list, i})
			n++
		}
	}
	add(0, newRows, fb)
	add(1, gone, fa)
	return probes, idx
}

func verifiedOf(st logstore.ExistState) string {
	switch st {
	case logstore.ExistAbsent:
		return "yes"
	case logstore.ExistFound:
		return "no"
	}
	return "unknown"
}

// patternsCompareWindowOK — SAF: B aralığı geçerli mi (sınırlı, ters değil,
// ≤ 31 gün). A için mevcut varsayılan (son 1 saat) geçerli.
func patternsCompareWindowOK(from, to time.Time) error {
	if from.IsZero() || to.IsZero() {
		return fmt.Errorf("karşılaştırma aralığı gerekli (bfrom, bto)")
	}
	if !to.After(from) {
		return fmt.Errorf("karşılaştırma aralığı ters ya da boş")
	}
	if to.Sub(from) > patternsCompareMaxSpan {
		return fmt.Errorf("karşılaştırma aralığı en çok 31 gün")
	}
	return nil
}

func (s *Server) getLogsPatternsCompare(w http.ResponseWriter, r *http.Request) {
	r = withServerTiming(r)
	q := r.URL.Query()
	fa, ok := s.logsPatternsFilter(w, r, q)
	if !ok {
		return
	}
	fb := fa
	fb.From, fb.To = parseTime(q.Get("bfrom")), parseTime(q.Get("bto"))
	if err := patternsCompareWindowOK(fb.From, fb.To); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	aFrom, aTo := snapLogsWindow(&fa, q.Get("from"), q.Get("to"), 30*time.Second)
	bFrom, bTo := snapLogsWindow(&fb, q.Get("bfrom"), q.Get("bto"), 30*time.Second)
	// Anahtar: Desenler anahtarı (TÜM süzgeç girdileri) + iki pencere (v0.5.187).
	key := "logs-patterns-compare:v1:" + logsPatternsKey(fa, aFrom, aTo, patternsCompareRows, patternsCompareSample, false) +
		":b=" + bFrom + "-" + bTo
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		cctx, cancel := context.WithTimeout(ctx, patternsCompareBudget)
		defer cancel()
		var ra, rb *logstore.PatternsResult
		var ea, eb error
		done := make(chan struct{}, 2)
		go func() {
			ra, ea = logstore.GroupBySignatureN(cctx, s.logs, fa, logstore.PatternsMaxGroups, patternsCompareSample)
			done <- struct{}{}
		}()
		go func() {
			rb, eb = logstore.GroupBySignatureN(cctx, s.logs, fb, logstore.PatternsMaxGroups, patternsCompareSample)
			done <- struct{}{}
		}()
		<-done
		<-done
		out := patternsCompareResult{A: compareWindowOf(fa, ra), B: compareWindowOf(fb, rb),
			New: []patternsCompareRow{}, Rising: []patternsCompareRow{}, Gone: []patternsCompareRow{}, Verify: "none"}
		if err := errors.Join(ea, eb); err != nil {
			if mapped := logstore.MapBackendSlow(err, cctx, ctx); errors.Is(mapped, logstore.ErrBackendSlow) {
				log.Printf("[logs] patterns compare degraded (backend=%s, svc=%q): %v", s.logs.Backend(), fa.Service, err)
				out.Degraded, out.Reason = true, "log backend slow/unreachable"
				return out, nil
			}
			return nil, logsQueryErr(logstore.MapBackendSlow(err, cctx, ctx), fa.Search)
		}
		rb = logstore.AlignPatternBaseline(ra, rb)
		out.New, out.Rising, out.Gone = comparePatternGroups(ra, rb, patternsCompareRows)
		probes, idx := compareVerifyProbes(out.New, out.Gone, fa, fb, patternsCompareVerifyMax)
		if len(probes) == 0 {
			return out, nil
		}
		ins, ok := logstore.Unwrap(s.logs).(logstore.PatternInsighter)
		if !ok {
			out.Verify = "unsupported"
			return out, nil
		}
		vctx, vcancel := context.WithTimeout(ctx, patternInsightsBudget)
		defer vcancel()
		st, err := ins.PatternsExist(vctx, fa, probes)
		if err != nil {
			log.Printf("[logs] patterns compare verify (svc=%q): %v", fa.Service, err)
			out.Verify = "error"
			return out, nil
		}
		out.Verify = existStatus(st)
		for j, at := range idx {
			v := "unknown"
			if j < len(st) {
				v = verifiedOf(st[j])
			}
			if at[0] == 0 {
				out.New[at[1]].Verified = v
			} else {
				out.Gone[at[1]].Verified = v
			}
		}
		return out, nil
	})
}
