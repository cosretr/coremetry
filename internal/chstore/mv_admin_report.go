package chstore

import (
	"context"
	"sync"
)

// mv_admin_report.go — v0.10.848 (833'ün kuyruk maddesi ii). GET
// /api/admin/clickhouse/dangling-mv tek istekte üç okuyucu çağırıyordu ve
// her biri mvInventory'yi (clusterAllReplicas system.tables) yeniden okuyor,
// ikisi resolveHostAddrs ile HER host'a yeniden bağlanıyordu: 3 envanter + 2
// adres turu. Üç okuma üç "gerçek" de üretebilirdi (iki okuma arasında
// kurulan bir MV sarkan listesiyle kapsamayı ayrıştırır — v0.10.825'in
// mvInventory'yi çıkarma gerekçesinin istek düzeyindeki ikizi). Artık: TEK
// envanter snapshot'ı + tembel, tek-seferlik adres çözücü (sync.OnceValues);
// üç okuyucu aynı satırlardan karar verir. Public tekiller (DanglingMVs /
// MVCoverage / MVLeftovers) eylem yolları için kaldı; hepsi aynı *From
// gövdesine iner, iki ayrı gerçek yok.

// mvInventorySnap — mvInventory'nin bir okumasının değer hâli.
type mvInventorySnap struct {
	rows        []mvTableRow
	cluster, db string
}

func (s *Store) mvInventorySnap(ctx context.Context) (mvInventorySnap, error) {
	rows, cluster, db, err := s.mvInventory(ctx)
	return mvInventorySnap{rows: rows, cluster: cluster, db: db}, err
}

// hostAddrsOnce — resolveHostAddrs ilk çağrıda koşar, sonrakiler aynı cevabı
// görür (hata dahil: bir turda çözülemeyen adres ikinci turda da çözülmez,
// yeniden bağlanmak yalnız gecikme ekler). Hiç çağrılmazsa (tek düğüm, boş
// artık listesi) hiç bağlanmaz.
func (s *Store) hostAddrsOnce(ctx context.Context) func() (map[string]string, error) {
	return sync.OnceValues(func() (map[string]string, error) {
		addrs, _, err := s.resolveHostAddrs(ctx)
		return addrs, err
	})
}

// MVAdminReport — kartın tek isteklik zarfı. Envanter okunamazsa err (eski
// akış: sarkan listesi düşerdi → 500); kapsama/artık hataları ALAN bazında
// taşınır, biri düşünce öteki yarı çalışmaya devam eder (v0.10.825/830).
// Leftovers hiç nil olmaz: kart "yok"u "ölçülmedi"den ayırır.
type MVAdminReport struct {
	Cluster     string
	Dangling    []DanglingMV
	Coverage    MVCoverageReport
	CoverageErr error
	Leftovers   []MVLeftover
	LeftoverErr error
}

func (s *Store) MVAdminReport(ctx context.Context) (MVAdminReport, error) {
	snap, err := s.mvInventorySnap(ctx)
	r := MVAdminReport{Cluster: snap.cluster, Leftovers: []MVLeftover{}}
	if err != nil {
		return r, err
	}
	addrs := s.hostAddrsOnce(ctx)
	r.Dangling = s.danglingMVsFrom(ctx, snap)
	r.Coverage, r.CoverageErr = s.mvCoverageReportFrom(ctx, snap, true, addrs)
	// v0.10.833 — öksüz kararı kapsamanın topladığı `TO INNER UUID` kümesine
	// bakar; kapsama düştüyse küme boştur ve öksüz satırları düğmesiz gelir.
	lo, lerr := s.mvLeftoversFrom(ctx, snap, r.Coverage.Targets, addrs)
	if lo != nil {
		r.Leftovers = lo
	}
	r.LeftoverErr = lerr
	return r, nil
}
