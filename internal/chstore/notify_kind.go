package chstore

// notify_kind.go — v0.10.747: kanal başına OLAY TÜRÜ süzgeci (operatör:
// "anomali, incident ve problems ayrı ayrı gelsin").
//
// Bildirim hattı tek kapıdan geçer (notify.SendProblemAlert, Problem
// şekli) ve o kapıdan bugüne dek iki kaynak karışık akıyordu: operatör
// KURALLARI (alert rule, builtin, SLO, DB, runtime, watcher) ve ANOMALİ
// MOTORU (metrik anomalisi, service silent, dış tarayıcı, exception
// fırtınası / paylaşılan bağımlılık). Kanal ikisini ayıramıyordu.
//
// Tür, Inbox'ın InboxKind gramerinin aynısı (problem | anomaly | incident)
// — tek kimlik grameri: operatör Inbox'ta gördüğü adla kanalı kurar.
// "incident" türü bu dilimde tanımlı ama üretici v0.10.748'de gelir
// (incident açılışı bugün hiç bildirim üretmiyor).
//
// Sınıflandırma RuleID önekiyle: kaynağın kendi sabiti (anomaly paketi
// clusterRulePrefix, evaluator fatalExcRuleID …) burada TEKRAR yazılır;
// tek-yazım kör noktasına karşı her kaynak paketi kendi sabitiyle bu
// fonksiyonu pinler (notify_kind_pin_test.go'lar).

import (
	"fmt"
	"strings"
)

const (
	NotifyKindProblem  = "problem"
	NotifyKindAnomaly  = "anomaly"
	NotifyKindIncident = "incident"
)

// NotifyKindsAll — geçerli değerler, UI sırasıyla.
var NotifyKindsAll = []string{NotifyKindProblem, NotifyKindAnomaly, NotifyKindIncident}

// IsNotifyKind — allow-list üyeliği (küçük harf, kırpılmış beklenir).
func IsNotifyKind(k string) bool {
	for _, v := range NotifyKindsAll {
		if v == k {
			return true
		}
	}
	return false
}

// ProblemNotifyKind — SAF: bir Problem'in bildirim türü. Anomali motoru
// önekleri: "anomaly:" (metrik / service_silent / dış tarayıcı),
// "anomaly-cluster:" (kümeleme), "exception-storm", "exception:" (fatal
// altyapı / paylaşılan bağımlılık patlaması — otomatik tespit, kural
// değil; operatör kararı 2026-09-17). Kalan her şey kural problemi.
func ProblemNotifyKind(p Problem) string {
	rid := p.RuleID
	switch {
	case strings.HasPrefix(rid, "incident:"): // v0.10.748 notify.incidentAsProblem
		return NotifyKindIncident
	case strings.HasPrefix(rid, "anomaly:"),
		strings.HasPrefix(rid, "anomaly-cluster:"),
		rid == "exception-storm",
		strings.HasPrefix(rid, "exception:"):
		return NotifyKindAnomaly
	}
	return NotifyKindProblem
}

// NormalizeNotifyKinds — kayıt anı: kırp, küçült, tekrarı at, sırayı
// koru. Bilinmeyen değer HATA (sessiz düşürme, "incident işaretledim
// ama gelmiyor"u gizlerdi). Boş/nil → nil (= süzgeç yok).
func NormalizeNotifyKinds(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		k := strings.ToLower(strings.TrimSpace(raw))
		if k == "" {
			continue
		}
		if !IsNotifyKind(k) {
			return nil, fmt.Errorf("unknown notification kind %q (allowed: %s)", raw, strings.Join(NotifyKindsAll, ", "))
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
