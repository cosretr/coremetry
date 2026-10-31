package api

// cosre_briefing_oracle.go — v0.10.1713: Login Briefing kalem 5
// (oracle_error_diff; briefing-audit "Tasarım önerisi", R5 + Q6).
//
// Okuma (Exceptions'ın Oracle grup modeliyle AYNI kaynak):
//   - filo, tik başına BİR kez: etkin her kaynak için OracleGroupAggregates
//     cur + prev (oracle_error_log FINAL, PK öneki source_id+time, LIMIT +
//     max_execution_time — GroupStatsCache'in okuması). Tavana çarpan okuma
//     son dakikaları atar → partial (eksik sayı "yok" sanılmasın).
//   - takım: ListExceptionGroups{Oracle: only, Services: takım} → parmak izi
//     → çözülmüş servis (R5). Çözülmemiş özne (`oracle:<kaynak>`) takım
//     kümesinde olmadığından satıra girmez (Q6: ayrı filo satırı, sonraki iş).
// Jenerik kod: kaynağın GenericCodes kümesi (oracle.GenericSet, QualifierFor
// ile aynı) — tek başına satır üretmez (briefing.OracleFindings).

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
	"github.com/cilcenk/coremetry/internal/oracle"
)

// briefingOracleGroupLimit — takım başına Oracle grup okuması tavanı.
const briefingOracleGroupLimit = 500

// briefingOracleSource — bir kaynağın iki pencere toplamı + jenerik kümesi.
type briefingOracleSource struct {
	ID        string
	Generic   map[string]bool
	Cur, Prev []chstore.OracleGroupAgg
}

// briefingOracleFleet — filo okuması. State: ok | skipped | partial.
type briefingOracleFleet struct {
	State   string
	Sources []briefingOracleSource
}

// readBriefingOracleFleet — etkin kaynaklar × (cur, prev).
func (s *Server) readBriefingOracleFleet(ctx context.Context, windowS int, now time.Time) briefingOracleFleet {
	if s.oracle == nil || s.store == nil {
		return briefingOracleFleet{State: "skipped"}
	}
	from, to := briefingWindow(now, windowS)
	pFrom, pTo := chstore.PriorWindow(from, to)
	out := briefingOracleFleet{State: "ok"}
	enabled := false
	for _, src := range s.oracle.CurrentSettings().Sources {
		if !src.Enabled || src.ID == "" {
			continue
		}
		enabled = true
		cur, errC := s.store.OracleGroupAggregates(ctx, src.ID, from, to)
		prev, errP := s.store.OracleGroupAggregates(ctx, src.ID, pFrom, pTo)
		if errC != nil || errP != nil || len(cur) >= chstore.OracleGroupAggLimit || len(prev) >= chstore.OracleGroupAggLimit {
			out.State = "partial"
			if errC != nil || errP != nil {
				continue
			}
		}
		out.Sources = append(out.Sources, briefingOracleSource{ID: src.ID, Generic: oracle.GenericSet(src), Cur: cur, Prev: prev})
	}
	if !enabled {
		return briefingOracleFleet{State: "skipped"}
	}
	return out
}

// briefingOracleTeam — takımın kalem 5 girdisi (env'ler paylaşır; Oracle env taşımaz).
type briefingOracleTeam struct {
	State string
	Rows  []briefing.OracleRow
}

// readBriefingOracleTeam — takımın Oracle grupları → satırlar.
func (s *Server) readBriefingOracleTeam(ctx context.Context, fleet briefingOracleFleet, svcs []string, windowS int, now time.Time) briefingOracleTeam {
	if fleet.State == "skipped" {
		return briefingOracleTeam{State: "skipped"}
	}
	from, to := briefingWindow(now, windowS)
	pFrom, _ := chstore.PriorWindow(from, to)
	groups, err := s.store.ListExceptionGroups(ctx, chstore.ExceptionGroupFilter{
		Oracle: "only", Services: svcs, ActiveFromNs: pFrom.UnixNano(), ActiveToNs: to.UnixNano(), Limit: briefingOracleGroupLimit,
	})
	if err != nil {
		return briefingOracleTeam{State: "partial"}
	}
	state := fleet.State
	if len(groups) >= briefingOracleGroupLimit {
		state = "partial"
	}
	return briefingOracleTeam{State: state, Rows: briefingOracleRows(fleet.Sources, groups, svcs)}
}

// briefingOracleRows — SAF: kaynak toplamları (dakika × kanal satırları) →
// parmak izi başına cur/prev ağırlığı; yalnız takımın ÇÖZÜLMÜŞ gruplarına
// (servis takım kümesinde) bağlanan parmak izleri satır olur.
func briefingOracleRows(sources []briefingOracleSource, groups []chstore.ExceptionGroup, svcs []string) []briefing.OracleRow {
	team := make(map[string]bool, len(svcs))
	for _, s := range svcs {
		team[s] = true
	}
	svcOf := map[string]string{}
	for _, g := range groups {
		if chstore.IsOracleGroup(g.Fingerprint) && team[g.Service] {
			svcOf[g.Fingerprint] = g.Service
		}
	}
	type acc struct {
		code, op  string
		generic   bool
		cur, prev uint64
	}
	by := map[string]*acc{}
	add := func(src briefingOracleSource, aggs []chstore.OracleGroupAgg, isCur bool) {
		for _, a := range aggs {
			fp := chstore.OracleGroupFingerprint(src.ID, a.Code, a.Op)
			if _, ok := svcOf[fp]; !ok {
				continue
			}
			x := by[fp]
			if x == nil {
				code := strings.TrimSpace(a.Code)
				x = &acc{code: code, op: strings.TrimSpace(a.Op), generic: src.Generic[strings.ToUpper(code)]}
				by[fp] = x
			}
			if isCur {
				x.cur += a.Weight
			} else {
				x.prev += a.Weight
			}
		}
	}
	for _, src := range sources {
		add(src, src.Cur, true)
		add(src, src.Prev, false)
	}
	fps := make([]string, 0, len(by))
	for fp := range by {
		fps = append(fps, fp)
	}
	sort.Strings(fps)
	out := make([]briefing.OracleRow, 0, len(fps))
	for _, fp := range fps {
		x := by[fp]
		out = append(out, briefing.OracleRow{Service: svcOf[fp], Code: x.code, Op: x.op, Generic: x.generic, Cur: x.cur, Prev: x.prev,
			PageURL: "/problems?exception=" + url.QueryEscape(fp)})
	}
	return out
}
