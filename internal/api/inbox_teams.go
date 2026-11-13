package api

// inbox_teams.go — v0.10.2121, Problems sadeleştirme S1
// (docs/frontend/problems-page-plan.md §4): "Ekip" kolonu için satırın
// farklı ekipleri. Tek ekipli satır bugünkü OwnerTeam / SRETeam alanlarıyla
// çizilir; bu dosya yalnız ÇOK servisli exception'ın (yayılım ortakları)
// ekip kümesini kurar. Ek okuma YOK: katalog (ListServiceMetadata) aynı
// derlemede zaten okundu.

import (
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// inboxTeamOf — SAF: bir servisin görünen ekibi (owner, yoksa SRE).
func inboxTeamOf(owner, sre string) string {
	if owner != "" {
		return owner
	}
	return sre
}

// inboxRowTeams — SAF: satırın farklı ekipleri (sıralı). Kendi ekibi
// zenginleştirilmiş OwnerTeam/SRETeam'den (db öznesinin türetilmiş
// sahipliği dahil); yayılım ortakları katalogdan. Kataloğu olmayan ortak
// atlanır (bilinmeyen ≠ ayrı ekip).
func inboxRowTeams(it InboxItem, md map[string]chstore.ServiceMetadata) []string {
	set := map[string]bool{}
	if t := inboxTeamOf(it.OwnerTeam, it.SRETeam); t != "" {
		set[t] = true
	}
	if it.Exception != nil {
		for _, svc := range it.Exception.SpreadServices {
			if m, ok := md[svc]; ok {
				if t := inboxTeamOf(m.OwnerTeam, m.SRETeam); t != "" {
					set[t] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// annotateInboxTeams — Teams alanını yalnız ≥ 2 farklı ekipte doldurur.
func annotateInboxTeams(items []InboxItem, md map[string]chstore.ServiceMetadata) {
	for i := range items {
		if ts := inboxRowTeams(items[i], md); len(ts) >= 2 {
			items[i].Teams = ts
		} else {
			items[i].Teams = nil
		}
	}
}
