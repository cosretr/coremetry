package rollout

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// worker_run.go — v0.10.982 — rollout_worker_runs satırı (docs/rollouts/
// v2-audit.md §10.3.8): DÖRT v2 işçisinin ortak koşu kaydı. `worker`
// anahtarda (ORDER BY (worker, started_at, host)) → her işçi yalnız kendi
// anahtar aralığını yazar. Tip burada: chstore (yazıcı) zaten bu paketi
// içe aktarır, P3/P4 işçileri de aynı tipi kullanır.
//
// YAZICI SÖZLEŞMESİ (§10.2, v0.10.959 incelemesi): started_at TTL çapasıdır;
// sıfır/epoch çapalı satır 1970'e yazılır ve ilk TTL birleşmesinde SESSİZCE
// düşer → ValidateWorkerRun INSERT'ten önce reddeder.

// v0.10.982 — işçi adları (§10.4 lider anahtarlarıyla aynı).
const (
	WorkerRolloutDetector = "rollout-detector"
	WorkerArgoCDMetrics   = "argocd-metrics"
	WorkerArgoCDAPI       = "argocd-api"
	WorkerADOEnrichment   = "ado-enrichment"
)

// WorkerRun — bir işçi tikinin koşu kaydı. Sayaçlar DDL tiplerine
// (UInt16/UInt32) yazıcıda kelepçelenir.
type WorkerRun struct {
	Worker          string
	StartedAt       time.Time // TTL çapası
	Host            string    // yazan pod (split-brain ayırıcı)
	FinishedAt      time.Time
	Status          string // ok | partial | failed | skipped (Run* sabitleri)
	ScopesTotal     int    // küme ya da instance
	ScopesOK        int
	SeriesRead      int
	Truncated       bool
	PartialResponse bool
	RowsWritten     int
	Unmapped        int
	APICalls        int // gönderilen sorgu / API çağrısı
	APIThrottled    int
	DurationMs      int
	// Error — hata + notlar; dedektörde teşhis sayaçlarının özeti de burada
	// (DDL'de ayrı kolon yok; 0015 bayt-eşliği değişmez).
	Error string
}

// ValidateWorkerRun — v0.10.982 — yazıcı sözleşmesi (SAF); nil = yazılabilir.
func ValidateWorkerRun(r WorkerRun) error {
	var errs []error
	if !v2InVocab(r.Worker, WorkerRolloutDetector, WorkerArgoCDMetrics, WorkerArgoCDAPI, WorkerADOEnrichment) {
		errs = append(errs, fmt.Errorf("worker %q sözlük dışı", r.Worker))
	}
	if err := v2Anchor("started_at", r.StartedAt); err != nil {
		errs = append(errs, err)
	}
	if err := v2Anchor("finished_at", r.FinishedAt); err != nil {
		errs = append(errs, err)
	} else if r.FinishedAt.Before(r.StartedAt) {
		errs = append(errs, errors.New("finished_at started_at'ten önce"))
	}
	if !v2InVocab(r.Status, RunOK, RunPartial, RunFailed, RunSkipped) {
		errs = append(errs, fmt.Errorf("status %q sözlük dışı", r.Status))
	}
	if strings.TrimSpace(r.Host) == "" {
		errs = append(errs, errors.New("host boş (split-brain ayırıcı)"))
	}
	return errors.Join(errs...)
}
