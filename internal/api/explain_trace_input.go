package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/promptfmt"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// explain_trace_input.go — trace explain'in KANIT paketi kurucusu
// (v0.9.482, operatör raporu).
//
// Semptom (prod fotoğrafı, v0.9.479 takibi): "Explain trace" hem trace'i
// hem ilişkili LOGLARI okuyup cevaplıyor; ama "Chat'te devam et"ten sonra
// çekmece sohbeti yalnız açıklamanın METNİNİ görüyordu. "logda ne yazıyor",
// "BKMQR-39 neden" gibi takipler ham kanıta erişemediği için kör cevap
// alıyordu.
//
// Çözüm: kanıt montajı handler'dan çıkarıldı; AYNI kurucu hem
// copilotExplainTrace hem copilotChatDrawer tarafından kullanılır — emsal
// anomaly.BuildExceptionExplainInput (v0.9.415, exception hattında aynı
// "tek kurucu, iki çağıran" kararı).
//
// Sözleşme: handler'ın ürettiği prompt BAYT-BAYT eskisiyle aynıdır
// (explain_trace_input_test.go bunu pinler) — bu bir davranış değişikliği
// değil, çıkarma işlemidir.

// errExplainTraceNotFound — trace'in span'i yok. Handler 404'e çevirir;
// çekmece yolunda sessizce kanıtsız devam edilir (soft-fail).
var errExplainTraceNotFound = errors.New("trace not found")

// traceLite — modele giden kompakt span. Tam attribute haritaları büyük
// trace'lerde prompt'u patlatır; kıdemli bir mühendisin waterfall'da
// bakacağı alanlar kalır. (v0.9.462'de handler içindeki anonim `lite`
// tipiydi; v0.9.482'de paket düzeyine çıktı — kanıt seçimi saf ve
// tablo-testli olabilsin.)
type traceLite struct {
	Name       string  `json:"name"`
	Service    string  `json:"service"`
	Kind       string  `json:"kind"`
	ParentSpan string  `json:"parent,omitempty"`
	SpanID     string  `json:"id"`
	DurationMs float64 `json:"durMs"`
	Status     string  `json:"status,omitempty"`
	StatusMsg  string  `json:"statusMsg,omitempty"`
	// DBSystem / DBStatement (v0.10.115) — yalnız HATA span'larında: SQL
	// hatasında çalışan ifade kanıtın kendisidir (şema hedefi buradan
	// çıkar). Başarılı span'larda taşınmaz — prompt bütçesi.
	DBSystem    string `json:"dbSystem,omitempty"`
	DBStatement string `json:"dbStatement,omitempty"`
	// Mark (v0.10.1149) — yalnız span explain'in trace listesinde seçili
	// span'de "◀ seçili"; trace explain'de hep boş (prompt bayt-bayt aynı).
	Mark string `json:"mark,omitempty"`
}

// traceLiteOf — SAF: span → kompakt satır (v0.10.1149'da döngüden
// çıkarıldı; trace explain + span explain'in trace bağlamı aynı biçim).
func traceLiteOf(sp chstore.SpanRow) traceLite {
	return traceLiteOfScaled(sp, 1)
}

// traceLiteOfScaled — v0.10.2378: hata span'inin SQL ifadesi tavanı kanıt
// ölçeğiyle (evidence_scale.go); ölçek 1 = 600, bayt bayt eski.
func traceLiteOfScaled(sp chstore.SpanRow, scale int) traceLite {
	l := traceLite{Name: sp.Name, Service: sp.ServiceName, Kind: sp.Kind,
		ParentSpan: sp.ParentSpanID, SpanID: sp.SpanID, DurationMs: float64(sp.EndTime-sp.StartTime) / 1e6}
	if sp.StatusCode == "error" {
		l.Status = "error"
		l.StatusMsg = sp.StatusMessage
		if sp.DBStatement != "" {
			l.DBSystem = sp.DBSystem
			l.DBStatement = truncRunesN(sp.DBStatement, scaleCap(traceLiteDBStatementRunes, scale))
		}
	}
	return l
}

// Trace explain girdisinin taban tavanları (v0.10.2378'de adlandırıldı;
// değerler aynı). Kanıt ölçeğiyle çarpılır (evidence_scale.go).
const (
	traceLiteDBStatementRunes = 600  // hata span'inin SQL ifadesi
	traceExplainLogFetch      = 250  // v0.10.2456 — trace_id ile okunan satır (30 → 250; pivotLogLimit ≤500)
	traceExplainLogLines      = 30   // v0.10.2456 — prompt'a giren ayrık satır/desen (15 → 30; desen ×adet)
	traceExplainLogBodyBytes  = 600  // log gövdesi
	traceExplainLogStackFirst = 1500 // ilk stack'li logun stack'i
	traceExplainLogStackRest  = 900  // sonraki logların stack'i
	// traceExplainLogsBlockBytes — LOGLAR bloğunun TOPLAM baytı. v0.10.2456:
	// her ölçekte uygulanır (× ölçek); gösterilmeyenler özet satırında.
	traceExplainLogsBlockBytes = 24000
)

// traceRootSpan — kök: parent'ı olmayan span; yoksa en erken başlayan
// (kırık/eksik parent zincirinde de bir cevap üretmeli). spans boş olmamalı.
func traceRootSpan(spans []chstore.SpanRow) chstore.SpanRow {
	root := spans[0]
	for _, sp := range spans {
		if sp.ParentSpanID == "" {
			return sp
		}
		if sp.StartTime < root.StartTime {
			root = sp
		}
	}
	return root
}

// traceExplainInput — kurulan girdi + deterministik kanıt.
type traceExplainInput struct {
	User     string   // explain/narration user prompt'unun gövdesi
	Evidence []string // kanıt span id'leri (waterfall kutulaması)
	// LogsBlock — User'ın İÇİNDEKİ log bölümü, ayrıca taşınır: çekmece
	// sohbetinde bütçe aşılırsa span listesi budanır, LOGLAR KORUNUR
	// (operatörün takip soruları log içeriğine dair — clampDrawerEvidence).
	LogsBlock string
	// Stack / StackService (v0.9.831) — "Kodu da incele" yolunun girdisi:
	// bu trace'in loglarındaki İLK exception.stacktrace ve onu basan
	// servis. Boş = trace'te stacktrace yok (kod bağlamı atlanır).
	//
	// Servis ayrıca taşınıyor çünkü depo çözümü SERVİS adından yapılıyor
	// ve stack'i basan servis, trace'in kök servisi olmak zorunda değil:
	// aşağı akıştaki bir servis patlar, kök yalnız 500'ü görür. Kökün
	// deposunda o dosya YOKTUR — yanlış depoda arama, sessiz bir
	// "eşleşme yok" olurdu.
	Stack string
	// RootService (v0.10.55) — trace'in KÖK span'ini yayan servis.
	//
	// Kimlik köprüsünün ORTAMINI bundan çıkarıyoruz
	// (envFromServiceName). Öncesinde deliverExplain yalnız `?service=`
	// query param'ına bakıyordu ve ✨ Explain uçlarının hiçbiri onu
	// göndermiyor: env HER ZAMAN "prod"a düşüyordu. Prod'da doğru sonuç
	// verdiği için görünmezdi; prod-dışı bir trace açıklandığında link
	// yanlış ortamın log sistemine gidiyordu.
	//
	// StackService DEĞİL: o, stacktrace'i BASAN servis ve trace'te
	// stacktrace olmayabilir. Kök servis her trace'te var.
	RootService  string
	StackService string
	// StackVersion (v0.10.1044, operatör: "Kod, dalın ucundan değil çalışan
	// sürümden okunsun") — stack'i taşıyan logun çalışan sürümü
	// (anomaly.StackVersion: logun kendi span'i → logun resource'u →
	// StackService'in bu trace'teki çoğunluğu). Ek okuma YOK — span'ler ve
	// log zaten elde. Boş = bilinmiyor → kod dal ucundan (bugünkü davranış).
	StackVersion string
	// DBStatements / ErrorText (v0.10.115) — hata span'larının SQL
	// ifadeleri (≤3, tekil) ve hata metni (status mesajları + stack başı):
	// şema kanıtının girdisi (api/schema_catalog.go buildSchemaEvidence).
	DBStatements []string
	ErrorText    string
	// OracleRows — v0.10.921 (Kademe A): prompt'a giren Oracle hata satırı
	// sayısı (UI kanıt satırı için); 0 = Oracle kapalı / satır yok.
	OracleRows int
	// Logs — v0.10.1742: trace log okumasının sonucu (UI Kanıt satırı için;
	// prompt'a girmez): rows (N satır) | none (0 satır) | failed (+ neden).
	Logs traceLogOutcome
	// MainOperation / OtherOperations / OperationSource — v0.10.1329
	// (operatör-bildirimli, dikiş): trace'in ana operasyonu KESİN kaynaktan
	// (pickMainOperation: bu trace'in Oracle satırları → span attribute'u;
	// fonksiyon kodu sözlüğü YOK). Source "oracle" | "span" | "" (bilinmiyor).
	// Bu sürümde prompt'a GİRMEZ — golden bayt-bayt aynı.
	MainOperation   string
	OtherOperations []string
	OperationSource string
	// RootVersion — v0.10.1329 (dikiş): kök servisin bu trace'teki çalışan
	// sürümü (anomaly.StackServiceVersion; ek okuma yok). Boş = bilinmiyor.
	RootVersion string
	// FunctionCodes — v0.10.1349 (operatör-bildirimli): trace'in span'lerindeki
	// tekil FUNCTION_CODE değerleri, kanıt sırasıyla (kök → giriş → diğer),
	// ≤5 (traceFunctionCodes). Ana operasyon KESİN kaynaktan bulunamazsa kod
	// incelemesi sözlükten ADAY operasyon arar; prompt'a GİRMEZ.
	FunctionCodes []string
	// ErrorOrigin — v0.10.1371 (operatör-bildirimli): deterministik HATA
	// ZİNCİRİ'nin kaynak span'i (en derin hatalı yaprak); "" = trace'te hata
	// yok. Kanıt listesinin (Evidence) BAŞINDA — waterfall önce onu kutular.
	ErrorOrigin string
	// ChainBlock — v0.10.1572 (chat_clarify_synth.go): User'ın başındaki HATA
	// ZİNCİRİ bloğunun kendisi ("" = trace'te hata yok). Oracle + trace sentezi
	// zinciri ayrı bloğa koyar; prompt'a buradan EK bir şey girmez.
	ChainBlock string
	// CriticalBlock / TopStep / TopExternalWait — v0.10.1627 (CoSRE kritik yol
	// planı R2): User'ın başındaki KRİTİK YOL bloğu (HATA ZİNCİRİ'nin ardından,
	// span JSON'undan önce), en büyük paylı adım ve o adımın dış bağımlılık
	// beklemesi olup olmadığı. Tek adımlı trace'te blok yok (TopStep nil) —
	// kanıt bayt bayt eskisi gibi.
	CriticalBlock   string
	TopStep         *tracechain.PathStep
	TopExternalWait bool
	// View — v0.10.2480 (trace_explain_evidence.go): anlatım biçiminde UI'ın
	// çizdiği sunucu-hesaplı kanıt (modele girmez; User iki biçimde aynı).
	View *traceEvidenceView
}

// traceExplainInputSeam — TEST DİKİŞİ (prod'da nil): trace kanıt paketi.
// v0.10.1572 — trace sentezi store'suz testte bu dikişten okur.
var traceExplainInputSeam func(ctx context.Context, id string) (traceExplainInput, error)

// buildTraceExplainInput — trace'in span'lerini çekip kompakt JSON'a
// indirger, ilişkili logları (varsa) ekler ve deterministik kanıt
// span'lerini hesaplar. Log sorgusu SADECE bu çağrı içinde, TEK pass;
// hata/boşluk durumunda sessizce trace-only'e düşer (v0.9.166 maliyet
// disiplini: proaktif/poll YOK).
func (s *Server) buildTraceExplainInput(ctx context.Context, id string) (traceExplainInput, error) {
	return s.buildTraceExplainInputNear(ctx, id, time.Time{})
}

// errExplainTraceAgedOut — v0.10.2201: MV trace'i biliyor ama ham span'ler
// saklama süresini aşmış (sayfadaki "aged out" kartının aynısı).
var errExplainTraceAgedOut = fmt.Errorf("%w: raw spans aged out", errExplainTraceNotFound)

// buildTraceExplainInputNear — v0.10.2201: `hint` (sıfır = yok) istemcinin
// bildiği trace başlangıcı; CH pencere aramasının İLK probe'u
// (GetTraceNear ±1 sa), ıskada kademeli arama sürer.
func (s *Server) buildTraceExplainInputNear(ctx context.Context, id string, hint time.Time) (traceExplainInput, error) {
	if traceExplainInputSeam != nil {
		return traceExplainInputSeam(ctx, id)
	}
	// v0.9.632 — operator-reported: Tempo fallback trace'i bulup
	// waterfall'ı çizerken explain "trace not found" diyordu. Burası
	// doğrudan s.store.GetTrace çağırıyordu; /api/traces/{id} ise CH
	// ıskalayınca Tempo'ya düşüyordu. Tek kural, tek yer:
	// resolveTraceSpans (trace_resolve.go).
	//
	// v0.10.2201 (operator-reported) — sayfa ile AYNI çözüm
	// (resolveTraceDetail): Distributed okuma 0 span döndüğünde sayfa tüm
	// replikalardan okuyup trace'i gösteriyor, açıklama "bulunamadı"
	// diyordu.
	res, err := s.resolveTraceDetail(ctx, id, hint)
	if err != nil {
		return traceExplainInput{}, err
	}
	spans := res.Spans
	if len(spans) == 0 {
		if res.StubReason == traceStubAgedOut {
			return traceExplainInput{}, errExplainTraceAgedOut
		}
		return traceExplainInput{}, errExplainTraceNotFound
	}
	// Trace penceresi (log sorgusu için) — cap'ten ÖNCE, tüm span'lar üstünden.
	// Kök servis: parent'ı olmayan span; yoksa en erken başlayan
	// (kırık/eksik parent zincirinde de bir cevap üretmeli).
	rootService := traceRootSpan(spans).ServiceName

	minT, maxT := spans[0].StartTime, spans[0].EndTime
	for _, sp := range spans {
		if sp.StartTime < minT {
			minT = sp.StartTime
		}
		if sp.EndTime > maxT {
			maxT = sp.EndTime
		}
	}
	// v0.9.462 (dürüstlük A6) — prompt tavanı 100 span'de kalır ama seçim
	// artık HEAD dilimi değil: kronolojik ilk-100, hatalı ve en yavaş
	// span'lar 100'ün dışındaysa modele HİÇ ulaşmıyordu — "en yavaş span"
	// kanıtı waterfall'da yanlış span'ı kutulayabiliyordu. pickExplainSpans
	// hataları + en yavaşları garantiler, kalanı kronolojik doldurur ve
	// seçimi zaman sırasına geri koyar (model akışı sırayla okur).
	totalSpans := len(spans)
	// v0.10.1044 — çalışan sürüm seçimi TÜM span'lerden (100'lük prompt
	// kesiminden önce): sayım ne kadar çok örnekse o kadar sağlam.
	allSpans := spans
	// v0.10.1371 — HATA ZİNCİRİ TÜM span'lerden (kaynak 100'lük kesimin
	// dışında kalabilir); sağlıklı trace'te OK=false ve blok "".
	chain := traceErrorChainOf(allSpans)
	// v0.10.1627 — KRİTİK YOL da TÜM span'lerden (en büyük paylı adım 100'lük
	// kesimin dışında kalabilir). Ek okuma yok.
	crit := traceCriticalPathOf(allSpans)
	// v0.10.2378 — kanıt ölçeği (evidence_scale.go): geniş pencereli modelde
	// span tavanı, event/stack tavanları, log satırı sayısı ve gövde/stack
	// bütçeleri çarpılır; ölçek 1'de her sayı aşağıdaki sabit, prompt bayt bayt
	// eski.
	evScale := s.evidenceScale(ctx)
	spans = pickExplainSpans(spans, scaleCap(traceExplainSpanCap, evScale))
	compact := make([]traceLite, 0, len(spans))
	var dbStmts, errTexts []string
	seenStmt := map[string]bool{}
	for _, sp := range spans {
		l := traceLiteOfScaled(sp, evScale)
		if l.Status == "error" {
			if l.DBStatement != "" && len(dbStmts) < 3 && !seenStmt[l.DBStatement] {
				seenStmt[l.DBStatement] = true
				dbStmts = append(dbStmts, l.DBStatement)
			}
			if len(errTexts) < 5 && sp.StatusMessage != "" {
				errTexts = append(errTexts, sp.StatusMessage)
			}
		}
		compact = append(compact, l)
	}
	payload, _ := json.Marshal(compact)
	// v0.10.1147 (operatör-bildirimli) — SPAN EVENT'LERİ. traceLite events
	// taşımıyordu: logu olmayan tek span'lik ERROR trace'in nedeni
	// (exception.type/message/stacktrace) modele hiç ulaşmıyordu. Ek okuma yok
	// — aynı span'lerin events alanı (explain_trace_events.go). Hata span'inin
	// exception'ı şema kanıtının hata metnine de girer.
	evd := buildTraceEventDigestLim(spans, traceEventLimitsFor(evScale))
	errTexts = append(errTexts, evd.ErrTexts...)

	// Trace'in LOGLARI — SADECE kullanıcı bu explain'i tetiklediğinde, log
	// store'a TEK sorgu (poll/proaktif YOK; operatör isteği v0.9.166).
	// Elastic loglarında stacktrace'ler span event'lerinden zengin
	// olabildiğinden trace + logları BİRLİKTE yorumlatırız. Pencere trace
	// span'larından ±1dk, bounded limit 30, hata-öncelikli, gövde/stack
	// truncate'li (2B prompt bütçesi). Log store yok/yavaş/boşsa sessizce
	// trace-only'e düşer — explain'i asla düşürmez.
	var logsBlock, rawStack, stackService string
	// v0.10.1044 — stack'li logun KENDİ span'i ve resource'u: çalışan sürüm
	// önce oradan (anomaly.StackVersion; canary'de çoğunluk yanlış seçer).
	var stackSpanID string
	var stackRes map[string]string
	// v0.10.921 (Kademe A) — Oracle hata satırları loglarla PARALEL okunur
	// (ClickHouse kopyası; canlı Oracle sorgusu yok). Aynı ±1 dk pencere,
	// kendi 4 sn bütçesi, sessiz düşüş: Explain'i asla düşürmez.
	var oracleBlock string
	var oracleRows int
	// v0.10.1329 — satırların KENDİSİ de tutulur (yalnız sayı değil): ana
	// operasyon seçimi (pickMainOperation) aynı okumadan, ek sorgu yok.
	var oracleRowsRead []chstore.OracleErrorRow
	// v0.10.1349 — okunan log kayıtları da tutulur: ana operasyonun (c) log
	// tier'ı (pickMainOperation) aynı okumadan, ek I/O yok.
	var logsRead []*logstore.LogRecord
	var oracleWG sync.WaitGroup
	// v0.10.1371 — zincirin ilk satırına giren exception (stack'li İLK log);
	// halkanın kendi exception event'i varsa Block onu tercih eder.
	var chainLogEx traceChainException
	if s.oracle != nil && s.oracle.HasEnabledSources() && s.store != nil {
		ofrom := time.Unix(0, minT).Add(-time.Minute)
		oto := time.Unix(0, maxT).Add(time.Minute)
		oracleWG.Add(1)
		go func() {
			defer oracleWG.Done()
			octx, ocancel := context.WithTimeout(ctx, oracleExplainFetchTimeout)
			defer ocancel()
			if rows, oerr := s.store.OracleErrorsByTrace(octx, strings.ToLower(id), ofrom, oto, scaleCap(oracleExplainMaxRows, evScale)*5); oerr == nil {
				oracleBlock, oracleRows = oracleExplainBlockScaled(rows, s.oracleSourceNames(), evScale)
				oracleRowsRead = rows
			}
		}()
	}
	// v0.10.1371 — kaynak operasyonun 24 sa p50'si (SpanDetail "×N slower" ile
	// aynı okuma: operation_summary_5m, SpanOperationRED). Oracle'la PARALEL,
	// kendi 2 sn bütçesi, sessiz düşüş: taban yoksa ×N yazılmaz.
	var chainWG sync.WaitGroup
	var chainBaseP50 float64
	var chainBaseOK bool
	if chain.OK && s.store != nil && chain.Origin.Span.Name != "" {
		origin := chain.Origin.Span
		chainWG.Add(1)
		go func() {
			defer chainWG.Done()
			bctx, bcancel := context.WithTimeout(ctx, traceChainBaselineTimeout)
			defer bcancel()
			end := time.Unix(0, origin.EndTime)
			if origin.EndTime <= 0 {
				end = time.Now()
			}
			if red, berr := s.store.SpanOperationRED(bctx, origin.ServiceName, origin.Name, end.Add(-traceDrillBaselineWin), end); berr == nil && red.Calls > 0 && red.P50Ms > 0 {
				chainBaseP50, chainBaseOK = red.P50Ms, true
			}
		}()
	}
	// v0.10.2456 — LOGLAR bloğu explain_trace_logs.go'da: ≤250 satır okuma,
	// requestId ek araması, desen toplama, her ölçekte bütçe, gösterilmeyen özeti.
	topSpan := ""
	if crit.OK && crit.Top >= 0 && crit.Top < len(crit.Steps) {
		topSpan = crit.Steps[crit.Top].SpanID
	}
	tl := s.traceExplainLogs(ctx, id, allSpans, minT, maxT, evScale, chain.Origin.Span.SpanID, topSpan)
	logsBlock, logsRead, logOutcome := tl.Block, tl.Read, tl.Outcome
	if tl.RawStack != "" {
		rawStack, stackService = tl.RawStack, tl.StackRec.ServiceName
		stackSpanID, stackRes = tl.StackRec.SpanID, tl.StackRec.ResourceAttributes
		chainLogEx = traceChainLogException(tl.StackRec, tl.RawStack) // v0.10.1371
	}

	// v0.10.1147 — loglarda stack yoksa "Kodu da incele" hata span'inin
	// exception event'indeki HAM stack'le çalışır (servis + span'in kendi
	// sürümü; StackVersion span kimliğinden çözer). Log stack'i varsa öncelik
	// onda kalır — bugünkü davranış.
	if rawStack == "" && evd.RawStack != "" {
		rawStack, stackService, stackSpanID = evd.RawStack, evd.RawStackService, evd.RawStackSpanID
	}

	oracleWG.Wait()
	chainWG.Wait() // v0.10.1371
	chain.BaseP50Ms, chain.BaseOK = chainBaseP50, chainBaseOK
	// v0.10.2461 (operatör-bildirimli) — span exception mesajıyla AYNI hatayı
	// anlatan log metni zincirde ve event bloğunda ÖNCE (explain_trace_logpair.go).
	pairTraceExceptionLogs(&chain, &evd, spans, logsRead)
	// v0.10.1371 — HATA ZİNCİRİ User'ın EN BAŞINDA (düz metin, çitsiz; çekmece
	// kırpması baştan tutar). traceExplainUser'a dokunulmaz (golden pin).
	chainBlock := ""
	if b := chain.Block(chainLogEx); b != "" {
		chainBlock = b + "\n\n"
	}
	// Oracle bloğu logların ARKASINA; çekmece kırpması (clampDrawerEvidence)
	// bu kuyruğu bütün tutar, span listesini keser.
	// v0.10.1147 — span event bloğu kuyruğun BAŞINA: kırpmada span listesi
	// kesilir, exception özeti korunur (yalnız-log modunda da baştan kesilir).
	tail := evd.Block() + logsBlock + oracleBlock
	mainOp, otherOps, opSource := pickMainOperation(oracleRowsRead, allSpans, logsRead)
	critHead, critTop := traceCriticalHead(crit)
	evidence := traceEvidenceSpanIDs(compact)
	if critTop != nil {
		evidence = prependUnique(critTop.SpanID, evidence) // v0.10.1627 — gecikme sahibi de kutulanır
	}
	// v0.10.2480 — kanıt görünümü (aynı hesaplardan, ek okuma yok).
	stackType := chainLogEx.Type
	for _, h := range chain.Hops {
		if h.Fail.ExType != "" {
			stackType = h.Fail.ExType
			break
		}
	}
	view := buildTraceEvidenceView(traceEvidenceSrc{Spans: allSpans, Analyzed: len(compact), Chain: chain, Crit: crit,
		Logs: logsRead, LogOutcome: logOutcome, RawStack: rawStack, StackSvc: stackService, StackType: stackType,
		Oracle: oracleRowsRead, MainOp: mainOp})
	return traceExplainInput{
		View:            view,
		User:            chainBlock + critHead + traceExplainUser(id, len(compact), totalSpans, string(payload), tail), // v0.10.1371 — zincir başta; v0.10.1627 — kritik yol ikinci
		Evidence:        prependUnique(chain.Origin.Span.SpanID, evidence),                                             // v0.10.1371 — kaynak önce kutulanır
		CriticalBlock:   strings.TrimSpace(critHead),                                                                   // v0.10.1627
		TopStep:         critTop,                                                                                       // v0.10.1627
		TopExternalWait: critTop != nil && crit.TopIsExternalWait(),                                                    // v0.10.1627
		ErrorOrigin:     chain.Origin.Span.SpanID,                                                                      // v0.10.1371
		ChainBlock:      strings.TrimSpace(chainBlock),                                                                 // v0.10.1572
		LogsBlock:       tail,
		OracleRows:      oracleRows,
		Logs:            logOutcome, // v0.10.1742
		Stack:           rawStack,
		StackService:    stackService,
		StackVersion:    anomaly.StackVersion(allSpans, stackService, stackSpanID, stackRes),
		RootService:     rootService,
		DBStatements:    dbStmts,
		ErrorText:       truncRunesN(strings.Join(errTexts, "\n")+"\n"+rawStack, 2000),
		// v0.10.1329 — dikiş: henüz prompt'a girmez.
		MainOperation:   mainOp,
		OtherOperations: otherOps,
		OperationSource: opSource,
		RootVersion:     anomaly.StackServiceVersion(allSpans, rootService),
		FunctionCodes:   traceFunctionCodes(allSpans), // v0.10.1349
	}, nil
}

// traceEvidenceSpanIDs — v0.9.408 (operatör: "kök neden kısımları
// kutulanmıyor") kanıt sözleşmesi: kanıt span'leri DETERMİNİSTİK, LLM
// çıktısını parse etmek yerine modele beslediğimiz AYNI veriden
// hesaplanır (gemma4 güvenilirliğinden bağımsız). Hata span'leri (≤5) +
// en yavaş span; UI bunları waterfall'da kutular. Saf; tablo-testli.
func traceEvidenceSpanIDs(compact []traceLite) []string {
	evidence := make([]string, 0, 6)
	slowestID, slowestDur := "", float64(-1)
	for _, c := range compact {
		if c.Status == "error" && len(evidence) < 5 {
			evidence = append(evidence, c.SpanID)
		}
		if c.DurationMs > slowestDur {
			slowestDur, slowestID = c.DurationMs, c.SpanID
		}
	}
	if slowestID != "" {
		dup := false
		for _, e := range evidence {
			if e == slowestID {
				dup = true
				break
			}
		}
		if !dup {
			evidence = append(evidence, slowestID)
		}
	}
	return evidence
}

// traceExplainUser — kanıt paketinin SON montajı. v0.9.482 çıkarmasında
// bu ifade bayt-bayt korunmalıydı (iki çağıran: explain handler'ı ve
// çekmece sohbeti) — golden testi bunu pinler.
//
// analyzed < total ise dürüstlük notu eklenir: hem modele hem operatöre
// aynı gerçek — analiz kısmi ama seçim hata+yavaşlık öncelikli, "head"
// değil (v0.9.462).
func traceExplainUser(id string, analyzed, total int, payload, logsBlock string) string {
	analyzedNote := ""
	if total > analyzed {
		analyzedNote = fmt.Sprintf(" (trace'in tamamı %d span; hatalar + en yavaşlar öncelikli %d span analiz edildi)", total, analyzed)
	}
	return fmt.Sprintf("Trace %s with %d spans%s:\n```json\n%s\n```%s", id, analyzed, analyzedNote, promptfmt.FenceSafe(payload), logsBlock) // v0.10.404 — çit kaçışı
}

// truncRunesN — rune-güvenli baş kesme (v0.10.115).
func truncRunesN(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
