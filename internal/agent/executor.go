// Package agent runs the runbook agent loop: it claims automated runbook
// steps (http / javascript / bash) from the API, executes them in-process, and
// posts the result back. The loop runs in COREMETRY_MODE=agent pods AND in the
// default monolithic `all` pod (main.go `if mode.agent`), never in the
// api/ingest/worker-only roles. Only the distributed agent role gives the
// operator a dedicated, non-root, restricted pod as the blast radius. (v0.7.4)
//
// v0.10.966 — bash adımı artık ebeveynin ortamını devralmaz: minimal env
// allowlist'i (bashenv.go), kendi süreç grubu (zaman aşımında ve adım
// sonunda TÜM grup öldürülür) ve dumpable=0 ebeveyn (internal/prochard;
// /proc/$PPID/environ kapalı). Dosyalar (/app/config.yaml, bağlı sır
// dosyaları, SA token) ve ağ adımdan HÂLÂ erişilebilir; gerçek yalıtım
// distributed agent rolüdür.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/dop251/goja"
)

// AutomatedStep is the minimal payload an agent needs to run one step. It is
// the agent-facing projection of a runbook StepState (kind + the kind's
// payload), decoupled from the chstore type so the agent package stays
// dependency-light.
type AutomatedStep struct {
	Kind      string            `json:"kind"` // http | javascript | bash
	URL       string            `json:"url,omitempty"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	Script    string            `json:"script,omitempty"`
	Command   string            `json:"command,omitempty"`
	TimeoutMs int               `json:"timeoutMs,omitempty"`
}

// StepResult is the outcome of executing one automated step.
type StepResult struct {
	Output string `json:"output"`          // stdout / return value / HTTP status+body (truncated)
	Error  string `json:"error,omitempty"` // empty = success
}

const (
	maxOutput      = 16 * 1024
	defaultTimeout = 10 * time.Second
	maxTimeout     = 5 * time.Minute
)

// Execute dispatches a step to the right executor. Unknown kinds return an
// error result (never panic) so a malformed step can't wedge the agent.
func Execute(ctx context.Context, s AutomatedStep) StepResult {
	to := stepTimeout(s.TimeoutMs)
	switch s.Kind {
	case "http":
		return executeHTTP(ctx, s, to)
	case "javascript":
		return executeJavaScript(s.Script, to)
	case "bash":
		return executeBash(ctx, s.Command, to)
	default:
		return StepResult{Error: fmt.Sprintf("agent cannot execute step kind %q", s.Kind)}
	}
}

func stepTimeout(ms int) time.Duration {
	if ms <= 0 {
		return defaultTimeout
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxTimeout {
		return maxTimeout
	}
	return d
}

func truncate(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n…[truncated]"
}

func executeHTTP(ctx context.Context, s AutomatedStep, to time.Duration) StepResult {
	method := strings.ToUpper(strings.TrimSpace(s.Method))
	if method == "" {
		method = http.MethodGet
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	var bodyR io.Reader
	if s.Body != "" {
		bodyR = strings.NewReader(s.Body)
	}
	req, err := http.NewRequestWithContext(cctx, method, s.URL, bodyR)
	if err != nil {
		return StepResult{Error: err.Error()}
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return StepResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxOutput))
	res := StepResult{Output: truncate(fmt.Sprintf("HTTP %d %s\n%s", resp.StatusCode, resp.Status, string(b)))}
	if resp.StatusCode >= 400 {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return res
}

// executeJavaScript runs script in a goja VM. goja installs NO host bindings
// by default — there is no require / fs / net / process / os, so the script is
// a pure-ECMAScript compute sandbox that cannot touch the agent's filesystem,
// network, env, or the host process. The only escape we guard is unbounded
// run time, via a hard wall-clock Interrupt.
func executeJavaScript(script string, to time.Duration) StepResult {
	vm := goja.New()
	timer := time.AfterFunc(to, func() { vm.Interrupt("execution timeout") })
	defer timer.Stop()
	v, err := vm.RunString(script)
	if err != nil {
		return StepResult{Error: err.Error()}
	}
	out := ""
	if v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		out = v.String()
	}
	return StepResult{Output: truncate(out)}
}

// v0.10.966 — boru tutan torun için üst sınır: /bin/sh çıktıktan (ya da
// zaman aşımından) sonra borular en geç bu kadar açık kalır.
const bashWaitDelay = 2 * time.Second

// executeBash — /bin/sh -c command. v0.10.966: ortam bashEnv allowlist'i;
// kendi süreç grubu (Setpgid) — zaman aşımında Cancel TÜM grubu öldürür
// (eskiden yalnız /bin/sh ölürdü, `sleep 999 &` torunu boruyu tutup
// CombinedOutput'u kilitlerdi); Wait'ten sonra grup koşulsuz süpürülür
// (adımın arka plan işleri adım sonunda ölür); WaitDelay boru asılmasını
// sınırlar; cappedOutput okuma sırasında belleği sınırlar (görünen çıktı
// truncate() ile aynı).
func executeBash(ctx context.Context, command string, to time.Duration) StepResult {
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", command)
	cmd.Env = bashEnv(os.Environ(), currentBashPolicy())            // allowlist
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}           // kendi süreç grubu
	cmd.Cancel = func() error { return killGroup(cmd.Process.Pid) } // zaman aşımı: TÜM grup
	cmd.WaitDelay = bashWaitDelay
	out := &cappedOutput{}
	cmd.Stdout, cmd.Stderr = out, out // tek pipe, CombinedOutput sırası
	err := cmd.Start()
	if err == nil {
		pgid := cmd.Process.Pid
		err = cmd.Wait()
		_ = killGroup(pgid) // süpürme: adımın arka plan işleri
	}
	res := StepResult{Output: out.String()}
	switch {
	// v0.10.966 — ErrWaitDelay ÖNCE: yalnız /bin/sh kendi başına 0 ile
	// çıktıysa döner (gerçek zaman aşımında Cancel grubu öldürür →
	// ExitError/ctx.Err()). Boruyu tutan iş WaitDelay'i adım süresinin
	// ötesine taşısa da adım başarılı + not; yoksa timeoutMs < ~bitiş+2sn
	// olan adım yanlışlıkla "timed out" sayılırdı.
	case errors.Is(err, exec.ErrWaitDelay):
		res.Output += "\n[agent] background processes left running by the command were terminated"
	case cctx.Err() == context.DeadlineExceeded:
		res.Error = "command timed out after " + to.String()
	case err != nil:
		res.Error = err.Error()
	}
	return res
}

// killGroup — v0.10.966: süreç grubunun TAMAMINA SIGKILL. ESRCH (grup zaten
// yok) os.ErrProcessDone'a çevrilir; böylece Cancel temiz bir çıkışı hataya
// dönüştürmez. Setpgid + Kill darwin ve linux'ta var (build tag gerekmez;
// release yalnız linux/amd64).
func killGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

// cappedOutput — v0.10.966: ilk maxOutput baytı tutar, taşmada truncated'ı
// işaretler. Write HER ZAMAN len(p), nil döner: alt süreç EPIPE almaz ve
// boşaltılmaya devam eder. Stdout ve Stderr'e AYNI işaretçi verilir, os/exec
// tek boru + tek kopyalayıcı goroutine kullanır (eşzamanlı yazım yok).
type cappedOutput struct {
	buf       []byte
	truncated bool
}

func (c *cappedOutput) Write(p []byte) (int, error) {
	room := maxOutput - len(c.buf)
	switch {
	case len(p) <= room:
		c.buf = append(c.buf, p...)
	default:
		if room > 0 {
			c.buf = append(c.buf, p[:room]...)
		}
		c.truncated = true
	}
	return len(p), nil
}

// String — truncate() ile bayt-bayt aynı biçim.
func (c *cappedOutput) String() string {
	if c.truncated {
		return string(c.buf) + "\n…[truncated]"
	}
	return string(c.buf)
}
