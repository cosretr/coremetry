package chstore

// ml_status.go — ML son tik durumu (v0.10.1764). Süreç-içi sayaçlar sre
// pod'unda kalır; karne api pod'undan okunur → lider her tik sonunda küçük
// bir blob yazar (system_settings `ml_tick_status`). "0 olay" ile
// "çalışmıyor" buradan ayrılır; VM bekleme (backoff) durumu buradan görünür.

import (
	"context"
	"encoding/json"
)

const mlTickStatusKey = "ml_tick_status"

// MLBackoffEntry — beklemedeki VM ailesi.
type MLBackoffEntry struct {
	Family    string `json:"family"`
	Until     int64  `json:"until"` // unix sn
	Failures  int    `json:"failures"`
	LastError string `json:"lastError,omitempty"`
}

// MLSignalTick — sinyal başına son tik özeti (v0.10.1767 doldurur).
type MLSignalTick struct {
	Ran        bool   `json:"ran"`
	Services   int    `json:"services"`
	SourceMs   int64  `json:"sourceMs"`
	Candidates int    `json:"candidates"`
	Suppressed int    `json:"suppressed"`
	Written    int    `json:"written"`
	Partial    int    `json:"partial"`
	Error      string `json:"error,omitempty"`
}

// MLTickStatus — son tik.
type MLTickStatus struct {
	At         int64                   `json:"at"` // unix sn
	DurationMs int64                   `json:"durationMs"`
	Events     int64                   `json:"events"`
	Steps      map[string]int64        `json:"steps,omitempty"` // adım → ms
	Signals    map[string]MLSignalTick `json:"signals,omitempty"`
	Backoff    []MLBackoffEntry        `json:"backoff"`
	Partial    int64                   `json:"partial"` // bu tikte kısmi ES sonucu alan dedektör
	Docs       int64                   `json:"docs"`    // log RED penceresinde toplanan belge
	LastError  string                  `json:"lastError,omitempty"`
}

func (s *Store) GetMLTickStatus(ctx context.Context) (MLTickStatus, bool, error) {
	raw, err := s.GetSetting(ctx, mlTickStatusKey)
	if err != nil || len(raw) == 0 {
		return MLTickStatus{}, false, err
	}
	var t MLTickStatus
	if err := json.Unmarshal(raw, &t); err != nil {
		return MLTickStatus{}, false, nil
	}
	return t, true, nil
}

func (s *Store) SaveMLTickStatus(ctx context.Context, t MLTickStatus) error {
	if t.Backoff == nil {
		t.Backoff = []MLBackoffEntry{}
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, mlTickStatusKey, raw)
}
