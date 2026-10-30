package api

// cosre_briefing_worker.go — CoSRE Login Briefing Faz 1 (docs/cosre/
// briefing-audit.md §7, "Tasarım önerisi"; operatör onayı 2026-10-30).
//
// Lider kilitli periyodik iş (StartOperationCatalog emsali): ayarın
// refresh_interval_s'inde bir (varsayılan 5 dk), kullanıcısı OLAN her takım
// (users.team, CanonTeam ile katlanmış) × ayardaki her env için kartı üretir
// ve cosre_briefings'e yazar. Takımın kataloğda servisi yoksa üretim yok
// (GET o kullanıcıya "Ekibin tanımlı değil" satırını döndürür).
//
// Kalemler Service sekmelerinin okuduğu fonksiyonlardan (uyumsuzluk = bug):
//   1 RED       — mcptools.ReadTeamServicesREDEnv (service_summary_5m /
//                 service_env_summary_5m), cur + chstore.PriorWindow
//   6 açık konu — mcptools.ReadInboxOpen (Service şeridiyle aynı Inbox çekirdeği)
//   7 rollout   — GetRecentDeploys (tik başına TEK filo okuması) adayları →
//                 servis başına GetServiceDeploys (Overview deploy işaretleri)
//                 + ComputeDeployImpact (deploy etki paneli), takım başına ≤3
// Kalem 2-5 (istemci metrikleri, exception, log, Oracle) sonraki dilimde;
// kart bunları `skipped` ile söyler (sessizce "yok" değil).
//
// Maliyet (R9): takım × env başına 2 MV + 1 inbox + ≤3×2 deploy okuması.
// Tek takımın okuma hatası diğerlerini durdurmaz; okunamayan kalem `partial`.

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	briefingTickBase       = 30 * time.Second
	briefingTeamTimeout    = 45 * time.Second
	briefingRolloutsMax    = 3
	briefingImpactWindowS  = 600
	briefingFleetDeployMax = 500
)

// pendingItems — bu dilimde henüz okunmayan kalemler (kartta `skipped`).
var briefingPendingItems = []string{briefing.ItemClient, briefing.ItemException, briefing.ItemLog, briefing.ItemOracle}

// briefingTeam — üretim birimi: kanonik anahtar + gösterim adı + kapsam.
type briefingTeam struct {
	Key, Label string
	Services   []string
	Trimmed    int
}

// briefingTeamsFrom — SAF: kullanıcıların takımları → kanonik anahtarla
// tekilleştirilmiş, sıralı liste. Engelli/devre dışı kullanıcı sayılmaz;
// gösterim adı ilk görülen (ada göre sıralı kullanıcı listesinden) yazım.
func briefingTeamsFrom(users []chstore.User, ta chstore.TeamAliases) []briefingTeam {
	sort.SliceStable(users, func(i, j int) bool { return users[i].Email < users[j].Email })
	seen := map[string]bool{}
	var out []briefingTeam
	for _, u := range users {
		if u.Disabled || u.Blocked {
			continue
		}
		label := strings.TrimSpace(u.Team)
		key := ta.CanonTeam(label)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, briefingTeam{Key: key, Label: label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// briefingTrimServices — SAF: MaxTeamServices tavanı (Q12: kırpılmış + "N servis dışarıda").
func briefingTrimServices(svcs []string) ([]string, int) {
	if len(svcs) > mcptools.MaxTeamServices {
		return svcs[:mcptools.MaxTeamServices], len(svcs) - mcptools.MaxTeamServices
	}
	return svcs, 0
}

// briefingWindow — SAF: pencere sonu son tam 5 dk kovası (yarım kova trafik düşüşü uydurmasın).
func briefingWindow(now time.Time, windowS int) (from, to time.Time) {
	to = now.UTC().Truncate(5 * time.Minute)
	return to.Add(-time.Duration(windowS) * time.Second), to
}

func toREDRows(rows []chstore.ServiceSummary) []briefing.REDRow {
	out := make([]briefing.REDRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, briefing.REDRow{Service: r.Name, Spans: r.SpanCount, Errors: r.ErrorCount, P99Ms: r.P99Ms})
	}
	return out
}

// briefingProblemRows — SAF: inbox satırları → kalem 6 girdisi.
func briefingProblemRows(rows []mcptools.InboxRow) []briefing.ProblemRow {
	out := make([]briefing.ProblemRow, 0, len(rows))
	for _, r := range rows {
		label := r.ID
		if i := strings.IndexByte(label, ':'); i >= 0 && r.Kind == "problem" {
			label = chstore.ProblemDisplayID(label[i+1:])
		}
		out = append(out, briefing.ProblemRow{ID: r.ID, DisplayID: label, Priority: r.Priority, Service: r.Service,
			Reason: r.Reason, PageURL: r.PageURL, SinceUnix: r.SinceUnix})
	}
	return out
}

// rolloutCandidates — SAF: filo deploy listesi → kapsamdaki servislerin
// pencere içindeki EN YENİ deploy'u, yeni önce, ≤ max.
func rolloutCandidates(deps []chstore.RecentDeployEntry, svcs []string, from time.Time, max int) []chstore.RecentDeployEntry {
	allow := make(map[string]bool, len(svcs))
	for _, s := range svcs {
		allow[s] = true
	}
	best := map[string]chstore.RecentDeployEntry{}
	for _, d := range deps {
		svc := strings.TrimSpace(d.Service)
		if !allow[svc] || d.FirstSeenNs < from.UnixNano() {
			continue
		}
		if b, ok := best[svc]; !ok || d.FirstSeenNs > b.FirstSeenNs {
			best[svc] = d
		}
	}
	out := make([]chstore.RecentDeployEntry, 0, len(best))
	for _, d := range best {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FirstSeenNs != out[j].FirstSeenNs {
			return out[i].FirstSeenNs > out[j].FirstSeenNs
		}
		return out[i].Service < out[j].Service
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// previousVersion — SAF: GetServiceDeploys (zaman ASC) içinde version'dan önceki sürüm.
func previousVersion(deps []chstore.Deploy, version string) (prev string, at int64, found bool) {
	for i, d := range deps {
		if d.Version == version {
			if i > 0 {
				prev = deps[i-1].Version
			}
			return prev, d.TimeUnixNs, true
		}
	}
	return "", 0, false
}

// briefingFleet — tik başına bir kez okunan, takımlar arası paylaşılan girdiler.
type briefingFleet struct {
	Deploys    []chstore.RecentDeployEntry
	DeploysErr error
}

func (s *Server) readBriefingFleet(ctx context.Context, windowS int) briefingFleet {
	deps, err := s.store.GetRecentDeploys(ctx, time.Duration(windowS)*time.Second, briefingFleetDeployMax)
	return briefingFleet{Deploys: deps, DeploysErr: err}
}

// buildBriefing — tek takım × env kartı (okumalar + saf derleme).
func (s *Server) buildBriefing(ctx context.Context, t briefingTeam, env string, cfg briefing.Config, fleet briefingFleet, now time.Time) briefing.Briefing {
	d := s.mcpDeps()
	th := cfg.Thresholds
	from, to := briefingWindow(now, cfg.WindowS)
	pFrom, pTo := chstore.PriorWindow(from, to)
	var findings []briefing.Finding
	var partial []string

	// 1 — RED. env yalnız İKİ pencereyi de ortam MV'si kapsıyorsa uygulanır
	// (biri daraltılmış biri toplam olursa oran yalan söyler).
	redEnv := ""
	if env != "" && s.store.EnvSummaryCovers(ctx, pFrom) {
		redEnv = env
	}
	cur, _, errC := mcptools.ReadTeamServicesREDEnv(ctx, d, t.Services, redEnv, from, to)
	prev, _, errP := mcptools.ReadTeamServicesREDEnv(ctx, d, t.Services, redEnv, pFrom, pTo)
	if errC != nil || errP != nil {
		partial = append(partial, briefing.ItemRED)
	} else {
		findings = append(findings, briefing.REDFindings(toREDRows(cur), toREDRows(prev), th, env, cfg.WindowS)...)
	}

	// 6 — açık konular (Inbox çekirdeği, takım birleşimi).
	if res, _, err := mcptools.ReadInboxOpen(ctx, d, t.Label, t.Services, env, mcptools.InboxOpenScan); err != nil {
		partial = append(partial, briefing.ItemProblem)
	} else {
		findings = append(findings, briefing.ProblemFindings(briefingProblemRows(res.Rows))...)
	}

	// 7 — rollout etkisi.
	if fleet.DeploysErr != nil {
		partial = append(partial, briefing.ItemRollout)
	} else {
		var rows []briefing.RolloutRow
		failed := false
		for _, c := range rolloutCandidates(fleet.Deploys, t.Services, from, briefingRolloutsMax) {
			svcDeps, err := s.store.GetServiceDeploys(ctx, c.Service, from.Add(-24*time.Hour), to)
			if err != nil {
				failed = true
				continue
			}
			prevV, at, ok := previousVersion(svcDeps, c.Version)
			if !ok {
				continue // Overview işaretlerinde yok → kartta da yok (aynı kaynak)
			}
			imp, err := s.store.ComputeDeployImpact(ctx, c.Service, c.Version, at, briefingImpactWindowS)
			if err != nil || imp == nil {
				failed = true
				continue
			}
			rows = append(rows, briefing.RolloutRow{Service: c.Service, FromVersion: prevV, ToVersion: c.Version, AtUnix: at / 1e9,
				BeforeSpans: imp.Before.Count, AfterSpans: imp.After.Count,
				ErrBeforePct: imp.Before.ErrorRate * 100, ErrAfterPct: imp.After.ErrorRate * 100})
		}
		if failed {
			partial = append(partial, briefing.ItemRollout)
		}
		findings = append(findings, briefing.RolloutFindings(rows, th, env, cfg.WindowS)...)
	}

	return briefing.Assemble(briefing.AssembleInput{
		ID: uistream.NewMessageID(), Team: t.Label, TeamKey: t.Key, Env: env, WindowS: cfg.WindowS,
		GeneratedAtMs: now.UnixMilli(), Services: t.Services, Trimmed: t.Trimmed,
		Findings: findings, Partial: partial, Skipped: briefingPendingItems,
	})
}

// storeBriefing — kartı yazar.
func (s *Server) storeBriefing(ctx context.Context, b briefing.Briefing) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return s.store.PutCosreBriefing(ctx, chstore.CosreBriefingRow{
		TeamKey: b.TeamKey, Env: b.Env, Team: b.Team, BriefingID: b.ID, ScopeHash: b.ScopeHash,
		WindowS: uint32(b.WindowS), GeneratedAt: time.UnixMilli(b.GeneratedAtMs), Silence: b.Silence, Payload: string(raw),
	})
}

// briefingTeamScope — takımın kapsamı (TeamServiceNames, owner VEYA sre; tavanlı).
func (s *Server) briefingTeamScope(ta chstore.TeamAliases, mds map[string]chstore.ServiceMetadata, t briefingTeam) briefingTeam {
	t.Services, t.Trimmed = briefingTrimServices(mcptools.TeamServiceNames(ta, mds, t.Label))
	return t
}

// runBriefingTick — bütün takımlar × env (lider).
func (s *Server) runBriefingTick(ctx context.Context, cfg briefing.Config, now time.Time) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		log.Printf("[briefing] kullanıcılar okunamadı: %v", err)
		return
	}
	mds, err := s.store.ListServiceMetadata(ctx)
	if err != nil {
		log.Printf("[briefing] katalog okunamadı: %v", err)
		return
	}
	ta := s.teamAliasesCtx(ctx)
	fleet := s.readBriefingFleet(ctx, cfg.WindowS)
	n := 0
	for _, t := range briefingTeamsFrom(users, ta) {
		t = s.briefingTeamScope(ta, mds, t)
		if len(t.Services) == 0 {
			continue
		}
		for _, env := range cfg.Envs {
			if ctx.Err() != nil {
				return
			}
			tctx, cancel := context.WithTimeout(ctx, briefingTeamTimeout)
			b := s.buildBriefing(tctx, t, env, cfg, fleet, now)
			err := s.storeBriefing(tctx, b)
			cancel()
			if err != nil {
				log.Printf("[briefing] %s/%q yazılamadı: %v", t.Key, env, err)
				continue
			}
			n++
		}
	}
	log.Printf("[briefing] %d kart üretildi (%s)", n, time.Since(now).Round(time.Millisecond))
}

// StartBriefingProducer — main (worker kipi) çağırır. Taban tik 30 s; üretim
// ayarın refresh_interval_s'inde bir (ayar değişimi bir sonraki tikte geçerli).
func (s *Server) StartBriefingProducer(ctx context.Context, lock cache.Lock) {
	leader := cache.NewLeaderHolder(lock, "cosre-briefing", cache.LeaderTTL(briefing.DefaultRefreshS*time.Second))
	leader.Start(ctx)
	var last time.Time
	tick := func() {
		if !leader.IsLeader() || s.store == nil {
			return
		}
		cfg := s.cosreBriefingConfig(ctx)
		if !cfg.Enabled || time.Since(last) < time.Duration(cfg.RefreshIntervalS)*time.Second {
			return
		}
		last = time.Now()
		s.runBriefingTick(chstore.WithQueryTag(ctx, "worker:cosre-briefing"), cfg, last)
	}
	tick()
	t := time.NewTicker(briefingTickBase)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
