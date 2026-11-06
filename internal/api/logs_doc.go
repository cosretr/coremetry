package api

// logs_doc.go — v0.10.1831 (Log Search Faz 1 dilim 1.8; audit §3.2 "Tam
// doküman: ids sorgusu … yalnız detay açılınca").
//
// /logs listesi ES'ten dar `_source` ile okunur (görünen kolonlar + satır
// çizim alanları); satır açılınca tam doküman BU uçtan, tek `ids`
// sorgusuyla (`size: 1`, kendi indeksinde) gelir. CH backend'i satırı
// zaten tam döner (`partial` hiç gelmez) → uç 400 "bu arka uçta gerekmez".
//
//   GET /api/logs/doc?ref=<index>/<_id>   tüm roller (okuma)
//
// Önbellek: anahtar ref'in tamamı (fnv), 60 sn — aynı satırı açıp kapatmak
// ES'e ikinci tur atmaz. Ayrı dosya: api.go BÜYÜMEZ; logs_routes.go'nun
// dokuz kalıplık pini (logs_routes_test) bozulmaz.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func init() { registerRoutesExtra("logs_doc", (*Server).registerLogsDocRoutes) }

func (s *Server) registerLogsDocRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs/doc", s.getLogsDoc)
}

// logsDocRefMax — referans uzunluk tavanı (indeks ≤255 + _id ≤512 bayt).
const logsDocRefMax = 800

func (s *Server) getLogsDoc(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" || len(ref) > logsDocRefMax || !strings.Contains(ref, "/") {
		writeJSONError(w, http.StatusBadRequest, "ref = <index>/<_id> gerekli")
		return
	}
	if s.logs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "log arka ucu yok")
		return
	}
	fetcher, ok := logstore.Unwrap(s.logs).(logstore.DocFetcher)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "bu log arka ucu satırı tam döner; doküman okuması gerekmez")
		return
	}
	key := "logs-doc:" + fnvStr(ref)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		rec, err := fetcher.FetchDoc(tctx, ref, time.Time{})
		if errors.Is(err, logstore.ErrDocNotFound) {
			return nil, fmt.Errorf("%w: %v", errNotFound, err)
		}
		if err == nil && rec != nil {
			logstore.AttachExceptions([]*logstore.LogRecord{rec}) // v0.10.1882 — tam doküman (yalnız ES yolu)
		}
		return rec, err
	})
}

// parseLogsSourceFields — SAF: URL `fields` (virgüllü kolon alanları) →
// temiz, tekil, ≤ MaxSourceFields liste (sıra korunur). Boş → nil (tam
// _source, bugünkü davranış).
func parseLogsSourceFields(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || len(p) > 200 {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
		if len(out) >= logstore.MaxSourceFields {
			break
		}
	}
	return out
}

// logsSourceKey — önbellek anahtarı parçası: kolon KÜMESİ sıralı + fnv
// (v0.5.187: uzunluk değil özet), sayımsız bayrak. Yoksa "" (eski anahtar).
func logsSourceKey(f logstore.Filter) string {
	if len(f.SourceFields) == 0 && !f.SkipTotal {
		return ""
	}
	cols := append([]string(nil), f.SourceFields...)
	sort.Strings(cols)
	return fmt.Sprintf(":sf=%s:st=%t", fnvStr(cols...), f.SkipTotal)
}
