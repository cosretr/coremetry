package vmetrics

// envmatch.go — v0.10.1376 (CoSRE küme kıyası, VM yarısı): span'in deploy_env
// değeri ile VictoriaMetrics'teki ortam etiketinin (varsayılan
// deployment_environment_name) değeri her kurulumda AYNI yazılmıyor. Prod'da
// metrik etiketi küme önekini taşıyor ("prod-eu-a"), span tarafı taşıyabilir de
// taşımayabilir de ("eu-a"). Bu dosya istenen ortam adını CANLI etiket
// değerlerine karşı SAF olarak çözer.
//
// Kademe (istenen her ad için, ilk tutan kademe kazanır):
//
//  1. exact  — birebir aynı değer;
//  2. ci     — büyük/küçük harf duyarsız (EqualFold);
//  3. prefix — TEK TARAFLI tek önek: canlı değerin ya da istenen adın başındaki
//     TEK `^[A-Za-z0-9]+-` parçası atılınca öbürüyle EqualFold; kalan ≥3
//     karakter. İki taraf birden soyulmaz: "prod-eu-a" ile "dr-eu-a" EŞLEŞMEZ
//     (farklı kümeler); "eu-a" ile "eu-b" de (kalan "b" çok kısa).
//
// Kazanan kademenin TÜM değerleri döner — belirsizlik çağırana görünür, hiçbiri
// sessizce seçilmez ("eu-a" → {"dr-eu-a", "prod-eu-a"} ikisi birlikte).

import (
	"regexp"
	"sort"
	"strings"
)

// Eşleşme kademeleri — v0.10.1376.
const (
	EnvTierExact  = "exact"
	EnvTierCI     = "ci"
	EnvTierPrefix = "prefix"
	EnvTierNone   = "none"
)

// v0.10.1376 — girdi tavanları: istenen ≤6 ortam (sohbet envSetMax), canlı
// değer ≤1000 (VM etiket-değeri ucunun limiti).
const (
	envMatchWantMax  = 6
	envMatchLiveMax  = 1000
	envPrefixMinRest = 3
)

// EnvMatch — v0.10.1376: tek istenen adın çözümü. Values sıralı, tekil.
type EnvMatch struct {
	Want   string   `json:"want"`
	Values []string `json:"values,omitempty"`
	Tier   string   `json:"tier"`
}

// envPrefixRe — v0.10.1376: TEK önek parçası ("prod-", "dr-").
var envPrefixRe = regexp.MustCompile(`^[A-Za-z0-9]+-`)

// stripEnvPrefix — SAF (v0.10.1376): baştaki tek önek atılmış kalan; önek yoksa
// ya da kalan <3 karakterse ok=false.
func stripEnvPrefix(s string) (string, bool) {
	loc := envPrefixRe.FindStringIndex(s)
	if loc == nil {
		return "", false
	}
	rest := s[loc[1]:]
	if len([]rune(rest)) < envPrefixMinRest {
		return "", false
	}
	return rest, true
}

// EnvShortForm — SAF (v0.10.1380): ortam değerinin öneksiz kısa biçimi
// ("prod-eu-a" → "eu-a"); kural stripEnvPrefix'in KENDİSİ (tek önek parçası,
// kalan ≥3). CoSRE sohbetinin ortam anma çözücüsü (api/chat_env_mentions.go)
// aynı tanımı kullanır — metrik eşleşmesiyle sohbet çözümü ayrışmaz.
func EnvShortForm(s string) (string, bool) { return stripEnvPrefix(s) }

// envPrefixMatch — SAF (v0.10.1376): tek taraflı önek toleransı.
func envPrefixMatch(want, live string) bool {
	if r, ok := stripEnvPrefix(live); ok && strings.EqualFold(want, r) {
		return true
	}
	if r, ok := stripEnvPrefix(want); ok && strings.EqualFold(r, live) {
		return true
	}
	return false
}

// MatchEnvValue — SAF (v0.10.1376): tek adın kademeli çözümü.
func MatchEnvValue(want string, live []string) EnvMatch {
	m := EnvMatch{Want: want, Tier: EnvTierNone}
	w := strings.TrimSpace(want)
	if w == "" {
		return m
	}
	if len(live) > envMatchLiveMax {
		live = live[:envMatchLiveMax]
	}
	tiers := []struct {
		name string
		ok   func(v string) bool
	}{
		{EnvTierExact, func(v string) bool { return v == w }},
		{EnvTierCI, func(v string) bool { return strings.EqualFold(v, w) }},
		{EnvTierPrefix, func(v string) bool { return envPrefixMatch(w, v) }},
	}
	for _, t := range tiers {
		var vals []string
		for _, v := range live {
			if v != "" && t.ok(v) {
				vals = append(vals, v)
			}
		}
		if len(vals) > 0 {
			m.Tier, m.Values = t.name, sortedUnique(vals)
			return m
		}
	}
	return m
}

// EnvTierEnvPart — 2026-11-21: ortam etiketi `<env>-<cluster>` birleşik
// ("prod-clusterA"); istenen ortam ("prod") değerin ilk parçası.
const EnvTierEnvPart = "envpart"

// MatchEnvRole — SAF (2026-11-21, operatör: prod deployment_environment_name
// ortamla kümeyi birleştiriyor — "prod-clusterA", bazı serilerde yalnız küme
// "clusterB"; openshift_cluster_name "clusterA"). ORTAM terimi için
// MatchEnvValue'a ek kademe: canlı değer `<want>-<rest>` ise ve rest bilinen
// bir küme (clusters: küme etiketinin canlı değerleri, büyük/küçük harf
// duyarsız; boşsa rest ≥3 karakter yeter) → eşleşir; yalnız küme taşıyan
// değer (v) clusterEnv[v] (küme varlığının ortamı) want ise eşleşir. Birebir /
// harf duyarsız eşleşme VARSA envpart değerleri ona EKLENİR (ortam "prod"
// tüm prod kümelerini kapsar); prefix kademesi (küme adı istenmiş) aynen.
func MatchEnvRole(want string, live, clusters []string, clusterEnv map[string]string) EnvMatch {
	m := MatchEnvValue(want, live)
	if m.Tier == EnvTierPrefix {
		return m
	}
	w := strings.TrimSpace(want)
	if w == "" {
		return m
	}
	known := make(map[string]bool, len(clusters))
	for _, c := range clusters {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			known[c] = true
		}
	}
	var vals []string
	for _, v := range live {
		if i := strings.IndexByte(v, '-'); i > 0 && strings.EqualFold(v[:i], w) {
			rest := v[i+1:]
			if (len(known) == 0 && len([]rune(rest)) >= envPrefixMinRest) || known[strings.ToLower(rest)] {
				vals = append(vals, v)
			}
			continue
		}
		if e, ok := clusterEnv[strings.ToLower(v)]; ok && strings.EqualFold(e, w) {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return m
	}
	if m.Tier == EnvTierNone {
		m.Tier = EnvTierEnvPart
	}
	m.Values = sortedUnique(append(m.Values, vals...))
	return m
}

// ClusterEnvMap — SAF (follow-up 2026-11-21): cluster varlığının span
// ortamlarından MatchEnvRole'un clusterEnv haritası (küçük harf küme → ortam).
// Çift (küme, deploy_env): deploy_env küme adının kendisiyse bilgi yok (atla);
// `<env>-<küme>` biçimindeyse ortam = önek; aksi hâlde deploy_env aynen. Bir
// küme birden çok ortama düşerse BELİRSİZ → haritaya girmez (yanlış eşleme
// yerine eşleme yok).
func ClusterEnvMap(pairs [][2]string) map[string]string {
	envs := map[string]map[string]bool{}
	for _, p := range pairs {
		c, e := strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
		if c == "" || e == "" || strings.EqualFold(c, e) {
			continue
		}
		env := e
		if i := strings.IndexByte(e, '-'); i > 0 && strings.EqualFold(e[i+1:], c) {
			env = e[:i]
		}
		k := strings.ToLower(c)
		if envs[k] == nil {
			envs[k] = map[string]bool{}
		}
		envs[k][strings.ToLower(env)] = true
	}
	out := map[string]string{}
	for c, set := range envs {
		if len(set) != 1 {
			continue
		}
		for e := range set {
			out[c] = e
		}
	}
	return out
}

// MatchEnvValues — SAF (v0.10.1376): istenen her ad için MatchEnvValue, girdi
// sırasıyla; ≤6 ad.
func MatchEnvValues(want, live []string) []EnvMatch {
	if len(want) > envMatchWantMax {
		want = want[:envMatchWantMax]
	}
	out := make([]EnvMatch, 0, len(want))
	for _, w := range want {
		out = append(out, MatchEnvValue(w, live))
	}
	return out
}

// EnvMatcher — SAF (v0.10.1376): PromQL seçici parçası. Tek değer →
// `label="v"`; birden çok → `label=~"v1|v2"` (her değer regexp.QuoteMeta,
// sıralı, tekil; VM regex'i tam çapalı); değer yoksa "". Dize kaçışı frontend
// lib/httpMetrics.ts promStr ile aynı (yalnız \ ve ") — altın dosya pinler.
func EnvMatcher(label string, values []string) string {
	vals := sortedUnique(values)
	switch len(vals) {
	case 0:
		return ""
	case 1:
		return label + "=" + PromStrTS(vals[0])
	}
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = regexp.QuoteMeta(v)
	}
	return label + "=~" + PromStrTS(strings.Join(q, "|"))
}

// PromStrTS — SAF (v0.10.1376): frontend promStr'ın Go ikizi — `"` ile sarar,
// yalnız \ ve " kaçışlanır (quotePromString'den farklı olarak \n'e dokunmaz:
// iki dilin ürettiği sorgu bayt bayt aynı olmalı).
func PromStrTS(v string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

// sortedUnique — SAF: boşları atar, sıralar, tekilleştirir.
func sortedUnique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
