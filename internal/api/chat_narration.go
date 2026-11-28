package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// chat_narration.go — v0.10.2491 (CoSRE sohbet deneyimi dilim 1): araç
// gruplarının KALICI anlatım satırı.
//
// `cosre_conversation.flow` = inline (varsayılan) iken her araç turunun
// (aynı model turunda çağrılan araçlar = bir grup) başında sunucu tek cümle
// yazar: `narration` olayı → v2 `data-narration` parçası (transient DEĞİL:
// mesajın parçası, arşive girer; istemci grubu bu parçadan sonraki araç
// parçalarıyla kurar). Cümle chat_status_line.go havuzundan (aynı evre ailesi,
// aynı seçim kuralı); yeni havuz yok. Ailesiz tur (dış MCP / bilinmeyen ad)
// tek genel cümle alır — grup satırsız kalmasın.
//
// Alışverişin İLK anlatımı ekran bağlamı cümlesini (`context`) taşır: sayfa
// bağlamından deterministik ("<x> servis sayfasındasınız, son 1 saate
// bakıyorsunuz."). Bağlamsız / genel sayfada alan yazılmaz. Araçsız tur
// (selamlaşma) anlatım üretmez, dolayısıyla bağlam satırı da çıkmaz.
//
// inline kipte geçici `status` YAZILMAZ (aynı cümle iki kez görünmesin);
// block kipte akış bayt bayt eski hâl (chat_narration_test.go pinler).
// Model metnine ve istemlere dokunulmaz; v1 yazıcısı olayı düşürür.

const chatNarrationEventKind = "narration"

// chatNarrationFallback — ailesiz araç turunun cümlesi (havuz dışı tek satır).
const chatNarrationFallback = "Araçları çalıştırıyorum…"

// flowInlineOn — v2 VE cosre_conversation.flow = inline.
func flowInlineOn(ctx context.Context) bool {
	return cosreV2Marked(ctx) && chstore.CurrentCosreConversation().Flow != chstore.CosreFlowBlock
}

// chatNarrator — tek alışverişin anlatım durumu (ctx'te; sıfır değer hazır).
type chatNarrator struct {
	context string // henüz yazılmamış ekran bağlamı cümlesi ("" = yok / yazıldı)
	last    string
}

type chatNarratorKey struct{}

// withChatNarrator — alışveriş başına bir anlatıcı; bağlam cümlesi ilk anlatımda.
func withChatNarrator(ctx context.Context, contextLine string) context.Context {
	return context.WithValue(ctx, chatNarratorKey{}, &chatNarrator{context: contextLine})
}

func chatNarratorOf(ctx context.Context) *chatNarrator {
	n, _ := ctx.Value(chatNarratorKey{}).(*chatNarrator)
	return n
}

// chatNarrationPayload — SAF: olay gövdesi {text, context?}.
func chatNarrationPayload(text, contextLine string) map[string]string {
	m := map[string]string{"text": text}
	if contextLine != "" {
		m["context"] = contextLine
	}
	return m
}

// narrate — inline kipte bir `narration` olayı yazar ve true döner; kip kapalıysa
// (block / v1) ya da anlatıcı yoksa hiçbir şey yazmaz, false döner.
func (n *chatNarrator) narrate(ctx context.Context, emit func(string, any), text string) bool {
	if n == nil || !flowInlineOn(ctx) {
		return false
	}
	if text == "" {
		text = chatNarrationFallback
	}
	emit(chatNarrationEventKind, chatNarrationPayload(text, n.context))
	n.context, n.last = "", text
	return true
}

// ── ekran bağlamı cümlesi ──

// pageLabelTR — sayfa kimliği (frontend lib/pageContext.ts PageId) → ad.
var pageLabelTR = map[string]string{
	"trace": "trace", "trace-compare": "trace karşılaştırma", "traces": "Trace'ler",
	"service": "servis", "service-backtrace": "servis geri izleme", "services": "Servisler",
	"problems": "Problem'ler", "anomalies": "Anomaliler", "inbox": "Gelen kutusu",
	"exceptions": "Exception'lar", "errors": "Hatalar", "logs": "Loglar", "explore": "Explore", "metrics": "Metrikler",
	"clusters": "Cluster'lar", "pod": "pod", "entity": "varlık", "hosts": "Host'lar",
	"rollouts": "Rollout'lar", "events": "Olaylar", "endpoints": "Endpoint'ler", "endpoint": "endpoint",
	"databases": "Veritabanları", "database": "veritabanı", "slow-queries": "Yavaş sorgular", "statement": "SQL ifadesi",
	"dashboard": "dashboard", "service-map": "Servis haritası", "topology": "Topoloji", "messaging": "Mesajlaşma",
	"external": "Dış çağrılar", "profiling": "Profiling", "slos": "SLO'lar", "alerts": "Alarmlar", "monitors": "Monitörler",
	"watchers": "İzleyiciler", "incidents": "Olaylar", "incident": "olay", "runbooks": "Runbook'lar", "runbook": "runbook",
	"shift": "Vardiya", "search": "Arama",
}

// pageSubjectTR — SAF: sayfanın öznesi (tür, ad); sıra en dar varlıktan geniş olana.
func pageSubjectTR(p *agentctx.PageContext) (kind, name string) {
	switch {
	case p.Pod != "":
		return "pod", p.Pod
	case p.Workload != "":
		return "workload", p.Workload
	case p.TraceID != "":
		id := p.TraceID
		if r := []rune(id); len(r) > 8 {
			id = string(r[:8]) + "…"
		}
		return "trace", id
	case p.ProblemID != "":
		return "Problem", p.ProblemID
	case p.ExceptionID != "":
		return "exception", p.ExceptionID
	case p.Service != "":
		if p.Operation != "" {
			return "servis", p.Service + " · " + p.Operation
		}
		return "servis", p.Service
	case p.Namespace != "":
		return "namespace", p.Namespace
	case p.Cluster != "":
		return "cluster", p.Cluster
	}
	return "", ""
}

// rangeDativeTR — SAF: zaman aralığı → "son 1 saate" / "… aralığına"; yoksa "".
func rangeDativeTR(r *agentctx.PageRange) string {
	if r == nil {
		return ""
	}
	if r.Preset == "custom" && r.FromMs > 0 && r.ToMs > r.FromMs {
		return fmt.Sprintf("%s → %s UTC aralığına", time.UnixMilli(r.FromMs).UTC().Format("2006-01-02 15:04"), time.UnixMilli(r.ToMs).UTC().Format("2006-01-02 15:04"))
	}
	p := strings.TrimSpace(r.Preset)
	if len(p) < 2 {
		return ""
	}
	n, unit := p[:len(p)-1], p[len(p)-1:]
	if strings.Trim(n, "0123456789") != "" || n == "" {
		return p + " aralığına"
	}
	word := map[string]string{"m": "dakikaya", "h": "saate", "d": "güne", "w": "haftaya"}[unit]
	if word == "" {
		return p + " aralığına"
	}
	return "son " + n + " " + word
}

// pageContextLineTR — SAF: ekran bağlamı cümlesi. Bağlamsız ya da yalnız genel
// sayfa (adı tabloda olmayan: ana sayfa, CoSRE, ayarlar) → "".
func pageContextLineTR(p *agentctx.PageContext) string {
	if p == nil || p.Empty() {
		return ""
	}
	label, ok := pageLabelTR[p.Page]
	if !ok {
		return ""
	}
	kind, name := pageSubjectTR(p)
	var b strings.Builder
	switch {
	case name != "" && kind == label: // öznenin kendi sayfası: "<ad> servis sayfası"
		fmt.Fprintf(&b, "%s %s sayfasındasınız", name, label)
	case name != "":
		fmt.Fprintf(&b, "%s sayfasındasınız (%s: %s)", label, kind, name)
	default:
		fmt.Fprintf(&b, "%s sayfasındasınız", label)
	}
	if rg := rangeDativeTR(p.TimeRange); rg != "" {
		fmt.Fprintf(&b, ", %s bakıyorsunuz", rg)
	}
	b.WriteString(".")
	return b.String()
}
