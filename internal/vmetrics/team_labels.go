package vmetrics

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// team_labels.go — 2026-11-21 (operatör: metrik serileri de `ug_team` /
// `sy_team` taşıyor — span resource anahtarları ug-team / sy-team ile AYNI
// anlam: ug → owner, sy → SRE; chstore.svcMetaProjection). Yalnız metrik
// gönderen servisin takımı bu etiketlerden doldurulur, EN DÜŞÜK öncelikle
// (span türetmesi ve katalog kazanır; chstore.MergeMetricTeams).
//
// Okuma TEK anlık sorgu: geniş yayılı birkaç metrik ailesinin son 1 sa'lik
// serileri (service, ug_team, sy_team) başına sayılır — tüm seri uzayı
// taranmaz. Sonuç servis sayısıyla sınırlı (entityVector tavanı).

// teamLabelMetrics — hemen her serviste bulunan aileler (OTel target_info,
// HTTP sunucu histogramı, JVM / .NET / CPython thread sayısı).
const teamLabelMetrics = `target_info|http_server_request_duration_seconds_count|jvm_thread_count|process_runtime_dotnet_thread_pool_threads_count|process_runtime_cpython_thread_count`

// TeamLabelQuery — SAF: takım etiketlerinin anlık sorgusu.
func TeamLabelQuery(svcLabel string) string {
	return `count by (` + svcLabel + `, ug_team, sy_team) (last_over_time({__name__=~"` + teamLabelMetrics + `"}[1h]))`
}

// ServiceTeamLabels — servis → (owner = ug_team, sre = sy_team); iki etiketi
// de boş satır atlanır. Aynı serviste birden çok değer → sözlükçe küçük
// (kararlı; span türetmesinin eşitlik kırıcısıyla aynı yön).
func (s *Service) ServiceTeamLabels(ctx context.Context) (map[string]chstore.ServiceTeams, error) {
	cfg, err := s.ready()
	if err != nil {
		return nil, err
	}
	svc := serviceLabel()
	rows, _, err := s.entityVector(ctx, cfg, TeamLabelQuery(svc), time.Now())
	if err != nil {
		return nil, err
	}
	return TeamsFromLabelRows(rows, svc), nil
}

// TeamsFromLabelRows — SAF: etiket kümeleri → servis takımları.
func TeamsFromLabelRows(rows []map[string]string, svcLabel string) map[string]chstore.ServiceTeams {
	out := map[string]chstore.ServiceTeams{}
	pick := func(cur, v string) string {
		if v == "" || (cur != "" && cur <= v) {
			return cur
		}
		return v
	}
	for _, l := range rows {
		name := l[svcLabel]
		ug, sy := l["ug_team"], l["sy_team"]
		if name == "" || (ug == "" && sy == "") {
			continue
		}
		t := out[name]
		t.OwnerTeam, t.SRETeam = pick(t.OwnerTeam, ug), pick(t.SRETeam, sy)
		out[name] = t
	}
	return out
}
