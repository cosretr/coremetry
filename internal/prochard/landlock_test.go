package prochard

import (
	"errors"
	"syscall"
	"testing"
)

// v0.10.970 — Landlock ölçüm yoklaması: errno → tipli neden eşlemesi saf
// fonksiyondur (landlockResult). Gerileme = boot satırı prod'da yanlış nedeni
// basar (ör. seccomp EPERM'i "çekirdekte yok" diye okunur) ve runbook
// yalıtımı v2 kararı yanlış ölçüme dayanır.
func TestLandlockResultReasonMapping(t *testing.T) {
	cases := []struct {
		name    string
		ret     uintptr
		errno   syscall.Errno
		wantABI int
		reason  LandlockReason // "" = kullanılabilir
	}{
		{"abi 1 (5.13)", 1, 0, 1, ""},
		{"abi 6", 6, 0, 6, ""},
		{"ENOSYS → kernel lacks it", 0, syscall.ENOSYS, 0, LandlockNoKernelSupport},
		{"EOPNOTSUPP → disabled at boot", 0, syscall.EOPNOTSUPP, 0, LandlockDisabledAtBoot},
		{"EPERM → blocked by runtime", 0, syscall.EPERM, 0, LandlockBlockedByRuntime},
		{"EINVAL → unexpected", 0, syscall.EINVAL, 0, LandlockUnexpectedErrno},
		{"ret 0 without errno → unexpected (ABI starts at 1)", 0, 0, 0, LandlockUnexpectedErrno},
		// -errno'yu geri çeviren taşınamaz bir yol olursa ret dev bir sayı olur;
		// ABI olarak okunmamalı.
		{"absurd ret → unexpected", ^uintptr(0), 0, 0, LandlockUnexpectedErrno},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abi, err := landlockResult(c.ret, c.errno)
			if c.reason == "" {
				if err != nil || abi != c.wantABI {
					t.Fatalf("got abi=%d err=%v, want abi=%d nil", abi, err, c.wantABI)
				}
				return
			}
			if abi != 0 {
				t.Fatalf("abi = %d on failure, want 0", abi)
			}
			var u *LandlockUnavailableError
			if !errors.As(err, &u) {
				t.Fatalf("err = %T %v, want *LandlockUnavailableError", err, err)
			}
			if u.Reason != c.reason {
				t.Fatalf("reason = %q, want %q", u.Reason, c.reason)
			}
			if u.Errno != c.errno {
				t.Fatalf("errno = %v, want %v", u.Errno, c.errno)
			}
		})
	}
}

// v0.10.970 — boot satırı operatörün prod'dan geri yapıştıracağı ölçümdür;
// biçim sabit kalmalı ("abi=N" ya da "unavailable (<neden>)").
func TestLandlockReportString(t *testing.T) {
	unavail := func(r LandlockReason, e syscall.Errno) error {
		return &LandlockUnavailableError{Reason: r, Errno: e}
	}
	cases := []struct {
		name string
		rep  LandlockReport
		want string
	}{
		{"available", LandlockReport{ABI: 4, Kernel: "6.8.0-synthetic", Seccomp: "filter"},
			"abi=4 kernel=6.8.0-synthetic seccomp=filter"},
		{"available, no context", LandlockReport{ABI: 1},
			"abi=1"},
		{"disabled at boot", LandlockReport{Err: unavail(LandlockDisabledAtBoot, syscall.EOPNOTSUPP), Kernel: "5.14.0-synthetic", Seccomp: "filter"},
			"unavailable (disabled-at-boot, EOPNOTSUPP) kernel=5.14.0-synthetic seccomp=filter"},
		{"seccomp EPERM", LandlockReport{Err: unavail(LandlockBlockedByRuntime, syscall.EPERM), Kernel: "6.1.0-synthetic", Seccomp: "filter"},
			"unavailable (blocked-by-runtime, EPERM) kernel=6.1.0-synthetic seccomp=filter"},
		// ENOSYS bir seccomp filtresi altında runtime profilinin varsayılan
		// errno'su da olabilir — satır bunu söylemeli, yoksa operatör
		// "çekirdek eski" diye yanlış karar verir.
		{"ENOSYS under seccomp filter", LandlockReport{Err: unavail(LandlockNoKernelSupport, syscall.ENOSYS), Kernel: "5.14.0-synthetic", Seccomp: "filter"},
			"unavailable (kernel-lacks-landlock, ENOSYS; a seccomp filter may also answer ENOSYS) kernel=5.14.0-synthetic seccomp=filter"},
		{"ENOSYS without seccomp", LandlockReport{Err: unavail(LandlockNoKernelSupport, syscall.ENOSYS), Kernel: "5.10.0-synthetic", Seccomp: "disabled"},
			"unavailable (kernel-lacks-landlock, ENOSYS) kernel=5.10.0-synthetic seccomp=disabled"},
		{"unexpected errno", LandlockReport{Err: unavail(LandlockUnexpectedErrno, syscall.EINVAL), Kernel: "6.8.0-synthetic"},
			"unavailable (unexpected-errno, errno 22) kernel=6.8.0-synthetic"},
		{"unsupported os", LandlockReport{Err: unavail(LandlockUnsupportedOS, 0)},
			"unavailable (unsupported-os)"},
		{"foreign error", LandlockReport{Err: errors.New("boom")},
			"unavailable (boom)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rep.String(); got != c.want {
				t.Fatalf("String()\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// v0.10.970 — /proc/self/status "Seccomp:" alanı (0/1/2). "Seccomp_filters:"
// satırı (5.9+) mod sanılmamalı.
func TestParseSeccompMode(t *testing.T) {
	cases := []struct {
		name, status, want string
	}{
		{"filter", "Name:\tcoremetry\nNoNewPrivs:\t1\nSeccomp:\t2\nSeccomp_filters:\t1\n", "filter"},
		{"disabled", "Seccomp:\t0\nSeccomp_filters:\t0\n", "disabled"},
		{"strict", "Seccomp:\t1\n", "strict"},
		{"filters line only", "Seccomp_filters:\t3\n", ""},
		{"no seccomp support", "Name:\tcoremetry\nState:\tR (running)\n", ""},
		{"unknown mode", "Seccomp:\t7\n", "mode-7"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseSeccompMode(c.status); got != c.want {
				t.Fatalf("parseSeccompMode = %q, want %q", got, c.want)
			}
		})
	}
}
