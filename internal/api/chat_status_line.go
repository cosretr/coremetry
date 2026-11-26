package api

import (
	"context"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_status_line.go — v0.10.2333 (conversation-audit §6.1 F1, karar 11):
// araç turu başlarken sunucunun yazdığı kısa ara durum cümlesi.
//
// Modelin kendi ara metni bu işi göremez: araç sunulan turda ilk 160 rune
// tutulur (provider/tools_stream_gate.go), kısa önsöz hiç görünmez. Cümle
// bu yüzden sunucu parçasıdır: geçici `data-status` (uistream translator;
// transient — mesaja ve arşive girmez, istemci cevap metni başlayınca ya da
// alışveriş bitince/durdurulunca siler).
//
// Kurallar:
//   - Tur başına EN ÇOK bir cümle (turda birden çok çağrı olsa da): evre
//     ailesi turun bilinen evreli İLK çağrısından (chat_step_phase.go).
//     Bilinmeyen araç (dış MCP, uydurma ad) cümle üretmez.
//   - Havuz araç başına değil evre ailesi başına; seçim deterministik
//     (tur sırası), aynı alışverişte art arda aynı cümle yok.
//   - Kapı: v2 VE cosre_conversation.statusLine (v0.10.2473: `style`tan
//     ayrıldı, varsayılan AÇIK — karar 11 tersine; statusLineOn). Cümle
//     deterministiktir (evre ailesi havuzu), model çağrısı yok. Kapalıyken
//     olay yok.
//   - Cümleler birinci tekil, hitapsız (sen/siz gerekmez), ünlemsiz,
//     emojisiz; "…" ile biter.

// chatStatusEventKind — data-status olayı (yalnız v2 yazıcısına ulaşır;
// v1OnlySink düşürür — chat_stop_event.go v2OnlyEventKinds).
const chatStatusEventKind = "status"

// chatStatusPool — evre ailesi → ara cümleler. Aileler stepPhaseByTool'un
// değerleriyle aynı (test pinler).
var chatStatusPool = map[string][]string{
	"Metrik":     {"Metriklere bakıyorum…", "Metrik serilerini çekiyorum…", "Zaman serisini okuyorum…", "Sayıları karşılaştırıyorum…"},
	"Log":        {"Loglara bakıyorum…", "Logları tarıyorum…", "Log kayıtlarını okuyorum…", "Loglarda iz sürüyorum…"},
	"Trace":      {"Trace'lere bakıyorum…", "Trace'i açıyorum…", "İsteğin yolunu izliyorum…", "Span'leri inceliyorum…"},
	"Sağlık":     {"Servisin durumuna bakıyorum…", "Hata oranı ve gecikmeye bakıyorum…", "Sağlık göstergelerini kontrol ediyorum…", "Operasyonları gözden geçiriyorum…"},
	"Problem":    {"Açık problemlere bakıyorum…", "Problem kaydını açıyorum…", "Kök neden ipuçlarını topluyorum…", "Benzer olaylara bakıyorum…"},
	"Anomali":    {"Anomalilere bakıyorum…", "Olağan dışı hareketleri arıyorum…", "Sapmaları kontrol ediyorum…"},
	"Exception":  {"Exception'lara bakıyorum…", "Hata gruplarını inceliyorum…", "Exception örneklerini açıyorum…"},
	"Topoloji":   {"Bağımlılık haritasına bakıyorum…", "Servisler arası çağrılara bakıyorum…", "Topolojiyi açıyorum…"},
	"Altyapı":    {"Pod'lara bakıyorum…", "Altyapı tarafını kontrol ediyorum…", "Workload'lara bakıyorum…", "Kaynak kullanımına bakıyorum…"},
	"Değişiklik": {"Son deploy'ları kontrol ediyorum…", "Değişikliklere bakıyorum…", "Yakın zamandaki sürümlere bakıyorum…"},
	"Bilgi":      {"Bilgi bankasına bakıyorum…", "Dokümanlara bakıyorum…", "Runbook'a bakıyorum…"},
}

// chatStatusFamily — SAF: turun ailesi = evresi bilinen ilk çağrının evresi.
func chatStatusFamily(calls []copilot.ToolCall) string {
	for _, tc := range calls {
		if p := stepPhaseTR(tc.Name); p != "" && len(chatStatusPool[p]) > 0 {
			return p
		}
	}
	return ""
}

// chatStatusPick — SAF: ailenin havuzundan tur sırasıyla seçim; seçilen bir
// önceki cümleyle aynıysa sıradaki. Aile yoksa "".
func chatStatusPick(family string, round int, last string) string {
	pool := chatStatusPool[family]
	if len(pool) == 0 {
		return ""
	}
	if round < 0 {
		round = 0
	}
	i := round % len(pool)
	if pool[i] == last {
		i = (i + 1) % len(pool)
	}
	return pool[i]
}

// chatStatusLine — tek alışverişin ara cümle izi (döngü fonksiyonunun yerel
// değişkeni; sıfır değer hazır).
type chatStatusLine struct{ last string }

// round — araç turunun başında (çağrılar yürütülmeden önce) en çok bir
// `status` olayı. Kapı kapalıysa ya da aile yoksa hiçbir şey yazmaz.
func (l *chatStatusLine) round(ctx context.Context, emit func(string, any), round int, calls []copilot.ToolCall) {
	if len(calls) == 0 || !statusLineOn(ctx) {
		return
	}
	s := chatStatusPick(chatStatusFamily(calls), round, l.last)
	if s == "" {
		return
	}
	l.last = s
	emit("status", map[string]string{"text": s})
}

// statusLineOn — v0.10.2473: v2 VE statusLine ayarı (style'dan bağımsız).
func statusLineOn(ctx context.Context) bool {
	return cosreV2Marked(ctx) && chstore.CurrentCosreConversation().StatusLine
}
