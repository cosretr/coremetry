package logstore

// exception_summary.go — v0.10.1882 (Log Search Faz 3 dilim 3.3; audit §8
// "Exception: sunucuda stackparse.FromLog ile tür + sınıf (kaynak tükenmesi) +
// uygulama çerçeveleri; satır içi katlı stack; yalnız kritik hatalar").
//
// YALNIZ GÖSTERİM: exception gruplama kimliğine (chstore.FingerprintException)
// DOKUNMAZ — yeniden parmak izi operatör kararı bekliyor. Satıra hesaplanmış
// bir özet iliştirilir (yalnız ES log yolu; CH satırı değişmez):
//
//   type / message — dış (en üst) exception; OTel `exception.type` /
//     `exception.message` öznitelikleri varsa onlar kazanır.
//   cause          — "Caused by" / " ---> " zincirinin en derini (farklıysa).
//   class          — kaba sınıf (classifyException, SAF tablo): resource
//     (kaynak tükenmesi) · timeout · connectivity · database · null · auth.
//     Kurallar anomaly/log_patterns.go emsali; YALNIZ etiket — öncelik, dedektör,
//     Problem üretimi YOK.
//   critical       — "yalnız kritik hatalar" süzgecinin tanımı: class=resource
//     ya da seviye FATAL (≥ 21).
//   frames / appFrames / appLines — stack satırları içindeki çerçeve sayıları ve
//     UYGULAMA çerçevesi satır indeksleri (istemci stack'i satır satır çizer,
//     uygulama satırlarını vurgular, framework satırlarını katlar). Stack metni
//     TEKRAR GÖNDERİLMEZ: gövdede ya da `stackField` özniteliğinde zaten var.

import (
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/stackparse"
)

// ExceptionSummary — LogRecord.Exception (JSON `exception`).
type ExceptionSummary struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	Cause   string `json:"cause,omitempty"`
	Class   string `json:"class,omitempty"`
	// Critical — "yalnız kritik hatalar": kaynak tükenmesi ya da FATAL.
	Critical bool `json:"critical,omitempty"`
	// StackField — stack'in okunduğu öznitelik; boş = gövde.
	StackField string `json:"stackField,omitempty"`
	Frames     int    `json:"frames"`
	AppFrames  int    `json:"appFrames"`
	// AppLines — stack metninin (satır bölünmüş) uygulama çerçevesi satır
	// indeksleri; tavanlı (exceptionAppLinesMax).
	AppLines []int `json:"appLines,omitempty"`
	// Top — en üstteki ≤3 uygulama çerçevesi, kısa biçim (Class.method:line).
	Top []string `json:"top,omitempty"`
}

const (
	exceptionAppLinesMax = 200
	exceptionTopMax      = 3
	exceptionMsgMax      = 300
	// exceptionScanMax — gövde/stack tarama tavanı (bayt): 500 satırlık örnekte
	// satır başına iş sınırlı kalsın.
	exceptionScanMax = 64 << 10
	fatalSeverity    = 21
)

// exceptionHeadRE — "pkg.Type: message" / "Exception in thread "x" pkg.Type: m"
// / ".NET System.X (0x80004005): m". Tür adı nitelikli olmalı ve
// Exception / Error / Throwable / Fault ile bitmeli (düz metinde yanlış eşleşme olmasın).
var exceptionHeadRE = regexp.MustCompile(`^\s*(?:Caused by:\s*|---> |Exception in thread "[^"]*"\s+|Unhandled exception\.\s*)?((?:[A-Za-z_$][\w$]*\.)+[A-Za-z_$][\w$]*(?:Exception|Error|Throwable|Fault))(?:\s*\(0x[0-9A-Fa-f]+\))?(?::\s*(.*))?$`)

// classRules — kaba sınıf; sıra önemli (ilk eşleşen). Metin = tür + " " + mesaj, küçük harf.
var classRules = []struct {
	class string
	subs  []string
}{
	{"resource", []string{"outofmemoryerror", "out of memory", "oomkilled", "cannot allocate memory", "stackoverflowerror",
		"too many open files", "no space left on device", "enospc", "rejectedexecutionexception",
		"connection is not available", "pool exhausted", "connection pool", "ij000453", "ij000655", "mqjca1011",
		"could not acquire jdbc", "no managed connections available", "unable to get managed connection", "insufficientmemory"}},
	{"timeout", []string{"timeoutexception", "timed out", "deadline exceeded", "i/o timeout", "read timeout", "taskcanceledexception"}},
	{"connectivity", []string{"connectexception", "connection refused", "econnrefused", "unknownhostexception", "noroutetohost",
		"connection reset", "httprequestexception", "tns-", "socketexception"}},
	{"database", []string{"ora-", "sqlexception", "deadlock", "jdbcexception", "dataaccessexception", "npgsqlexception",
		"optimisticlockexception", "lazyinitializationexception", "constraintviolationexception"}},
	{"null", []string{"nullpointerexception", "nullreferenceexception"}},
	{"auth", []string{"authenticationexception", "accessdeniedexception", "unauthorizedaccessexception", "401 unauthorized"}},
}

// classifyException — SAF.
func classifyException(typ, msg string) string {
	t := strings.ToLower(typ + " " + msg)
	for _, r := range classRules {
		for _, s := range r.subs {
			if strings.Contains(t, s) {
				return r.class
			}
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

// findStack — stackparse.FromLog + .NET gövdesi (FromLog gövdeyi Java çerçeve
// tanımıyla sınar; .NET "at X in f:line N" gövdesi için ParseDotnet).
func findStack(attrs map[string]string, body string) (stack, field string) {
	st, fromBody := stackparse.FromLog(attrs, body)
	if st != "" {
		if !fromBody {
			for k, v := range attrs {
				if v == st {
					field = k
					break
				}
			}
		}
		return st, field
	}
	if len(stackparse.ParseDotnet(body)) >= 2 {
		return body, ""
	}
	return "", ""
}

// SummarizeException — SAF: satırın exception özeti; exception yoksa nil.
func SummarizeException(attrs map[string]string, body string, severity uint8) *ExceptionSummary {
	if len(body) > exceptionScanMax {
		body = body[:exceptionScanMax]
	}
	stack, field := findStack(attrs, body)
	if len(stack) > exceptionScanMax {
		stack = stack[:exceptionScanMax]
	}
	out := &ExceptionSummary{StackField: field}
	if t := strings.TrimSpace(attrs["exception.type"]); t != "" {
		out.Type, out.Message = t, clip(attrs["exception.message"], exceptionMsgMax)
	}
	text := stack
	if text == "" {
		text = body
	}
	lines := strings.Split(text, "\n")
	var heads [][2]string
	for _, ln := range lines {
		if m := exceptionHeadRE.FindStringSubmatch(strings.TrimRight(ln, "\r")); m != nil {
			heads = append(heads, [2]string{m[1], m[2]})
		}
	}
	if out.Type == "" && len(heads) > 0 {
		out.Type, out.Message = heads[0][0], clip(heads[0][1], exceptionMsgMax)
	}
	causeMsg := ""
	if len(heads) > 1 && heads[len(heads)-1][0] != out.Type {
		out.Cause, causeMsg = heads[len(heads)-1][0], heads[len(heads)-1][1]
	}
	if out.Type == "" {
		return nil // stack var ama başlık yok ya da hiç exception yok: rozet uydurulmaz
	}
	if stack != "" {
		dotnet := len(stackparse.ParseDotnet(stack)) > 0
		for i, ln := range lines {
			var fr []stackparse.Frame
			if dotnet {
				fr = stackparse.ParseDotnet(ln)
			} else {
				fr = stackparse.ParseJava(ln)
			}
			if len(fr) != 1 {
				continue
			}
			out.Frames++
			if !stackparse.IsAppFrame(fr[0], nil) {
				continue
			}
			out.AppFrames++
			if len(out.AppLines) < exceptionAppLinesMax {
				out.AppLines = append(out.AppLines, i)
			}
			if len(out.Top) < exceptionTopMax {
				out.Top = append(out.Top, fr[0].Short())
			}
		}
	}
	out.Class = classifyException(out.Type, out.Message+" "+out.Cause+" "+causeMsg)
	out.Critical = out.Class == "resource" || severity >= fatalSeverity
	return out
}

// AttachExceptions — sayfa satırlarına özet iliştirir (yerinde).
func AttachExceptions(rows []*LogRecord) {
	for _, r := range rows {
		if r == nil {
			continue
		}
		r.Exception = SummarizeException(r.Attributes, r.Body, r.Severity)
	}
}
