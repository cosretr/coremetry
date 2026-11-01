package chstore

// ml_backfill.go — ML geçmiş doldurma ilerlemesi (v0.10.1763). system_settings
// `ml_backfill:<signal>` blobu: hangi saate kadar işlendi, eksik saatler,
// toplam/biten. Restart'ta kaldığı saatten devam; api pod'u karnede gösterir
// (süreç-içi sayaç sre pod'unda kalır, bu kayıt her pod'dan okunur).

import (
	"context"
	"encoding/json"
	"time"
)

// MLBackfillProgress — ilerleme kaydı.
type MLBackfillProgress struct {
	Signal    string  `json:"signal"`
	Total     int     `json:"total"`     // planlanan saat
	Done      int     `json:"done"`      // başarıyla yazılan saat
	Missing   []int64 `json:"missing"`   // yazılamayan saat başları (unix sn)
	DoneUntil int64   `json:"doneUntil"` // bu saat başına kadar (dahil) işlendi; unix sn
	PlanEnd   int64   `json:"planEnd"`   // planın bitiş saati (unix sn)
	Complete  bool    `json:"complete"`
	Running   bool    `json:"running"`
	StartedAt int64   `json:"startedAt"` // unix sn
	UpdatedAt int64   `json:"updatedAt"` // unix sn
	LastError string  `json:"lastError,omitempty"`
}

func mlBackfillKey(signal string) string { return "ml_backfill:" + signal }

// GetMLBackfillProgress — kayıt yoksa ok=false.
func (s *Store) GetMLBackfillProgress(ctx context.Context, signal string) (MLBackfillProgress, bool, error) {
	raw, err := s.GetSetting(ctx, mlBackfillKey(signal))
	if err != nil || len(raw) == 0 {
		return MLBackfillProgress{}, false, err
	}
	var p MLBackfillProgress
	if err := json.Unmarshal(raw, &p); err != nil {
		return MLBackfillProgress{}, false, nil // bozuk kayıt = yok
	}
	return p, true, nil
}

// SaveMLBackfillProgress — tam kayıt (UpdatedAt burada damgalanır).
func (s *Store) SaveMLBackfillProgress(ctx context.Context, p MLBackfillProgress) error {
	p.UpdatedAt = time.Now().Unix()
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, mlBackfillKey(p.Signal), raw)
}
