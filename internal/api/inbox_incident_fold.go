package api

// inbox_incident_fold.go — v0.10.1084 (operatör: "tekilleştir, Exceptions'taki
// format güzel"). Otomatik açılan bir incident ve ona bağlı problem /inbox'ta
// İKİ ayrı P1 satırıydı (Incident · db:couchbase@X ve Alert rule ·
// db:couchbase@X): aynı olay iki kez sayılıyor, iki kez triyaj ediliyordu.
//
// Kural (sunucuda, sayaçlardan / sıralamadan / tavandan ÖNCE — çip sayıları
// gizlenen satırları saymasın):
//   - AÇIK (resolved / closed değil) bir incident'ın bağlı problemleri listede
//     varsa o problem satırları GİZLENİR, incident satırı kalır.
//   - Incident satırı onların yerini tutar: açıklaması birincil (en erken
//     başlayan) bağlı problemin cümlesi (v0.10.1081 incident sayfası manşetiyle
//     aynı kural), Incident.ProblemCount = bağlı problem sayısı ("2 problem"),
//     öncelik = incident + gizlenen problemlerin GÖRÜNÜM önceliklerinin en
//     yükseği (forceNonExceptionP3'ten SONRA: P1 db-health problemi warning
//     incident'ın P3'üne gömülmesin), ilk görülme = en erken, son görülme = en
//     geç.
//   - Incident'ı kapanmış (resolved / closed) problem kendisi olarak görünür.
//   - Incident satırı listede yoksa (bir süzgeç onu eledi) problem gizlenmez:
//     gizlenen her satırın yerini tutan bir satır ekranda olmalı.
//   - Anomali satırları incident değil — dokunulmaz.
//
// Bağlantı (incident → problem id'leri) çağrı başına TEK okuma
// (chstore.IncidentProblemIDs), yalnız listede hem açık incident hem problem
// satırı varken; okuma düşerse katlama YOK (bugünkü iki satır — hiçbir satır
// kaybolmaz, güvenli yön).

import (
	"context"
	"log"
	"strings"
)

// inboxIncidentFolds — incident satırı bağlı problemleri katlar mı: açık
// (open / acknowledged / boş = eski satır). resolved ve closed katlamaz.
func inboxIncidentFolds(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "resolved", "closed":
		return false
	}
	return true
}

// inboxResolveSuffixes — problem açıklamasına eklenen yaşam döngüsü ekleri
// (FE incidentSummary.ts problemSentence ikizi + db-health kapanış ekleri);
// olayın cümlesi değil, kırpılır.
var inboxResolveSuffixes = []string{" · auto-resolved:", " · resolved:"}

// inboxProblemSentence — SAF: problemin tek cümlesi (kapanış eki kırpılmış).
func inboxProblemSentence(desc string) string {
	s := strings.TrimSpace(desc)
	for _, suf := range inboxResolveSuffixes {
		if i := strings.Index(s, suf); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

// inboxHeadline — SAF: satırın KALIN başlığı, FE lib/inboxRowText.ts
// inboxRowHeadline ikizi (Exceptions biçimi): problem / incident → olayın
// cümlesi (yoksa başlık), exception ailesi ve diğerleri → başlık (exception
// satırında Title = tip). "detail" sıralaması ekranda okunan metne göre olsun.
func inboxHeadline(it InboxItem) string {
	if it.Kind == "problem" || it.Kind == "incident" {
		if s := inboxProblemSentence(it.Description); s != "" {
			return s
		}
	}
	if it.Title == "" && it.Exception != nil {
		return it.Exception.Type
	}
	return it.Title
}

// foldIncidentProblems — SAF: yukarıdaki kural. attached: incident id →
// bağlı problem id'leri. Girdi sırası korunur; gizlenen satırlar düşer.
func foldIncidentProblems(items []InboxItem, attached map[string][]string) []InboxItem {
	if len(attached) == 0 {
		return items
	}
	byProblem := make(map[string]int)
	for i, it := range items {
		if it.Kind == "problem" && it.Problem != nil && it.Problem.ID != "" {
			byProblem[it.Problem.ID] = i
		}
	}
	if len(byProblem) == 0 {
		return items
	}
	hidden := make(map[int]bool)
	for i := range items {
		inc := items[i].Incident
		if items[i].Kind != "incident" || inc == nil || !inboxIncidentFolds(inc.Status) {
			continue
		}
		ids := dedupeStrings(attached[inc.ID])
		if len(ids) == 0 {
			continue
		}
		ref := *inc
		ref.ProblemCount = len(ids)
		items[i].Incident = &ref

		primary := -1
		for _, id := range ids {
			j, ok := byProblem[id]
			if !ok || hidden[j] {
				continue
			}
			hidden[j] = true
			p := items[j]
			if priorityRank(p.Priority) > priorityRank(items[i].Priority) {
				items[i].Priority = p.Priority
				items[i].PriorityReason = "bağlı problem: " + p.PriorityReason
			}
			if p.StartedAt > 0 && (items[i].StartedAt == 0 || p.StartedAt < items[i].StartedAt) {
				items[i].StartedAt = p.StartedAt
			}
			if p.LastSeen > items[i].LastSeen {
				items[i].LastSeen = p.LastSeen
			}
			if primary < 0 || p.StartedAt < items[primary].StartedAt ||
				(p.StartedAt == items[primary].StartedAt && p.Problem.ID < items[primary].Problem.ID) {
				primary = j
			}
		}
		if primary >= 0 {
			if s := inboxProblemSentence(items[primary].Description); s != "" {
				items[i].Description = s
			}
			if c := items[primary].Category; c != "" {
				items[i].Category = c
			}
		}
	}
	if len(hidden) == 0 {
		return items
	}
	kept := make([]InboxItem, 0, len(items)-len(hidden))
	for i, it := range items {
		if !hidden[i] {
			kept = append(kept, it)
		}
	}
	return kept
}

// inboxFoldCandidates — SAF: katlama okumasına gidecek açık incident id'leri;
// listede problem satırı yoksa boş (okuma yok).
func inboxFoldCandidates(items []InboxItem) []string {
	hasProblem := false
	var ids []string
	for _, it := range items {
		switch {
		case it.Kind == "problem" && it.Problem != nil:
			hasProblem = true
		case it.Kind == "incident" && it.Incident != nil && inboxIncidentFolds(it.Incident.Status):
			ids = append(ids, it.Incident.ID)
		}
	}
	if !hasProblem {
		return nil
	}
	return dedupeStrings(ids)
}

// foldInboxIncidents — G/Ç yarısı: tek okuma + SAF katlama. Okuma hatası →
// katlama yok (soft-fail, tek log).
func (s *Server) foldInboxIncidents(ctx context.Context, items []InboxItem) []InboxItem {
	ids := inboxFoldCandidates(items)
	if len(ids) == 0 {
		return items
	}
	attached, err := s.store.IncidentProblemIDs(ctx, ids)
	if err != nil {
		log.Printf("[inbox] incident katlaması: bağlı problem okuması düştü, satırlar katlanmadı: %v", err)
		return items
	}
	return foldIncidentProblems(items, attached)
}

// dedupeStrings — boşsuz, tekrarsız, ilk görülme sırası.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
