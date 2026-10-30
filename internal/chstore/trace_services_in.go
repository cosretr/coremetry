package chstore

// trace_services_in.go — v0.10.1684 (CoSRE v2 araç seti dilim 3,
// docs/cosre/tools-audit.md §9): TraceFilter.Services — servis KÜMESİ (IN).
//
// Tekil Service'in çoğul ikizi; anlam AYNI (span düzeyi daraltma, satır
// kümedeki servislerin span'lerinden kurulur — v0.5.440 servis anlamı):
//
//   - normalizeTraceServices (SAF, GetTraces girişi): kırp, boşları at,
//     tekilleştir, sırala, MaxTraceServices'te kes. Service ya da
//     RequireServices doluysa küme DÜŞER (tekil yol bayt bayt aynı). Tek
//     elemanlı küme Service'e çevrilir — tekil yolun tüm hızlı dalları
//     (hata-önce, servis dilimi) aynen koşar. Kümeyle sayım (CountMode)
//     "skip"e zorlanır: sayım yolları kümeyi taşımaz.
//   - ham yol: buildGetTracesWhere `service_name IN (…)` (spans PK öneki).
//   - MV yolu: 1. aşama trace_service_index_5m `service_name IN (…)` akışkan
//     dilimi (traceServicesSliceScanSQL); hatalı-yalnız istek MV'den DÜŞER
//     (tracesMVEligible) — dilim hata bilmez, yoğun serviste nadir hatalar en
//     yeni N'in dışında kalırdı (v0.10.812 sınıfı); ham yol hatayı span
//     WHERE'iyle bulur.

import (
	"context"
	"sort"
	"strings"
	"time"
)

// MaxTraceServices — Services kümesinin tavanı (mcptools.MaxTeamServices ile eşit; test pinler).
const MaxTraceServices = 100

// normalizeServiceSet — SAF: kırp, boşları at, tekilleştir, sırala, tavanla.
func normalizeServiceSet(in []string, max int) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeTraceServices — SAF: GetTraces girişinde Services sözleşmesi.
func normalizeTraceServices(f TraceFilter) TraceFilter {
	if len(f.Services) == 0 {
		return f
	}
	if f.Service != "" || len(f.RequireServices) > 0 {
		f.Services = nil
		return f
	}
	f.Services = normalizeServiceSet(f.Services, MaxTraceServices)
	if len(f.Services) == 1 {
		f.Service, f.Services = f.Services[0], nil
		return f
	}
	if len(f.Services) > 0 {
		f.CountMode = "skip"
	}
	return f
}

// inHolders — SAF: n adet "?" virgüllü.
func inHolders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// servicesArgs — SAF: IN bağları.
func servicesArgs(svcs []string) []any {
	out := make([]any, len(svcs))
	for i, s := range svcs {
		out[i] = s
	}
	return out
}

// traceServicesSliceScanSQL — servis kümesinin 1. aşaması: tekil
// traceServiceSliceScanSQL'in IN ikizi. Birden çok PK öneki olduğundan okuma
// sırası tam değil; LIMIT top-N yığını + max_execution_time 10 sınırlar.
func (s *Store) traceServicesSliceScanSQL(order string, n int) string {
	dir := "DESC"
	if order == "asc" {
		dir = "ASC"
	}
	return `
		SELECT trace_id, time_bucket
		FROM trace_service_index_5m
		WHERE service_name IN (` + inHolders(n) + `) AND time_bucket >= ? AND time_bucket < ?
		ORDER BY time_bucket ` + dir + `
		LIMIT ?
		SETTINGS max_execution_time = 10,
		         optimize_read_in_order = 1,
		         ` + s.shardSkipSetting()
}

// traceServicesSlice — kümenin penceredeki en yeni (asc: en eski) `want` id'si.
func (s *Store) traceServicesSlice(
	ctx context.Context, f TraceFilter, want int,
) (ids []any, cut time.Time, exhausted bool, err error) {
	if want <= 0 || len(f.Services) == 0 {
		return nil, time.Time{}, false, nil
	}
	budget := want * traceSliceOverprovision
	if budget > traceSliceMaxRows {
		budget = traceSliceMaxRows
	}
	args := append(servicesArgs(f.Services), f.From, f.To)
	return s.scanIDSlice(ctx, s.traceServicesSliceScanSQL(f.Order, len(f.Services)), args, f.From, want, budget)
}

// LogServicesSet — SAF (v0.10.1684): log süzgecinin servis kümesi; tekil
// Service doluysa nil (tekil yol aynen), aksi normalize + MaxTraceServices.
func LogServicesSet(service string, services []string) []string {
	if service != "" {
		return nil
	}
	return normalizeServiceSet(services, MaxTraceServices)
}
