package chstore

import (
	"context"
	"log"
)

// incident_problem_counts.go — Incidents listesi için bağlı problem sayıları
// (v0.10.797; dış skill denetimi 2026-09-19 I15: incident-response "etki
// büyüdükçe görünür olsun"). Başlık ilk problemden donuk kalır (bilinçli,
// incident.go:433-437); satır ise "Problems 3 (2 açık)" der. Sayım
// OpenIncidentRollups'la aynı LEFT JOIN semantiği (yaşı geçmiş problem
// sayılmaz), ama verilen id kümesi için — resolved incident'lar da dahil.

// IncidentProblemCount — bir incident'a bağlı problemler: toplam ve
// çözülmemiş.
type IncidentProblemCount struct {
	Total      int
	Unresolved int
}

const incidentProblemCountMaxIDs = 500 // liste tavanı 200; sınır güvenliği

// IncidentProblemCounts — tek sorgu, id kümesi sınırlı. State tabloları
// (incidents/problems) ana bağlantıda okunur (conn_strategy sözleşmesi).
func (s *Store) IncidentProblemCounts(ctx context.Context, ids []string) (map[string]IncidentProblemCount, error) {
	out := map[string]IncidentProblemCount{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > incidentProblemCountMaxIDs {
		ids = ids[:incidentProblemCountMaxIDs]
	}
	rows, err := s.conn.Query(ctx, `
		SELECT ip.incident_id                                          AS id,
		       toInt32(countIf(p.id != ''))                            AS n,
		       toInt32(countIf(p.id != '' AND p.status != 'resolved')) AS unresolved
		FROM incident_problems ip FINAL
		LEFT JOIN problems p FINAL ON p.id = ip.problem_id
		WHERE ip.incident_id IN (?)
		GROUP BY ip.incident_id
		LIMIT 500
		SETTINGS max_execution_time = 10, distributed_product_mode = 'global'`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n, unresolved int32
		if err := rows.Scan(&id, &n, &unresolved); err != nil {
			return nil, err
		}
		out[id] = IncidentProblemCount{Total: int(n), Unresolved: int(unresolved)}
	}
	return out, rows.Err()
}

// EnrichIncidentsWithProblemCounts — okuma-anı zenginleştirme
// (EnrichIncidentsWithRootCause deseni): sorgu hatası soft-fail, satırlar
// sayısız döner ve FE "—" basar; liste asla boşalmaz.
func (s *Store) EnrichIncidentsWithProblemCounts(ctx context.Context, rows []Incident) []Incident {
	if len(rows) == 0 {
		return rows
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	counts, err := s.IncidentProblemCounts(ctx, ids)
	if err != nil {
		log.Printf("[incident] problem counts: %v", err)
		return rows
	}
	for i := range rows {
		c := counts[rows[i].ID] // bağlı problemi olmayan incident 0/0
		n, u := c.Total, c.Unresolved
		rows[i].ProblemCount, rows[i].UnresolvedProblems = &n, &u
	}
	return rows
}
