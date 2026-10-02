package api

// chat_source_code.go — v0.10.1050 (operatör: "Sohbet kod okuyabilsin: takip
// soruları bugün kod okuyamıyor."): read_source_code aracının api yarısı.
//
// AI panelinde açıklamadan sonra SOHBET'te sorulan "bu metodun devamında ne
// var?", "X sınıfına da bak" sorularına sohbetin kod okuma yolu yoktu; tek kod
// okuyucusu explain'in buildCodeContext'iydi (copilot_code.go). Araç
// mcptools/source_code.go'da, DevOps zinciri devops/source_read.go'da; bu
// dosya ikisinin arasındaki SUNUCU kararlarını tutar.
//
// ── NEREDE SUNULUR (güvenlik incelemesi, v0.10.1050) ───────────────────────
//
//   - YALNIZ panel takibinde: trace/span öznesi → serbest döngü (yalnız YERLİ
//     araçlar); v0.10.1053 — exception öznesi → çekmecenin TEK araçlı döngüsü
//     (yalnız bu araç; chat_exception_followup.go). Bağımsız sohbetin
//     döngüsünde dış MCP araçları da var ve onay adımı yok: OTLP ingest
//     kimliksiz, yani ekilmiş bir log satırı modeli bir dosyayı okuyup kodunu
//     bir dış aracın argümanına koymaya yönlendirebilirdi — kod üçüncü tarafa
//     çıkardı.
//   - YALNIZ oturum kullanıcısına: cmk_ API token'ı da /api/copilot/chat'e
//     girer (claims UserID "token:<id>"); ek modelden AYNEN alıntı istediği
//     için "MCP sunucusunda yok" sözleşmesi token'la dolanılırdı. Token ve
//     kimliksiz çağrı ROLDEN BAĞIMSIZ dışarıda (sourceCodeCallerAllowed).
//   - YALNIZ DevOps bağlıyken (Deps.SourceCode nil → ChatToolList düşürür).
//     Rol kapısı aracın kendi MinRole'ü (mcptools.SourceCodeMinRole,
//     toolsForRole) — viewer düzeyi: viewer, editor ve admin görür.
//   Sunulmadığında katalog ve döngü prompt'u bayt bayt eski (pinli).
//
// ── NEYE ERİŞİR ────────────────────────────────────────────────────────────
//
//   - Servis sohbetin trace ÖZNESİNDE geçmeli (öznenin span'leri alışveriş
//     başına BİR kez okunur — sürüm türetimiyle aynı okuma). Telemetri gönderen
//     herkes bir servis adı "yaratabilir" ve ad konvansiyonu onu projedeki
//     HERHANGİ bir depoya çevirirdi; "telemetride bir yerde var" sınır değildir,
//     "operatörün baktığı trace'in parçası" sınırdır. Trace okunamazsa dürüst ıska.
//     v0.10.1053 — exception öznesinde sınır daha dar: YALNIZ grubun servisi ve
//     explain'in stack'ini basan servis (örnek trace'teki öteki servisler DEĞİL);
//     sürüm o stack'i veren olaydan (sourceExceptionScope).
//   - Depo YALNIZ katalog pini / ad konvansiyonu (buildCodeContext ile aynı
//     pinReadDecision + ResolveRepo). Sürümü model SEÇEMEZ: öznenin span'lerinden
//     explain'in yardımcısıyla (anomaly.StackVersion) ya da dal sırası.
//   - "Kod tarayıcıya gitmez" (copilot_code.go) — modelin kendi cevabı DIŞINDA:
//     step-result önizlemesi yalnız referans (mcptools.SourceCodeReference),
//     ai_calls örneği maskeli özet (mcptools.SourceCodeLogSummary), takip
//     cevabının sayı denetimi kanıtı yalnız referans satırı
//     (mcptools.SourceCodeRefLine). Span'lere ve audit satırına zaten yalnız
//     ad/argüman/bayt gider (chat_span.go, mcp_observe.go).

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// sourceTraceServicesMax — "servis trace'te yok" cevabında modele anılan en
// çok servis sayısı.
const sourceTraceServicesMax = 10

// sourceCodeReaderOrNil — Deps.SourceCode: DevOps bağlı değilse nil (araç
// sohbete sunulmaz). mcpDeps her istekte kurulduğu için bağlantı Ayarlar'dan
// açılıp kapandığında bir sonraki sohbet turu bunu görür.
func (s *Server) sourceCodeReaderOrNil() mcptools.SourceCodeReader {
	if s.devops == nil || !s.devops.Configured() {
		return nil
	}
	return s.readSourceCode
}

// sourceCodeCallerAllowed — SAF: çağıran bir oturum kullanıcısı mı? API
// token'ı (UserID "token:<id>", promqlHistoryDisabledFor emsali) ve kimliksiz
// çağrı DEĞİL.
func sourceCodeCallerAllowed(c *auth.Claims) bool {
	if c == nil {
		return false
	}
	uid := strings.TrimSpace(c.UserID)
	return uid != "" && !strings.HasPrefix(uid, "token:")
}

// sourceCodeToolsFor — SAF: read_source_code bu alışverişte sunulacak mı?
// Değilse ADIYLA düşer — katalog, spec'ler ve prompt eki ondan önce kurulmaz.
// Sunulma: panel takibi (trace/span serbest döngüsü; v0.10.1053 — exception
// çekmecesinin tek araçlı döngüsü de) + oturum kullanıcısı (rol ve DevOps
// kapıları zaten tools'a uygulandı).
func sourceCodeToolsFor(tools []mcp.Tool, panelFollowUp bool, c *auth.Claims) []mcp.Tool {
	if panelFollowUp && sourceCodeCallerAllowed(c) {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Name != mcptools.SourceCodeToolName {
			out = append(out, t)
		}
	}
	return out
}

// readSourceCode — mcptools.SourceCodeReader. req argüman kapısından geçti
// (mcptools.validateReadSourceArgs). error yalnız argüman sınıfı (servis
// öznenin kapsamında yok); anlatılabilir her arıza Outcome'da.
func (s *Server) readSourceCode(ctx context.Context, req mcptools.SourceCodeRequest) (mcptools.SourceCodeRead, error) {
	var out mcptools.SourceCodeRead
	if s.devops == nil || !s.devops.Configured() {
		out.Outcome, out.Reason = devops.SourceUnconfigured, "kod entegrasyonu yapılandırılmamış"
		return out, nil
	}
	svc := req.Service
	if !mcptools.ValidSourceService(svc) { // biçim kapısı — istekten ÖNCE (savunma; araç da uygular)
		return out, errors.New("service geçersiz (harf/rakamla başlar; yalnız harf, rakam, . _ -)")
	}
	// v0.10.1053 — exception öznesi (chat_exception_followup.go): kapsam ve
	// sürüm explain girdisinden; trace dalına hiç girilmez.
	if ex, _ := ctx.Value(sourceExceptionKey{}).(*sourceExceptionScope); ex != nil {
		version, miss, reason, err := ex.decide(svc)
		if err != nil {
			return out, err
		}
		if miss != "" {
			out.Outcome, out.Reason = miss, reason
			return out, nil
		}
		return s.readScopedSource(ctx, req, version, "sohbet öznesi (exception örneği)"), nil
	}
	// KAPSAM: servis sohbetin trace öznesinde geçmeli.
	sub, _ := ctx.Value(sourceSubjectKey{}).(*sourceSubject)
	if sub == nil {
		out.Outcome, out.Reason = devops.SourceOutOfScope, "kod yalnız panelde incelenen trace'in servisleri için okunur — bu sohbette trace öznesi yok"
		return out, nil
	}
	spans := s.subjectSpans(ctx, sub)
	if len(spans) == 0 {
		out.Outcome, out.Reason = devops.SourceTraceUnreadable, "trace okunamadı — servis doğrulanamadığı için kod okunmadı"
		return out, nil
	}
	if names := traceServiceNames(spans); !containsString(names, svc) {
		if len(names) > sourceTraceServicesMax {
			names = names[:sourceTraceServicesMax]
		}
		return out, fmt.Errorf("service %q bu trace'te bulunamadı — yalnız trace'teki servislerin kodu okunur: %s", svc, strings.Join(names, ", "))
	}
	// Sürüm SUNUCUDA: öznenin span'lerinden (model ref seçemez); yoksa dal.
	return s.readScopedSource(ctx, req, subjectServiceVersion(spans, svc, sub.spanID), "sohbet öznesi (trace)"), nil
}

// readScopedSource — kapsamdan GEÇMİŞ servisin depo çözümü ve okuması (iki
// öznenin ortak kuyruğu). version sunucunun öznesinden ("" → dal); basis
// okunan ref çalışan sürümse "neden bu ref" cevabı.
func (s *Server) readScopedSource(ctx context.Context, req mcptools.SourceCodeRequest, version, basis string) mcptools.SourceCodeRead {
	var out mcptools.SourceCodeRead
	svc := req.Service
	pin := ""
	if s.store != nil {
		// Katalog pini — buildCodeContext ile AYNI karar (fail-closed: pin
		// okunamazsa konvansiyona düşülmez, yanlış depodan kod okunmaz).
		md, err := s.store.GetServiceMetadataStrict(ctx, svc)
		mdRepo := ""
		if md != nil {
			mdRepo = md.Repository
		}
		p, abort := pinReadDecision(mdRepo, md != nil, err)
		if abort != "" {
			out.Outcome, out.Reason = devops.SourceCatalogError, abort
			return out
		}
		pin = p
	}
	res := devops.ResolveRepo(svc, pin, s.devops.ResolveConfig())
	out.RepoSource = res.Source
	if res.Repo == "" {
		reason := res.Reason
		if reason == "" {
			reason = "servis için depo çözülemedi"
		}
		out.Outcome, out.Reason = devops.SourceRepoUnresolved, reason
		return out
	}
	out.SourceRead = s.devops.ReadSource(ctx, devops.SourceRequest{
		Repo: res.Repo, Hint: res.Project, Service: svc,
		File: req.File, Line: req.Line, Context: req.ContextLines, Version: version,
	})
	if out.Version != "" {
		out.VersionBasis = basis
	}
	return out
}

// traceServiceNames — SAF: span'lerdeki servis adları (tekil, sıralı).
func traceServiceNames(spans []chstore.SpanRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, sp := range spans {
		if n := sp.ServiceName; n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// sourceSubject — sohbetin trace öznesi (alışveriş ctx'inde); span'ler
// TEMBEL ve alışveriş başına EN ÇOK BİR KEZ okunur (kapsam + sürüm aynı okuma).
type sourceSubject struct {
	traceID, spanID string
	once            sync.Once
	spans           []chstore.SpanRow
}

type sourceSubjectKey struct{}

// withSourceSubject — trace takibinin öznesini araç ctx'ine bağlar (boş id → ctx aynen).
func withSourceSubject(ctx context.Context, traceID, spanID string) context.Context {
	if strings.TrimSpace(traceID) == "" {
		return ctx
	}
	return context.WithValue(ctx, sourceSubjectKey{}, &sourceSubject{traceID: traceID, spanID: spanID})
}

// subjectSpans — öznenin span'leri (bir kez okunur; okunamazsa boş).
func (s *Server) subjectSpans(ctx context.Context, sub *sourceSubject) []chstore.SpanRow {
	sub.once.Do(func() { sub.spans = s.sourceSubjectSpans(ctx, sub.traceID) })
	return sub.spans
}

// sourceSubjectSpans — explain'in trace okuması (resolveTraceSpans: Tempo önce,
// sonra CH). Store'suz sunucu (yalnız testler) Tempo'yla sınırlı.
func (s *Server) sourceSubjectSpans(ctx context.Context, traceID string) []chstore.SpanRow {
	if s.store == nil {
		if s.tempo != nil && s.tempo.Configured() {
			if spans, err := s.tempo.LookupTrace(ctx, traceID); err == nil {
				return spans
			}
		}
		return nil
	}
	spans, _, err := s.resolveTraceSpans(ctx, traceID)
	if err != nil {
		return nil
	}
	return spans
}

// subjectServiceVersion — SAF: explain'in sürüm yardımcısı (anomaly.StackVersion,
// v0.10.1044). Odak span İSTENEN servise aitse önce onun sürümü (canary dersi:
// çoğunluk patlayan sürümü kaybeder), değilse o servisin span'lerinde çoğunluk.
func subjectServiceVersion(spans []chstore.SpanRow, service, spanID string) string {
	focus := ""
	for _, sp := range spans {
		if sp.SpanID == spanID && sp.ServiceName == service {
			focus = spanID
			break
		}
	}
	return anomaly.StackVersion(spans, service, focus, nil)
}

// chatSourceCodePromptTR — read_source_code bu turun kataloğundaysa döngü
// prompt'una giren kısa ek; değilse "" ve döngü prompt'u bayt bayt eskisi.
func chatSourceCodePromptTR(tools []mcp.Tool) string {
	for _, t := range tools {
		if t.Name == mcptools.SourceCodeToolName {
			return copilot.SourceCodeChatAddendum() + "\n\n"
		}
	}
	return ""
}

// chatStepPreview — step-result önizlemesi. read_source_code'un başarılı
// sonucu YALNIZ referans olarak tele çıkar (yol:aralık · ref; kod satırı YOK —
// "kod tarayıcıya gitmez"); hata sonucu (argüman hatası, ToolErrorJSON — kod
// taşımaz) ve diğer araçlar bugünkü 4 KB kırpmasından geçer.
func chatStepPreview(tool, content string, isErr bool) (string, bool) {
	if tool == mcptools.SourceCodeToolName && !isErr {
		ref, _ := clipStepPreview(mcptools.SourceCodeReference(content))
		return ref, false
	}
	return clipStepPreview(content)
}

// chatFollowUpEvidence — trace takibinin sayı denetimi kanıtına giren çıktı:
// read_source_code için YALNIZ referans satırı (koddaki sabitler ve satır
// numaraları uydurulmuş bir sayıyı temellendirmesin); diğer araçlar aynen.
func chatFollowUpEvidence(tool, content string) string {
	if tool == mcptools.SourceCodeToolName {
		return mcptools.SourceCodeRefLine(content)
	}
	return content
}

// chatCodeReadSummary — yürütülen read_source_code çağrısının ai_calls özeti
// (maskeli; kod yok). Başka araç ya da hata → "".
func chatCodeReadSummary(tool, content string, isErr bool) string {
	if tool != mcptools.SourceCodeToolName || isErr {
		return ""
	}
	return mcptools.SourceCodeLogSummary(content)
}

// chatPromptSample — ai_calls prompt örneği: operatörün son mesajı + (varsa)
// okunan kodun MASKELİ özetleri; explain yolunun "[kod: …]" sözleşmesinin
// sohbet ikizi. Kod okunmadıysa bayt bayt eski örnek.
func chatPromptSample(user string, codeReads []string) string {
	if len(codeReads) == 0 {
		return user
	}
	return user + "\n\n" + strings.Join(codeReads, "\n")
}
