package chstore

// problem_rootcause.go — Problem "Kök neden" etiketinin
// yapısal ekleri (CoSRE doğruluk ölçümü Faz A, dilim A1).
//
// Etiketin ÇEKİRDEĞİ (ne oldu metni, kategori, çözüm, kim / ne zaman) mevcut
// onaylı incident_cases satırıdır — SRE değerlendirmesinin zaten okuduğu
// "insan onaylı kök neden" (sre/eval.go). incident_cases'te suçlu bileşen ve
// bağlantı kolonu YOK; kolon eklemek iki-boot probe'u ister (/clickhouse-schema
// §3). Bu iki alan bu yüzden saved_views sistem satırında durur (invariant 5,
// yeni şema yok):
//
//	page='problem-rootcause', id='problem-rootcause-<problemId>', owner_id='',
//	name='label', query_string = ProblemRootCauseExtra JSON (≤4 KB).
//
// Yalnız PUT /api/problems/{id}/root-cause-label (editor+, denetimli) yazar;
// genel /api/views ucu yazamaz (systemSavedViewPages).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	// ProblemRootCausePage — eklerin saved_views page değeri.
	ProblemRootCausePage = "problem-rootcause"
	// ProblemRootCauseIDPrefix — satır kimliği öneki.
	ProblemRootCauseIDPrefix = "problem-rootcause-"
	// problemRootCauseMaxBytes — gövde tavanı.
	problemRootCauseMaxBytes = 4 << 10
	// ProblemRootCauseBatchMax — toplu okuma tavanı (replay raporu ≤ bu kadar Problem).
	ProblemRootCauseBatchMax = 200
)

// ProblemRootCauseExtra — etiketin incident_cases'te yeri olmayan alanları.
type ProblemRootCauseExtra struct {
	ProblemID string `json:"problemId"`
	// Culprit — suçlu servis ya da bileşen ("" = belirtilmedi). Servis adıysa
	// FE servis sayfasına bağlar; db:/ext: önekli düğüm olduğu gibi yazılır.
	Culprit string `json:"culprit,omitempty"`
	// Link — isteğe bağlı http(s) bağlantı (postmortem, ticket, PR).
	Link string `json:"link,omitempty"`
	By   string `json:"by,omitempty"`
	At   int64  `json:"at"` // unix ns — son yazım
}

// ProblemRootCauseID — SAF: problemId → satır kimliği.
func ProblemRootCauseID(problemID string) string { return ProblemRootCauseIDPrefix + problemID }

// decodeProblemRootCause — SAF: satır → ek (sahte / başka sayfa / silinmiş → ok=false).
func decodeProblemRootCause(v SavedView) (ProblemRootCauseExtra, bool) {
	if v.Page != ProblemRootCausePage || v.OwnerID != "" || v.Name == "" {
		return ProblemRootCauseExtra{}, false
	}
	var e ProblemRootCauseExtra
	if err := json.Unmarshal([]byte(v.QueryString), &e); err != nil || e.ProblemID == "" || v.ID != ProblemRootCauseID(e.ProblemID) {
		return ProblemRootCauseExtra{}, false
	}
	return e, true
}

// GetProblemRootCauseExtra — Problem'in etiket eki; yoksa (nil, nil).
func (s *Store) GetProblemRootCauseExtra(ctx context.Context, problemID string) (*ProblemRootCauseExtra, error) {
	if problemID == "" {
		return nil, nil
	}
	v, err := s.GetSavedView(ctx, ProblemRootCauseID(problemID))
	if err != nil || v == nil {
		return nil, err
	}
	e, ok := decodeProblemRootCause(*v)
	if !ok {
		return nil, nil
	}
	return &e, nil
}

// PutProblemRootCauseExtra — eki yazar (tam satır).
func (s *Store) PutProblemRootCauseExtra(ctx context.Context, e ProblemRootCauseExtra) error {
	if e.ProblemID == "" {
		return fmt.Errorf("problem root cause: problemId zorunlu")
	}
	if e.At == 0 {
		e.At = time.Now().UnixNano()
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(raw) > problemRootCauseMaxBytes {
		return fmt.Errorf("problem root cause: gövde %d B > tavan %d B", len(raw), problemRootCauseMaxBytes)
	}
	return s.UpsertSavedView(ctx, SavedView{ID: ProblemRootCauseID(e.ProblemID), Name: "label",
		Page: ProblemRootCausePage, QueryString: string(raw), CreatedAt: e.At})
}

// listProblemRootCauseSQL — toplu okuma (replay raporu): kimlik listesi, FINAL, LIMIT.
const listProblemRootCauseSQL = `
		SELECT id, owner_id, name, page, query_string, pinned, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND id IN ?
		LIMIT ?
		SETTINGS max_execution_time = 5`

// ListProblemRootCauseExtras — verilen Problem'lerin ekleri (problemId → ek; ≤200).
func (s *Store) ListProblemRootCauseExtras(ctx context.Context, problemIDs []string) (map[string]ProblemRootCauseExtra, error) {
	out := map[string]ProblemRootCauseExtra{}
	if len(problemIDs) == 0 {
		return out, nil
	}
	if len(problemIDs) > ProblemRootCauseBatchMax {
		problemIDs = problemIDs[:ProblemRootCauseBatchMax]
	}
	ids := make([]string, 0, len(problemIDs))
	for _, id := range problemIDs {
		ids = append(ids, ProblemRootCauseID(id))
	}
	rows, err := s.querySavedViews(ctx, listProblemRootCauseSQL, ProblemRootCausePage, ids, len(ids))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if e, ok := decodeProblemRootCause(r); ok {
			out[e.ProblemID] = e
		}
	}
	return out, nil
}
