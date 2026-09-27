//go:build linux

package prochard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// v0.10.970 — gerçek çekirdekte yoklama: ya ABI ≥1 ya da bilinen üç tipli
// nedenden biri döner. unexpected-errno ya da unsupported-os linux'ta bir
// bulgudur (errno eşlemesi eksik). Yoklama hiçbir kural seti UYGULAMAZ:
// ardından dosya okuma/yazma değişmeden çalışır.
func TestLandlockABIProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before-probe")
	if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}

	abi, err := LandlockABI()
	if err == nil {
		if abi < 1 {
			t.Fatalf("abi = %d with nil error, want ≥1", abi)
		}
		t.Logf("landlock abi=%d", abi)
	} else {
		var u *LandlockUnavailableError
		if !errors.As(err, &u) {
			t.Fatalf("err = %T %v, want *LandlockUnavailableError", err, err)
		}
		switch u.Reason {
		case LandlockNoKernelSupport, LandlockDisabledAtBoot, LandlockBlockedByRuntime:
			t.Logf("landlock unavailable: %v", err)
		default:
			t.Fatalf("linux reason = %q (errno %d), want one of the typed kernel/boot/runtime reasons", u.Reason, u.Errno)
		}
		if abi != 0 {
			t.Fatalf("abi = %d on failure, want 0", abi)
		}
	}

	// İdempotent ve yan etkisiz: ikinci yoklama aynı sonucu verir, dosya
	// sistemi erişimi kısıtlanmamıştır.
	abi2, err2 := LandlockABI()
	if abi2 != abi || (err == nil) != (err2 == nil) {
		t.Fatalf("second probe = (%d, %v), first = (%d, %v)", abi2, err2, abi, err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("read after probe: %v (probe must not enforce a ruleset)", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "after-probe"), []byte("synthetic"), 0o600); err != nil {
		t.Fatalf("write after probe: %v (probe must not enforce a ruleset)", err)
	}

	rep := ProbeLandlock()
	if rep.ABI != abi {
		t.Fatalf("ProbeLandlock().ABI = %d, want %d", rep.ABI, abi)
	}
	if rep.Kernel == "" {
		t.Fatal("ProbeLandlock().Kernel empty: /proc/sys/kernel/osrelease unreadable")
	}
	t.Logf("boot line tail: %s", rep)
}
