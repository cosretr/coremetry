package argocd

// discover.go — v0.10.957 — Argo CD instance keşfinin SAF yarısı (P1.4
// salt-okunur probe; audit §5.1–5.2, §11 H1.1/H1.2; annex §7.2 "öner,
// asla otomatik yazma").
//
// Probe (api katmanı, argocd_settings_routes.go) hub Thanos'unun
// label-values API'sini thanos konsol taşımasıyla çağırır ve iş (`job`)
// başına namespace / exported_namespace değerlerini JobProbe'a doldurur.
// Burada o gözlemden ADAY instance'lar türetilir; hiçbir şey kaydedilmez.
//
// ── NEDEN label-values, NEDEN count() değil ──────────────────────────────
//
// argocd_app_info ~40k seri (§5.4): /api/v1/series etiket setlerini
// döndürür (~20–30 MB). label-values yalnız TEKİL değerleri döndürür
// (onlarca), sunucu-uygulamalı limit + zaman penceresi taşır ve bir PromQL
// değerlendirmesi başlatmaz. Karşılığı iş başına 2 çağrı (+ paylaşılan iş
// adında namespace başına 1) — üst sınır api katmanında.
//
// ── §5.2 durumları (H1.2) ────────────────────────────────────────────────
//
//	A  exported_namespace VAR ve == namespace   → hub ns = namespace (ServiceMonitor, varsayılan honorLabels)
//	C  exported_namespace VAR ve ≠ namespace    → hub ns = namespace, apps-in-any-namespace
//	B  exported_namespace YOK (honorLabels:true) → namespace = Application ns; hub ns TAHMİNİ
//	   (tek değer) ya da bilinmez (çok değer); operatör doğrular
//
// Aynı iş adı birden çok namespace'te görülürse (ArgoCD CR'lerinin hepsi
// "argocd" adlı → iş "argocd-metrics"), exported_namespace varken her
// (iş, namespace) ayrı bir instance'tır.
//
// v0.10.957 — inceleme (§5.6 iki hub): keşif HUB BAŞINA koşar; adaylar
// probe edilen hub'ın hubClusterId'sini taşır ve yalnız O hub'daki kayıtlı
// instance'larla eşlenir (iki hub'da aynı ns ayrı instance'tır). Kimlik
// önerisi TÜM hub'lardaki kimliklerle çakışmaz (id tüm hub'larda tekil).

import (
	"sort"
	"strconv"
	"strings"
)

// AppInfoMetric — hub envanter serisi (§5.1).
const AppInfoMetric = "argocd_app_info"

var promStringEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)

// AppInfoSelector — `argocd_app_info{job="…",namespace="…"}` (boş
// parçalar atlanır). Değerler PromQL dize sabiti olarak kaçışlanır; küme
// etiketi enjeksiyonu thanos EffectiveMatchers'ın işi (burada YOK).
func AppInfoSelector(job, namespace string) string {
	var m []string
	if job != "" {
		m = append(m, `job="`+promStringEscaper.Replace(job)+`"`)
	}
	if namespace != "" {
		m = append(m, `namespace="`+promStringEscaper.Replace(namespace)+`"`)
	}
	if len(m) == 0 {
		return AppInfoMetric
	}
	return AppInfoMetric + "{" + strings.Join(m, ",") + "}"
}

// JobProbe — bir `job` değeri için hub gözlemi.
type JobProbe struct {
	Job        string
	Namespaces []string // argocd_app_info{job=J} altındaki `namespace` değerleri
	// Exported — namespace → exported_namespace değerleri. Boş/nil = iş
	// genelinde exported_namespace YOK (durum B).
	Exported  map[string][]string
	Truncated bool   // herhangi bir değer listesi limitte kesildi
	Err       string // bu işin sorgusu başarısız (URL'siz, maskeli metin)
}

// Candidate — keşif önerisi (Instance'a kopyalanabilir; discovered:true).
type Candidate struct {
	ID               string   `json:"id"`
	HubClusterID     string   `json:"hubClusterId"` // v0.10.957 — probe edilen hub (§5.6)
	Name             string   `json:"name"`
	HubNamespace     string   `json:"hubNamespace"`
	MetricsJob       string   `json:"metricsJob"`
	AppsAnyNamespace bool     `json:"appsAnyNamespace"`
	Discovered       bool     `json:"discovered"`
	NamespaceCase    string   `json:"namespaceCase,omitempty"` // A | B | C (H1.2)
	AppNamespaces    []string `json:"appNamespaces,omitempty"` // gözlenen Application ns'leri
	ConfiguredID     string   `json:"configuredId,omitempty"`  // bu (ns, iş) zaten kayıtlıysa o instance
	Note             string   `json:"note,omitempty"`
	Error            string   `json:"error,omitempty"`
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// BuildCandidates — SAF: hubClusterID hub'ının gözlemi → sıralı aday
// listesi (iş, hub ns). Boş iş adı atlanır; sorgusu başarısız iş Error
// taşıyan tek aday olur. Kayıtlı instance eşlemesi YALNIZ aynı hub'daki
// kayıtlarla: aynı (hubNamespace, metricsJob); kayıtta iş boşsa yalnız
// namespace; aday ns'i bilinmiyorsa yalnız iş. Eşleşmeyen aday için kimlik
// önerisi hub ns'inden (yoksa işten) türetilir ve herhangi bir hub'daki
// kayıtlı ya da önceki adaylarla çakışırsa -2, -3… alır.
func BuildCandidates(hubClusterID string, probes []JobProbe, existing []Instance) []Candidate {
	var out []Candidate
	for _, p := range probes {
		if p.Job == "" {
			continue
		}
		if p.Err != "" {
			out = append(out, Candidate{MetricsJob: p.Job, Name: p.Job, Discovered: true, Error: p.Err})
			continue
		}
		nss := uniqSorted(p.Namespaces)
		exported := false
		for _, v := range p.Exported {
			if len(uniqSorted(v)) > 0 {
				exported = true
				break
			}
		}
		if !exported {
			c := Candidate{MetricsJob: p.Job, Discovered: true, NamespaceCase: "B", AppNamespaces: nss,
				Note: "exported_namespace yok (honorLabels: true): namespace Application ns'idir; hubNamespace tahmini — doğrulayın"}
			if len(nss) == 1 {
				c.HubNamespace = nss[0]
			} else {
				c.AppsAnyNamespace = len(nss) > 1
				c.Note = "exported_namespace yok (honorLabels: true) ve birden çok Application ns'i: hubNamespace bilinmiyor — elle girin"
			}
			out = append(out, withTrunc(c, p.Truncated))
			continue
		}
		for _, ns := range nss {
			ex := uniqSorted(p.Exported[ns])
			c := Candidate{MetricsJob: p.Job, HubNamespace: ns, Discovered: true, AppNamespaces: ex}
			switch {
			case len(ex) == 0:
				c.NamespaceCase = "B"
				c.AppNamespaces = []string{ns}
				c.Note = "bu namespace'in serilerinde exported_namespace yok; hubNamespace tahmini — doğrulayın"
			case len(ex) == 1 && ex[0] == ns:
				c.NamespaceCase = "A"
			default:
				c.NamespaceCase = "C"
				c.AppsAnyNamespace = true
			}
			out = append(out, withTrunc(c, p.Truncated))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MetricsJob != out[j].MetricsJob {
			return out[i].MetricsJob < out[j].MetricsJob
		}
		return out[i].HubNamespace < out[j].HubNamespace
	})
	taken := map[string]bool{}
	var sameHub []Instance
	for _, inst := range existing {
		taken[inst.ID] = true
		if inst.HubClusterID == hubClusterID {
			sameHub = append(sameHub, inst)
		}
	}
	for i := range out {
		c := &out[i]
		c.HubClusterID = hubClusterID
		if id := matchConfigured(*c, sameHub); id != "" {
			c.ID, c.ConfiguredID = id, id
		} else {
			base := c.HubNamespace
			if base == "" {
				base = c.MetricsJob
			}
			c.ID = uniqueID(suggestID(base), taken)
		}
		taken[c.ID] = true
		if c.Name == "" {
			c.Name = c.HubNamespace
			if c.Name == "" {
				c.Name = c.MetricsJob
			}
		}
	}
	return out
}

func withTrunc(c Candidate, truncated bool) Candidate {
	if truncated {
		if c.Note != "" {
			c.Note += "; "
		}
		c.Note += "değer listesi probe limitinde kesildi (truncated)"
	}
	return c
}

func matchConfigured(c Candidate, existing []Instance) string {
	if c.Error != "" {
		return ""
	}
	for _, inst := range existing {
		switch {
		case c.HubNamespace != "" && inst.HubNamespace == c.HubNamespace && (inst.MetricsJob == "" || inst.MetricsJob == c.MetricsJob):
			return inst.ID
		case c.HubNamespace == "" && inst.MetricsJob != "" && inst.MetricsJob == c.MetricsJob:
			return inst.ID
		}
	}
	return ""
}

// suggestID — serbest metin → instanceIDRe'ye uyan kimlik (küçük harf,
// [a-z0-9-], baş/son tiresiz, ≤63). Boş kalırsa "instance".
func suggestID(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	if out == "" {
		return "instance"
	}
	return out
}

func uniqueID(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		b := base
		if len(b)+len(suffix) > 63 {
			b = strings.TrimRight(b[:63-len(suffix)], "-")
		}
		if id := b + suffix; !taken[id] {
			return id
		}
	}
}
