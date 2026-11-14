package copilot

// record_surface.go — v0.10.1316 (değerlendirme seti gerçek ajan döngüsünde):
// ai_calls satırının yüzeyine ZORUNLU önek.
//
// Ajan kipi evalset vakası canlı sohbet handler'ını (copilotChat) süreç
// içinde koşar; döngünün tek satırı evalset-<Yüzey> etiketiyle yazılır ama
// döngüden ÖNCEKİ kademeler (guided anlatımı, niyet sınıflandırıcısı, wiki,
// RAG …) kendi üretim etiketlerini (chat-guided, chat-intent …) basar. O
// satırlar önek taşımazsa /ai sayaçlarına, bütçeye ve router-gap raporuna
// değerlendirme çağrısı olarak karışırdı (K2 ihlali). Önek yalnız KAYITTA
// uygulanır: profil yönlendirmesi (profiles.go) etiketin kendisini okumaya
// devam eder — ajan kipi profili zaten WithProfile ile sabitler.

import (
	"context"
	"strings"
)

type recordSurfacePrefixKey struct{}

// WithRecordSurfacePrefix — v0.10.1316: bu ctx'ten yazılan HER ai_calls
// satırının yüzeyi prefix ile başlar (zaten başlıyorsa dokunulmaz). Boş
// prefix ctx'i aynen döndürür.
func WithRecordSurfacePrefix(ctx context.Context, prefix string) context.Context {
	if prefix == "" {
		return ctx
	}
	return context.WithValue(ctx, recordSurfacePrefixKey{}, prefix)
}

// recordSurface — v0.10.1316, SAF (ctx okuması dışında): kayda giden yüzey.
// İşaretsiz ctx'te surface aynen (üretim yolu bayt bayt eski).
func recordSurface(ctx context.Context, surface string) string {
	if ctx == nil {
		return surface
	}
	p, _ := ctx.Value(recordSurfacePrefixKey{}).(string)
	if p == "" || strings.HasPrefix(surface, p) {
		return surface
	}
	return p + surface
}

// RecordAs — v0.10.2134 (router boşlukları D1): ctx'in KAYIT etiketini
// surface yapar, çözülmüş profili ise sabitler. Neden sabitleme: profil
// yönlendirmesi CallMeta.Surface'i okur (profiles.go) — etiketi değiştirip
// profili sabitlemeseydik satır, çağrıyı yapan modelin değil yeni etiketin
// (haritada yok → varsayılan) modelini yazardı. Çağrı yolu değişmez; yalnız
// RecordUsage'a giden ctx için kullanılır. Boş surface ctx'i aynen döndürür.
func (s *Service) RecordAs(ctx context.Context, surface string) context.Context {
	if ctx == nil || surface == "" {
		return ctx
	}
	if s != nil && profileFromContext(ctx) == "" {
		s.mu.RLock()
		id := s.resolveProfileLocked(ctx).cfg.ID
		s.mu.RUnlock()
		if id != "" {
			ctx = WithProfile(ctx, id)
		}
	}
	m := MetaFromContext(ctx)
	m.Surface = surface
	return WithMeta(ctx, m)
}
