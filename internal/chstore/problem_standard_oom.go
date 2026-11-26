package chstore

// problem_standard_oom.go — v0.10.2476 (OOMKilled pod → Problem; ml
// pod_oom dedektörü). Manşet cümlesi burada bir kez TANIMLANIR; ML tiki
// Problem açıklamasına bu işlevle yazar (ml svcEventSentence), standart
// açıklamadan okur (MLPromotedHeadline → mlPromotedMsg) — İngilizce arayüz
// için cümle anahtara geri okunur (h.mlOom / h.mlOomNoLim). Sonraki adım:
// servisin Altyapı sekmesi (pod tablosu + bellek grafiği) ve mevcut
// rec.sat.oom önerisi.

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MLPodOOMWindowMin — manşetteki sayım penceresi (dakika; ml pod_oom aynı sabit).
const MLPodOOMWindowMin = 15

// MLPodOOMSentence — SAF: "<iş yükü> · pod bellek sınırında sonlandırıldı
// (OOMKilled): N pod, son 15 dk'da M kez — limit X Mi." limitMi ≤ 0 = limit
// bilinmiyor (ek yazılmaz).
func MLPodOOMSentence(workload string, pods, kills int, limitMi float64) string {
	s := workload + " · pod bellek sınırında sonlandırıldı (OOMKilled): " + strconv.Itoa(pods) + " pod, son " +
		strconv.Itoa(MLPodOOMWindowMin) + " dk'da " + strconv.Itoa(kills) + " kez"
	if lim := int64(limitMi + 0.5); lim > 0 {
		s += " — limit " + strconv.FormatInt(lim, 10) + " Mi"
	}
	return s + "."
}

var mlOOMRe = regexp.MustCompile(`^(.+?) · pod bellek sınırında sonlandırıldı \(OOMKilled\): ([0-9]+) pod, son ([0-9]+) dk'da ([0-9]+) kez(?: — limit ([0-9]+) Mi)?\.$`)

// mlOOMMsg — SAF: manşet TAM olarak MLPodOOMSentence ise anahtar.
func mlOOMMsg(headline string) *StdMsg {
	m := mlOOMRe.FindStringSubmatch(headline)
	if m == nil {
		return nil
	}
	if m[5] == "" {
		return stdM("h.mlOomNoLim", "wl", m[1], "n", m[2], "win", m[3], "m", m[4])
	}
	return stdM("h.mlOom", "wl", m[1], "n", m[2], "win", m[3], "m", m[4], "lim", m[5])
}

// mlOOMNextStep — SAF: manşet OOM cümlesiyse sonraki adım (servisin Altyapı
// sekmesi: pod tablosu + bellek grafiği; öneri metni rec.sat.oom).
func mlOOMNextStep(p Problem) (StdNextStep, bool) {
	h, ok := MLPromotedHeadline(p)
	if !ok {
		return StdNextStep{}, false
	}
	m := mlOOMRe.FindStringSubmatch(h)
	if m == nil {
		return StdNextStep{}, false
	}
	q := url.Values{}
	q.Set("name", p.Service)
	q.Set("tab", "infra")
	return StdNextStep{Text: "OOMKilled pod'lar (" + m[1] + "): bellek limitini / sızıntıyı gözden geçirin",
		Href: "/service?" + q.Encode(), Msg: stdM("rec.sat.oom", "pods", m[1])}, true
}

// ── Öncelik ve etki (v0.10.2476: P1 kodu oran olarak görünmez) ──────────

// MLOOMMetric — OOM Problem'inin ölçüsü. Value = 30 dk kill sayısı,
// Threshold = iş yükünün pod sayısı (oran DEĞİL: öncelik kendi dalında,
// computePriorityMsg → oomProblemPriority; büyük ihlal merdivenine girmez).
const MLOOMMetric = "oom_kills"

// OOM öncelik kuralı sabitleri (ml pod_oom aynı tanımı kullanır).
const (
	OOMRepeatWindowMin = 30
	OOMRepeatP1        = 3
)

// OOMPriority — SAF (TEK TANIM): P2; 30 dk'da ≥ 3 kill → P1; iş yükünün
// ≥ 2 pod'u varsa ve ≥ %50'si 15 dk'da OOMKilled → P1. Tek replikalı iş
// yükü yalnız tekrar koluyla P1 olur.
func OOMPriority(killed, total, kills30 int) (string, string, *StdMsg) {
	k, n, t := strconv.Itoa(kills30), strconv.Itoa(killed), strconv.Itoa(total)
	win := strconv.Itoa(OOMRepeatWindowMin)
	if kills30 >= OOMRepeatP1 {
		return "P1", win + " dk'da " + k + " kez OOMKilled", stdM("prio.oomRepeat", "k", k, "win", win)
	}
	if total >= 2 && killed*2 >= total {
		pct := strconv.Itoa(killed * 100 / total)
		return "P1", n + "/" + t + " pod (%" + pct + ") bellek sınırında sonlandırıldı",
			stdM("prio.oomShare", "n", n, "t", t, "pct", pct)
	}
	return "P2", "OOMKilled: " + n + "/" + t + " pod, " + win + " dk'da " + k + " kez",
		stdM("prio.oom", "n", n, "t", t, "k", k, "win", win)
}

// oomProblemFacts — SAF: OOM Problem'inin gerçekleri (OOMKilled pod sayısı
// manşetten, 30 dk kill ve toplam pod Value / Threshold'dan). ok=false: OOM
// satırı değil.
func oomProblemFacts(p Problem) (killed, total, kills30 int, ok bool) {
	if p.Metric != MLOOMMetric || !strings.HasPrefix(p.RuleID, "anomaly-auto:") {
		return 0, 0, 0, false
	}
	m := mlOOMRe.FindStringSubmatch(strings.TrimSpace(StripRequestSplit(StripEvidenceSource(p.Description))))
	if m == nil {
		return 0, 0, 0, false
	}
	killed, _ = strconv.Atoi(m[2])
	return killed, max(int(p.Threshold+0.5), killed), int(p.Value + 0.5), true
}

// oomProblemPriority — SAF: OOM Problem'inin önceliği; P2 iken açık kalma
// (StaleCriticalHours) kuralı aynen.
func oomProblemPriority(p Problem, nowNs int64, cfg ProblemPriorityConfig) (string, string, *StdMsg, bool) {
	killed, total, k30, ok := oomProblemFacts(p)
	if !ok {
		return "", "", nil, false
	}
	prio, why, msg := OOMPriority(killed, total, k30)
	if prio == "P2" && cfg.StaleCriticalHours > 0 && p.Status != "resolved" {
		if h := float64(nowNs-p.StartedAt) / float64(time.Hour); h >= cfg.StaleCriticalHours {
			return "P1", fmt.Sprintf("critical open %.1fh", h), stdM("prio.stale", "h", fmt.Sprintf("%.1f", h)), true
		}
	}
	return prio, why, msg, true
}

// oomImpact — SAF: "Kim etkileniyor" ölçüsü (oran yok).
func oomImpact(p Problem) (StdMeasure, bool) {
	killed, total, k30, ok := oomProblemFacts(p)
	if !ok {
		return StdMeasure{}, false
	}
	n, t, k, win := strconv.Itoa(killed), strconv.Itoa(total), strconv.Itoa(k30), strconv.Itoa(OOMRepeatWindowMin)
	return StdMeasure{Text: n + "/" + t + " pod OOMKilled, " + win + " dk'da " + k + " kez", Value: p.Value, Normal: p.Threshold,
		Msg: stdM("imp.oom", "n", n, "t", t, "k", k, "win", win)}, true
}
