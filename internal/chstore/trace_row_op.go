package chstore

// trace_row_op.go — v0.10.1807 — servis süzgeçli /traces'te satırın Name ve
// route'u SÜZGEÇTEKİ SERVİSİN giriş span'idir.
//
// Operator-reported (2026-11-05): /traces?service=orders-api'de Name kolonu
// (ve ondan kurulan "METHOD route") kök span'in — gateway'in — adını
// gösteriyordu; kök span'i olmayan trace'te (gateway traceparent, prod'da
// yaygın) "—". Kök neden: satır etiketi her iki yolda da KÖK kuralıyla
// kuruluyordu (MV: argMaxIfMerge(root_name_state) / entry_route_state, kök
// yoksa ''; ham: anyIf(kök) → any(name)), süzgeçteki servis hiç sorulmadı.
//
// Kural (v0.10.1806 ek kolonlarıyla AYNI — traceSpanRank tek gövde):
// servis süzgeci varken o servisin giriş span'i (server/consumer, en erken) >
// o servisin herhangi span'i (en erken) > kök > adı olan en erken span.
// Süzgeçsiz: kök > adı olan en erken span (ham liste SQL'i, repo.go). MV
// yolu süzgeçsizken kök yoksa '' döner — onu doldurmak sayfa başına ham
// okuma ister; bilerek YAPILMADI.
//
// TraceRow.RootName / RootRoute adları KORUNDU (FE'de yaygın): servis
// süzgeci altında bu iki alan servisin giriş span'ini taşır.
//
// Maliyet: sayfa belli olduktan sonra TEK sınırlı sorgu (≤ traceExtrasChunkIDs
// id/parça; UI sayfası ≤ 50) — id listesi idx_trace bloom'unda, pencere
// sayfa satırlarının gerçek min/max'ı (+5 dk geç-span payı), LIMIT nIDs,
// max_execution_time. fillTraceExtras ile aynı sınıf.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// traceRowOpRankKey — argMinIf sıralama anahtarı: (rank, time, span_id).
// span_id son bağ kırıcı: Name ve route aynı anahtarla seçildiği için
// eşit (rank, time) iki span'den ikisi de AYNI span'i seçer. SAF.
func traceRowOpRankKey(service string) (string, []any) {
	rank, args := traceSpanRank(service)
	return "(" + rank + ", time, span_id)", args
}

// traceRowOpRankKeyNoService — ham liste SQL'inin (arg yuvası yok) anahtarı.
var traceRowOpRankKeyNoService, _ = traceRowOpRankKey("")

// traceRowOpSQL — servis süzgeçli satır Name/route okuması. Yer tutucu
// sırası: projeksiyon arg'ları → from, to → id'ler. SAF; tablo testli.
func traceRowOpSQL(nIDs int, service string) (string, []any) {
	rank, rankArgs := traceRowOpRankKey(service)
	holders := strings.TrimSuffix(strings.Repeat("?,", nIDs), ",")
	sql := "SELECT trace_id" +
		", argMinIf(name, " + rank + ", name != '') AS op_name" +
		", argMinIf(http_route, " + rank + ", name != '') AS op_route" +
		" FROM spans WHERE time >= ? AND time <= ? AND trace_id IN (" + holders + ")" +
		" GROUP BY trace_id" +
		fmt.Sprintf(" LIMIT %d", nIDs) +
		" SETTINGS max_execution_time = 15"
	args := make([]any, 0, 2*len(rankArgs))
	args = append(args, rankArgs...)
	args = append(args, rankArgs...)
	return sql, args
}

// traceRowOp — bir trace için seçilen span'in adı ve route'u.
type traceRowOp struct{ name, route string }

// mergeTraceRowOps — SAF (tablo testli): seçilen ad/route'u satırlara yazar.
// Ad boşsa (adı olan span yok / okuma o trace'i bulamadı) satır olduğu gibi
// kalır. Sıra ve satır kümesi değişmez.
func mergeTraceRowOps(rows []TraceRow, ops map[string]traceRowOp) {
	for i := range rows {
		op, ok := ops[rows[i].TraceID]
		if !ok || op.name == "" {
			continue
		}
		rows[i].RootName, rows[i].RootRoute = op.name, op.route
	}
}

// fillServiceRowOps — servis süzgeci varken (traceExtrasService) sayfanın
// Name/route'unu servisin giriş span'inden yeniden yazar. Yumuşak düşer:
// okuma hata verirse satırlar eski etiketleriyle kalır (liste boş kalmaz).
func (s *Store) fillServiceRowOps(ctx context.Context, rows []TraceRow, f TraceFilter) {
	service := traceExtrasService(f)
	if service == "" || len(rows) == 0 {
		return
	}
	from, to := traceExtrasWindow(traceExtrasBounds(rows))
	ops := make(map[string]traceRowOp, len(rows))
	for start := 0; start < len(rows); start += traceExtrasChunkIDs {
		end := min(start+traceExtrasChunkIDs, len(rows))
		sql, args := traceRowOpSQL(end-start, service)
		args = append(args, from, to)
		for _, r := range rows[start:end] {
			args = append(args, r.TraceID)
		}
		t0 := time.Now()
		n, err := s.readTraceRowOps(ctx, sql, args, ops)
		f.Explain.step("row-op", sql, args, t0, n, err)
		if err != nil {
			log.Printf("[traces] servis giriş span'i okunamadı (%d id) — Name kök etiketinde kalır: %v", end-start, err)
			return
		}
	}
	mergeTraceRowOps(rows, ops)
}

func (s *Store) readTraceRowOps(ctx context.Context, sql string, args []any, ops map[string]traceRowOp) (int, error) {
	qrows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	defer qrows.Close()
	n := 0
	for qrows.Next() {
		var id string
		var op traceRowOp
		if err := qrows.Scan(&id, &op.name, &op.route); err != nil {
			return n, err
		}
		ops[id] = op
		n++
	}
	return n, qrows.Err()
}
