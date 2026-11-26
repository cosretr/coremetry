package api

// inbox_closed.go — v0.10.2476 (operatör: "problemler kapanınca ayrıca
// gözüksün aktifler arasında kalmasın"): Problems sayfasının "Kapanan"
// görünümü, `GET /api/inbox?status=closed`.
//
// Ne döner: seçili pencerede (?since=; yoksa son 24 sa) KAPANMIŞ Problem
// satırları, en son kapanan önce; satır kapanış anını (resolvedAt) taşır,
// sayfa kapanış + süre çizer. Exception / anomali / incident kaynakları
// okunmaz — `ignored` pivotunun yalnız exception olması gibi, bu pivot
// yalnız Problem'dir (kapanmak Problem'in fiili; exception "resolve",
// anomali "cleared" ayrı durumlar).
//
// Neden ayrı derleme (inboxView'in büyük kapanışı değil): açık kuyruğun
// kaynak kapıları, taban (minOcc), incident / graf katlaması ve şerit
// sayımları "şu an ne yanıyor" sorusuna göre kurulu ve kaynak metni
// testlerle pinli; kapanmış satıra hiçbiri uymaz. Aynı daraltmalar
// (servis, arama, ortam, ekip, şerit, öncelik / tür / kategori çipleri)
// burada aynı yardımcılarla uygulanır; gövde şekli aynıdır.
//
// Önbellek: anahtar inboxListKey'den gelir ve statusFilter'ı taşır —
// `closed` açık görünümle aynı girdiyi paylaşamaz.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// inboxStatusClosed — Kapanan pivotunun ?status= değeri.
const inboxStatusClosed = "closed"

// inboxClosedDefaultWindow — ?since= yokken Kapanan penceresi.
const inboxClosedDefaultWindow = 24 * time.Hour

// inboxClosedWindow — SAF: Kapanan görünümünün penceresi. ?since= basamağı
// (normalizeInboxSince ile aynı küme) KAPANIŞ anına uygulanır; boş = 24 sa.
func inboxClosedWindow(since string) time.Duration {
	if d := inboxSinceDuration(since); d > 0 {
		return d
	}
	return inboxClosedDefaultWindow
}

// problemResolvedAtNs — SAF: kapanmış satırın kapanış anı (0 = açık / yok).
func problemResolvedAtNs(p chstore.Problem) int64 {
	if p.Status != "resolved" || p.ResolvedAt == nil {
		return 0
	}
	return *p.ResolvedAt
}

// sortInboxClosed — SAF: en son kapanan önce; eşitlikte kimlik (kararlı).
func sortInboxClosed(items []InboxItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ResolvedAt != items[j].ResolvedAt {
			return items[i].ResolvedAt > items[j].ResolvedAt
		}
		return items[i].ID < items[j].ID
	})
}

// closedInboxQuery — Kapanan derlemesinin girdileri (inboxView'de
// normalize edilmiş, önbellek anahtarındaki değerlerin aynısı).
type closedInboxQuery struct {
	service, search, ownerTeam, sreTeam, team, env string
	limit, srcLimit                                int
	sortID, sortDir, since, subject                string
	kinds, prios, cats                             []string
	graphMerge                                     bool
}

func (s *Server) closedInboxBuild(q closedInboxQuery) func(ctx context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		now := time.Now()
		items := make([]InboxItem, 0, 64)
		scanCapped := false
		mdMap, mdErr := s.store.ListServiceMetadata(ctx)
		var teamServices []string // nil = takım kısıtı yok
		if (q.ownerTeam != "" || q.sreTeam != "" || q.team != "") && mdErr == nil {
			ta := s.teamAliasesCtx(ctx)
			if q.ownerTeam != "" || q.sreTeam != "" {
				teamServices = servicesForTeam(ta, mdMap, q.ownerTeam, q.sreTeam)
			}
			if q.team != "" {
				teamServices = intersectServices(teamServices, servicesForUserTeam(ta, mdMap, q.team))
			}
		}
		teamIsEmpty := teamServices != nil && len(teamServices) == 0
		if !teamIsEmpty {
			probs, err := s.store.ListProblems(ctx, chstore.ProblemFilter{
				Status:                  pickStatus(inboxStatusClosed),
				Services:                teamServices,
				ServicesAllowDBSubjects: true,
				SubjectKind:             inboxProblemLane(q.subject),
				ResolvedSinceNs:         now.Add(-inboxClosedWindow(q.since)).UnixNano(),
				OrderByResolved:         true,
				Limit:                   q.srcLimit,
			})
			if err != nil {
				return nil, err
			}
			scanCapped = len(probs) >= q.srcLimit
			probs = s.enrichProblemsForRead(ctx, probs)
			probs = s.store.EnrichProblemsWithEventCounts(ctx, probs)
			for _, p := range probs {
				if inboxKeepsProblem(p.Status, inboxStatusClosed) {
					items = append(items, problemToInbox(p))
				}
			}
		}
		items = closedInboxNarrow(items, q.service, q.search)
		items = inboxEnvScopeItems(items, s.resolveInboxEnvMembers(ctx, q.env))
		for i := range items {
			if md, ok := mdMap[items[i].Service]; ok {
				items[i].OwnerTeam, items[i].SRETeam = md.OwnerTeam, md.SRETeam
			}
		}
		if q.ownerTeam != "" || q.sreTeam != "" || q.team != "" {
			ta := s.teamAliasesCtx(ctx)
			kept := items[:0]
			for _, it := range items {
				if (q.ownerTeam != "" && !ta.TeamEqual(it.OwnerTeam, q.ownerTeam)) ||
					(q.sreTeam != "" && !ta.TeamEqual(it.SRETeam, q.sreTeam)) ||
					!inboxTeamKeepsRow(ta, it.OwnerTeam, it.SRETeam, q.team) {
					continue
				}
				kept = append(kept, it)
			}
			items = kept
		}
		forceNonExceptionP3(items) // açık görünümle aynı görünüm önceliği
		counts := inboxFacetCounts(items)
		oldest := inboxOldestFirstSeen(items)
		items = applyInboxFacets(items, q.kinds, q.prios)
		for i := range items {
			if items[i].Category == "" {
				items[i].Category = inboxDerivedCategory(items[i])
			}
		}
		items = applyInboxCategoryFacet(items, q.cats)
		s.attachInboxStandards(ctx, items, now)
		if q.sortID == inboxSortDefault && q.sortDir == "desc" {
			sortInboxClosed(items)
		} else {
			sortInboxItems(items, q.sortID, q.sortDir)
		}
		total := len(items)
		if q.limit > 0 && len(items) > q.limit {
			items = items[:q.limit]
		}
		return withInboxGraphMode(map[string]any{
			"items": items, "total": total, "limit": q.limit, "truncated": total > q.limit,
			"scanCapped": scanCapped, "minOcc": 0, "hiddenByMinOcc": 0,
			"counts": counts, "oldestFirstSeen": oldest, "subject": q.subject,
			// Kapanan penceresi (sn) — sayfa "son 24 saatte kapananlar" der.
			"closedWindowSec": int64(inboxClosedWindow(q.since) / time.Second),
		}, q.graphMerge), nil
	}
}

// closedInboxNarrow — SAF: servis alt dizgisi + serbest arama (açık
// görünümle aynı semantik: servis / başlık / kaynak, harf duyarsız).
func closedInboxNarrow(items []InboxItem, service, search string) []InboxItem {
	if service == "" && search == "" {
		return items
	}
	svc, needle := strings.ToLower(service), strings.ToLower(search)
	kept := items[:0]
	for _, it := range items {
		if svc != "" && !strings.Contains(strings.ToLower(it.Service), svc) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(it.Service), needle) &&
			!strings.Contains(strings.ToLower(it.Title), needle) &&
			!strings.Contains(strings.ToLower(it.Source), needle) {
			continue
		}
		kept = append(kept, it)
	}
	return kept
}
