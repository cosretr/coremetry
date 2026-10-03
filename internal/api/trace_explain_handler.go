package api

// trace_explain_handler.go — v0.10.948 (CoSRE araştırma asistanı, Faz B):
// POST /api/copilot/explain-trace/{id} ("CoSRE'ye sor") handler'ı api.go'dan
// buraya TAŞINDI (aynı ad, aynı rota — ai_routes.go). api.go büyümez kuralı:
// yeni davranış kendi dosyasında, api.go'dan yalnız eksildi.
//
// v0.10.1036 (operatör: "Aslında CoSRE'nin eski explain trace'teki yapısı daha
// iyiydi, neden sonradan değişti. … Eski kanıt toplayıcı güzeldi."; varsayılan
// için: "dönsün") — İKİ yol da KLASİK kanıt toplayıcısından geçer
// (buildTraceExplainInput: trace — Tempo önce, sonra CH — + loglar + Oracle
// satırları; kanıt span'leri sunucuda, traceEvidenceSpanIDs):
//
//   - VARSAYILAN ("CoSRE'ye sor") — explainTraceClassicPrepared:
//     SystemPromptTrace, akan üretim, klasik önbellek anahtarı
//     (explainCacheKey(SystemPromptTrace(), in.User, "")), trace hiçbir yerde
//     yoksa düz metin 404. answer çerçevesi: text, exchangeId,
//     evidenceSpanIds (waterfall kutulaması), code (nil), oracleRows, kimlik
//     köprüsü links (+ isabette cached/cachedAtMs). Adım olayı, `sources`,
//     sayı uyarısı ve "Kaynak durumu" künyesi YOK.
//   - "Kodu da incele" (includeCode) — klasik + kod bağlamı (v0.10.1035,
//     değişmedi): kod çekici loglardaki stacktrace'e ve onun servisine bağlı,
//     bağlam taşmasında kodu yarıya indirip çağrıyı yeniden yapar.
//
// v0.10.1065 (operatör onaylı: "CoSRE'ye sor eskisi gibi") — v0.10.1036'nın
// iki adımlı planının ikinci adımı: uçtan erişilemeyen v0.10.948 trace
// incelemesi SİLİNDİ (trace_investigate.go, inceleme istemi, adım/künye
// mekanizması, ön yüz adım listesi). Kıyas penceresi takip sohbetine taşındı
// (chat_trace_followup.go traceCompareWindow).

import (
	"errors"
	"net/http"

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
// v0.10.948 — varsayılan yol trace incelemesiydi; v0.10.1036 — varsayılan yine
// klasik toplayıcı (dosya başlığı); v0.10.1065 — inceleme silindi.
func (s *Server) copilotExplainTrace(w http.ResponseWriter, r *http.Request) {
	r, xid := withExchange(r)
	// v0.9.831 — "Kodu da incele" (opsiyonel gövde). Kod bağlamı
	// trace'in LOGLARINDAKİ stacktrace'ten çıkar ve o stack'i basan
	// SERVİSİN deposunda aranır (bkz. traceExplainInput.StackService).
	opts := decodeExplainOptions(r)
	if !opts.IncludeCode {
		// v0.10.1036 — operatör: "dönsün". v0.10.948 öncesinin varsayılanı:
		// klasik kanıt + SystemPromptTrace, akan; trace yoksa 404 (Tempo ve CH).
		p, err := s.explainTraceClassicPrepared(r)
		if err != nil {
			writeExplainPrepareErr(w, err)
			return
		}
		s.deliverExplain(w, r, xid, p.extra, p.run, p.service, p.cacheKey)
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
	// v0.10.1044 — kod stack'i basan servisin ÇALIŞAN sürümünden
	// (in.StackVersion; boşsa dal ucu).
	cc := s.buildCodeContext(r.Context(), in.StackService, in.Stack, in.StackVersion)
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

// explainTraceClassicPrepared — klasik kodsuz yol (Tempo önce, sonra CH). Trace
// yoksa errExplainTraceNotFound (çağıran düz metin 404'e çevirir).
//
// v0.10.948 — anahtar klasik prompt'tan (explainCacheKey(SystemPromptTrace…)).
// Sohbetle PAYLAŞILMAZ: sohbetin odaklı yolu anahtarsız (cacheKey ""),
// açıklama isteği ise Explain çekmecesini açar.
//
// v0.10.1036 — VARSAYILAN yol yine bu (copilotExplainTrace'in kodsuz dalı).
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
