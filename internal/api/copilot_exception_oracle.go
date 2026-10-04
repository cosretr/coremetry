package api

// copilot_exception_oracle.go — v0.10.1100 (operatör onaylı): exception
// grubunun AI girdisini KAYNAĞINA göre seçen tek nokta. `ora:` grubu (Oracle
// hata tablosu, v0.10.1092) span-merkezli girdiyle boş/jenerik açıklama
// alıyordu (stack yok, örnek span yok); artık Oracle bağlamı + kendi sistem
// prompt'u. Kullananlar: ✨ "Explain root cause" (copilot_exception.go) ve
// insight kartı (insight.go). AI çekmecesinin takip sohbeti (read_source_code
// yolu, drawerExceptionInput) bilinçli olarak DEĞİŞMEDİ — takip, açıklamanın
// metnini bağlam olarak zaten taşır. api.go BÜYÜMEZ.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// TEST DİKİŞLERİ (exceptionInputSeam emsali): store'suz testte iki kurucu da
// sahteyle değiştirilir; prod'da aşağıdaki varsayılanlar.
var (
	spanExceptionInputFn = func(s *Server, ctx context.Context, g *chstore.ExceptionGroup, loc *time.Location) anomaly.ExceptionExplainInput {
		return anomaly.BuildExceptionExplainInput(ctx, s.store, s.logs, g, loc)
	}
	oracleExceptionInputFn = func(s *Server, ctx context.Context, g *chstore.ExceptionGroup, loc *time.Location) anomaly.ExceptionExplainInput {
		var rd anomaly.OracleContextReader
		if s.store != nil { // nil *Store'u arayüze sarmak nil olmayan arayüz verirdi
			rd = s.store
		}
		return anomaly.BuildOracleExceptionExplainInput(ctx, rd, g, loc)
	}
)

// exceptionExplainInput — grubun girdisi + sistem prompt'u. `ora:` → Oracle
// kurucusu + SystemPromptOracleException; aksi span kurucusu + SystemPromptException.
func (s *Server) exceptionExplainInput(ctx context.Context, g *chstore.ExceptionGroup, loc *time.Location) (anomaly.ExceptionExplainInput, string) {
	if chstore.IsOracleGroup(g.Fingerprint) {
		return oracleExceptionInputFn(s, ctx, g, loc), copilot.SystemPromptOracleException()
	}
	return spanExceptionInputFn(s, ctx, g, loc), copilot.SystemPromptException()
}
