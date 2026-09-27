//go:build linux

// Package prochard — süreç sertleştirme (v0.10.966). Coremetry sürecini
// aynı-uid alt süreçlere (runbook bash adımı, MCP stdio sunucuları) karşı
// kapatır: env allowlist'i ancak ebeveynin /proc/<pid>/environ'u da
// okunamıyorsa bir sınırdır.
package prochard

import "syscall"

// DisableDumpable — v0.10.966 — PR_SET_DUMPABLE=0: aynı-uid alt süreç
// /proc/<pid>/environ, mem, fd, ptrace ile ebeveyne erişemez.
// execve dumpable'ı sıfırlar; alt süreçler (ps, /proc/self) etkilenmez.
// Dumpable adres-alanı başınadır: main başladıktan sonra her an çağrılabilir.
// Süreç kendi /proc/self girdilerine (exe, fd, maps) erişmeye devam eder;
// yalnız 0400 dosyalar (/proc/self/environ, /proc/self/io) kapanır — repo
// bunları okumaz, Go runtime'ın okumaları main'den öncedir.
func DisableDumpable() error {
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); e != 0 {
		return e
	}
	return nil
}

// Supported — bu platformda DisableDumpable gerçek bir etki yapar.
const Supported = true
