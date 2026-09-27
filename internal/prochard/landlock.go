package prochard

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

// v0.10.970 — Runbook yalıtımı v2, adım 1: YALNIZ ÖLÇÜM (operatör: "Önerilerine
// evet", 2026-09-27 — önce prod çekirdek desteğini ölç, sonra karar ver;
// docs/plans/runbook-isolation-v2.md). LandlockABI hiçbir kural seti KURMAZ ve
// süreci KISITLAMAZ: landlock_create_ruleset(NULL, 0, VERSION) yalnız
// çekirdeğin desteklediği en yüksek ABI'yi sorar, fd açmaz. Bu dosya platformdan
// bağımsız, saf parçaları taşır (errno → neden eşlemesi, boot satırı biçimi,
// /proc/self/status ayrıştırma); syscall landlock_linux.go'da.

// LandlockReason — Landlock'un neden kullanılamadığı (tipli, log'a basılır).
type LandlockReason string

const (
	// LandlockNoKernelSupport — ENOSYS: çekirdek Landlock'suz derlenmiş ya da
	// 5.13'ten eski. Bir seccomp filtresi altında ENOSYS runtime profilinin
	// varsayılan errno'su da olabilir (boot satırı bunu ayrıca söyler).
	LandlockNoKernelSupport LandlockReason = "kernel-lacks-landlock"
	// LandlockDisabledAtBoot — EOPNOTSUPP: çekirdekte var ama boot'ta
	// kapalı (lsm= listesinde landlock yok).
	LandlockDisabledAtBoot LandlockReason = "disabled-at-boot"
	// LandlockBlockedByRuntime — EPERM: container runtime'ın seccomp
	// profili syscall'ı reddediyor.
	LandlockBlockedByRuntime LandlockReason = "blocked-by-runtime"
	// LandlockUnsupportedOS — linux dışı (darwin geliştirme); syscall yapılmaz.
	LandlockUnsupportedOS LandlockReason = "unsupported-os"
	// LandlockUnexpectedErrno — belgelenmemiş errno ya da anlamsız dönüş;
	// linux'ta bir bulgudur, eşleme genişletilmeli.
	LandlockUnexpectedErrno LandlockReason = "unexpected-errno"
)

// LandlockUnavailableError — LandlockABI'nin tipli hatası. Errno, syscall
// yapılmadıysa (linux dışı) ya da çekirdek errno'suz anlamsız bir değer
// döndüyse 0'dır.
type LandlockUnavailableError struct {
	Reason LandlockReason
	Errno  syscall.Errno
}

func (e *LandlockUnavailableError) Error() string {
	if e.Errno == 0 {
		return "landlock unavailable: " + string(e.Reason)
	}
	return fmt.Sprintf("landlock unavailable: %s (%s)", e.Reason, errnoName(e.Errno))
}

// landlockMaxPlausibleABI — üst akıl sınırı: ABI 2021'de 1'den başladı,
// yılda ~1-2 artar. Bunun üstü errno'suz bir çöp dönüştür, ABI sayılmaz.
const landlockMaxPlausibleABI = 1 << 10

// landlockResult — v0.10.970 — landlock_create_ruleset(VERSION) dönüşünü
// (ret, errno) ABI'ye ya da tipli nedene çevirir. Saf: linux ve darwin'de
// aynı tabloyla test edilir.
func landlockResult(ret uintptr, errno syscall.Errno) (int, error) {
	if errno != 0 {
		return 0, &LandlockUnavailableError{Reason: landlockReasonFor(errno), Errno: errno}
	}
	if ret < 1 || ret > landlockMaxPlausibleABI {
		return 0, &LandlockUnavailableError{Reason: LandlockUnexpectedErrno}
	}
	return int(ret), nil
}

// landlockReasonFor — errno → neden. Linux'ta ENOTSUP == EOPNOTSUPP (95);
// iki sabit ayrı case olarak yazılamaz (linux'ta derleme hatası).
func landlockReasonFor(errno syscall.Errno) LandlockReason {
	switch errno {
	case syscall.ENOSYS:
		return LandlockNoKernelSupport
	case syscall.EOPNOTSUPP:
		return LandlockDisabledAtBoot
	case syscall.EPERM:
		return LandlockBlockedByRuntime
	default:
		return LandlockUnexpectedErrno
	}
}

// errnoName — log için sembolik ad; bilinmeyen errno sayıyla basılır
// (errno.Error() metni platforma göre değişir, satır sabit kalmalı).
func errnoName(e syscall.Errno) string {
	switch e {
	case syscall.ENOSYS:
		return "ENOSYS"
	case syscall.EOPNOTSUPP:
		return "EOPNOTSUPP"
	case syscall.EPERM:
		return "EPERM"
	default:
		return fmt.Sprintf("errno %d", int(e))
	}
}

// LandlockReport — v0.10.970 — boot ölçümü: ABI ya da neden, artı yorum için
// bağlam (çekirdek sürümü, seccomp modu). String() main.go'daki
// "[prochard] landlock: …" satırının gövdesidir; operatör bu satırı prod'dan
// geri yapıştırır, biçim test ile sabitlenmiştir.
type LandlockReport struct {
	ABI     int    // ≥1 kullanılabilirse
	Err     error  // kullanılamıyorsa *LandlockUnavailableError
	Kernel  string // /proc/sys/kernel/osrelease; bilinmiyorsa ""
	Seccomp string // "disabled" | "strict" | "filter" | "mode-N"; bilinmiyorsa ""
}

func (r LandlockReport) String() string {
	var b strings.Builder
	if r.Err == nil {
		fmt.Fprintf(&b, "abi=%d", r.ABI)
	} else {
		b.WriteString("unavailable (")
		var u *LandlockUnavailableError
		if errors.As(r.Err, &u) {
			b.WriteString(string(u.Reason))
			if u.Errno != 0 {
				b.WriteString(", " + errnoName(u.Errno))
			}
			// ENOSYS + seccomp filtresi belirsizdir: runtime profili listede
			// olmayan syscall'a ENOSYS dönebilir. Çekirdek sürümüyle birlikte
			// okunmalı (docs/plans/runbook-isolation-v2.md §5).
			if u.Reason == LandlockNoKernelSupport && r.Seccomp == "filter" {
				b.WriteString("; a seccomp filter may also answer ENOSYS")
			}
		} else {
			b.WriteString(r.Err.Error())
		}
		b.WriteString(")")
	}
	if r.Kernel != "" {
		b.WriteString(" kernel=" + r.Kernel)
	}
	if r.Seccomp != "" {
		b.WriteString(" seccomp=" + r.Seccomp)
	}
	return b.String()
}

// parseSeccompMode — /proc/self/status "Seccomp:" alanı: 0 kapalı, 1 strict,
// 2 filter. "Seccomp_filters:" (5.9+) mod DEĞİLDİR. Alan yoksa (çekirdek
// CONFIG_SECCOMP'suz) "".
func parseSeccompMode(status string) string {
	for _, line := range strings.Split(status, "\n") {
		v, ok := strings.CutPrefix(line, "Seccomp:")
		if !ok {
			continue
		}
		switch v = strings.TrimSpace(v); v {
		case "0":
			return "disabled"
		case "1":
			return "strict"
		case "2":
			return "filter"
		case "":
			return ""
		default:
			return "mode-" + v
		}
	}
	return ""
}
