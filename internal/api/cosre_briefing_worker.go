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
//   3 exception — aynı Inbox okumasının exception/httperror satırları: pencerede
//                 ilk görülen (new) ya da geri dönen (regressed) — Q5, R1
//   4 log       — takım ERROR sayısı, readTeamLogs (sohbet takım logları ile
//                 aynı ES okuması + önbellek), 60 dk dilimler cur + prior (Q7);
//                 env'siz (takım alanı) → metin "tüm ortamlar" der
//   6 açık konu — mcptools.ReadInboxOpen (Service şeridiyle aynı Inbox çekirdeği);
//                 kalem 3'e giden satır burada tekrar sayılmaz
//   7 rollout   — GetRecentDeploys (tik başına TEK filo okuması) adayları →
//                 servis başına GetServiceDeploys (Overview deploy işaretleri)
//                 + ComputeDeployImpact (deploy etki paneli), takım başına ≤3
//   2 istemci   — v0.10.1712, cosre_briefing_client.go: VM HTTP/gRPC sunucu
//                 ailesi (readServiceHTTPRED, en yoğun servis) + Kafka
//                 (runKafkaQuestions, takım kümesi tek sorgu); Q11 → giden
//                 HTTP istemcisi Faz 2
//   5 Oracle    — v0.10.1713, cosre_briefing_oracle.go: OracleGroupAggregates
//                 (filo, tik başına) + takımın çözülmüş ora: grupları; jenerik
//                 kod tek başına satır değil. Kaynak yok → `skipped`.
//
// Maliyet (R9): takım × env başına 2 MV + 1 inbox + ≤3×2 deploy okuması
// + 1 katalog (kalem 2 tür ayrımı, spanmetrics_1m) + ≤ 6 VM (kalem 2:
// tür başına 2 sorgu × ≤ 2 tür + 2 Kafka sorusu; v0.10.1727).
// Ölçüm (v0.10.1728): briefing.Cost çağrı noktalarında; /admin/stats
// `briefing` + tik başına `[briefing] tick …` satırı (cosre_briefing_cost.go).
// Tek takımın okuma hatası diğerlerini durdurmaz; okunamayan kalem `partial`.

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
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

// splitInboxRows — SAF: tek Inbox okuması → kalem 3 (yeni/geri dönen
// exception) + kalem 6 (geri kalan açık konular); bir satır yalnız birinde.
func splitInboxRows(rows []mcptools.InboxRow, fromUnix int64) (ex []briefing.ExceptionRow, rest []mcptools.InboxRow) {
	for _, r := range rows {
		er := briefing.ExceptionRow{ID: r.ID, Kind: r.Kind, Service: r.Service, Title: r.Title, Status: r.Status,
			Priority: r.Priority, PageURL: r.PageURL, StartedAtUnix: r.SinceUnix}
		if _, ok := briefing.ExceptionQualifies(er, fromUnix); ok {
			ex = append(ex, er)
			continue
		}
		rest = append(rest, r)
	}
	return ex, rest
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
	Oracle     briefingOracleFleet // v0.10.1713 kalem 5
}

func (s *Server) readBriefingFleet(ctx context.Context, cfg briefing.Config, now time.Time, cost *briefing.Cost) briefingFleet {
	deps, err := s.store.GetRecentDeploys(ctx, time.Duration(cfg.WindowS)*time.Second, briefingFleetDeployMax)
	cost.AddCH(1)
	return briefingFleet{Deploys: deps, DeploysErr: err, Oracle: s.readBriefingOracleFleet(ctx, cfg, now, cost)}
}

// briefingLogs — kalem 4 okuması (takım başına bir kez; env'ler paylaşır).
type briefingLogs struct {
	State      string // ok | unsupported | partial
	Cur, Prev  int64
	LowerBound bool
}

// readBriefingLogs — cur + prior pencere, ≤60 dk dilimler; dilimler readTeamLogs'tan.
func (s *Server) readBriefingLogs(ctx context.Context, team string, windowS int, now time.Time, cost *briefing.Cost) briefingLogs {
	fields := s.teamLogFields()
	if len(fields) == 0 || s.logs == nil {
		return briefingLogs{State: teamLogsUnsupported}
	}
	from, to := briefingWindow(now, windowS)
	pFrom, pTo := chstore.PriorWindow(from, to)
	values := s.resolveTeamLogValues(ctx, team)
	cfgDigest := s.teamLogsCfgDigest()
	out := briefingLogs{State: teamLogsOK}
	sum := func(a, b time.Time, dst *int64) bool {
		for _, sl := range briefing.LogSlices(a.Unix(), b.Unix()) {
			o := s.readTeamLogs(ctx, cfgDigest, fields, values, time.Unix(sl[0], 0), time.Unix(sl[1], 0))
			if o.issued {
				if s.logs.Backend() == "elasticsearch" {
					cost.AddES(1)
				} else {
					cost.AddCH(1)
				}
			}
			if o.State == teamLogsUnsupported {
				out.State = teamLogsUnsupported
				return false
			}
			if o.State != teamLogsOK || o.Res == nil {
				out.State = "partial"
				return false
			}
			*dst += o.Res.ErrorCount
			out.LowerBound = out.LowerBound || o.Res.CountIsLowerBound
		}
		return true
	}
	if sum(from, to, &out.Cur) {
		sum(pFrom, pTo, &out.Prev)
	}
	return out
}

// briefingLogsURL — SAF: takımın /logs görünümü (team= süzgeci, v0.10.1477).
func briefingLogsURL(team string, windowS int) string {
	return "/logs?team=" + url.QueryEscape(team) + "&range=" + briefing.RangeParam(windowS)
}

// buildBriefing — tek takım × env kartı (okumalar + saf derleme).
func (s *Server) buildBriefing(ctx context.Context, t briefingTeam, env string, cfg briefing.Config, fleet briefingFleet, logs briefingLogs, now time.Time, cost *briefing.Cost) briefing.Briefing {
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
	cost.AddCH(2)
	if errC != nil || errP != nil {
		partial = append(partial, briefing.ItemRED)
	} else {
		findings = append(findings, briefing.REDFindings(toREDRows(cur), toREDRows(prev), th, env, cfg.WindowS)...)
	}

	// 2 — istemci / sunucu-ailesi metrikleri (VM + Kafka).
	var skipped []string
	switch rows, state := s.readBriefingClient(ctx, t.Services, cur, env, pFrom, from, to, cost); state {
	case "skipped":
		skipped = append(skipped, briefing.ItemClient)
	case "partial":
		partial = append(partial, briefing.ItemClient)
		fallthrough
	default:
		findings = append(findings, briefing.ClientFindings(rows, th, env, cfg.WindowS)...)
	}

	// 3 + 6 — tek Inbox okuması (şerit kümesi, takım birleşimi).
	res, _, err := mcptools.ReadInboxOpen(ctx, d, t.Label, t.Services, env, mcptools.InboxOpenScan)
	cost.AddCH(1)
	if err != nil {
		partial = append(partial, briefing.ItemException, briefing.ItemProblem)
	} else {
		ex, rest := splitInboxRows(res.Rows, from.Unix())
		findings = append(findings, briefing.ExceptionFindings(ex, from.Unix())...)
		findings = append(findings, briefing.ProblemFindings(briefingProblemRows(rest))...)
	}

	// 4 — takım ERROR logları.
	switch logs.State {
	case teamLogsOK:
		findings = append(findings, briefing.LogFindings(t.Label, logs.Cur, logs.Prev, logs.LowerBound, th, briefingLogsURL(t.Label, cfg.WindowS))...)
	case teamLogsUnsupported:
		skipped = append(skipped, briefing.ItemLog)
	default:
		partial = append(partial, briefing.ItemLog)
	}

	// 5 — Oracle hata kodları (env'siz; çözülmüş ora: grupları).
	switch o := s.readBriefingOracleTeam(ctx, fleet.Oracle, t.Services, cfg.WindowS, now, cost); o.State {
	case "skipped":
		skipped = append(skipped, briefing.ItemOracle)
	case "partial":
		partial = append(partial, briefing.ItemOracle)
		fallthrough
	default:
		findings = append(findings, s.withOracleTrace(ctx, o.Rows, briefing.OracleFindings(o.Rows, th), from, to, cost)...)
	}
	// 5b — Q6 filo satırı (tik başına okunmuş; takım satırından ayrı).
	if fleet.Oracle.State != "skipped" {
		if fleet.Oracle.UnresolvedFailed {
			partial = append(partial, briefing.ItemOracle)
		}
		findings = append(findings, fleet.Oracle.Unresolved...)
	}

	// 7 — rollout etkisi.
	if fleet.DeploysErr != nil {
		partial = append(partial, briefing.ItemRollout)
	} else {
		var rows []briefing.RolloutRow
		failed := false
		for _, c := range rolloutCandidates(fleet.Deploys, t.Services, from, briefingRolloutsMax) {
			svcDeps, err := s.store.GetServiceDeploys(ctx, c.Service, from.Add(-24*time.Hour), to)
			cost.AddCH(1)
			if err != nil {
				failed = true
				continue
			}
			prevV, at, ok := previousVersion(svcDeps, c.Version)
			if !ok {
				continue // Overview işaretlerinde yok → kartta da yok (aynı kaynak)
			}
			imp, err := s.store.ComputeDeployImpact(ctx, c.Service, c.Version, at, briefingImpactWindowS)
			cost.AddCH(1)
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
		Findings: findings, Partial: partial, Skipped: skipped,
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
	cost := &briefing.Cost{}
	teams := 0
	defer func() {
		tk := cost.Tick(now.Unix(), teams, time.Since(now).Milliseconds())
		recordBriefingTick(tk)
		log.Print(briefing.TickLogLine(tk))
	}()
	users, err := s.store.ListUsers(ctx)
	cost.AddCH(1)
	if err != nil {
		log.Printf("[briefing] kullanıcılar okunamadı: %v", err)
		return
	}
	mds, err := s.store.ListServiceMetadata(ctx)
	cost.AddCH(1)
	if err != nil {
		log.Printf("[briefing] katalog okunamadı: %v", err)
		return
	}
	ta := s.teamAliasesCtx(ctx)
	fleet := s.readBriefingFleet(ctx, cfg, now, cost)
	n := 0
	for _, t := range briefingTeamsFrom(users, ta) {
		t = s.briefingTeamScope(ta, mds, t)
		if len(t.Services) == 0 {
			continue
		}
		teams++
		lctx, lcancel := context.WithTimeout(ctx, briefingTeamTimeout)
		logs := s.readBriefingLogs(lctx, t.Label, cfg.WindowS, now, cost)
		lcancel()
		for _, env := range cfg.Envs {
			if ctx.Err() != nil {
				return
			}
			tctx, cancel := context.WithTimeout(ctx, briefingTeamTimeout)
			b := s.buildBriefing(tctx, t, env, cfg, fleet, logs, now, cost)
			s.phraseBriefing(tctx, &b, cfg)
			err := s.storeBriefing(tctx, b)
			cost.AddCH(1)
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
