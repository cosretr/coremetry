//go:build linux

package prochard

import (
	"syscall"
	"testing"
)

// v0.10.966 — DisableDumpable sonrası çekirdek PR_GET_DUMPABLE=0 döner.
// Gerileme = runbook bash adımı `cat /proc/$PPID/environ` ile JWT secret'ı
// ve CH parolasını okur (env allowlist'i atlatılır).
func TestDisableDumpable(t *testing.T) {
	if !Supported {
		t.Fatal("linux'ta Supported=true olmalı")
	}
	if err := DisableDumpable(); err != nil {
		t.Fatalf("DisableDumpable: %v", err)
	}
	v, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
	if e != 0 || v != 0 {
		t.Fatalf("PR_GET_DUMPABLE = %d (errno %v), want 0", v, e)
	}
	// İdempotent: ikinci çağrı da başarılı.
	if err := DisableDumpable(); err != nil {
		t.Fatalf("ikinci DisableDumpable: %v", err)
	}
}
