//go:build linux

package prochard

import (
	"os"
	"strings"
	"syscall"
)

// landlockCreateRulesetVersion — LANDLOCK_CREATE_RULESET_VERSION (1U << 0),
// include/uapi/linux/landlock.h. golang.org/x/sys/unix bu sabiti taşır ama
// go.mod'da dolaylı; yalnız bir sabit + syscall numarası için doğrudan
// bağımlılığa yükseltilmedi (v0.10.970).
const landlockCreateRulesetVersion = 1 << 0

// LandlockABI — v0.10.970 — çekirdeğin desteklediği en yüksek Landlock ABI
// sürümünü (≥1) ya da *LandlockUnavailableError döner. YALNIZ ÖLÇÜM:
// landlock_create_ruleset(attr=NULL, size=0, flags=VERSION) fd açmaz, kural
// seti kurmaz, süreci kısıtlamaz; idempotenttir. Uyarı (belgeli, plan §5):
// varsayılanı KILL/TRAP olan özel bir Localhost seccomp profili bu çağrıda
// süreci öldürür — RuntimeDefault (restricted-v2 dahil) errno döner.
func LandlockABI() (int, error) {
	r, _, e := syscall.RawSyscall(sysLandlockCreateRuleset, 0, 0, landlockCreateRulesetVersion)
	return landlockResult(r, e)
}

// ProbeLandlock — v0.10.970 — boot satırı için LandlockABI + bağlam: çekirdek
// sürümü (/proc/sys/kernel/osrelease) ve seccomp modu (/proc/self/status;
// ikisi de 0444, dumpable=0 sonrası okunur). Okunamayan alan boş kalır.
func ProbeLandlock() LandlockReport {
	abi, err := LandlockABI()
	rep := LandlockReport{ABI: abi, Err: err}
	if b, e := os.ReadFile("/proc/sys/kernel/osrelease"); e == nil {
		rep.Kernel = strings.TrimSpace(string(b))
	}
	if b, e := os.ReadFile("/proc/self/status"); e == nil {
		rep.Seccomp = parseSeccompMode(string(b))
	}
	return rep
}
