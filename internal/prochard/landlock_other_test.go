//go:build !linux

package prochard

import (
	"errors"
	"testing"
)

// v0.10.970 — linux dışında Landlock yok: yoklama syscall yapmadan
// unsupported-os döner (darwin geliştirme ortamı boot satırı).
func TestLandlockABIUnsupportedOS(t *testing.T) {
	abi, err := LandlockABI()
	var u *LandlockUnavailableError
	if abi != 0 || !errors.As(err, &u) || u.Reason != LandlockUnsupportedOS || u.Errno != 0 {
		t.Fatalf("LandlockABI() = (%d, %v), want (0, unsupported-os)", abi, err)
	}
	if got, want := ProbeLandlock().String(), "unavailable (unsupported-os)"; got != want {
		t.Fatalf("ProbeLandlock().String() = %q, want %q", got, want)
	}
}
