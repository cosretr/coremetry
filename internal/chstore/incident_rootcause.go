package chstore

// incident_rootcause.go — v0.10.698 (Dynatrace paritesi #1, dilim B)
// kök neden zenginleştirmesinin kalanı. Liste/detay zenginleştirmesi
// (EnrichIncidentsWithRootCause, pickIncidentRootCause) Incidents ölü
// kod 5'te (v0.10.2075) handler'larıyla birlikte silindi; toplu bağlı
// problem okuması Inbox katlaması + problem standardı için kalır.

import "context"

// incidentProblemIDCap — bir listenin (≤200 incident) bağlı problem
// kümesi için tek sorgunun üst sınırı; OpenIncidentRollups'un 5000 tavanıyla
// aynı sınıf. Bağlı problem sayısı incident başına küçük, tavan yalnız kaza
// sigortası.
const incidentProblemIDCap = 5000

// IncidentProblemIDs — birden çok incident'ın bağlı problem id'leri TEK
// sorguda (incident_problems FINAL, IN-listesi bağlı literal). Anahtar
// problem_id olduğu için tarama tablo boyu; tablo küçük state tablosu,
// tavan + max_execution_time sigorta.
func (s *Store) IncidentProblemIDs(ctx context.Context, incidentIDs []string) (map[string][]string, error) {
	out := make(map[string][]string)
	ids := boundHypothesisIDs(incidentIDs)
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	holders := ""
	for i, id := range ids {
		if i > 0 {
			holders += ", "
		}
		holders += "?"
		args = append(args, id)
	}
	rows, err := s.conn.Query(ctx,
		`SELECT incident_id, problem_id FROM incident_problems FINAL
		 WHERE incident_id IN (`+holders+`)
		 LIMIT `+itoa(int64(incidentProblemIDCap))+`
		 SETTINGS max_execution_time = 10`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var inc, pid string
		if err := rows.Scan(&inc, &pid); err != nil {
			return nil, err
		}
		out[inc] = append(out[inc], pid)
	}
	return out, rows.Err()
}
