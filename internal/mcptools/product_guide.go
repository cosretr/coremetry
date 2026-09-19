package mcptools

// product_guide.go — v0.10.809 (operatör isteği 2026-09-19: "chatten soru soran
// kullanıcılara … yapıcı, bilgilendirici, onboarding'i kolaylaştıran bilgiler").
// CoSRE ürünün NASIL kullanılacağını UYDURMASIN: sayfa haritası ve "şu soruya şu
// yol" adımları burada, sunucuda; model yalnız aktarır. TEK TIK sözleşmesi
// (operatör kararı): bağlantı verilir, sayfa otomatik AÇILMAZ — open_page
// niyeti "aç/götür" denince zaten açar. Guided how_to yolu (api/copilot_howto.go)
// ve serbest döngü (tool) aynı tabloyu okur.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// GuideLink — uygulama-köklü bağlantı; FE "Aç →" çipi olarak çizer (tıklanır).
type GuideLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// GuideTopic — bir "nasıl yaparım" konusu. Steps içindeki {svc} servis adıyla
// (yoksa "servisin"), {trace} trace id ile (yoksa "trace id") doldurulur.
type GuideTopic struct {
	ID       string
	Title    string   // "Hatalı trace'lere ulaşmak"
	Ask      string   // çip metni: "Hatalı trace'lere nasıl ulaşırım?"
	Keywords []string // küçük harf; çok sözcüklü olabilir
	Steps    []string
	Links    func(svc, traceID string) []GuideLink
	Related  []string
}

func svcQ(svc string) string { return url.QueryEscape(svc) }

func svcLink(svc, label, tab string) GuideLink {
	if svc == "" {
		return GuideLink{Label: "Servisler", Href: "/services"}
	}
	href := "/service?name=" + svcQ(svc)
	if tab != "" {
		href += "&tab=" + tab
	}
	return GuideLink{Label: svc + " · " + label, Href: href}
}

var productGuide = []GuideTopic{
	{
		ID: "error_traces", Title: "Hatalı trace'lere ulaşmak", Ask: "Hatalı trace'lere nasıl ulaşırım?",
		Keywords: []string{"hata", "hatalı", "error", "hata oranı", "error rate", "trace", "operation", "operations", "endpoint", "sırala", "en çok", "ulaş", "bul"},
		Steps: []string{
			"Servisler sayfasından {svc} servisini seç.",
			"Servis sayfasında Operations sekmesine geç ve tabloyu Hata oranı sütununa göre sırala; en çok hata veren operation en üstte olur.",
			"Bir operation satırından Traces'e geç — liste o operation'ın hatalı trace'leriyle açılır; bir trace'e tıklayınca şelale ve loglar aynı sayfada.",
			"Bana da sorabilirsin: \"{svc} hatalı trace'ler\" ya da \"{svc} en yavaş trace'ler\".",
		},
		Links: func(svc, _ string) []GuideLink {
			out := []GuideLink{{Label: "Servisler", Href: "/services"}}
			if svc != "" {
				out = append(out, svcLink(svc, "Operations", "operations"),
					GuideLink{Label: "Trace'ler (hatalı)", Href: "/traces?service=" + svcQ(svc) + "&hasError=true"})
			} else {
				out = append(out, GuideLink{Label: "Trace'ler (hatalı)", Href: "/traces?hasError=true"})
			}
			return out
		},
		Related: []string{"slow_traces", "trace_logs"},
	},
	{
		ID: "trace_logs", Title: "Bir trace'in loglarına ulaşmak", Ask: "Bir trace'in loglarını nereden görürüm?",
		Keywords: []string{"log", "loglar", "logs", "trace id", "traceid", "trace'in", "ilişkili", "korelasyon", "span"},
		Steps: []string{
			"Trace sayfasını aç: Traces listesinden tıkla ya da Trace ID kutusuna {trace} yapıştır.",
			"Trace sayfasındaki Logs sekmesi aynı trace id'yle logları getirir; Logs sayfasında da traceId süzgeci var.",
			"Trace id'yi bana yapıştır: trace'i açıklar, loglarını ve olası kök nedeni birlikte okurum.",
		},
		Links: func(_, traceID string) []GuideLink {
			out := []GuideLink{{Label: "Trace'ler", Href: "/traces"}}
			if traceID != "" {
				out = append(out, GuideLink{Label: "Trace", Href: "/trace?id=" + url.QueryEscape(traceID)},
					GuideLink{Label: "Loglar (trace)", Href: "/logs?traceId=" + url.QueryEscape(traceID)})
			} else {
				out = append(out, GuideLink{Label: "Loglar", Href: "/logs"})
			}
			return out
		},
		Related: []string{"error_traces", "logs_search"},
	},
	{
		ID: "slow_traces", Title: "En yavaş trace'leri bulmak", Ask: "En yavaş trace'leri nasıl bulurum?",
		Keywords: []string{"yavaş", "slow", "gecikme", "latency", "p95", "p99", "süre", "duration", "en uzun"},
		Steps: []string{
			"Traces sayfasında {svc} süzgeciyle Süre'ye göre azalan sırala.",
			"Servis sayfasının Operations sekmesindeki p95/p99 sütunları hangi operation'ın yavaşladığını gösterir.",
			"Bana \"{svc} en yavaş trace'ler\" ya da \"{svc} neden yavaş\" diyebilirsin.",
		},
		Links: func(svc, _ string) []GuideLink {
			if svc == "" {
				return []GuideLink{{Label: "Trace'ler (en yavaş)", Href: "/traces?sort=duration&order=desc"}}
			}
			return []GuideLink{{Label: "Trace'ler (en yavaş)", Href: "/traces?service=" + svcQ(svc) + "&sort=duration&order=desc"},
				svcLink(svc, "Operations", "operations")}
		},
		Related: []string{"error_traces", "topology"},
	},
	{
		ID: "problems", Title: "Ne bozuk — problemler ve incident'lar", Ask: "Açık problemleri nereden görürüm?",
		Keywords: []string{"problem", "problems", "alarm", "alert", "inbox", "incident", "bozuk", "ne oldu", "açık", "öncelik"},
		Steps: []string{
			"Inbox tek liste: açık problemler, exception grupları ve incident'lar öncelik (P1→P3) sırasıyla.",
			"Problems sayfası kural bazlı problemleri, Incidents sayfası birleştirilmiş olayları ve zaman çizelgesini gösterir.",
			"Bana \"açık problemler\" ya da \"{svc} sağlığı nasıl\" diyebilirsin.",
		},
		Links: func(svc, _ string) []GuideLink {
			out := []GuideLink{{Label: "Inbox", Href: "/inbox"}}
			if svc != "" {
				out = append(out, GuideLink{Label: "Problemler", Href: "/problems?service=" + svcQ(svc)})
			} else {
				out = append(out, GuideLink{Label: "Problemler", Href: "/problems"})
			}
			return append(out, GuideLink{Label: "Incident'lar", Href: "/incidents"})
		},
		Related: []string{"alerts", "error_traces"},
	},
	{
		ID: "slo", Title: "SLO tanımlamak ve bütçeyi izlemek", Ask: "SLO nasıl tanımlanır?",
		Keywords: []string{"slo", "sli", "bütçe", "budget", "hedef", "burn", "yanma", "kullanılabilirlik", "availability"},
		Steps: []string{
			"SLOs sayfasında servis + SLI türü (kullanılabilirlik / gecikme eşiği) + hedef (ör. %99.9) + pencere (ör. 30 gün) ile tanımla.",
			"Durum, kalan hata bütçesi ve yanma hızı aynı sayfada; servis sayfasının üst şeridi SLO ihlallerini gösterir.",
			"Bana \"{svc} SLO'yu tutuyor mu\" ya da \"bütçe ne zaman biter\" diyebilirsin.",
		},
		Links: func(svc, _ string) []GuideLink {
			out := []GuideLink{{Label: "SLO'lar", Href: "/slos"}}
			if svc != "" {
				out = append(out, svcLink(svc, "Overview", ""))
			}
			return out
		},
		Related: []string{"alerts", "problems"},
	},
	{
		ID: "logs_search", Title: "Log aramak", Ask: "Logları nasıl ararım?",
		Keywords: []string{"log arama", "log search", "kql", "mesaj", "message", "severity", "seviye", "filtre", "filter", "desen", "pattern", "exception"},
		Steps: []string{
			"Logs sayfasında servis, seviye (error = 17) ve serbest metin ya da KQL ile ara; Desenler paneli tekrar eden mesajları gruplar.",
			"Bir alandaki değere göre süzmek için alan=değer yaz (ör. url.full, message); trace id süzgeci de var.",
			"Bana \"{svc} hata logları\" ya da \"message alanında timeout geçen loglar\" diyebilirsin.",
		},
		Links: func(svc, _ string) []GuideLink {
			if svc == "" {
				return []GuideLink{{Label: "Loglar", Href: "/logs"}}
			}
			return []GuideLink{{Label: "Loglar (error)", Href: "/logs?service=" + svcQ(svc) + "&severity=17"}, {Label: "Loglar", Href: "/logs"}}
		},
		Related: []string{"trace_logs", "error_traces"},
	},
	{
		ID: "topology", Title: "Bağımlılıklar ve servis haritası", Ask: "Bir servisin bağımlılıklarını nereden görürüm?",
		Keywords: []string{"bağımlılık", "dependency", "topology", "topoloji", "service map", "servis haritası", "çağıran", "çağırıyor", "çağrılan", "upstream", "downstream", "komşu"},
		Steps: []string{
			"Service Map servisler arası çağrıları hata/gecikme ağırlığıyla çizer; bir düğüme tıklayınca servis sayfasına gidersin.",
			"Servis sayfasının Topology sekmesi yalnız {svc} servisinin komşularını (kimi çağırıyor, kim çağırıyor) gösterir.",
			"Bana \"{svc} kimleri çağırıyor\" ya da \"{svc} ile X arasındaki istekler\" diyebilirsin.",
		},
		Links: func(svc, _ string) []GuideLink {
			out := []GuideLink{{Label: "Service Map", Href: "/service-map"}}
			if svc != "" {
				out = append(out, svcLink(svc, "Topology", "topology"))
			}
			return out
		},
		Related: []string{"slow_traces", "problems"},
	},
	{
		ID: "explore", Title: "Explore ve grafikler", Ask: "Metrik/span sorgusunu nereden yaparım?",
		Keywords: []string{"explore", "sorgu", "query", "metrik", "metric", "dashboard", "grafik", "chart", "panel", "pano"},
		Steps: []string{
			"Explore span ve metrik sorguları için tek yüzey: filtrele, grupla, iki pencereyi karşılaştır.",
			"Dashboards'ta kayıtlı panolar var; Explore'daki bir grafiği panoya ekleyebilirsin.",
			"Bana \"{svc} error_rate grafiği\" diyebilirsin — grafiği gerçek veriden çizerim.",
		},
		Links: func(_, _ string) []GuideLink {
			return []GuideLink{{Label: "Explore", Href: "/explore"}, {Label: "Dashboards", Href: "/dashboards"}}
		},
		Related: []string{"slo", "topology"},
	},
	{
		ID: "alerts", Title: "Alarm kuralı ve bildirim", Ask: "Alarm kuralını nasıl kurarım?",
		Keywords: []string{"alarm kuralı", "alert rule", "kural", "bildirim", "notification", "watcher", "monitor", "slack", "teams", "mail", "e-posta", "sustur"},
		Steps: []string{
			"Alerts sayfasında kural tanımla: metrik/eşik/pencere ya da SLO burn; kural bazında ekip yönlendirmesi kuralın Notify alanında.",
			"Bildirim kanalları (Slack, Teams, e-posta, webhook) Settings → Notifications'ta; susturma Inbox/Problems satırından.",
			"Bana \"hangi kurallar tetiklendi\" ya da \"{svc} için hangi alarmlar var\" diyebilirsin.",
		},
		Links: func(_, _ string) []GuideLink {
			return []GuideLink{{Label: "Alerts", Href: "/alerts"}, {Label: "Bildirim kanalları", Href: "/settings/channels"}}
		},
		Related: []string{"problems", "slo"},
	},
	{
		ID: "pods", Title: "Pod ve cluster sağlığı", Ask: "Pod'ları nereden görürüm?",
		Keywords: []string{"pod", "pods", "cluster", "kubernetes", "k8s", "restart", "cpu", "bellek", "memory", "namespace", "workload", "oom"},
		Steps: []string{
			"Clusters sayfası cluster → namespace → workload → pod ağacı; Pod sayfası restart, CPU ve bellek eğrilerini gösterir.",
			"Servis sayfasının Pods sekmesi {svc} servisinin pod'larını cluster'a göre listeler.",
			"Bana \"{svc} pod'ları nasıl\" ya da \"shop namespace'indeki servisler\" diyebilirsin.",
		},
		Links: func(svc, _ string) []GuideLink {
			out := []GuideLink{{Label: "Clusters", Href: "/clusters"}}
			if svc != "" {
				out = append(out, svcLink(svc, "Pods", "pods"))
			}
			return out
		},
		Related: []string{"problems", "deploys"},
	},
	{
		ID: "deploys", Title: "Deploy ve rollout etkisi", Ask: "Son deploy'un etkisini nereden görürüm?",
		Keywords: []string{"deploy", "rollout", "sürüm", "versiyon", "version", "release", "değişiklik", "change", "yayın"},
		Steps: []string{
			"Rollouts sayfası deploy/rollout olaylarını ve öncesi-sonrası etkisini gösterir; servis sayfası grafiklerinde deploy işaretçileri var.",
			"Bana \"{svc} son deploy etkisi\" diyebilirsin — öncesi/sonrası RED'i karşılaştırırım.",
		},
		Links: func(svc, _ string) []GuideLink {
			out := []GuideLink{{Label: "Rollouts", Href: "/rollouts"}}
			if svc != "" {
				out = append(out, svcLink(svc, "Overview", ""))
			}
			return out
		},
		Related: []string{"problems", "error_traces"},
	},
	{
		ID: "ask_cosre", Title: "CoSRE'ye nasıl soru sorulur", Ask: "Sana nasıl soru sorabilirim?",
		Keywords: []string{"cosre", "asistan", "neler yapabilirsin", "nasıl sorarım", "nasıl soru", "ne sorabilirim", "yardım", "help"},
		Steps: []string{
			"Servis adı + soru ver: \"{svc} sağlığı nasıl\", \"{svc} neden yavaş\", \"{svc} hata logları\", \"{svc} son deploy etkisi\".",
			"32 haneli trace id ya da 16 haneli span id yapıştır: onu açıklar, loglarıyla birlikte okurum.",
			"Bir servis/sayfa açıkken sormak bağlamı otomatik verir; \"sabitle\" ile bağlamı kilitleyebilirsin. Yol tarifi için \"… nasıl bakarım / nereden görürüm\" de.",
		},
		Links: func(_, _ string) []GuideLink {
			return []GuideLink{{Label: "Servisler", Href: "/services"}, {Label: "Trace'ler", Href: "/traces"}, {Label: "Inbox", Href: "/inbox"}}
		},
		Related: []string{"error_traces", "trace_logs", "problems"},
	},
}

// GuideTopics — salt-okunur kopya (sıra sabit).
func GuideTopics() []GuideTopic { return append([]GuideTopic(nil), productGuide...) }

func guideTopicByID(id string) (GuideTopic, bool) {
	for _, t := range productGuide {
		if t.ID == id {
			return t, true
		}
	}
	return GuideTopic{}, false
}

// MatchGuideTopics — SAF: soruda geçen anahtar sayısına göre sıralı konular
// (eşitlikte tablo sırası); hiç eşleşme yoksa nil. Küçük harf alt-dize eşleşmesi
// (Türkçe ekler: "trace'lerine" → "trace" eşleşir).
func MatchGuideTopics(question string) []GuideTopic {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return nil
	}
	type scored struct {
		t     GuideTopic
		score int
		order int
	}
	var hits []scored
	for i, t := range productGuide {
		n := 0
		for _, k := range t.Keywords {
			if strings.Contains(q, k) {
				n++
			}
		}
		if n > 0 {
			hits = append(hits, scored{t, n, i})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].order < hits[j].order
	})
	if len(hits) == 0 {
		return nil // boş dilim değil nil: "eşleşme yok" sözleşmesi
	}
	out := make([]GuideTopic, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.t)
	}
	return out
}

// GuideAnswer — model/FE'ye giden şekil.
type GuideAnswer struct {
	Topic   string      `json:"topic"`
	Title   string      `json:"title"`
	Steps   []string    `json:"steps"`
	Links   []GuideLink `json:"links"`
	Related []string    `json:"related,omitempty"`
}

// GuideFor — SAF: topicID doluysa o konu, yoksa soruya en iyi eşleşme;
// yer tutucular doldurulur. Bulunamazsa ok=false.
func GuideFor(topicID, question, service, traceID string) (GuideAnswer, bool) {
	var t GuideTopic
	var ok bool
	if topicID != "" {
		t, ok = guideTopicByID(strings.ToLower(strings.TrimSpace(topicID)))
	} else if m := MatchGuideTopics(question); len(m) > 0 {
		t, ok = m[0], true
	}
	if !ok {
		return GuideAnswer{}, false
	}
	svcText, trText := "servisin", "trace id"
	if service != "" {
		svcText = service
	}
	if traceID != "" {
		trText = traceID
	}
	steps := make([]string, 0, len(t.Steps))
	for _, st := range t.Steps {
		steps = append(steps, strings.NewReplacer("{svc}", svcText, "{trace}", trText).Replace(st))
	}
	return GuideAnswer{Topic: t.ID, Title: t.Title, Steps: steps, Links: t.Links(service, traceID), Related: t.Related}, true
}

// RenderGuideTR — SAF: sohbet cevabı / prompt kanıtı metni.
func RenderGuideTR(a GuideAnswer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "NASIL: %s\n", a.Title)
	for i, st := range a.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, st)
	}
	if len(a.Links) > 0 {
		b.WriteString("Bağlantılar: ")
		for i, l := range a.Links {
			if i > 0 {
				b.WriteString(" · ")
			}
			b.WriteString(l.Label)
		}
		b.WriteString(" (tıklayınca açılır; sayfa kendiliğinden değişmez).\n")
	}
	return b.String()
}

// GuideIndexTR — konu bulunamayınca liste (çipler Ask metinleridir).
func GuideIndexTR() string {
	var b strings.Builder
	b.WriteString("Hangi konuda yol tarif edeyim?\n")
	for _, t := range productGuide {
		fmt.Fprintf(&b, "- %s\n", t.Ask)
	}
	return b.String()
}

type productGuideArgs struct {
	Topic    string `json:"topic,omitempty"`
	Question string `json:"question,omitempty"`
	Service  string `json:"service,omitempty"`
	TraceID  string `json:"trace_id,omitempty"`
}

func productGuideTool(_ Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "product_guide",
		ShortDescription: "Ürün yol tarifi (nasıl/nereden): adımlar + bağlantı; sayfayı açma.",
		Description: "Coremetry product guide for 'how do I…' / 'where do I see…' questions. Returns the server-owned " +
			"step list (Turkish) and app-relative links for a topic: error_traces, trace_logs, slow_traces, problems, slo, " +
			"logs_search, topology, explore, alerts, pods, deploys, ask_cosre. Pass `question` for keyword matching or " +
			"`topic` for an exact id; `service` / `trace_id` fill placeholders and links. No match → `topics` index. " +
			"Relay the steps and offer the links; never navigate the operator yourself and never invent pages.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic":    map[string]any{"type": "string", "description": "Exact topic id (see description). Empty = match by question."},
				"question": map[string]any{"type": "string", "description": "The operator's question, verbatim."},
				"service":  map[string]any{"type": "string", "description": "Service name to fill into steps and links (optional)."},
				"trace_id": map[string]any{"type": "string", "description": "Trace id to fill into trace/log links (optional)."},
			},
		},
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var a productGuideArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			ans, ok := GuideFor(a.Topic, a.Question, a.Service, a.TraceID)
			if !ok {
				idx := make([]map[string]string, 0, len(productGuide))
				for _, t := range productGuide {
					idx = append(idx, map[string]string{"topic": t.ID, "title": t.Title, "ask": t.Ask})
				}
				return map[string]any{"found": false, "topics": idx, "text": GuideIndexTR()}, nil
			}
			return map[string]any{"found": true, "guide": ans, "text": RenderGuideTR(ans)}, nil
		},
	}
}
