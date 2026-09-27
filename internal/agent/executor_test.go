package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v0.7.4 — agent executors run operator-authored automated runbook steps in
// an isolated pod. These pin the security-relevant behaviors: JS is a no-I/O
// sandbox with a wall-clock timeout, bash captures output + times out, HTTP
// surfaces status, unknown kinds error rather than panic. A regression here
// could hang the agent (no timeout) or leak host access from the JS sandbox.

func TestExecuteJavaScript(t *testing.T) {
	if r := executeJavaScript("1 + 2", time.Second); r.Error != "" || r.Output != "3" {
		t.Fatalf("js eval = %+v, want output 3", r)
	}
	if r := executeJavaScript("throw new Error('boom')", time.Second); r.Error == "" {
		t.Fatal("js throw should surface as Error, not panic")
	}
	// No host bindings — require/process must be undefined (the sandbox).
	if r := executeJavaScript("typeof require + ',' + typeof process", time.Second); r.Output != "undefined,undefined" {
		t.Fatalf("sandbox leak: %q (want undefined,undefined)", r.Output)
	}
	// Infinite loop must be interrupted by the timeout, not hang.
	if r := executeJavaScript("while(true){}", 200*time.Millisecond); r.Error == "" {
		t.Fatal("infinite loop should be interrupted with an error")
	}
}

func TestExecuteBash(t *testing.T) {
	if r := executeBash(context.Background(), "echo hello", time.Second); r.Error != "" || strings.TrimSpace(r.Output) != "hello" {
		t.Fatalf("bash echo = %+v", r)
	}
	if r := executeBash(context.Background(), "sleep 5", 150*time.Millisecond); r.Error == "" {
		t.Fatal("bash past timeout should error")
	}
}

func TestExecuteHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "v" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	if r := Execute(context.Background(), AutomatedStep{Kind: "http", URL: srv.URL, Headers: map[string]string{"X-Test": "v"}}); r.Error != "" || !strings.Contains(r.Output, "ok") {
		t.Fatalf("http = %+v", r)
	}
	if r := Execute(context.Background(), AutomatedStep{Kind: "http", URL: srv.URL}); r.Error == "" {
		t.Fatal("http 400 should surface as error")
	}
}

func TestExecuteUnknownKind(t *testing.T) {
	if r := Execute(context.Background(), AutomatedStep{Kind: "python"}); r.Error == "" {
		t.Fatal("unknown kind should return an error result, not panic")
	}
}

// v0.10.966 — bash adımı ebeveynin sırlarını görmez, kendi süreç grubunda
// koşar ve zaman aşımında / adım sonunda TÜM grubu öldürür. Canlı testler
// (darwin + linux; CI -race işi de koşar). Ölüm kanıtı işaret DOSYASI ile:
// `kill -0` zombiye de "yaşıyor" der.

const bashSentinel = "synthetic-sentinel-961"

func TestExecuteBashNoSecretEnv(t *testing.T) {
	t.Setenv("COREMETRY_SECRET_PROBE", bashSentinel)
	r := executeBash(context.Background(), "env", 5*time.Second)
	if r.Error != "" {
		t.Fatalf("env hata verdi: %+v", r)
	}
	if strings.Contains(r.Output, bashSentinel) {
		t.Fatalf("COREMETRY_ sırrı bash adımına sızdı:\n%s", r.Output)
	}
	if !strings.Contains(r.Output, "PATH=") {
		t.Fatalf("PATH bash adımına geçmedi:\n%s", r.Output)
	}
}

func TestExecuteBashPassthrough(t *testing.T) {
	t.Cleanup(func() { ConfigureBashEnv(nil) })
	t.Setenv("CM961_PROBE", "ok")
	t.Setenv("COREMETRY_SECRET_PROBE", bashSentinel)

	// Knob'suz: taban dışı ad geçmez.
	if r := executeBash(context.Background(), `echo "[$CM961_PROBE]"`, 5*time.Second); strings.TrimSpace(r.Output) != "[]" {
		t.Fatalf("knob'suz CM961_PROBE geçmemeli: %+v", r)
	}
	ConfigureBashEnv([]string{"CM961_PROBE"})
	if r := executeBash(context.Background(), `echo "$CM961_PROBE"`, 5*time.Second); r.Error != "" || strings.TrimSpace(r.Output) != "ok" {
		t.Fatalf("passthrough çalışmadı: %+v", r)
	}
	// COREMETRY_* knob'la bile açılmaz (parse reddeder; bashEnv sert reddeder).
	if _, rej := ConfigureBashEnv([]string{"COREMETRY_*", "COREMETRY_SECRET_PROBE"}); len(rej) != 2 {
		t.Fatalf("COREMETRY_ girdileri reddedilmeli: %q", rej)
	}
	if r := executeBash(context.Background(), "env", 5*time.Second); strings.Contains(r.Output, bashSentinel) {
		t.Fatalf("COREMETRY_* knob'la sızdı:\n%s", r.Output)
	}
}

func TestExecuteBashTimeoutKillsGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "m")
	start := time.Now()
	r := executeBash(context.Background(), "(sleep 1; echo alive > "+marker+") & sleep 30; true", 200*time.Millisecond)
	el := time.Since(start)
	if !strings.Contains(r.Error, "timed out") {
		t.Fatalf("zaman aşımı hatası bekleniyordu: %+v", r)
	}
	if limit := 200*time.Millisecond + bashWaitDelay + time.Second; el > limit {
		t.Fatalf("zaman aşımı %v sürdü (sınır %v)", el, limit)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("torun süreç zaman aşımından sağ çıktı (grup öldürülmedi)")
	}
}

func TestExecuteBashBackgroundSwept(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "m")
	r := executeBash(context.Background(), "(sleep 1; echo alive > "+marker+") >/dev/null 2>&1 & echo hi", 5*time.Second)
	if r.Error != "" || r.Output != "hi\n" {
		t.Fatalf("ayrık arka plan işi = %+v, want Output \"hi\\n\"", r)
	}

	// Boruyu tutan arka plan işi: WaitDelay sonra borular kapanır, adım başarılı.
	start := time.Now()
	r2 := executeBash(context.Background(), "sleep 30 & echo hi", 5*time.Second)
	el := time.Since(start)
	if r2.Error != "" || !strings.HasPrefix(r2.Output, "hi") || !strings.Contains(r2.Output, "background processes left running") {
		t.Fatalf("boru tutan arka plan işi = %+v", r2)
	}
	if limit := bashWaitDelay + time.Second; el > limit {
		t.Fatalf("boru tutan iş %v sürdü (sınır %v)", el, limit)
	}

	// v0.10.966 — WaitDelay adım süresini aşsa da (1 sn < 2 sn) /bin/sh 0 ile
	// bitti: adım başarılı + not, "timed out" DEĞİL (switch'te ErrWaitDelay önce).
	start = time.Now()
	r3 := executeBash(context.Background(), "sleep 30 & echo hi", time.Second)
	el = time.Since(start)
	if r3.Error != "" || !strings.HasPrefix(r3.Output, "hi") || !strings.Contains(r3.Output, "background processes left running") {
		t.Fatalf("kısa zaman aşımında boru tutan arka plan işi = %+v, want Error \"\" + not", r3)
	}
	if limit := bashWaitDelay + time.Second; el > limit {
		t.Fatalf("kısa zaman aşımında boru tutan iş %v sürdü (sınır %v)", el, limit)
	}
	// Gerçek zaman aşımı değişmedi: /bin/sh kendisi bitmiyor.
	if r4 := executeBash(context.Background(), "sleep 30 & sleep 30", 300*time.Millisecond); !strings.Contains(r4.Error, "timed out") {
		t.Fatalf("gerçek zaman aşımı = %+v, want \"timed out\"", r4)
	}

	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("arka plan işi adım sonunda süpürülmedi")
	}
}

func TestExecuteBashOutputCap(t *testing.T) {
	r := executeBash(context.Background(), `head -c 1000000 /dev/zero | tr '\0' a`, 10*time.Second)
	if r.Error != "" {
		t.Fatalf("1 MB çıktı hata verdi: %q", r.Error)
	}
	if max := maxOutput + len("\n…[truncated]"); len(r.Output) > max {
		t.Fatalf("çıktı %d bayt, tavan %d", len(r.Output), max)
	}
	if !strings.HasSuffix(r.Output, "[truncated]") {
		t.Fatalf("kırpma işareti yok: ...%q", r.Output[len(r.Output)-32:])
	}
}

// cappedOutput truncate() ile bayt-bayt aynı sonucu verir ve yazarı asla
// hatayla durdurmaz (alt süreç EPIPE almaz, boşaltılmaya devam eder).
func TestCappedOutputMatchesTruncate(t *testing.T) {
	for _, n := range []int{0, 1, maxOutput - 1, maxOutput, maxOutput + 1, 3 * maxOutput} {
		src := strings.Repeat("x", n)
		c := &cappedOutput{}
		for off := 0; off < len(src); off += 1000 {
			end := min(off+1000, len(src))
			if w, err := c.Write([]byte(src[off:end])); err != nil || w != end-off {
				t.Fatalf("n=%d Write = (%d, %v)", n, w, err)
			}
		}
		// v0.10.966 — tavan okuma sırasında da geçerli: tampon hiçbir n için maxOutput'u aşmaz
		if len(c.buf) > maxOutput {
			t.Fatalf("n=%d tamponda %d bayt, tavan %d", n, len(c.buf), maxOutput)
		}
		if got, want := c.String(), truncate(src); got != want {
			t.Fatalf("n=%d cappedOutput len %d, truncate len %d", n, len(got), len(want))
		}
	}
}
