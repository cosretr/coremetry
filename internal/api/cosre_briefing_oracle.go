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
//     kümesinde olmadığından takım satırına girmez.
//   - v0.10.1718 (Q6): çözülmemiş özneler TEK filo satırı — tik başına bir
//     ListExceptionGroups{Oracle: only, Services: etkin kaynakların
//     `oracle:<ad>` sentetik servisleri}; kart başına takım satırının ARDINDA.
//   - v0.10.1718: satırın üst bulgusu için bir örnek trace (OracleErrorsByOpCode,
//     PK öneki + LIMIT + max_execution_time; takım başına ≤ 1, filo tik başına
//     ≤ 1 sorgu) → kartın trace çipi.
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

// briefingOracleTraceScan — örnek trace için okunan en yeni satır sayısı
// (Oracle satırlarının çoğu trace taşımayabilir; ilk geçerli kimlik alınır).
const briefingOracleTraceScan = 20

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
	// Unresolved — Q6 filo bulguları (çözülmemiş özneler; Scope = fleet);
	// UnresolvedFailed: grup okuması başarısız → kalem partial.
	Unresolved       []briefing.Finding
	UnresolvedFailed bool
}

// readBriefingOracleFleet — etkin kaynaklar × (cur, prev) + Q6 filo satırı.
func (s *Server) readBriefingOracleFleet(ctx context.Context, cfg briefing.Config, now time.Time) briefingOracleFleet {
	if s.oracle == nil || s.store == nil {
		return briefingOracleFleet{State: "skipped"}
	}
	from, to := briefingWindow(now, cfg.WindowS)
	pFrom, pTo := chstore.PriorWindow(from, to)
	out := briefingOracleFleet{State: "ok"}
	enabled := false
	var fallbacks []string
	for _, src := range s.oracle.CurrentSettings().Sources {
		if !src.Enabled || src.ID == "" {
			continue
		}
		enabled = true
		fallbacks = append(fallbacks, chstore.OracleGroupFallbackService(src.Name))
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
	groups, err := s.store.ListExceptionGroups(ctx, chstore.ExceptionGroupFilter{
		Oracle: "only", Services: fallbacks, ActiveFromNs: pFrom.UnixNano(), ActiveToNs: to.UnixNano(), Limit: briefingOracleGroupLimit,
	})
	if err != nil {
		out.UnresolvedFailed = true
		return out
	}
	out.UnresolvedFailed = len(groups) >= briefingOracleGroupLimit
	rows := briefingOracleRows(out.Sources, groups, fallbacks)
	out.Unresolved = s.withOracleTrace(ctx, rows, briefing.OracleFleetFindings(rows, cfg.Thresholds), from, to)
	return out
}

// withOracleTrace — satırın ÜST bulgusuna (briefing.TopFinding, Lines ile aynı
// sıra) bir örnek trace kimliği ekler; okuma hatası / trace yok → çipsiz.
func (s *Server) withOracleTrace(ctx context.Context, rows []briefing.OracleRow, fs []briefing.Finding, from, to time.Time) []briefing.Finding {
	top := briefing.TopFinding(fs)
	if top < 0 {
		return fs
	}
	r, ok := briefing.OracleRowFor(rows, fs[top])
	if !ok || r.SourceID == "" {
		return fs
	}
	errs, err := s.store.OracleErrorsByOpCode(ctx, r.SourceID, r.Op, r.Code, from, to, briefingOracleTraceScan)
	if err != nil {
		return fs
	}
	ids := make([]string, 0, len(errs))
	for _, e := range errs {
		ids = append(ids, e.TraceID)
	}
	fs[top].TraceID = briefing.FirstTraceID(ids)
	return fs
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
		src, code, op string
		generic       bool
		cur, prev     uint64
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
				x = &acc{src: src.ID, code: code, op: strings.TrimSpace(a.Op), generic: src.Generic[strings.ToUpper(code)]}
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
			PageURL: "/problems?exception=" + url.QueryEscape(fp), SourceID: x.src})
	}
	return out
}
