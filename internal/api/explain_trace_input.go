package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/promptfmt"
	"github.com/cilcenk/coremetry/internal/stackparse"
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
}

// buildTraceExplainInput — trace'in span'lerini çekip kompakt JSON'a
// indirger, ilişkili logları (varsa) ekler ve deterministik kanıt
// span'lerini hesaplar. Log sorgusu SADECE bu çağrı içinde, TEK pass;
// hata/boşluk durumunda sessizce trace-only'e düşer (v0.9.166 maliyet
// disiplini: proaktif/poll YOK).
func (s *Server) buildTraceExplainInput(ctx context.Context, id string) (traceExplainInput, error) {
	// v0.9.632 — operator-reported: Tempo fallback trace'i bulup
	// waterfall'ı çizerken explain "trace not found" diyordu. Burası
	// doğrudan s.store.GetTrace çağırıyordu; /api/traces/{id} ise CH
	// ıskalayınca Tempo'ya düşüyordu. Tek kural, tek yer:
	// resolveTraceSpans (trace_resolve.go).
	spans, _, err := s.resolveTraceSpans(ctx, id)
	if err != nil {
		return traceExplainInput{}, err
	}
	if len(spans) == 0 {
		return traceExplainInput{}, errExplainTraceNotFound
	}
	// Trace penceresi (log sorgusu için) — cap'ten ÖNCE, tüm span'lar üstünden.
	// Kök servis: parent'ı olmayan span; yoksa en erken başlayan
	// (kırık/eksik parent zincirinde de bir cevap üretmeli).
	root := spans[0]
	for _, sp := range spans {
		if sp.ParentSpanID == "" {
			root = sp
			break
		}
		if sp.StartTime < root.StartTime {
			root = sp
		}
	}
	rootService := root.ServiceName

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
	spans = pickExplainSpans(spans, traceExplainSpanCap)
	compact := make([]traceLite, 0, len(spans))
	var dbStmts, errTexts []string
	seenStmt := map[string]bool{}
	for _, sp := range spans {
		dur := float64(sp.EndTime-sp.StartTime) / 1e6
		l := traceLite{Name: sp.Name, Service: sp.ServiceName, Kind: sp.Kind,
			ParentSpan: sp.ParentSpanID, SpanID: sp.SpanID, DurationMs: dur}
		if sp.StatusCode == "error" {
			l.Status = "error"
			l.StatusMsg = sp.StatusMessage
			if sp.DBStatement != "" {
				l.DBSystem = sp.DBSystem
				l.DBStatement = truncRunesN(sp.DBStatement, 600)
				if len(dbStmts) < 3 && !seenStmt[l.DBStatement] {
					seenStmt[l.DBStatement] = true
					dbStmts = append(dbStmts, l.DBStatement)
				}
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
	evd := buildTraceEventDigest(spans)
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
	var oracleWG sync.WaitGroup
	if s.oracle != nil && s.oracle.HasEnabledSources() && s.store != nil {
		ofrom := time.Unix(0, minT).Add(-time.Minute)
		oto := time.Unix(0, maxT).Add(time.Minute)
		oracleWG.Add(1)
		go func() {
			defer oracleWG.Done()
			octx, ocancel := context.WithTimeout(ctx, oracleExplainFetchTimeout)
			defer ocancel()
			if rows, oerr := s.store.OracleErrorsByTrace(octx, strings.ToLower(id), ofrom, oto, oracleExplainMaxRows*5); oerr == nil {
				oracleBlock, oracleRows = oracleExplainBlock(rows, s.oracleSourceNames())
			}
		}()
	}
	if s.logs != nil {
		from := time.Unix(0, minT).Add(-time.Minute)
		to := time.Unix(0, maxT).Add(time.Minute)
		lctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		if page, lerr := logstore.LogsForTrace(lctx, s.logs, id, from, to, 30); lerr == nil && page != nil && len(page.Logs) > 0 {
			type liteLog struct {
				Sev    string `json:"sev,omitempty"`
				Svc    string `json:"svc,omitempty"`
				ExType string `json:"exType,omitempty"`
				Stack  string `json:"stack,omitempty"`
				Body   string `json:"body,omitempty"`
			}
			logs := page.Logs
			sort.SliceStable(logs, func(i, j int) bool { return logs[i].Severity > logs[j].Severity })
			ll := make([]liteLog, 0, 15)
			for _, lg := range logs {
				if len(ll) >= 15 {
					break
				}
				// v0.9.1182 (operatör-bildirimli) — stack ARANIR, tek bir
				// anahtardan okunmaz. Eskiden yalnız `exception.stacktrace`
				// bakılıyordu; ECS kurulumlarında alan `error.stack_trace` ve
				// Java'nın en yaygın deseninde stack ayrı bir alanda DEĞİL,
				// mesajın arkasında. İkisinde de kod çekici "stacktrace yok"
				// diyordu ve model stack'i 600 baytlık gövde bütçesinin içinden
				// yarım okuyordu ("metot ismi tam görünmüyor" notu buydu).
				stackText, stackFromBody := stackparse.FromLog(lg.Attributes, lg.Body)
				bodyForPrompt := lg.Body
				if stackFromBody {
					// Frame'ler artık stack alanında ve orada bütçe daha büyük;
					// gövdeye yalnız mesaj başı kalır, aynı baytı iki kez
					// ödemeyelim.
					bodyForPrompt = stackparse.MessageHead(lg.Body)
				}
				e := liteLog{Sev: lg.SeverityText, Svc: lg.ServiceName, Body: truncate(bodyForPrompt, 600)}
				e.ExType = lg.Attributes["exception.type"] // nil map okuması güvenli
				// v0.9.1182 — stack ataması artık `lg.Attributes != nil`
				// koşulunun DIŞINDA. Gövdeden gelen bir stack, özniteliksiz bir
				// kayıtta da geçerlidir ve eski yerleşim onu sessizce yutardı:
				// tam olarak bu bug'ın (stack gövdede) en saf hâli.
				if strings.TrimSpace(stackText) != "" {
					// v0.9.842 — the FIRST log carrying a stacktrace gets a
					// bigger budget (1500 vs 900). Because the sort above is
					// severity-first, that log is the trace's most serious
					// error, and it is the one the new "Stacktrace Detayı"
					// section is asked to name a class, a method and a
					// deployment unit from. 900 bytes routinely cut the frame
					// list before the application frames — the framework
					// preamble survived and the answer to "where in OUR code"
					// did not. Only the first: the remaining stacks stay at
					// 900 so the prompt budget does not grow with every
					// duplicate of the same failure.
					stackLimit := 900
					if rawStack == "" {
						stackLimit = 1500
					}
					e.Stack = truncate(stackText, stackLimit)
					// v0.9.831 — kod çekici için HAM stack (prompt'a giren
					// 900-byte kesilmiş kopya değil): frame'ler dosya+satır
					// taşıyor ve kesik bir satır konumlandırılamaz. İlk
					// stack'li log kazanır; sıralama severity-öncelikli
					// olduğu için bu, trace'in EN CİDDİ hatası.
					if rawStack == "" {
						rawStack, stackService = stackText, lg.ServiceName
						stackSpanID, stackRes = lg.SpanID, lg.ResourceAttributes
					}
				}
				ll = append(ll, e)
			}
			if lp, e := json.Marshal(ll); e == nil {
				logsBlock = fmt.Sprintf("\n\nBu trace'in ilişkili LOGLARI (log store; stacktrace burada span event'lerinden zengin olabilir), yüksek severity önce:\n```json\n%s\n```\n\nTrace waterfall'ı VE logları BİRLİKTE yorumla — hata/stacktrace varsa kök nedeni logdaki stacktrace + exception.type'a dayandır ve ilgili span'ın StatusMsg'ıyla eşleştir.", promptfmt.FenceSafe(string(lp)))
			}
		}
		cancel()
	}

	// v0.10.1147 — loglarda stack yoksa "Kodu da incele" hata span'inin
	// exception event'indeki HAM stack'le çalışır (servis + span'in kendi
	// sürümü; StackVersion span kimliğinden çözer). Log stack'i varsa öncelik
	// onda kalır — bugünkü davranış.
	if rawStack == "" && evd.RawStack != "" {
		rawStack, stackService, stackSpanID = evd.RawStack, evd.RawStackService, evd.RawStackSpanID
	}

	oracleWG.Wait()
	// Oracle bloğu logların ARKASINA; çekmece kırpması (clampDrawerEvidence)
	// bu kuyruğu bütün tutar, span listesini keser.
	// v0.10.1147 — span event bloğu kuyruğun BAŞINA: kırpmada span listesi
	// kesilir, exception özeti korunur (yalnız-log modunda da baştan kesilir).
	tail := evd.Block() + logsBlock + oracleBlock
	return traceExplainInput{
		User:         traceExplainUser(id, len(compact), totalSpans, string(payload), tail),
		Evidence:     traceEvidenceSpanIDs(compact),
		LogsBlock:    tail,
		OracleRows:   oracleRows,
		Stack:        rawStack,
		StackService: stackService,
		StackVersion: anomaly.StackVersion(allSpans, stackService, stackSpanID, stackRes),
		RootService:  rootService,
		DBStatements: dbStmts,
		ErrorText:    truncRunesN(strings.Join(errTexts, "\n")+"\n"+rawStack, 2000),
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
