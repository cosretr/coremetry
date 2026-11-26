package api

// anomalies_unified.go — v0.10.2051: /anomalies TEK listesi sunucuda
// birleşir, sıralanır ve sayfalanır.
//
//   GET /api/anomalies/unified?section=open|done&signal=&offset=&limit=&sort=&dir=
//   GET /api/anomalies/unified?event=<eski olay id>   — olayın birleşik satırı (üyeleriyle)
//
// Neden: v0.10.2027 birleştirmeyi tarayıcıda yapıyordu (unifiedRows.ts).
// İki kaynak (ml_events, anomaly_events) 200'er satırla kesiliyor, liste
// sayfalanmıyordu; yoğun pencerede satırlar SESSİZCE düşüyordu ("Table >
// 100 rows" kuralı). Neden iki ucu genişletmek DEĞİL: sayfalama birleşik
// satır üzerinde olmalı — kaynak başına sayfa, bir grubu iki sayfaya
// bölerdi. Kural (gruplama, durum, sıra) ml.BuildUnified'da, D2 zinciri
// ml.ChainWindows'un TEK gövdesiyle; cümle tarayıcıda (mevcut işlevler).
//
// Pencere eski iki uçla aynı: son 24 saat (last_seen). Okuma tavanı kaynak
// başına 2000; değen cevap `truncated: true` taşır ve sayfa bunu tek satırla
// söyler. Elasticsearch çağrısı YOK (yalnız CH: ml_events FINAL,
// anomaly_events FINAL, kararlar). Rol kapısı yok (salt okuma; ML ucu gibi
// her rol görür). serveCached 30 sn; anahtar tüm girdileri taşır.
//
// v0.10.2083 — sort/dir (operatör 2026-11-13: başlık sıralaması geri): beyaz
// liste ml.UnifiedSortCols (started|service|signals|status), tanınmayan →
// bölüm varsayılanı. Sıra birleşik satırların TAMAMINDA, sayfadan önce
// (ml.SortUnified) — 2. sayfa aynı sıranın devamı, grup bölünmez.
//
// v0.10.2243 (anomali kartı Dilim C, K8/K11) — view: "" servis ekibi listesi
// (telemetry_volume sınıfı ve eski svc_volume_spike satırları SQL'de
// dışarıda) | platform (yalnız onlar; eski dedektör olayı okunmaz) | held
// ("Elenenler", yalnız admin).
//
// v0.10.2473 — gürültü kapısı kipi (ml ayarı publishGates: off | shadow | on,
// varsayılan on) İSTEK başına ayardan okunur ve önbellek anahtarına girer.
// on: servis / platform görünümü publish_state='held' satırı düşürür; held
// görünümü tutulan olayları, satır başına kalan kapıları (heldGates: ad,
// değer, eşik) ve sayacı (held: olay sayısı + kapı başına) taşır. shadow:
// liste aynen, held görünümü "on olsaydı tutulurdu" olaylarını gösterir
// (ml.ShadowHeld). off: held görünümü boş.

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("anomalies-unified", (*Server).registerAnomaliesUnifiedRoutes) }

func (s *Server) registerAnomaliesUnifiedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/anomalies/unified", auth.RequireAnyRole(mlReadRoles, s.getAnomaliesUnified))
}

const (
	unifiedTTL         = 30 * time.Second
	unifiedWindow      = 24 * time.Hour
	unifiedLegacyCap   = 2000
	unifiedPageDefault = 50
	unifiedPageMax     = 100
	unifiedOffsetMax   = 10000
	unifiedExecSec     = 10
)

// unifiedParams — SAF çözülmüş girdiler (anahtar ve sorgu aynı değerlerden).
type unifiedParams struct {
	Section string // open | done
	Signal  string // "" | trace | log | metric
	Offset  int
	Limit   int
	Event   string // eski olay kimliği; doluysa diğerleri varsayılana iner
	Sort    string // ml.UnifiedSortCols üyesi; "" = bölüm varsayılanı
	Dir     string // asc | desc; Sort boşken ""
	View    string // "" | platform | held (v0.10.2243)
	// Gates — v0.10.2473: gürültü kapısı kipi (chstore.MLGates*). İstekten
	// DEĞİL ayardan gelir (handler doldurur); anahtara girer.
	Gates string
	// Unattached — v0.10.2481: yalnız açık bir Problem'e bağlı OLMAYAN
	// satırlar (Sinyaller › Bağlanmamış anomaliler; anomalies_unified_attached.go).
	// Yalnız servis görünümünde ve liste kipinde anlamlı; başka yerde false.
	Unattached bool
}

// parseUnifiedParams — SAF: sınırlama anahtardan ÖNCE (crafted değer ayrı
// girdi basmasın).
func parseUnifiedParams(get func(string) string) unifiedParams {
	p := unifiedParams{Section: "open", Limit: unifiedPageDefault}
	if ev := strings.TrimSpace(get("event")); ev != "" && len(ev) <= 256 {
		p.Event = ev
		return p
	}
	if get("section") == "done" {
		p.Section = "done"
	}
	switch sig := get("signal"); sig {
	case chstore.MLSignalTrace, chstore.MLSignalLog, chstore.MLSignalMetric:
		p.Signal = sig
	}
	if n := parseInt(get("limit"), unifiedPageDefault); n >= 1 && n <= unifiedPageMax {
		p.Limit = n
	}
	if n := parseInt(get("offset"), 0); n > 0 {
		p.Offset = min(n, unifiedOffsetMax)
	}
	p.Sort, p.Dir = ml.NormalizeUnifiedSort(p.Section, get("sort"), get("dir"))
	switch v := get("view"); v {
	case chstore.MLViewPlatform, chstore.MLViewHeld, chstore.MLViewLoad:
		p.View = v
	}
	p.Unattached = p.View == "" && get("unattached") == "1"
	return p
}

// unifiedCachePrefix — "anomaly:" altında: eski olay kararı / susturma
// yazımları (anomaly:) bu listeyi de düşürür; ML kararı ve ML ayarı ml.go'da
// ayrıca düşürür.
const unifiedCachePrefix = "anomaly:unified:"

// unifiedKey — SAF: TÜM girdiler anahtarda (v0.5.187); olay kimliği fnv
// özetiyle (serbest metin anahtara çıplak girmez), saat dilimi özetin dili.
func unifiedKey(p unifiedParams, tz string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(p.Event))
	// v5 (v0.10.2476): servis görünümü yük / yapı ve invalid satırları düşürür.
	// v6 (v0.10.2481): unattached (Problem'e bağlı satırları düşür) anahtarda.
	return fmt.Sprintf(unifiedCachePrefix+"v6:sec=%s:sig=%s:off=%d:lim=%d:sort=%s:dir=%s:view=%s:gates=%s:unatt=%t:ev=%x:tz=%s",
		p.Section, p.Signal, p.Offset, p.Limit, p.Sort, p.Dir, p.View, p.Gates, p.Unattached, h.Sum64(), tz)
}

// unifiedAnomalyRow — birleşik satır. Liste kipinde yalnız lider (mlLead YA
// DA legacyLead; ML liderinin kanıt JSON'u taşınmaz), olay kipinde üyeler de.
type unifiedAnomalyRow struct {
	Key         string                 `json:"key"`
	Service     string                 `json:"service"`
	Start       int64                  `json:"start"`
	End         int64                  `json:"end"`
	Signals     []string               `json:"signals"`
	Status      string                 `json:"status"`
	MLLead      *mlEventRow            `json:"mlLead,omitempty"`
	LegacyLead  *chstore.AnomalyEvent  `json:"legacyLead,omitempty"`
	MLCount     int                    `json:"mlCount"`
	LegacyCount int                    `json:"legacyCount"`
	ML          []mlEventRow           `json:"ml,omitempty"`
	Legacy      []chstore.AnomalyEvent `json:"legacy,omitempty"`
	// HeldGates — v0.10.2473: yalnız held görünümünde; satırın (lider ML
	// olayının) kaldığı gürültü kapıları = elenme nedeni.
	HeldGates []heldGate `json:"heldGates,omitempty"`
}

// heldGate — kalan gürültü kapısı: ad (min_volume | min_abs_diff |
// visible_deviation), gözlenen değer ve eşik. Cümleyi arayüz kurar (TR / EN).
type heldGate struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

// heldSummary — Elenenler sayacı: pencerede (24 sa) elenen olay sayısı ve
// kapı başına olay (bir olay birden çok kapıda kalabilir).
type heldSummary struct {
	Events int            `json:"events"`
	ByGate map[string]int `json:"byGate"`
}

// heldGateInvalid — v0.10.2476: doğrulayıcıdan kalan kayıt (publish_state
// invalid) Elenenler'de bu adla; değer / eşik yok.
const heldGateInvalid = "invalid_record"

// heldGatesOf — SAF: olayın kaldığı gürültü kapıları (ml.HeldGates) → yanıt;
// invalid kayıt tek satır heldGateInvalid.
func heldGatesOf(e chstore.MLEvent) []heldGate {
	if e.PublishState == ml.PublishInvalid {
		return []heldGate{{Name: heldGateInvalid}}
	}
	var out []heldGate
	for _, g := range ml.HeldGates(e) {
		out = append(out, heldGate{Name: g.Name, Value: g.Value, Threshold: g.Threshold})
	}
	return out
}

// summarizeHeld — SAF: Elenenler sayacı.
func summarizeHeld(items []chstore.MLEvent) *heldSummary {
	s := &heldSummary{Events: len(items), ByGate: map[string]int{}}
	for _, e := range items {
		for _, g := range heldGatesOf(e) {
			s.ByGate[g.Name]++
		}
	}
	return s
}

type unifiedAnomaliesResponse struct {
	Items     []unifiedAnomalyRow `json:"items"`
	Total     int                 `json:"total"` // bu bölümün (süzgeç sonrası) kesin satır sayısı
	OpenTotal int                 `json:"openTotal"`
	DoneTotal int                 `json:"doneTotal"`
	Offset    int                 `json:"offset"`
	Limit     int                 `json:"limit"`
	Truncated bool                `json:"truncated"`         // bir kaynak okuma tavanına değdi
	Partial   string              `json:"partial,omitempty"` // "ml" | "legacy": o kaynak okunamadı; "problems": bağ süzgeci uygulanamadı (v0.10.2481)
	Row       *unifiedAnomalyRow  `json:"row,omitempty"`     // olay kipi
	// GatesMode / Held — v0.10.2473: yalnız held görünümünde; gürültü kapısı
	// kipi (off | shadow | on) ve Elenenler sayacı.
	GatesMode string       `json:"gatesMode,omitempty"`
	Held      *heldSummary `json:"held,omitempty"`
}

// unifiedMLFilter — SAF: listenin ml_events süzgeci. Kip on iken servis /
// platform görünümü held satırı düşürür (SQL yalnız o iki görünümde uygular);
// olay ve held görünümü düşürmez. shadow / off: süzgeç yok.
func unifiedMLFilter(view string, since time.Time, gates string) chstore.MLEventsFilter {
	return chstore.MLEventsFilter{Since: since, Limit: chstore.MLUnifiedCap, View: view, HideHeld: gates == chstore.MLGatesOn}
}

// pageUnified — SAF: süzgeç → bölüm → sıra (tüm bölüm, sayfadan ÖNCE) → sayfa.
// Grup = satır, bölünmez.
func pageUnified(rows []ml.UnifiedRow, p unifiedParams) (page []ml.UnifiedRow, total, openN, doneN int) {
	var open, done []ml.UnifiedRow
	for _, r := range rows {
		if !r.HasSignal(p.Signal) {
			continue
		}
		if ml.UnifiedOpen(r.Status) {
			open = append(open, r)
		} else {
			done = append(done, r)
		}
	}
	sel := open
	if p.Section == "done" {
		sel = done
	}
	ml.SortUnified(sel, p.Section, p.Sort, p.Dir)
	total = len(sel)
	from := min(p.Offset, total)
	to := min(from+p.Limit, total)
	return sel[from:to], total, len(open), len(done)
}

func (s *Server) getAnomaliesUnified(w http.ResponseWriter, r *http.Request) {
	p := parseUnifiedParams(r.URL.Query().Get)
	if p.View == chstore.MLViewHeld { // Elenenler yalnız admin (K8)
		if c := auth.FromContext(r.Context()); c == nil || c.Role != auth.RoleAdmin {
			http.Error(w, "insufficient role", http.StatusForbidden)
			return
		}
	}
	// v0.10.2473: kip ayardan (kontrol düzlemi önbelleği; ek CH okuması yok)
	// ve anahtarda — kip değişince eski kipin sayfası sunulmaz.
	p.Gates = s.store.GetMLConfig(r.Context()).PublishGatesMode()
	loc := ml.OperatorLocation(s.store.AnomalySensitivityForDetectors())
	s.serveCached(w, r, unifiedKey(p, loc.String()), unifiedTTL, func(ctx context.Context) (any, error) {
		return s.buildAnomaliesUnified(ctx, p, loc)
	})
}

// heldOnly — SAF: Elenenler'in olayları. on: gerçekten tutulan satırlar
// (publish_state=held — ana listede olmayanlar; kip açılmadan önce yazılmış,
// listede duran eski satır buraya girmez). shadow: on olsaydı tutulacaklar
// (ml.ShadowHeld). off: kapılar değerlendirilmiyor → boş. v0.10.2476: invalid
// kayıt (doğrulayıcıdan kaldı; hiçbir listede yok) her kipte burada.
func heldOnly(items []chstore.MLEvent, gates string) []chstore.MLEvent {
	out := items[:0:0]
	for _, e := range items {
		if e.PublishState == ml.PublishInvalid {
			out = append(out, e)
			continue
		}
		switch gates {
		case chstore.MLGatesOn:
			if e.PublishState == ml.PublishHeld {
				out = append(out, e)
			}
		case chstore.MLGatesShadow:
			if ml.ShadowHeld(e) {
				out = append(out, e)
			}
		}
	}
	return out
}

func (s *Server) buildAnomaliesUnified(ctx context.Context, p unifiedParams, loc *time.Location) (any, error) {
	now := time.Now()
	since := now.Add(-unifiedWindow)
	resp := unifiedAnomaliesResponse{Items: []unifiedAnomalyRow{}, Offset: p.Offset, Limit: p.Limit}

	view := p.View
	if view == "" && p.Event == "" {
		view = chstore.MLViewService
	}
	items, mlErr := s.store.ListMLEvents(ctx, unifiedMLFilter(view, since, p.Gates))
	var legacy []chstore.AnomalyEvent
	var legErr error
	if p.View == "" { // platform / held: yalnız ML olayları
		legacy, legErr = s.store.ListAnomalyEvents(ctx, chstore.ListAnomalyEventsFilter{
			SinceNs: since.UnixNano(), Limit: unifiedLegacyCap, MaxExecSec: unifiedExecSec})
	}
	if p.View == chstore.MLViewHeld {
		items = heldOnly(items, p.Gates)
		resp.GatesMode, resp.Held = p.Gates, summarizeHeld(items)
	}
	switch {
	case mlErr != nil && legErr != nil:
		return nil, mlErr
	case mlErr != nil:
		items, resp.Partial = nil, "ml"
	case legErr != nil:
		legacy, resp.Partial = nil, "legacy"
	}
	if len(legacy) > 0 {
		legacy = s.store.EnrichAnomaliesWithVerdicts(ctx, legacy)
	}
	resp.Truncated = len(items) >= chstore.MLUnifiedCap || len(legacy) >= unifiedLegacyCap

	cfg := s.store.GetMLConfig(ctx)
	slack := ml.GroupSlack(cfg)
	groups := s.mlGroupsFor(ctx, items, slack)
	shadows := groupShadows(items, groups)
	in := ml.UnifiedInput{ML: items, Groups: groups, Shadows: shadows, Legacy: legacy, Now: now, Slack: slack}
	rows := ml.BuildUnified(in)
	if p.Unattached && p.Event == "" {
		rows = s.unattachedUnified(ctx, rows, items, legacy, since, &resp)
	}

	toRow := func(u ml.UnifiedRow, members bool) unifiedAnomalyRow {
		out := unifiedAnomalyRow{Key: u.Key, Service: u.Service, Start: u.Start, End: u.End, Signals: u.Signals,
			Status: u.Status, MLCount: len(u.ML), LegacyCount: len(u.Legacy)}
		if out.Signals == nil {
			out.Signals = []string{}
		}
		mlRows := func(idx []int) []mlEventRow {
			evs := make([]chstore.MLEvent, len(idx))
			for i, j := range idx {
				evs[i] = items[j]
			}
			rs := mlEventRows(evs, cfg, now, loc, mlListExtras{Groups: groups, IsBatch: s.store.AnomalySensitivityForDetectors().IsBatchService})
			for i := range rs {
				rs[i].GroupShadow = nil
				if g := groups[rs[i].ID]; g != nil {
					if d, ok := shadows[g.ID]; ok {
						rs[i].GroupShadow = &d
					}
				}
			}
			return rs
		}
		if u.MLLead >= 0 {
			lead := mlRows([]int{u.MLLead})[0]
			if !members {
				lead.Evidence = ""
			}
			out.MLLead = &lead
			if p.View == chstore.MLViewHeld {
				out.HeldGates = heldGatesOf(items[u.MLLead])
			}
		}
		if u.LegLead >= 0 {
			lead := legacy[u.LegLead]
			out.LegacyLead = &lead
		}
		if members {
			out.ML = mlRows(u.ML)
			for _, j := range u.Legacy {
				out.Legacy = append(out.Legacy, legacy[j])
			}
		}
		return out
	}

	if p.Event != "" {
		for _, u := range rows {
			if slices.ContainsFunc(u.Legacy, func(j int) bool { return legacy[j].ID == p.Event }) {
				row := toRow(u, true)
				resp.Row = &row
				break
			}
		}
	} else {
		page, total, openN, doneN := pageUnified(rows, p)
		resp.Total, resp.OpenTotal, resp.DoneTotal = total, openN, doneN
		for _, u := range page {
			resp.Items = append(resp.Items, toRow(u, false))
		}
	}
	if resp.Partial != "" {
		return uncacheable(resp), nil
	}
	return resp, nil
}
