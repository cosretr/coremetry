//go:build linux && !mips && !mipsle && !mips64 && !mips64le

package prochard

// sysLandlockCreateRuleset — v0.10.970 — landlock_create_ruleset syscall
// numarası. 5.1'den beri yeni syscall'lar tüm "generic" mimarilerde aynı
// numarayı alır (amd64, arm64, 386, arm, ppc64/le, riscv64, s390x, loong64:
// 444); yalnız mips ailesi farklı (landlock_nr_mips*_linux.go). Stdlib
// syscall paketi donduruldu, sabit çoğu mimaride yok.
const sysLandlockCreateRuleset = 444
