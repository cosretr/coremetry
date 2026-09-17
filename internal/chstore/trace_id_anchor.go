package chstore

// trace_id_anchor.go — v0.10.753 (trace bütünlüğü denetimi Q1-#1; operatör
// onayı 2026-09-17): listenin `?traceId=` yolu sessizce 1 saatle
// sınırlıydı. FE 32-hex id için from/to atar, handler "to yoksa now, from
// yoksa to-1h" varsayar, kimlik-önce çıpalama TraceID'yi dışlar
// (identityFirstEligible) ve boş-teşhis TraceID'de bastırılır → 1 saatten
// eski her trace için sessiz boş liste. Detay sayfası (GetTrace) ise
// pencereyi TraceWindow ile (MV 24h→7g→90g) çözüyordu.
//
// Şimdi liste de aynı probe ile pencereyi trace'in GERÇEK zamanına
// çıpalar; sonuç mevcut IdentityHit OUT paramıyla yanıta gider
// (`identity.traceId=true`, windowFrom/To) — handler'a satır eklenmez.
// Bulunamazsa pencere aynen kalır ve hits=0 gider: FE "son 90 günün trace
// özetinde yok" der (detay sayfası 31 günlük ham taramayı da dener, liste
// denemez — sınırsız tarama sert kısıt).

import (
	"context"
	"time"
)

// applyTraceIDAnchor — SAF: probe sonucunu filtreye ve IdentityHit'e
// işler. ok=false → pencere dokunulmaz, hits=0.
func applyTraceIDAnchor(f *TraceFilter, lo, hi time.Time, ok bool) IdentityHit {
	hit := IdentityHit{Keys: []string{"trace_id"}, MatchedKey: "trace_id", TraceID: true}
	if !ok || f == nil {
		return hit
	}
	f.From, f.To = lo, hi
	hit.Hits = 1
	hit.WindowFromNs, hit.WindowToNs = lo.UnixNano(), hi.UnixNano()
	return hit
}

// anchorTraceID — GetTraces'in ilk adımı (TraceID doluyken).
func (s *Store) anchorTraceID(ctx context.Context, f *TraceFilter) IdentityHit {
	lo, hi, ok := s.TraceWindow(ctx, f.TraceID)
	return applyTraceIDAnchor(f, lo, hi, ok)
}
