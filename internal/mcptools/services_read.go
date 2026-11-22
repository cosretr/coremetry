package mcptools

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// services_read.go — servis RED okumasının MV yolu (v0.10.25, Copilot
// denetimi bulgusu).
//
// ── KUSUR: AÇIKLAMA MODELE YALAN SÖYLÜYORDU ─────────────────────────────
//
// `list_services` açıklaması modele şunu diyordu:
//
//	"Reads the 5-minute pre-aggregate so it's cheap to call repeatedly"
//
// Handler ise KOŞULSUZ olarak `GetServicesFilteredIn` → `GetServicesQuery`
// → ham `spans` GROUP BY çağırıyordu. MV ikizi (`GetServicesAggFiltered2`)
// zaten VAR ve `/api/services` onu `servicesUseMV` kapısıyla kullanıyor;
// yalnız tool'da kapı yoktu.
//
// İki ayrı zarar:
//
//  1. CLAUDE.md sert kısıtı: MV varken ham `spans` okumak BUG. Bu iki
//     tool kataloğun en çok çağrılacakları ("şu an hangi servis
//     sağlıksız" giriş noktası) ve model onları 5 turluk döngüde birden
//     çok kez çağırabiliyor.
//  2. Açıklama, modelin MALİYET MODELİdir. "Ucuz, tekrar tekrar çağır"
//     diyen bir katalog, küçük modeli döngüde tekrar çağırmaya İTİYOR —
//     yani yalan, kendi maliyetini büyütüyor.
//
// ── NEDEN İKİ YOL DA GEREKLİ ────────────────────────────────────────────
//
// MV'de `deploy_env` boyutu YOK (aynı sebeple /api/services'in kapısı
// `env == ""` istiyor). Operatör env'e daralttığında okuma ham span'lere
// düşmek ZORUNDA. Kapı bu yüzden kaldırılamıyor; yapılabilecek şey onu
// tool'a da uygulamak ve açıklamayı iki yolu da anlatacak şekilde
// düzeltmek.
//
// Pencere eşiği de aynı gerekçeyle duruyor: MV kovaları 5 dakikalık, daha
// dar bir pencerede MV'den okumak kovanın tamamını o pencereye atfetmek
// olurdu.

// servicesReadUseMV — MV yolu uygun mu.
//
// `/api/services`'in `servicesUseMV` kapısıyla AYNI sözleşme; cluster
// boyutu tool yüzeyinde hiç yok, o yüzden iki girdi yeterli.
func servicesReadUseMV(window time.Duration, env string) bool {
	return window >= 5*time.Minute && env == ""
}

// readServices — servis RED listesi, uygun olduğunda MV'den.
//
// Dönüş tipi iki yolda da `[]chstore.ServiceSummary`; çağıran hangi yolun
// koştuğunu bilmek zorunda değil. İkinci dönüş, cevabın zarfında
// operatöre/modele ilan edilebilsin diye kaynağı söylüyor — bir maliyet
// iddiası ölçülmeden yazılmamalı, ve artık yazılmıyor.
func readServices(
	ctx context.Context, d Deps, from, to time.Time,
	nameContains, env string, limit int,
) ([]chstore.ServiceSummary, string, error) {
	if servicesReadUseMV(to.Sub(from), env) {
		rows, err := d.Store.GetServicesAggFiltered2(
			ctx, from, to, nameContains, nil, "rps", "desc", limit, 0,
			chstore.ServiceDisplayFilters{})
		if err == nil {
			return rows, "service_summary_5m", nil
		}
		// MV yolu düştü: ham yola geç ama SESSİZCE değil — kaynak
		// alanı "spans" diyecek, yani cevabın kendisi hangi yoldan
		// geldiğini taşıyor.
	}
	rows, err := d.Store.GetServicesFilteredIn(
		ctx, 0, from, to, nameContains, nil, "rps", "desc", limit, 0, "", env)
	if err != nil {
		return nil, "", err
	}
	return rows, "spans", nil
}

// readServicesIn — v0.10.464 (D3): ad KÜMESİ için readServices ikizi (bulanık
// çözümden gelen tam adlar). Aynı MV/ham yol kararı, aynı kaynak etiketi.
func readServicesIn(
	ctx context.Context, d Deps, from, to time.Time,
	names []string, env string, limit int,
) ([]chstore.ServiceSummary, string, error) {
	if len(names) == 0 {
		return nil, "", nil
	}
	if servicesReadUseMV(to.Sub(from), env) {
		rows, err := d.Store.GetServicesAggFilteredIn(ctx, from, to, "", names, "rps", "desc", limit, 0)
		if err == nil {
			return rows, "service_summary_5m", nil
		}
	}
	rows, err := d.Store.GetServicesFilteredIn(ctx, 0, from, to, "", names, "rps", "desc", limit, 0, "", env)
	if err != nil {
		return nil, "", err
	}
	return rows, "spans", nil
}

// serviceCatalogueMax — bulanık çözüm için okunan katalog tavanı
// (api.guidedServiceNames ile aynı: 2000).
const serviceCatalogueMax = 2000

// resolveServiceFuzzy — v0.10.464 (D3): ifadeyi canlı katalogla çözer
// (ResolveServiceAmong). Katalog okuması picker'ın DISTINCT'i (ucuz).
// v0.10.2451 — katalog span adları ∪ metrik servis adları (serviceCatalogue).
func resolveServiceFuzzy(ctx context.Context, d Deps, phrase string) (exact string, candidates []string, err error) {
	names, err := serviceCatalogue(ctx, d)
	if err != nil {
		return "", nil, err
	}
	exact, candidates = ResolveServiceAmong(phrase, names, 8)
	return exact, candidates, nil
}

// serviceCatalogue — v0.10.2451: span kataloğu (ListServiceNames, ≤2000) ∪
// VictoriaMetrics RED'i olan servisler (Deps.MetricServiceNames). Span okuma
// hatası hata; metrik tarafı yumuşak (nil → yalnız span, eski davranış).
func serviceCatalogue(ctx context.Context, d Deps) ([]string, error) {
	names, _, err := d.Store.ListServiceNames(ctx, "", serviceCatalogueMax, 0)
	if err != nil {
		return nil, err
	}
	return UnionServiceNames(names, metricServiceNames(ctx, d)), nil
}

// metricServiceNames — nil-güvenli Deps.MetricServiceNames.
func metricServiceNames(ctx context.Context, d Deps) []string {
	if d.MetricServiceNames == nil {
		return nil
	}
	return d.MetricServiceNames(ctx)
}

// UnionServiceNames — SAF: span adları sırasıyla, ardından span'de olmayan
// metrik adları sıralı; tekrarsız. api.guidedServiceNames de kullanır.
func UnionServiceNames(span, metric []string) []string {
	if len(metric) == 0 {
		return span
	}
	seen := make(map[string]bool, len(span)+len(metric))
	out := make([]string, 0, len(span)+len(metric))
	for _, n := range span {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	var extra []string
	for _, n := range metric {
		if n != "" && !seen[n] {
			seen[n] = true
			extra = append(extra, n)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// metricOnlyService — v0.10.2451: ad (tam ya da tek yakın eşleşme) metrik
// kataloğunda varsa o ad, yoksa "". Span kataloğuna BAKMAZ: çağıran span
// okumasının boş döndüğünü zaten bilir.
func metricOnlyService(ctx context.Context, d Deps, name string) string {
	ms := metricServiceNames(ctx, d)
	if len(ms) == 0 || strings.TrimSpace(name) == "" {
		return ""
	}
	exact, _ := ResolveServiceAmong(name, ms, 8)
	return exact
}

// metricOnlyNote — modele metrik-yalnız servisin anlamı + sonraki adım.
const metricOnlyNote = "metric-only: the service has VictoriaMetrics RED (unsampled app histograms / spanmetrics) but no spans in this window — upstream tail sampling dropped its traces or it is not traced. It EXISTS; read its RED with query_metrics (family red) or compare_baseline; span tools (traces, operations, exemplars) will be empty."

// metricOnlyHealth — SAF: get_service_health'in metrik-yalnız cevabı.
func metricOnlyHealth(name string, openProblems uint64) map[string]any {
	return map[string]any{
		"found": true, "service": name, "metric_only": true,
		"traces_in_window": false, "open_problems": openProblems, "note": metricOnlyNote,
	}
}

// metricOnlyMatches — v0.10.2451: list_services name_contains için span
// satırlarında olmayan, alt-dizeyle eşleşen metrik servis adları (≤max).
// name_contains boşsa yok (filo listesini şişirmez).
func metricOnlyMatches(ctx context.Context, d Deps, nameContains string, rows any, max int) []string {
	needle := strings.ToLower(strings.TrimSpace(nameContains))
	if needle == "" {
		return nil
	}
	ms := metricServiceNames(ctx, d)
	if len(ms) == 0 {
		return nil
	}
	have := map[string]bool{}
	if rs, ok := rows.([]chstore.ServiceSummary); ok {
		for _, r := range rs {
			have[r.Name] = true
		}
	}
	return metricOnlyFilter(ms, have, needle, max)
}

// metricOnlyFilter — SAF çekirdek: alt-dize (küçük harf iğne), span'de yok, ≤max.
func metricOnlyFilter(metric []string, have map[string]bool, needle string, max int) []string {
	var out []string
	for _, n := range metric {
		if len(out) >= max {
			break
		}
		if !have[n] && strings.Contains(strings.ToLower(n), needle) {
			out = append(out, n)
		}
	}
	return out
}

// ReadServiceEnvRED — v0.10.1274: tek servisin TEK ortamdaki RED'i (guided
// servis-sağlık demeti için; get_service_health ile AYNI okuma yolu — env
// verildiğinde sınırlı ham span taraması). found=false: servis bu ortamda
// bu pencerede span üretmedi.
func ReadServiceEnvRED(ctx context.Context, d Deps, service, env string, from, to time.Time) (chstore.ServiceSummary, bool, error) {
	rows, _, err := readServicesIn(ctx, d, from, to, []string{service}, env, 1)
	if err != nil || len(rows) == 0 {
		return chstore.ServiceSummary{}, false, err
	}
	return rows[0], true, nil
}
