package api

// oracle_op_resolve.go — v0.10.1133 — Oracle OPERASYON ADI → fonksiyon kodu
// çözümleyicisi (operatör: CoSRE operasyon adıyla sorulan trace'lere "trace
// bulunamadı" diyordu; ad span'lerde yok, span'ler yalnız FUNCTION_CODE taşır).
//
// İnce depo sarmalayıcı: ortak kapsam (oracleOpScope — kod kaynakları + span
// anahtarı, GET /api/oracle/operations ile AYNI kural) + mevcut alt-dize
// okumaları (önce OracleOperationExact, sonra yakın adlar için
// OracleOperationSearch; ikisi de 7 gün, LIMIT, max_execution_time) + SAF
// çekirdek mcptools.ResolveOracleOpHits (yalnız TAM ad eşleşmesi kod üretir;
// alt-dize isabetleri ayrı "yakın adlar"). Üç tüketici aynı yolu kullanır:
// guided trace araması (trace_nl_search.go), sohbet ajanının
// resolve_oracle_operation aracı ve dış MCP (mcpDeps → Deps.OracleOps).
//
// Etkin Oracle kaynağı yoksa CH'ye HİÇ gidilmez; sonuç boş, hata yok.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// ResolveOracleOperation — ad → tam eşleşen operasyonun fonksiyon kodları
// (kod başına satır) + yakın adlar.
func (s *Server) ResolveOracleOperation(ctx context.Context, name string) (mcptools.OracleOpResolution, error) {
	sc := s.oracleOpScope()
	res := mcptools.OracleOpResolution{Enabled: sc.Enabled, SpanKey: sc.SpanKey, Matches: []mcptools.OracleOpMatch{}, NearNames: []string{}}
	if !sc.Enabled {
		return res, nil
	}
	q := oracleOpSearchQuery(name)
	if q == "" {
		return res, nil
	}
	now := time.Now()
	from := now.Add(-oracleOpSearchWindow)
	// Önce TAM ad (eşitlik sorgusu): alt-dize listesinin LIMIT'i tam eşi
	// dışarıda bırakamaz. Alt-dize araması yalnız yakın adlar içindir.
	exact, err := s.store.OracleOperationExact(ctx, q, sc.CodeSources, from, now)
	if err != nil {
		return res, err
	}
	res = mcptools.ResolveOracleOpHits(q, sc.SpanKey, exact)
	sub, err := s.store.OracleOperationSearch(ctx, q, sc.CodeSources, from, now, oracleOpSearchLimitMax)
	if err != nil {
		return res, err
	}
	res.NearNames = mcptools.ResolveOracleOpHits(q, sc.SpanKey, sub).NearNames
	return res, nil
}

// oracleOpSource — mcptools.OracleOpSource adaptörü. Değer olarak her zaman
// kurulur (nil değil): Oracle ayarı açılıp kapandıkça Enabled canlı cevap verir,
// boot'ta bir kez kurulan dış MCP Deps'i bayatlamaz.
type oracleOpSource struct{ s *Server }

func (o oracleOpSource) Enabled() bool { return o.s.oracleOpsEnabled() }

func (o oracleOpSource) ResolveOracleOperation(ctx context.Context, name string) (mcptools.OracleOpResolution, error) {
	return o.s.ResolveOracleOperation(ctx, name)
}
