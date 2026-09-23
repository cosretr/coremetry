package oracle

import (
	"regexp"
	"strings"
)

// podservice.go — v0.10.908 (operatör 2026-09-23: "instanceid kolonunda pod
// adları yazıyor, oradan da servis ismi çıkar; genellikle prod yazan kısma
// kadar servis ismi"). Hata satırını loglayan pod'un adı, trace Coremetry'de
// olmasa da servisi söyler. Türetme SAF; doğrulama çağıranda (yalnız
// Coremetry'de CANLI bir servis adı kabul edilir — uydurma servis yok).

var (
	// Deployment pod'u: <ad>-<replicaset hash 5-10>-<pod eki 5>.
	podDeploymentRe = regexp.MustCompile(`^(.+)-[a-z0-9]{5,10}-[a-z0-9]{5}$`)
	// StatefulSet pod'u: <ad>-<sıra>.
	podStatefulRe = regexp.MustCompile(`^(.+)-\d{1,4}$`)
)

// podServiceCandidates — SAF: pod adından aday servis adları, öncelik sırasıyla,
// tekrarsız. bsa-mobile-login-prod-7b9949bb74-l4bg5 →
// [bsa-mobile-login-prod, bsa-mobile-login]; …-prod-oneagent-55675dfc-xxxxx →
// [...-prod-oneagent, ...-prod, ...]. Pod biçimine uymayan değer (host adı:
// WMOBAPPP84) aday üretmez.
func podServiceCandidates(pod string) []string {
	p := strings.ToLower(strings.TrimSpace(pod))
	if p == "" {
		return nil
	}
	base := ""
	if m := podDeploymentRe.FindStringSubmatch(p); m != nil {
		base = m[1]
	} else if m := podStatefulRe.FindStringSubmatch(p); m != nil {
		base = m[1]
	}
	if base == "" || !strings.Contains(base, "-") {
		return nil
	}
	out := []string{base}
	add := func(s string) {
		s = strings.Trim(s, "-")
		if s == "" {
			return
		}
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	// "prod yazan kısma kadar": son "-prod" ile biten önek, sonra onsuz hâli.
	if i := strings.LastIndex(base, "-prod"); i > 0 {
		add(base[:i+len("-prod")])
		add(base[:i])
	}
	return out
}

// podService — SAF: adaylardan ilk canlı olan ("" = yok).
func podService(pod string, alive map[string]bool) string {
	if len(alive) == 0 {
		return ""
	}
	for _, c := range podServiceCandidates(pod) {
		if alive[c] {
			return c
		}
	}
	return ""
}
