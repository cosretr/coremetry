//go:build !linux

package prochard

// LandlockABI — v0.10.970 — linux dışında Landlock yok; syscall yapılmaz.
func LandlockABI() (int, error) {
	return 0, &LandlockUnavailableError{Reason: LandlockUnsupportedOS}
}

// ProbeLandlock — v0.10.970 — linux dışı: çekirdek/seccomp bağlamı yok.
func ProbeLandlock() LandlockReport {
	abi, err := LandlockABI()
	return LandlockReport{ABI: abi, Err: err}
}
