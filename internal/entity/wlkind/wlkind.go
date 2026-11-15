// Package wlkind — iş yükü kimliğinin TEK kanonikleştirmesi (v0.10.2232,
// Entity S2; docs/architecture/entity-autodiscovery-plan.md §2.1).
//
// Önceden aynı sezgisel üç yerde, üç ayrı alfabeyle yaşıyordu (entity
// DerivedWorkload, thanos stripPodSuffixes, FE podWorkloadName); aynı
// iş yükü kaynağa göre Deployment / ReplicaSet / Derived kimliği alıyordu.
// Bu paket yaprak (hiçbir iç paketi import etmez): entity, thanos ve
// chstore aynı fonksiyonu çağırır. Yetkili kaynak (KSM sahiplik zinciri)
// her zaman önce gelir; pod adı yalnız sahibi bilinmeyen pod içindir.
package wlkind

import "strings"

// Kanonik türler. Derived: pod adının kısa sonekinden tür ayırt
// edilemiyor (DaemonSet ya da Job) — yalan söylemek yerine.
const (
	Deployment  = "Deployment"
	StatefulSet = "StatefulSet"
	DaemonSet   = "DaemonSet"
	ReplicaSet  = "ReplicaSet"
	Derived     = "Derived"
)

// k8s rand.SafeEncodeString alfabesi (sesli harf yok) + eski fixture'lar
// için 0, 1, 3.
const safeAlphabet = "0123456789bcdfghjklmnpqrstvwxz"

func allIn(s, set string) bool {
	for _, c := range s {
		if !strings.ContainsRune(set, c) {
			return false
		}
	}
	return true
}

// isRand5 — pod'un 5 karakterlik rasgele soneki.
func isRand5(s string) bool { return len(s) == 5 && allIn(s, safeAlphabet) }

// isRSHash — pod-template-hash: güvenli alfabe 5–10 ya da eski hex 8–10.
// "canary" gibi sözcükler (sesli harf, hex dışı harf) elenir.
func isRSHash(s string) bool {
	switch {
	case len(s) >= 5 && len(s) <= 10 && allIn(s, safeAlphabet):
		return true
	case len(s) >= 8 && len(s) <= 10 && allIn(s, "0123456789abcdef"):
		return true
	}
	return false
}

func isOrdinal(s string) bool { return len(s) >= 1 && len(s) <= 4 && allIn(s, "0123456789") }

// FromPod — sahibi bilinmeyen pod'un iş yükü, yalnız pod adından:
//
//	<ad>-<rs-hash>-<rand5> → Deployment <ad>
//	<ad>-<0..9999>         → StatefulSet <ad>
//	<ad>-<rand5>           → Derived <ad> (DaemonSet ya da Job)
//
// Tutmayan ad → ok=false (tekil pod; iş yükü uydurulmaz).
func FromPod(pod string) (kind, name string, ok bool) {
	i := strings.LastIndexByte(pod, '-')
	if i <= 0 || i == len(pod)-1 {
		return "", "", false
	}
	head, last := pod[:i], pod[i+1:]
	if isOrdinal(last) {
		return StatefulSet, head, true
	}
	if !isRand5(last) {
		return "", "", false
	}
	if j := strings.LastIndexByte(head, '-'); j > 0 && isRSHash(head[j+1:]) {
		return Deployment, head[:j], true
	}
	return Derived, head, true
}

// Canonical — (tür, ad) → kanonik (tür, ad). Sahibi okunamamış ReplicaSet
// "<ad>-<rs-hash>" bir Deployment'ın revizyonudur → Deployment <ad>.
// Diğer türler olduğu gibi (Deployment, StatefulSet, DaemonSet, CronJob,
// Job, Rollout ve CRD türleri).
func Canonical(kind, name string) (string, string) {
	if kind == ReplicaSet {
		if i := strings.LastIndexByte(name, '-'); i > 0 && isRSHash(name[i+1:]) {
			return Deployment, name[:i]
		}
	}
	return kind, name
}

// Replaceable — bu kimlik yetkili bir kaynak gelince yerini kanonik
// kimliğe bırakır (same_as): Derived ve "<ad>-<rs-hash>" ReplicaSet.
// Hash'siz sahipsiz ReplicaSet kendi başına iş yüküdür, bırakmaz.
func Replaceable(kind, name string) bool {
	if kind == Derived {
		return true
	}
	k, _ := Canonical(kind, name)
	return kind == ReplicaSet && k != ReplicaSet
}
