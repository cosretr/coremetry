//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/prochard"
)

// v0.10.966 — env allowlist'i (bashEnv) `cat /proc/$PPID/environ` ile
// atlatılamaz: ebeveyn PR_SET_DUMPABLE=0 iken aynı-uid alt süreç ebeveynin
// exec-anı ortamını okuyamaz. Test ikilisi kendini yardımcı olarak yeniden
// çalıştırır, çünkü /proc/<pid>/environ os.Setenv'i DEĞİL exec-anı ortamını
// gösterir; sentinel yardımcının exec ortamına konur. "control" kipi
// sentinel'i GÖRMELİ (test gerçek vektörü ölçüyor), "harden" kipi görmemeli.
// root CAP_SYS_PTRACE taşır ve dumpable'ı aşar: root'ta atlanır (GitHub
// runner'ları root değildir, CI'da koşar).

func TestExecuteBashCannotReadParentEnviron(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root CAP_SYS_PTRACE ile dumpable=0'ı aşar; root olmayan kullanıcıyla koş")
	}
	run := func(mode string) string {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperParentEnviron$", "-test.count=1")
		cmd.Env = append(os.Environ(), "CM961_HELPER="+mode, "COREMETRY_SECRET_PROBE="+bashSentinel)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s yardımcısı başarısız: %v\n%s", mode, err, out)
		}
		if !strings.Contains(string(out), "OUT<<") {
			t.Fatalf("%s yardımcısı koşmadı:\n%s", mode, out)
		}
		return string(out)
	}

	control := run("control")
	if !strings.Contains(control, bashSentinel) {
		t.Fatalf("control: sentinel /proc/$PPID/environ'da görünmeli (vektör ölçülmüyor):\n%s", control)
	}

	harden := run("harden")
	if strings.Contains(harden, bashSentinel) {
		t.Fatalf("harden: dumpable=0'a rağmen ebeveyn ortamı okundu:\n%s", harden)
	}
	if !strings.Contains(harden, "Permission denied") && strings.Contains(harden, "ERR<<>>") {
		t.Fatalf("harden: ne 'Permission denied' ne adım hatası:\n%s", harden)
	}
	if !strings.Contains(harden, "EXE=ok") || !strings.Contains(harden, "FD=ok") {
		t.Fatalf("harden: süreç kendi /proc/self girdilerini okuyamıyor:\n%s", harden)
	}
}

// TestHelperParentEnviron yalnız yeniden çalıştırılan yardımcıdır.
func TestHelperParentEnviron(t *testing.T) {
	mode := os.Getenv("CM961_HELPER")
	if mode == "" {
		t.Skip("yalnız TestExecuteBashCannotReadParentEnviron yardımcısı")
	}
	if mode == "harden" {
		if err := prochard.DisableDumpable(); err != nil {
			t.Fatalf("DisableDumpable: %v", err)
		}
	}
	r := executeBash(context.Background(), `cat /proc/$PPID/environ | tr '\0' '\n'`, 5*time.Second)
	fmt.Printf("OUT<<%s>>ERR<<%s>>\n", r.Output, r.Error)
	exe, fd := "ok", "ok"
	if _, err := os.Executable(); err != nil {
		exe = "err:" + err.Error()
	}
	if _, err := os.ReadDir("/proc/self/fd"); err != nil {
		fd = "err:" + err.Error()
	}
	fmt.Printf("EXE=%s FD=%s\n", exe, fd)
}
