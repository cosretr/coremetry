package ml

// new_template.go — v0.10.2069: ML log sinyalinin "ilk kez görüldü" kipi
// (eski log_template_new'in ML karşılığı; eski dedektör silinmedi).
//
// Karar ÇATALSIZ: anomaly.NewLogTemplateFamilies — eski kayıtçının aynı üç
// adımı (MaskerVersion sessiz penceresi, v0.10.1030/2040 aile kararı,
// v0.10.2028/2029 ≥ 20 satır + batch / ERROR+ kuralı). Bu dosya yalnız
// sonucu ml_events olayına çevirir; böylece olay servis gruplarına ve ≥ 2
// sinyal Problem kuralına (group_gate) katılır.
//
// Maliyet: tik başına en çok 2 CH okuması (log_templates defteri: aday +
// aday varsa bilinen; ikisi de LIMIT 500). Elasticsearch isteği YOK: olay
// sonrası adımlar da ES'e gitmez — örnek satır defterden (collectSamples
// dalı), servis çözümünün pod sorgusuna yalnız zaten atılıyorsa katılır
// (resolveLogServices). Kardinalite: aday okuması 500 satırla sınırlı,
// olay günlük tavanı DailyEventCap.

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	detNewTemplate = "new_template"
	// newTplPartPrefix — olay kimliğinin bölümü: Drain kalıp kimliği (metin
	// değil; kesilmiş metin iki kalıbı aynı kimliğe düşürmesin).
	newTplPartPrefix = "newtpl|"
	// newTplMinWindow — eski kayıtçının penceresi (2 × 5 dk): puller
	// döngüsü ile tik arasındaki kaymayı yutar.
	newTplMinWindow = 10 * time.Minute
)

// NewTemplates — dedektör.
type NewTemplates struct {
	capper
}

func NewNewTemplates() *NewTemplates { return &NewTemplates{capper: capper{daily: map[string]int{}}} }

func (d *NewTemplates) Name() string { return "new_template" }

func (d *NewTemplates) Enabled(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detNewTemplate) }

// newTemplateWindow — SAF: aday doğum penceresi. Tik aralığından kısa
// olursa iki tik arasında doğan kalıp kaçardı → en az 2 × tik.
func newTemplateWindow(tickSec int) time.Duration {
	return max(newTplMinWindow, 2*time.Duration(tickSec)*time.Second)
}

func (d *NewTemplates) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("depo yok: yeni log kalıbı sessiz")
	}
	found, _, err := anomaly.NewLogTemplateFamilies(ctx, deps.Store, newTemplateWindow(cfg.TickSec))
	if err != nil {
		return nil, err
	}
	var evs []chstore.MLEvent
	for _, a := range found {
		if a.Service == "" || !d.take(detNewTemplate, cfg, now) {
			continue
		}
		evs = append(evs, newTemplateMLEvent(a, cfg, now))
	}
	return evs, nil
}

// newTemplateMLEvent — SAF: eski olayın alanları (newTemplateEvent) ML
// olayına. Taban 0 (taban türü "none"), gözlenen = kalıbın satır sayısı,
// skor = kritik z (tek başına kesin karar, rare_template emsali).
func newTemplateMLEvent(a anomaly.LogTemplateAnomaly, cfg chstore.MLConfig, now time.Time) chstore.MLEvent {
	start := a.FirstSeenNs
	if start <= 0 {
		start = now.UnixNano()
	}
	last := max(a.LastSeenNs, start)
	ev := mergeEvidence("", map[string]any{
		"templateId": a.TemplateID,
		"template":   a.Template,
		"sample":     truncRunes(a.Sample, sampleBodyCap),
		"logQuery":   logstore.PatternSearchQuery(a.Template), // v0.10.2026: Logs bağlantısı o kalıba
		"lines":      a.TotalCount,
		"firstSeen":  a.FirstSeenNs,
		"legacyId":   a.LegacyEventID(), // /anomalies: aynı kalıbın eski satırı bastırılır
	})
	return chstore.MLEvent{
		ID: chstore.MLEventID(detNewTemplate, a.Service, newTplPartPrefix+a.TemplateID), Detector: detNewTemplate, Signal: chstore.MLSignalLog,
		Stage: chstore.MLStageShadow, Service: a.Service, Code: a.Template, StartedAt: start, LastSeen: last,
		WindowSec: uint32(cfg.BucketSec), Baseline: 0, Observed: float64(a.TotalCount), Score: math.Abs(cfg.CriticalZ), Direction: "up", Evidence: ev,
	}
}
