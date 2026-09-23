package chstore

// trace_services.go — v0.10.768 (Oracle test özeti). Trace id → Coremetry
// servisi. "Bu trace Coremetry'de var mı, hangi servis?" sorusu için; ids
// tavanlı, pencere [from, to], LIMIT + bütçe. Yalnız spans → telemetryReadConn.
//
// v0.10.892 (operatör, prod 2026-09-23: "hep aynı servisler geliyor") — SEÇİM
// KURALI DEĞİŞTİ. Eski kural kök span'ın servisiydi; kök hemen her trace'te
// GİRİŞ NOKTASIDIR (apigateway-*, yarp, onlineintegration, loginivr) → Oracle
// hatası hangi serviste doğmuş olursa olsun hep aynı birkaç kanal servisi
// listeleniyordu. Yeni sıra: (1) trace'te HATA veren span'lardan EN GEÇ
// başlayanının servisi (zincirde en derin hata = hatanın doğduğu yer;
// yukarıya yayılan hatalar daha erken başlar), (2) yoksa kök, (3) yoksa
// herhangi bir span. Kök ayrıca döner ki özet "kanal"ı da gösterebilsin.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const traceServicesMaxIDs = 200

// traceServicesSQL — SAF: iki zaman sınırı + IN listesi + LIMIT + bütçe.
// v0.10.895 — beşinci kolon ex_type: trace'te en geç başlayan exception'ın
// tipi (Oracle jenerik kodun ikincil ayırt edicisi, audit §6.1 2. basamak);
// exFragments ile — MATERIALIZED kolon yoksa JSON_VALUE ifadesine düşer.
func traceServicesSQL(n int) string { return traceFactsSQL(n, exFragments(false)) }

func traceFactsSQL(n int, frag exFrag) string {
	holders := strings.TrimSuffix(strings.Repeat("?,", n), ",")
	return fmt.Sprintf(`
		SELECT trace_id,
		       argMaxIf(service_name, time, status_code = 'error') AS err_svc,
		       anyIf(service_name, parent_id = '')                  AS root_svc,
		       any(service_name)                                    AS any_svc,
		       argMaxIf(%s, time, %s)                                AS ex_type
		FROM spans
		WHERE time >= ? AND time <= ?
		  AND trace_id IN (%s)
		GROUP BY trace_id
		LIMIT %d
		SETTINGS max_execution_time = 5`, frag.Type, frag.Match, holders, traceServicesMaxIDs)
}

// TraceFact — v0.10.895: trace id → servis (pickTraceService) + exception tipi.
type TraceFact struct {
	Service string `json:"service"`
	ExType  string `json:"exType,omitempty"`
}

// TraceServicesByIDs — ids ≤200 (fazlası kırpılır). Bulunmayan id haritada
// yoktur; bulunan ama servissiz (olmamalı) "" ile döner. Değer: hata span'ının
// servisi → kök → herhangi (pickTraceService).
func (s *Store) TraceServicesByIDs(ctx context.Context, ids []string, from, to time.Time) (map[string]string, error) {
	facts, err := s.TraceFactsByIDs(ctx, ids, from, to)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(facts))
	for id, f := range facts {
		out[id] = f.Service
	}
	return out, nil
}

// TraceFactsByIDs — v0.10.895: servis + exception tipi tek sorguda (Oracle
// özne çözücüsü ve jenerik kod ayırt edicisi). ids ≤200 (fazlası kırpılır).
func (s *Store) TraceFactsByIDs(ctx context.Context, ids []string, from, to time.Time) (map[string]TraceFact, error) {
	out := map[string]TraceFact{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > traceServicesMaxIDs {
		ids = ids[:traceServicesMaxIDs]
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, from, to)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.telemetryReadConn().Query(ctx, traceFactsSQL(len(ids), exFragments(s.hasExCols)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, errS, root, anyS, exType string
		if err := rows.Scan(&id, &errS, &root, &anyS, &exType); err != nil {
			return nil, err
		}
		out[id] = TraceFact{Service: pickTraceService(errS, root, anyS), ExType: exType}
	}
	return out, rows.Err()
}

// pickTraceService — SAF (tablo testli): hata span'ı → kök → herhangi.
func pickTraceService(errSvc, rootSvc, anySvc string) string {
	switch {
	case errSvc != "":
		return errSvc
	case rootSvc != "":
		return rootSvc
	}
	return anySvc
}
