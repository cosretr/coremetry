package api

import (
	"context"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// oracleTraceLookup — v0.10.917 (operator-reported: "trace bulmasına rağmen
// bulunamadı diyor"). Oracle testinin "N/M trace Coremetry'de" sayımı
// yalnız ClickHouse'a bakıyordu; trace sayfası ise ÖNCE Tempo'ya bakar
// (v0.9.632). Örneklemeyle CH'ye girmemiş bir trace sayfada açılırken
// testte "bulunamadı" görünüyordu. CH'de çıkmayan en çok
// oracleTempoLookupMax id Tempo'da denenir (4 paralel, id başına
// tempoFirstBudget). Yalnız TEST yolunda — poller'ın her tikinde Tempo'ya
// id başına istek atmak maliyetli.
const (
	oracleTempoLookupMax  = 10
	oracleTempoLookupPara = 4
)

func (s *Server) oracleTraceLookup(ctx context.Context, ids []string, from, to time.Time) (map[string]string, error) {
	out, err := s.store.TraceServicesByIDs(ctx, ids, from, to)
	if err != nil {
		return nil, err
	}
	if s.tempo == nil || !s.tempo.Configured() {
		return out, nil
	}
	var missing []string
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			missing = append(missing, id)
			if len(missing) >= oracleTempoLookupMax {
				break
			}
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, oracleTempoLookupPara)
	for _, id := range missing {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			tctx, cancel := context.WithTimeout(ctx, tempoFirstBudget)
			defer cancel()
			spans, terr := s.tempo.LookupTrace(tctx, id)
			if terr != nil || len(spans) == 0 {
				return
			}
			mu.Lock()
			out[id] = serviceFromSpans(spans)
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out, nil
}

// serviceFromSpans — SAF: CH traceFactsSQL kuralının ikizi — son hata
// veren span'ın servisi; yoksa kök span'ınki; o da yoksa ilk span'ınki.
func serviceFromSpans(spans []chstore.SpanRow) string {
	errSvc, errAt := "", int64(-1)
	root := ""
	for _, sp := range spans {
		if sp.StatusCode == "error" && sp.StartTime >= errAt && sp.ServiceName != "" {
			errSvc, errAt = sp.ServiceName, sp.StartTime
		}
		if sp.ParentSpanID == "" && root == "" {
			root = sp.ServiceName
		}
	}
	switch {
	case errSvc != "":
		return errSvc
	case root != "":
		return root
	case len(spans) > 0:
		return spans[0].ServiceName
	}
	return ""
}
