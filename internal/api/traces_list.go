package api

// traces_list.go — GET /api/traces gövdesi (v0.10.2466'da api.go'dan
// taşındı: api.go büyümez; rota kaydı api.go'da kaldı). Yeni: ?wait=long +
// kısmi sonuç zarfı (traces_long_wait.go).

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func (s *Server) getTraces(w http.ResponseWriter, r *http.Request) {
	// FAZ 2 — ?traceIds= skips phase-1 and serves bounded extras only (traces_extras.go).
	if s.serveTracesExtras(w, r) {
		return
	}
	q := r.URL.Query()
	f, ferr := parseTraceFilter(q)
	if ferr != nil {
		http.Error(w, ferr.Error(), http.StatusBadRequest)
		return
	}
	// 20s cache. /api/traces is the heaviest uncached read: a filtered
	// query disqualifies the trace_summary MV fast-path and falls back to
	// a raw GROUP BY trace_id over spans. Pagination round-trips, sort /
	// filter toggles and concurrent operators all repeat the same
	// predicate, so serveCached gives cache-hit + singleflight coalescing
	// + stale-while-revalidate. Key = full query string (same shape as the
	// traces-agg / span-metric keys); the handler is a pure function of the
	// query string (GET, no body/role variance). raw from/to keep a
	// relative window stable within the TTL (do NOT key on parsed now()-
	// ticking time, the v0.5.184 class).
	// v0.10.2466 — ?wait=long: operatörün AÇIK "Daha uzun bekle" isteği
	// (traces_long_wait.go); bilinmeyen değer 400, bütçe tavanla sınırlı.
	longWaitSec, lwErr := parseTracesWait(q.Get("wait"), chstore.LongWaitCap())
	if lwErr != nil {
		writeJSONError(w, http.StatusBadRequest, lwErr.Error())
		return
	}
	key := "traces:" + cacheRawQuery(r) + tracesRootDefKeySuffix(q, s.store.TraceRootDef()) + tracesLongWaitKeySuffix(longWaitSec) // v0.10.256 grid; v0.10.733 kök tanımı (rootOnly iken); v0.10.2466 bütçe
	// v0.10.326 — ?explain=1 (yalnız admin): cache'i ATLAR, yanıta yol
	// kararları + her CH adımı (SQL/arg/ms/satır/hata) eklenir. Prod'da
	// tekrar etmeyen "kısa pencere boş" sınıfının teşhis aracı.
	explain := q.Get("explain") == "1"
	if explain && !explainAllowed(auth.FromContext(r.Context())) {
		writeJSONError(w, http.StatusForbidden, "explain requires admin")
		return
	}
	// v0.10.329 — explain kaydı HER istekte tutulur (ucuz: SQL metni + arg);
	// yanıta yalnız explain=1'de girer, boş sonuçta pod loguna düşer.
	f.Explain = &chstore.TraceExplain{}
	fn := func(ctx context.Context) (any, error) {
		// v0.10.2466 — bütçe aşımı 500 değil kısmi sonuç (chstore/trace_partial.go).
		ctx = chstore.WithLongWait(ctx, longWaitSec)
		if longWaitSec > 0 {
			// Toplam son tarih: basamakların her biri uzun bütçeyi ayrı ayrı
			// yemesin; dolunca en geniş biten basamağın satırları kısmi döner.
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, tracesLongWaitTotal(longWaitSec))
			defer cancel()
		}
		var partial chstore.TracePartial
		f.Partial = &partial
		t0 := time.Now()
		// OUT param (v0.8.369): set to the recency-slice size when a
		// non-time sort was ranked within the newest-N slice, so the
		// UI can hint honestly (0 = exact/global ordering served).
		rankedWithin := 0
		f.RankedWithin = &rankedWithin
		// v0.9.297 — OUT param: set when Stage 2 exhausted its resources
		// and the window had to be halved to answer at all. Emitted only
		// when it actually moved, exactly like rankedWithinRecent.
		var narrowedFrom time.Time
		f.NarrowedFrom = &narrowedFrom
		// v0.10.342 — kimlik-önce arama (function_id / trace id) sonucu.
		// v0.10.343 — Operator-reported: değer "Trace ID…" kutusuna yazılmıştı
		// (?traceId=). 32-hex olmayan traceId bir KİMLİKTİR (function_id gibi):
		// Search'e taşınır (ham hâli — büyük/küçük harf duyarlı eşitlik),
		// TraceID boşalır; eski istemci/API çağrıları da bundan yararlanır.
		if search, tid := identityFromTraceIDParam(q.Get("traceId"), f.Search); tid == "" && f.TraceID != "" {
			f.TraceID, f.Search = "", search
		}
		var identity chstore.IdentityHit
		f.IdentityHit = &identity
		traces, total, hasMore, err := s.store.GetTraces(ctx, f)
		if err != nil {
			return nil, err
		}
		resp := map[string]interface{}{"traces": traces, "hasMore": hasMore}
		if p := tracesPartialPayload(partial, time.Since(t0), longWaitSec); p != nil {
			resp["partial"] = p
		}
		// v0.10.329 — boş liste öz-teşhisi (operatör, prod: kısa pencerede
		// aramalı liste boş, şerit dolu; lokalde tekrar etmiyor). Aynı
		// WHERE + arama yüklemiyle span sayımı: N>0 = liste sorgusu
		// kaybediyor, N=0 = veri/yüklem. Yanıta emptyDiag, pod loguna
		// explain (SQL/arg/ms/satır) — operatör tıklaması gerekmez.
		if !partial.Hit && chstore.EmptyDiagWanted(f, len(traces)) { // v0.10.2466 — kısmi boş liste teşhis sayımı koşmaz (o da bütçeyi aşardı)
			n, capped, cerr := s.store.CountMatchingSpans(ctx, f)
			diag := newEmptyDiag(n, capped) // v0.10.813 — eşleşen span var ama liste boş = tavan/sorgu uyuşmazlığı sayacı; v0.10.852 capped
			if cerr != nil {
				diag["error"] = cerr.Error()
			}
			// v0.10.530 — MV>0 ∧ eşleşen=0: "TTL" mi "yüklem" mi? Servisin
			// yüklemsiz ham span sayısı ayırır (trace_explain.go §v0.10.530);
			// yalnız eşleşen 0 ve servis seçiliyken (bir ek count(), PK öneki).
			if f.Service != "" && cerr == nil && n == 0 {
				if sn, serr := s.store.CountServiceSpans(ctx, f); serr == nil {
					diag["serviceSpans"] = sn
				} else {
					diag["serviceSpansError"] = serr.Error()
				}
			}
			// v0.10.339 — Operator-reported (prod): `channel_code = …` çipiyle
			// liste boş, aynı span'ler çipsiz listede o değerle görünüyor.
			// Filtre terfi kolonuna derlenmişti ve kolon o replikada değeri
			// taşımıyordu. Boş sonuçta kolon/dizi sayımı host başına
			// karşılaştırılır; dizi fazlaysa aynı istek dizi yoluyla yeniden
			// koşar (doğru ama yavaş) ve harita askıya alınır.
			if pk := chstore.PromotedFilterKeys(f); len(pk) > 0 && cerr == nil && n == 0 {
				diag["promotedKeys"] = pk
				if hosts, perr := s.store.PromotedMismatch(ctx, f); perr != nil {
					diag["promotedProbeError"] = perr.Error()
				} else {
					diag["promotedHosts"] = hosts
					if chstore.PromotedMismatchDetected(hosts) {
						f2 := f
						f2.NoPromoted = true
						if t2, _, hm2, err2 := s.store.GetTraces(ctx, f2); err2 == nil {
							traces, hasMore = t2, hm2
							resp["traces"], resp["hasMore"] = traces, hasMore
							diag["promotedFallback"] = true
						} else {
							diag["promotedFallbackError"] = err2.Error()
						}
						s.store.SuspendPromotedKeys(pk)
						log.Printf("[traces] PROMOTED COLUMN MISMATCH keys=%v hosts=%+v — dizi yoluna düşüldü (%d satır), harita askıya alındı; replika şemasını kontrol et (system.mutations / DROP+ADD onarımı)", pk, hosts, len(traces))
					}
				}
			}
			resp["emptyDiag"] = diag
			if ej, jerr := json.Marshal(f.Explain); jerr == nil {
				log.Printf("[traces] EMPTY list (window=%s→%s service=%q search=%q filters=%d hasError=%v sort=%s) matchingSpans=%d explain=%s",
					f.From.UTC().Format(time.RFC3339), f.To.UTC().Format(time.RFC3339), f.Service, f.Search, len(f.Filters), f.HasError, f.Sort, n, ej)
			}
		}
		// v0.10.124 — pencere MV'de boş bir güne değiyorsa ham yoldan okundu;
		// UI bunu söyler (sihirbaz doldurunca kendiliğinden kaybolur).
		if s.store.TraceMVGap(ctx, f.From, f.To) {
			resp["mvGap"] = true
		}
		if !narrowedFrom.IsZero() {
			// The operator asked about a window this query could not
			// afford. Say which window actually answered — a top-N over
			// a smaller one is a different answer, not a slower one.
			resp["narrowedFromNs"] = narrowedFrom.UnixNano()
		}
		if rankedWithin > 0 {
			resp["rankedWithinRecent"] = rankedWithin
		}
		if identity.Keys != nil {
			resp["identity"] = identity
		}
		// Only emit `total` when the caller actually computed one — clients
		// distinguish "unknown total" from "zero total" by the field's
		// presence, not its value.
		if f.CountMode != "skip" {
			resp["total"] = total
		}
		if explain {
			resp["explain"] = f.Explain
		}
		return resp, nil
	}
	if explain {
		res, err := fn(r.Context())
		if err != nil {
			// Hata da teşhistir: adımlar kaydedildi, hatayla birlikte döner.
			writeJSON(w, map[string]any{"error": err.Error(), "explain": f.Explain})
			return
		}
		writeJSON(w, res)
		return
	}
	s.serveCached(w, r, key, 20*time.Second, fn)
}
