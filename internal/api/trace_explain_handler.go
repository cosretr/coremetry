package api

// trace_explain_handler.go — v0.10.948 (CoSRE araştırma asistanı, Faz B):
// POST /api/copilot/explain-trace/{id} ("CoSRE'ye sor") handler'ı api.go'dan
// buraya TAŞINDI (aynı ad, aynı rota — ai_routes.go). api.go büyümez kuralı:
// yeni davranış kendi dosyasında, api.go'dan yalnız eksildi.
//
// v0.10.1034 (operatör: "Kod inceleme çalışma mantığı ile direkt Ask CoSRE
// farklı.") — TEK kanıt hattı. "Kodu da incele" artık ayrı bir toplayıcı
// değil, Ask CoSRE incelemesinin üstüne kod:
//
//   - KODSUZ (varsayılan) — trace incelemesi (trace_investigate.go): sunucu
//     salt-okunur araçları sohbetin yürütme yolundan çalıştırır, adımları
//     akıtır, cevap SystemPromptTraceInvestigation ile gelir ve AKAR; sonuna
//     kanıtta bulunamayan sayı uyarısı ve "Kaynak durumu" künyesi eklenir.
//   - "Kodu da incele" (includeCode) — AYNI inceleme (aynı okumalar, aynı
//     adımlar, aynı seçili-span odağı), ardından kod + şema kanıtı
//     (trace_investigate_code.go): stack + onu basan servis incelemenin log
//     okumasının ham kayıtlarından (seçili span'in logunda stack yoksa TEK ek
//     trace geneli okuma — adım olarak görünür, inceleme kanıtına girmez, kökeni
//     söylenir), hata metni + SQL get_trace'in span listesinden; kod çekimi de
//     bir adım. Model SystemPromptTraceInvestigationWithCode ile BUFFERED
//     çağrılır (copilotExplainEvidence: bağlam taşmasında kod yarıya iner ya da
//     düşer); kuyruk aynı. Eski çekince ("incelemeyle birleştirmek iki taşma
//     stratejisini çarpıştırırdı") karşılandı: akan yol ile yarıya-indirme
//     zinciri aynı istekte hiç birlikte koşmaz.
//
// İkisinde de answer çerçevesi: text, exchangeId, evidenceSpanIds, code
// (kodluda depo/dosya:satır künyesi, kodsuzda null), oracleRows, links,
// sources (+ isabette cached/cachedAtMs). Önbellek anahtarı okumalardan ÖNCE
// (traceInvestigationCacheKey; sistem istemi anahtarda → kodlu/kodsuz ayrı
// satır). KLASİK yol (buildTraceExplainInput + SystemPromptTrace[WithCode])
// YALNIZ Tempo yedeği: trace ClickHouse'ta yok ama Tempo'da olabilir
// (get_trace Tempo'ya bakmaz) — kodlu da kodsuz da bugünkü gibi.
//
// Seçili span `?span=<16 hex>` ile gelir (copilotExplainSpan'in sorgu
// sözleşmesi); odak servisi o span'in servisi olur — kodlu istekte de.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
)

// copilotExplainTrace fetches the spans for a trace, builds a compact
// JSON description, and asks the model for an SRE-flavoured summary.
// Heavy lifting (gathering context) happens server-side so the
// browser doesn't ship trace data back to the API just to ship it on
// to Anthropic.
//
// v0.9.482 — kanıt montajı (span seçimi + ilişkili loglar + dürüstlük
// notu) buildTraceExplainInput'a taşındı: AI çekmecesindeki sohbet AYNI
// paketi kurabilsin (operatör raporu: "logda ne yazıyor" takipleri kör
// cevaplanıyordu). Prompt bayt-bayt aynıdır — explain_trace_input_test.go
// pinler. Emsal: anomaly.BuildExceptionExplainInput (v0.9.415).
//
// v0.10.948 — varsayılan yol trace incelemesi (dosya başlığı).
// v0.10.1034 — "Kodu da incele" de aynı incelemeden geçer (+ kod); klasik
// gövde yalnız Tempo yedeğinde (explainTraceClassicPrepared).
func (s *Server) copilotExplainTrace(w http.ResponseWriter, r *http.Request) {
	r, xid := withExchange(r)
	// v0.9.831 — "Kodu da incele" (opsiyonel gövde). Kod bağlamı
	// trace'in LOGLARINDAKİ stacktrace'ten çıkar ve o stack'i basan
	// SERVİSİN deposunda aranır (bkz. traceExplainInput.StackService).
	opts := decodeExplainOptions(r)
	s.explainTraceInvestigation(w, r, xid, opts.IncludeCode)
}

// explainTraceInvestigation — v0.10.948: varsayılan yol. Önbellek anahtarı
// incelemeden ÖNCE (isabet hiçbir okuma çalıştırmaz, adım olayı çıkmaz);
// ıskada inceleme akan kipte adımlarını yayınlar, sonra model akar.
//
// v0.10.1034 — includeCode: aynı inceleme, ardından kod (buffered üretim,
// trace_investigate_code.go). Anahtar kodlu sistem istemiyle (ayrı satır);
// isabette ne okuma ne kod çekimi koşar, künye yan kayıttan gelir.
func (s *Server) explainTraceInvestigation(w http.ResponseWriter, r *http.Request, xid string, includeCode bool) {
	traceID, spanID := r.PathValue("id"), traceInvestigationSpanParam(r)
	system := copilot.SystemPromptTraceInvestigation()
	if includeCode {
		system = copilot.SystemPromptTraceInvestigationWithCode()
	}
	key := traceInvestigationCacheKey(system, traceID, spanID)
	runner := newTraceInvestigationRunner(s, r)
	s.deliverExplainPrepared(w, r, xid, key,
		func() (map[string]any, string) {
			m := s.traceInvestigationMetaGet(r.Context(), key)
			return m.frameExtra(), m.Service
		},
		func(emit func(string, any)) (explainPrepared, error) {
			ictx := withInvestigationRunner(r.Context(), runner)
			if includeCode {
				ictx = withInvestigationCodeInputs(ictx) // L okuması ham-kayıt kancasıyla (yalnız kodlu istekte)
			}
			inv, err := s.investigateTrace(ictx, traceID, spanID, emit)
			if errors.Is(err, errTraceInvestigationFallback) {
				return s.explainTraceClassicPrepared(r, includeCode)
			}
			if err != nil {
				return explainPrepared{}, err
			}
			if includeCode {
				return s.traceInvestigationCodePrepared(r, runner, emit, inv, key), nil
			}
			return s.traceInvestigationPrepared(r, system, inv, key), nil
		})
}

// traceInvestigationPrepared — incelemenin üretim yarısı: model akar, ardından
// sunucunun kuyruğu (sayı uyarısı + Kaynak durumu) answer metnine biner.
// v0.10.948 — kuyruk YALNIZ model gerçekten aktıysa son delta olur (deltalar
// answer.text'e toplanır); akıyamayan uçta (buffered düşüş, GitHub) delta hiç
// yok, metnin tamamı — kuyruk dahil — yalnız answer.text'te (sıfır-delta
// sözleşmesi: answer çerçevesi her zaman asıl kaynak). Geçici kaynak arızası
// ya da oturmamış trace varsa cevap saklanmaz (cacheable).
func (s *Server) traceInvestigationPrepared(r *http.Request, system string, inv *traceInvestigation, key string) explainPrepared {
	run := invAnswerWithTail(s.explainPrompt(r, system, inv.User), inv)
	meta := inv.cacheMeta()
	p := explainPrepared{extra: meta.frameExtra(), run: run, service: inv.RootService}
	if inv.cacheable(time.Now()) {
		p.cacheKey = key
		p.onStore = func(ctx context.Context) { s.traceInvestigationMetaSet(ctx, key, meta) }
	}
	return p
}

// invAnswerWithTail — v0.10.948: üretim yarısını sunucu kuyruğuyla sarar (SAF
// dikiş; boş-cevap sözleşmesi base'siz test edilir).
func invAnswerWithTail(base explainRun, inv *traceInvestigation) explainRun {
	return func(onDelta func(string)) (string, error) {
		// v0.10.948 — model gerçekten aktı mı (buffered düşüşte sıfır delta).
		// Düz bool yeter: StreamText onDelta'yı base dönmeden, eşzamanlı çağırır.
		streamed := false
		wrapped := onDelta
		if onDelta != nil {
			wrapped = func(d string) {
				if d != "" {
					streamed = true
				}
				onDelta(d)
			}
		}
		out, err := base(wrapped)
		if err != nil {
			return out, err
		}
		// v0.10.948 — model boş döndüyse kuyruk eklenmez: boş cevap boş kalır ki
		// explainCacheSet saklamasın ve FE "Model boş yanıt" düşüşünü göstersin.
		if strings.TrimSpace(out) == "" {
			return "", nil
		}
		tail := inv.answerTail(out)
		if streamed {
			onDelta(tail)
		}
		return out + tail, nil
	}
}

// explainTraceClassicPrepared — trace ClickHouse'ta yok, Tempo yapılandırılmış:
// klasik yol (Tempo önce, sonra CH). Trace yoksa 404 aynen.
//
// v0.10.948 — anahtar klasik prompt'tan (explainCacheKey(SystemPromptTrace…));
// deliverExplainPrepared hazırlıktan SONRA bu anahtara da bakar (Tempo'daki
// trace'e her tıklama LLM'e gitmesin). Sohbetle PAYLAŞILMAZ: sohbetin odaklı
// yolu anahtarsız (cacheKey ""), açıklama isteği ise Explain çekmecesini açar
// (varsayılan yol: trace incelemesi, traceInvestigationCacheKey).
//
// v0.10.1034 — includeCode: Tempo yedeğinde "Kodu da incele"nin ESKİ klasik
// gövdesi aynen (explainTraceClassicCodePrepared); artık ana yol değil.
func (s *Server) explainTraceClassicPrepared(r *http.Request, includeCode bool) (explainPrepared, error) {
	in, err := s.buildTraceExplainInput(r.Context(), r.PathValue("id"))
	if err != nil {
		return explainPrepared{}, err
	}
	if includeCode {
		return s.explainTraceClassicCodePrepared(r, in), nil
	}
	return explainPrepared{
		extra:    traceExplainExtra(in, devops.CodeContext{}, false),
		run:      s.explainPrompt(r, copilot.SystemPromptTrace(), in.User),
		service:  in.RootService,
		cacheKey: explainCacheKey(copilot.SystemPromptTrace(), in.User, ""),
	}, nil
}

// explainTraceClassicCodePrepared — v0.10.1034: v0.9.831'den beri "Kodu da
// incele"nin klasik gövdesi, BAYT BAYT eskisi (yalnız handler'dan taşındı):
// kod bağlamı klasik toplayıcının stack'inden, şema kanıtı (v0.10.115), anahtar
// GERÇEK prompt'tan (v0.10.83 — kod bloğu kimliğe girer; deliverExplainPrepared
// hazırlıktan sonra bu anahtara da bakar), buffered üretim + taşma zinciri.
// Yalnız Tempo yedeğinde koşar (trace ClickHouse'ta yok).
func (s *Server) explainTraceClassicCodePrepared(r *http.Request, in traceExplainInput) explainPrepared {
	cc := s.buildCodeContext(r.Context(), in.StackService, in.Stack)
	se := s.buildSchemaEvidence(in.ErrorText, in.DBStatements, mapperBlocks(cc))
	return explainPrepared{
		extra: traceExplainExtra(in, cc, true),
		run: explainPromptBuffered(func() (string, error) {
			return s.copilotExplainEvidence(r,
				copilot.SystemPromptTrace(), copilot.SystemPromptTraceWithCode(), in.User, cc, se)
		}),
		service:  in.RootService,
		cacheKey: explainCacheKey(copilot.SystemPromptTraceWithCode(), in.User, cc.PromptBlock()+se.Block),
	}
}
