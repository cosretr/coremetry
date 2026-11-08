package loginsummary

// adapters.go — gerçek hizmetleri Builder arayüzlerine bağlar.

import (
	"context"
	"errors"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// vmAdapter — *vmetrics.Service Configured + QueryPromQLRange'ı zaten sağlar.
type vmAdapter struct{ svc *vmetrics.Service }

// VMServiceLabel — v0.10.1948: VM'de service.name'in etiketi (Deps.ServiceLabel).
func VMServiceLabel() string { return vmetrics.ServiceLabel() }

// VMFromService — nil servis her zaman "yapılandırılmamış".
func VMFromService(svc *vmetrics.Service) VM { return vmAdapter{svc: svc} }

func (a vmAdapter) Configured() bool { return a.svc != nil && a.svc.Configured() }

func (a vmAdapter) QueryPromQLRange(ctx context.Context, q string, from, to time.Time, step, maxPts int) ([]chstore.SpanMetricSeries, error) {
	if a.svc == nil {
		return nil, errors.New("loginsummary: VM yapılandırılmamış")
	}
	return a.svc.QueryPromQLRange(ctx, q, from, to, step, maxPts)
}
