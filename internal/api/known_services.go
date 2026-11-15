package api

import (
	"context"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// known_services.go — v0.10.1344: ext-peer çözümünün bilinen-servis yedeği.
//
// Operatör raporu (2026-10-18, card-web-api komşuluğu): enstrümante
// acm-customer-lookup-prod, GSLB host'u `ext:<svc>-prod-app.<kurum>` ile
// çağrılıyordu. ResolveExtPeer'in kümesi yalnız GRAFTAKİ kenar adlarıydı;
// 1 hop'ta (ya da hedef servis penceredeki kenar setine girmediğinde)
// gerçek servis kümede yoktu, etiket-önek aşaması da eşleşecek ad bulamadı.
// Yedek: son 1 günde service_summary_5m'e yazmış servis adları (MV okuması,
// sınırlı, 5 sn). Süreç içi 60 sn bellek; hata kısa (15 sn) belleklenir ki
// CH sorunluyken her istek yeniden denemesin. Hata → yedeksiz (eski
// davranış), istek düşmez.

const (
	knownServicesTTL    = 60 * time.Second
	knownServicesErrTTL = 15 * time.Second
)

var knownServicesMemo struct {
	mu    sync.Mutex
	store *chstore.Store
	names []string
	exp   time.Time
}

// knownServiceNames — bellekli ad listesi (paylaşılan dilim: DEĞİŞTİRME).
// store nil → nil.
func (s *Server) knownServiceNames(ctx context.Context) []string {
	if s == nil || s.store == nil {
		return nil
	}
	m := &knownServicesMemo
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.store == s.store && now.Before(m.exp) {
		return m.names
	}
	names, err := s.store.KnownServiceNames(ctx)
	m.store = s.store
	if err != nil {
		m.names, m.exp = nil, now.Add(knownServicesErrTTL)
		return nil
	}
	m.names, m.exp = names, now.Add(knownServicesTTL)
	return names
}

// knownServiceSet — yedekten kurulmuş TAZE küme (çağıran üstüne ekleyebilir).
func (s *Server) knownServiceSet(ctx context.Context) *chstore.ServiceNameSet {
	return chstore.NewServiceNameSet(s.knownServiceNames(ctx)...)
}
