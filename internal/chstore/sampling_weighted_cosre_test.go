package chstore

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

// v0.10.2457 — CoSRE lookup readers under sampling weight.

// cosreReaderSQLs — every touched reader text, built the way the store
// builds it with weighting OFF (w=false / no coverage).
func cosreReaderSQLs(w bool) map[string]string {
	out := map[string]string{}
	s, _ := endpointRoutesSQL(nil, "x")
	out["routes_frag"] = s
	s, _ = endpointRoutesSQL([]string{"a"}, "")
	out["routes_svc"] = s
	s, _ = endpointObservedSQL("cluster", EndpointObservedQuery{Services: []string{"a"}, Host: "h", URL: "/u"}, w)
	out["observed_svc"] = s
	s, _ = endpointObservedSQL("cluster", EndpointObservedQuery{Host: "h"}, w)
	out["observed_host"] = s
	for _, m := range []string{WhereModeErrors, WhereModeSlow, WhereModeMessaging} {
		s, _ = whereSQL("cluster", WhereQuery{Service: "a", Mode: m, Peer: "osb", MinMs: 5}, w)
		out["where_"+m] = s
	}
	out["flow"] = messagingFlowSQL("t", "s")
	out["flow_topic"] = messagingFlowSQL("t", "")
	out["downstream"] = downstreamEdgesSQLFor(w)
	out["hop_down_env"] = DependencyHopSQLW(false, true, w)
	out["hop_up"] = DependencyHopSQLW(true, false, w)
	out["dep_red"] = dependencyServiceREDSQL()
	out["errprof_env"] = ServiceErrorProfileSQLW(true, w)
	out["errprof"] = ServiceErrorProfileSQLW(false, w)
	out["optotals"] = ServiceOpTotalsSQL
	return out
}

// cosreOffHashes — sha256[:16] of the v0.10.2456 texts (captured before
// this change): mode off / shadow must keep them byte for byte.
var cosreOffHashes = map[string]string{
	"routes_frag":     "59886e70399d1555",
	"routes_svc":      "6e50389ddb359b81",
	"observed_svc":    "67bc139aa9258cdc",
	"observed_host":   "04ef4ec5e772bb26",
	"where_errors":    "3db8491c5142c1d2",
	"where_slow":      "dc63f60af1218921",
	"where_messaging": "fa0522b6728cd038",
	"flow":            "8b3c2a86bf7f7c32",
	"flow_topic":      "0a3010cf78820760",
	"downstream":      "3b86052106c229fc",
	"hop_down_env":    "379a578cb5a25c72",
	"hop_up":          "4eaffe3699304a3f",
	"dep_red":         "a05e7c8ac5f3026c",
	"errprof_env":     "91f6e9d353b3e208",
	"errprof":         "34e230e09e235e5d",
	"optotals":        "14d2f0615978701d",
}

func sqlHash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))[:16] }

func TestCoSREReadersModeOffIdentical(t *testing.T) {
	markers := map[string]time.Time{}
	for _, mv := range weightedMVNames() {
		markers[mv] = time.Unix(1_700_000_000, 0)
	}
	from := time.Unix(1_800_000_000, 0)
	got := cosreReaderSQLs(false)
	if len(got) != len(cosreOffHashes) {
		t.Fatalf("reader set changed: %d vs %d", len(got), len(cosreOffHashes))
	}
	for name, sql := range got {
		if h := sqlHash(sql); h != cosreOffHashes[name] {
			t.Errorf("%s: mode-off SQL changed (hash %s, want %s)", name, h, cosreOffHashes[name])
		}
		for _, mode := range []string{SamplingModeOff, SamplingModeShadow, ""} {
			for _, mv := range []string{"spanmetrics_1m", "messaging_caller_summary_5m", "service_summary_5m", "operation_summary_5m"} {
				c := weightedCoverage(mode, markers, from, mv)
				args := []any{1, 2, 3}
				if out, a := weighSQLArgsWith(c, sql, args); out != sql || len(a) != 3 {
					t.Errorf("%s: mode %q rewrote SQL / binds", name, mode)
				}
			}
		}
	}
	// Topology: no read weighting with mode off, whatever the marker.
	if topoPeriodsWeightedWith(false, time.Time{}, from) {
		t.Error("topology weighted with read off")
	}
}

func TestCoSREReadersWeighted(t *testing.T) {
	off, on := cosreReaderSQLs(false), cosreReaderSQLs(true)
	full := weightCov{since: time.Unix(1_700_000_000, 0), full: true}
	mvOf := map[string]bool{"routes_frag": true, "routes_svc": true, "flow": true, "flow_topic": true, "dep_red": true, "optotals": true}
	for name, sql := range on {
		if mvOf[name] {
			sql = weighSQLWith(full, sql)
		}
		if sql == off[name] {
			t.Errorf("%s: weighting changed nothing", name)
			continue
		}
		if countPlaceholders(sql) != countPlaceholders(off[name]) {
			t.Errorf("%s: placeholder count changed (%d → %d)", name, countPlaceholders(off[name]), countPlaceholders(sql))
		}
		switch {
		case strings.HasPrefix(name, "observed"), strings.HasPrefix(name, "where"), strings.HasPrefix(name, "errprof"):
			if strings.Contains(sql, "count()") || !strings.Contains(sql, "sum(toFloat64(sample_weight))") || !strings.Contains(sql, ", sample_weight\n  FROM spans") {
				t.Errorf("%s: raw counts not weighted / weight not projected:\n%s", name, sql)
			}
		case name == "downstream", strings.HasPrefix(name, "hop"):
			if !strings.Contains(sql, topoWCalls) || !strings.Contains(sql, topoWErrors) {
				t.Errorf("%s: topology calls not weighted", name)
			}
		default:
			if strings.Contains(sql, "countMerge(") || strings.Contains(sql, "countIfMerge(") {
				t.Errorf("%s: stored merge left in a fully covered window:\n%s", name, sql)
			}
		}
	}
	// Raw quantiles + countIf → weighted twins; trace ids stay stored.
	if w := on["where_errors"]; !strings.Contains(w, "sumIf(toFloat64(sample_weight), to_flag)") || !strings.Contains(w, "quantileTDigestWeighted(0.95)") || !strings.Contains(w, "argMax(trace_id, duration)") {
		t.Errorf("trace_where weighted text:\n%s", w)
	}
	if e := on["errprof"]; !strings.Contains(e, "uniq(trace_id)") {
		t.Error("error profile: distinct error traces must stay stored")
	}
	// Topology avg keeps Σd / stored calls.
	if d := on["downstream"]; !strings.Contains(d, "sum(sum_duration_ns) / nullIf(sum(calls), 0)") {
		t.Errorf("downstream avg must divide by stored calls:\n%s", d)
	}
	if h := on["hop_up"]; !strings.Contains(h, "AS cn, sumIf(calls, time_bucket < cur_from) AS rn\nFROM topology_edges_5m") {
		t.Errorf("hop: stored-call tail missing:\n%s", h)
	}
	// MessagingFlow GLOBAL IN subquery untouched by the rewrite.
	if f := weighSQLWith(full, off["flow"]); !strings.Contains(f, "destination GLOBAL IN (SELECT destination FROM messaging_caller_summary_5m") {
		t.Error("flow: GLOBAL IN subquery changed")
	}
}

func TestTopoPeriodsWeightedWith(t *testing.T) {
	ref := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name    string
		read    bool
		onSince time.Time
		want    bool
	}{
		{"read off", false, ref.Add(-time.Hour), false},
		{"on before reference", true, ref.Add(-time.Hour), true},
		{"on at reference", true, ref, true},
		{"on inside the pair → stored both sides", true, ref.Add(time.Hour), false},
		{"onSince unknown", true, time.Time{}, true},
	}
	for _, c := range cases {
		if got := topoPeriodsWeightedWith(c.read, c.onSince, ref); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
