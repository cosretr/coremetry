package api

// logs_ai_digest.go — Log Search Faz 6 (audit §7 "Servis hızlı özeti":
// "Ortak deterministik katman: desen özeti + önceki pencere farkı. LLM yalnız
// anlatır."). CoSRE log yüzeylerinin ORTAK kanıt katmanı: "Bu logları açıkla"
// (6.3, copilot_explain_logs.go) ve servis log özeti (6.7) aynı
// buildLogDigest + renderLogDigestTR çiftini çağırır.
//
// ES İŞİ (2026-11-06 olayı kuralı): yalnız kullanıcı tıklayınca; iki aralık ×
// TEK sayfa (500 satır, dar `_source`) = 2 `_search`, paralel, 10 sn bütçe.
// Doğrulama `_msearch`'ü (Desenler karşılaştırması) YOK — burada "yeni" =
// önceki pencerenin örnekleminde yok (ipucu; model "ilk kez" demez).
// İstekler ES sayacına isteğin rota etiketiyle düşer (withRouteQueryTag).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	logDigestSample = 500 // aralık başına TEK ES sayfası
	logDigestTop    = 8
	logDigestNew    = 5
	logDigestRising = 5
	logDigestGone   = 3
	logDigestBudget = 10 * time.Second
	logDigestTplMax = 140 // rune
)

// logDigest — kapsamın desen özeti + hemen önceki eşit pencereyle farkı.
type logDigest struct {
	From, To         time.Time // istenen pencere
	PrevFrom, PrevTo time.Time
	Cur              *logstore.PatternsResult
	CurErr           error
	PrevOK           bool
	New              []patternsCompareRow
	Rising           []patternsCompareRow
	Gone             []patternsCompareRow
}

// logDigestPrevWindow — SAF: [from-(to-from), from]. Boş/ters pencere → sıfır.
func logDigestPrevWindow(from, to time.Time) (time.Time, time.Time) {
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return time.Time{}, time.Time{}
	}
	return from.Add(-to.Sub(from)), from
}

// withRouteQueryTag — POST copilot uçları serveCached'ten geçmez; ES sayacı
// (Admin → Elasticsearch, yüzey × tür) ve CH log_comment isteği rota adıyla
// görsün diye ctx'e aynı etiketi koyar.
func withRouteQueryTag(r *http.Request) *http.Request {
	return r.WithContext(chstore.WithQueryTag(r.Context(), chstore.RouteQueryTag(r.Pattern, r.URL.Path)))
}

// buildLogDigest — iki örneklem paralel; önceki pencere okunamazsa fark
// bölümü "okunamadı" olur, ana özet yine anlatılır.
func (s *Server) buildLogDigest(ctx context.Context, f logstore.Filter) logDigest {
	d := logDigest{From: f.From, To: f.To}
	d.PrevFrom, d.PrevTo = logDigestPrevWindow(f.From, f.To)
	if s.logs == nil {
		d.CurErr = errExplainLogsUnconfigured
		return d
	}
	cctx, cancel := context.WithTimeout(ctx, logDigestBudget)
	defer cancel()
	var prev *logstore.PatternsResult
	var prevErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.Cur, d.CurErr = logstore.GroupBySignatureN(cctx, s.logs, f, logstore.PatternsMaxGroups, logDigestSample)
	}()
	if !d.PrevFrom.IsZero() {
		pf := f
		pf.From, pf.To = d.PrevFrom, d.PrevTo
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev, prevErr = logstore.GroupBySignatureN(cctx, s.logs, pf, logstore.PatternsMaxGroups, logDigestSample)
		}()
	}
	wg.Wait()
	if d.CurErr != nil {
		d.CurErr = logstore.MapBackendSlow(d.CurErr, cctx, ctx)
		return d
	}
	if prev != nil && prevErr == nil {
		d.PrevOK = true
		prev = logstore.AlignPatternBaseline(d.Cur, prev)
		n, r, g := comparePatternGroups(d.Cur, prev, logDigestNew)
		d.New, d.Rising, d.Gone = n, r, g
		if len(d.Rising) > logDigestRising {
			d.Rising = d.Rising[:logDigestRising]
		}
		if len(d.Gone) > logDigestGone {
			d.Gone = d.Gone[:logDigestGone]
		}
	}
	return d
}

// digestUnavailable — ana örneklem okunamadı mı (yanıtta hata gibi davranılır).
func (d logDigest) unavailable() bool { return d.CurErr != nil || d.Cur == nil }

// digestSlow — okunamama nedeni arka uç yavaşlığı mı (degraded yanıtı).
func (d logDigest) slow() bool { return errors.Is(d.CurErr, logstore.ErrBackendSlow) }

func digestClock(ns int64) string {
	if ns <= 0 {
		return "—"
	}
	return time.Unix(0, ns).UTC().Format("15:04:05")
}

// renderLogDigestTR — SAF: ortak kanıt bölümü. Okunamayan kaynak itiraf
// edilir (yokluk sıfır değil); örnekleme tavanı dolduysa kapsanan alt pencere
// yazılır (v0.10.441 dürüstlük zarfı).
func renderLogDigestTR(d logDigest) string {
	var b strings.Builder
	if !d.From.IsZero() && !d.To.IsZero() {
		fmt.Fprintf(&b, "PENCERE: %s → %s UTC (%s)\n", d.From.UTC().Format("2006-01-02 15:04"),
			d.To.UTC().Format("15:04"), fmtRangeTR(int64(d.To.Sub(d.From).Seconds())))
	}
	switch {
	case d.unavailable():
		b.WriteString("DESENLER: OKUNAMADI — kapsam hakkında sonuç çıkarma (sıfır DEĞİL).\n")
		return b.String()
	case len(d.Cur.Groups) == 0:
		b.WriteString("DESENLER: bu kapsamda örneklenen satır yok.\n")
		return b.String()
	}
	c := d.Cur
	fmt.Fprintf(&b, "DESENLER (%d örnek satırdan %d farklı desen, ilk %d):\n", c.Sampled, c.Distinct, min(len(c.Groups), logDigestTop))
	if c.Truncated && c.CoveredFromNs > 0 {
		fmt.Fprintf(&b, "(Örnekleme tavanı doldu: sayımlar yalnız %s → %s UTC alt penceresini anlatır; başlangıç daha önce olabilir.)\n",
			digestClock(c.CoveredFromNs), digestClock(c.CoveredToNs))
	}
	for i, g := range c.Groups {
		if i >= logDigestTop {
			break
		}
		fmt.Fprintf(&b, "%d. %q — %d satır, %s, örnekte ilk %s son %s", i+1, oneLineSample(g.Template, logDigestTplMax),
			g.Count, sevWord(g.Severity, g.SeverityText), digestClock(g.FirstSeen), digestClock(g.LastSeen))
		if len(g.Services) > 0 {
			fmt.Fprintf(&b, "; servisler: %s", strings.Join(g.Services, ", "))
			if g.ServiceCount > len(g.Services) {
				fmt.Fprintf(&b, " +%d", g.ServiceCount-len(g.Services))
			}
		}
		b.WriteString("\n")
	}
	if !d.PrevOK {
		b.WriteString("ÖNCEKİ PENCERE: okunamadı — yeni / artan desen bilgisi yok (sıfır DEĞİL).\n")
		return b.String()
	}
	fmt.Fprintf(&b, "ÖNCEKİ PENCEREYLE FARK (%s → %s UTC; örneklem karşılaştırması, kesin sayım değil):\n",
		d.PrevFrom.UTC().Format("15:04"), d.PrevTo.UTC().Format("15:04"))
	row := func(label string, rows []patternsCompareRow, ratio bool) {
		if len(rows) == 0 {
			fmt.Fprintf(&b, "%s: yok\n", label)
			return
		}
		fmt.Fprintf(&b, "%s:\n", label)
		for _, r := range rows {
			fmt.Fprintf(&b, "- %q — şimdi %d, önce %d", oneLineSample(r.Template, logDigestTplMax), r.CountA, r.CountB)
			if ratio && r.Ratio > 0 {
				fmt.Fprintf(&b, " (payı ×%.1f)", r.Ratio)
			}
			b.WriteString("\n")
		}
	}
	row("YENİ (önceki örneklemde yok)", d.New, false)
	row("ARTAN", d.Rising, true)
	row("KAYBOLAN", d.Gone, false)
	return b.String()
}

// digestPatternLinks — SAF: yeni + artan desenlerin arama dizeleri (UI bunları
// mevcut süzgece AND'ler; "linkler süzgeçli"). Sorgusu boş olan atlanır, ≤n.
func digestPatternLinks(d logDigest, n int) []logsExplainPatternLink {
	out := []logsExplainPatternLink{}
	seen := map[string]bool{}
	add := func(kind string, rows []patternsCompareRow) {
		for _, r := range rows {
			if len(out) >= n {
				return
			}
			if r.Query == "" || seen[r.Query] {
				continue
			}
			seen[r.Query] = true
			out = append(out, logsExplainPatternLink{Kind: kind, Label: oneLineSample(r.Template, 80), Query: r.Query})
		}
	}
	add("new", d.New)
	add("rising", d.Rising)
	return out
}

// logsExplainPatternLink — yanıttaki desen bağlantısı (FE lib/types.ts
// LogsExplainPatternLink).
type logsExplainPatternLink struct {
	Kind  string `json:"kind"` // new | rising
	Label string `json:"label"`
	Query string `json:"query"`
}
