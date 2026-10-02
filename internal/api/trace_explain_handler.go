package api

// trace_explain_handler.go — v0.10.948 (CoSRE araştırma asistanı, Faz B):
// POST /api/copilot/explain-trace/{id} ("CoSRE'ye sor") handler'ı api.go'dan
// buraya TAŞINDI (aynı ad, aynı rota — ai_routes.go). api.go büyümez kuralı:
// yeni davranış kendi dosyasında, api.go'dan yalnız eksildi.
//
// İki yol:
//
//   - VARSAYILAN — trace incelemesi (trace_investigate.go): sunucu salt-okunur
//     araçları sohbetin yürütme yolundan çalıştırır, adımları akıtır, cevap
//     SystemPromptTraceInvestigation ile beş başlıkta gelir; sonuna kanıtta
//     bulunamayan sayı uyarısı ve "Kaynak durumu" künyesi eklenir. answer
//     çerçevesi: text, exchangeId, evidenceSpanIds, code, oracleRows, links,
//     sources (+ isabette cached/cachedAtMs).
//   - "Kodu da incele" (includeCode) — KLASİK yol aynen korunur
//     (buildTraceExplainInput + SystemPromptTrace + kod bağlamı): kod çekici
//     loglardaki stacktrace'e ve onun servisine bağlı, bağlam taşmasında kodu
//     yarıya indirip çağrıyı yeniden yapıyor — incelemeyle birleştirmek iki
//     taşma stratejisini çarpıştırırdı. Aynı klasik yol, trace ClickHouse'ta
//     yok ama Tempo'da olabilir durumunda da koşar (get_trace Tempo'ya bakmaz).
//
// Seçili span `?span=<16 hex>` ile gelir (copilotExplainSpan'in sorgu
// sözleşmesi); odak servisi o span'in servisi olur.

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
// v0.10.948 — varsayılan yol trace incelemesi (dosya başlığı); klasik gövde
// yalnız "Kodu da incele" dalında.
func (s *Server) copilotExplainTrace(w http.ResponseWriter, r *http.Request) {
	r, xid := withExchange(r)
	// v0.9.831 — "Kodu da incele" (opsiyonel gövde). Kod bağlamı
	// trace'in LOGLARINDAKİ stacktrace'ten çıkar ve o stack'i basan
	// SERVİSİN deposunda aranır (bkz. traceExplainInput.StackService).
	opts := decodeExplainOptions(r)
	if !opts.IncludeCode {
		s.explainTraceInvestigation(w, r, xid)
		return
	}
	in, err := s.buildTraceExplainInput(r.Context(), r.PathValue("id"))
	if errors.Is(err, errExplainTraceNotFound) {
		http.Error(w, "trace not found", http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	// v0.10.83 — önbellek anahtarı GERÇEK prompt'tan: kod dalında kod
	// bloğu da kimliğe girer (kodlu/kodsuz cevap ayrı satır; blok
	// değişirse anahtar değişir). Kodsuz klasik anahtar:
	// explainTraceClassicPrepared.
	cc := s.buildCodeContext(r.Context(), in.StackService, in.Stack)
	// v0.10.115 — SQL hatasında şema kanıtı (hata span'ının db_statement'ı
	// → katalog); kod bloğunun arkasına, kendi bütçesiyle.
	se := s.buildSchemaEvidence(in.ErrorText, in.DBStatements, mapperBlocks(cc))
	cacheKey := explainCacheKey(copilot.SystemPromptTraceWithCode(), in.User, cc.PromptBlock()+se.Block)
	run := explainPromptBuffered(func() (string, error) {
		return s.copilotExplainEvidence(r,
			copilot.SystemPromptTrace(), copilot.SystemPromptTraceWithCode(), in.User, cc, se)
	})
	// v0.9.1127 (Faz 1.5) — cevabın çıkışı tek yerden (deliverExplain).
	s.deliverExplain(w, r, xid, traceExplainExtra(in, cc, opts.IncludeCode), run, in.RootService, cacheKey)
}

// explainTraceInvestigation — v0.10.948: varsayılan yol. Önbellek anahtarı
// incelemeden ÖNCE (isabet hiçbir okuma çalıştırmaz, adım olayı çıkmaz);
// ıskada inceleme akan kipte adımlarını yayınlar, sonra model akar.
func (s *Server) explainTraceInvestigation(w http.ResponseWriter, r *http.Request, xid string) {
	traceID, spanID := r.PathValue("id"), traceInvestigationSpanParam(r)
	system := copilot.SystemPromptTraceInvestigation()
	key := traceInvestigationCacheKey(system, traceID, spanID)
	runner := newTraceInvestigationRunner(s, r)
	s.deliverExplainPrepared(w, r, xid, key,
		func() (map[string]any, string) {
			m := s.traceInvestigationMetaGet(r.Context(), key)
			return m.frameExtra(), m.Service
		},
		func(emit func(string, any)) (explainPrepared, error) {
			inv, err := s.investigateTrace(withInvestigationRunner(r.Context(), runner), traceID, spanID, emit)
			if errors.Is(err, errTraceInvestigationFallback) {
				return s.explainTraceClassicPrepared(r)
			}
			if err != nil {
				return explainPrepared{}, err
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
func (s *Server) explainTraceClassicPrepared(r *http.Request) (explainPrepared, error) {
	in, err := s.buildTraceExplainInput(r.Context(), r.PathValue("id"))
	if err != nil {
		return explainPrepared{}, err
	}
	return explainPrepared{
		extra:    traceExplainExtra(in, devops.CodeContext{}, false),
		run:      s.explainPrompt(r, copilot.SystemPromptTrace(), in.User),
		service:  in.RootService,
		cacheKey: explainCacheKey(copilot.SystemPromptTrace(), in.User, ""),
	}, nil
}
