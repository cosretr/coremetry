package chstore

// rollout_v2_store_test.go — v0.10.982 — Rollouts v2 P2.2 CH kapısı
// sözleşmesi (docs/rollouts/v2-audit.md §10.2, §10.3, §10.6):
//
//   - Okumalar FINAL + küme süzgeci + keyset (ORDER BY önekinde) + LIMIT +
//     max_execution_time; olay okuması (iş yükü, incarnation) başına
//     generation'ı en büyük satırı verir (LIMIT 1 BY).
//   - INSERT kolon listeleri V2Event / V2WorkloadState `ch` etiketleriyle
//     SIRASIYLA aynı (v2model_test.go bunları 0015 DDL'ine pinler → zincir).
//   - Bağlama: DEFAULT 0 zamanları epoch'a (V2CHTime), diziler nil değil,
//     açık istemci version'ı; yazıcı sözleşmesini ihlal eden satır batch'e
//     girmeden reddedilir.
//   - rollout_worker_runs: sayaçlar DDL tiplerine kelepçeli, version açık.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
)

func chTagList(v any) string {
	t := reflect.TypeOf(v)
	tags := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tags = append(tags, t.Field(i).Tag.Get("ch"))
	}
	return strings.Join(tags, ", ")
}

func normCols(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestRolloutV2ColsMatchModel(t *testing.T) {
	if got, want := normCols(rolloutV2EventCols), chTagList(rollout.V2Event{}); got != want {
		t.Fatalf("rollout_events kolonları modelden sapmış:\n got %s\nwant %s", got, want)
	}
	if got, want := normCols(rolloutV2StateCols), chTagList(rollout.V2WorkloadState{}); got != want {
		t.Fatalf("rollout_workload_state kolonları modelden sapmış:\n got %s\nwant %s", got, want)
	}
	for _, ddlCol := range []string{"scopes_total", "partial_response", "api_throttled", "duration_ms", "version"} {
		if !strings.Contains(rolloutWorkerRunCols, ddlCol) {
			t.Errorf("rollout_worker_runs kolonu eksik: %s", ddlCol)
		}
	}
	if n := len(strings.Split(rolloutWorkerRunCols, ",")); n != 17 {
		t.Fatalf("rollout_worker_runs 17 kolon (DDL): %d", n)
	}
}

func TestRolloutV2ReadSQLShape(t *testing.T) {
	st := rolloutV2StatesPageSQL()
	for _, want := range []string{
		"FROM rollout_workload_state FINAL",
		"WHERE cluster_id = ?",
		"AND (namespace, workload_kind, workload) > (?, ?, ?)",
		"ORDER BY namespace, workload_kind, workload",
		"LIMIT 20000",
		"max_execution_time = 15",
	} {
		if !strings.Contains(st, want) {
			t.Errorf("durum sorgusunda %q yok:\n%s", want, st)
		}
	}
	if strings.Count(st, "?") != 4 {
		t.Errorf("durum sorgusu 4 bağ: %d", strings.Count(st, "?"))
	}
	ev := rolloutV2LatestEventsPageSQL()
	for _, want := range []string{
		"FROM rollout_events FINAL",
		"WHERE cluster_id = ?",
		"AND (namespace, workload_kind, workload, incarnation_at) > (?, ?, ?, toDateTime64(?, 3, 'UTC'))",
		"ORDER BY namespace, workload_kind, workload, incarnation_at, generation DESC",
		"LIMIT 1 BY namespace, workload_kind, workload, incarnation_at",
		"LIMIT 20000",
		"max_execution_time = 15",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("olay sorgusunda %q yok:\n%s", want, ev)
		}
	}
	// LIMIT 1 BY, sayfa LIMIT'inden ÖNCE gelmeli (CH sözdizimi + anlam).
	if strings.Index(ev, "LIMIT 1 BY") > strings.Index(ev, "LIMIT 20000") {
		t.Error("LIMIT 1 BY sayfa LIMIT'inden önce olmalı")
	}
	for _, q := range []string{st, ev} {
		if strings.Contains(q, "coremetry.") {
			t.Error("tablolar niteliksiz anılmalı")
		}
	}
}

func v2TestEvent() rollout.V2Event {
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	return rollout.V2Event{ClusterID: "c-a", Namespace: "pay", WorkloadKind: rollout.V2KindDeployment, Workload: "api",
		IncarnationAt: t0, Generation: 2, StartedAt: t0.Add(time.Minute), Status: rollout.V2StatusProgressing,
		ChangeType: rollout.V2ChangeRollout, UpdatedAt: t0.Add(2 * time.Minute), Version: 42}
}

func v2TestState() rollout.V2WorkloadState {
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	return rollout.V2WorkloadState{ClusterID: "c-a", Namespace: "pay", WorkloadKind: rollout.V2KindDeployment, Workload: "api",
		IncarnationAt: t0, Generation: 2, FirstSeenAt: t0, LastSeenAt: t0.Add(time.Minute), Version: 43}
}

func TestRolloutV2EventArgs(t *testing.T) {
	e := v2TestEvent()
	args := rolloutV2EventArgs(e)
	if len(args) != len(strings.Split(rolloutV2EventCols, ",")) {
		t.Fatalf("argüman sayısı %d ≠ kolon sayısı", len(args))
	}
	epoch := time.Unix(0, 0).UTC()
	// succeeded_at, stuck_at, finished_at (19–21) → epoch sentinel'i.
	for _, i := range []int{19, 20, 21} {
		if got := args[i].(time.Time); !got.Equal(epoch) {
			t.Errorf("arg %d DEFAULT 0 zamanı epoch olmalı: %v", i, got)
		}
	}
	for _, i := range []int{15, 16} {
		if got, ok := args[i].([]string); !ok || got == nil {
			t.Errorf("arg %d dizi nil olmamalı: %#v", i, args[i])
		}
	}
	if args[24].(uint64) != 42 {
		t.Error("açık version bağlanmalı")
	}
	s := v2TestState()
	sargs := rolloutV2StateArgs(s)
	if len(sargs) != len(strings.Split(rolloutV2StateCols, ",")) {
		t.Fatalf("durum argüman sayısı %d", len(sargs))
	}
	if got := sargs[8].(time.Time); !got.Equal(epoch) {
		t.Errorf("pending_started_at epoch olmalı: %v", got)
	}
}

func TestRolloutV2ValidateBatch(t *testing.T) {
	if err := validateV2Events([]rollout.V2Event{v2TestEvent()}); err != nil {
		t.Fatalf("geçerli olay reddedildi: %v", err)
	}
	bad := v2TestEvent()
	bad.StartedAt = time.Time{} // TTL çapası sıfır → satır 1970'e yazılır ve düşerdi
	if err := validateV2Events([]rollout.V2Event{v2TestEvent(), bad}); err == nil || !strings.Contains(err.Error(), "started_at") {
		t.Fatalf("sıfır çapalı olay batch'i reddetmeli: %v", err)
	}
	st := v2TestState()
	st.LastSeenAt = time.Unix(0, 0)
	if err := validateV2States([]rollout.V2WorkloadState{st}); err == nil || !strings.Contains(err.Error(), "last_seen_at") {
		t.Fatalf("epoch çapalı durum reddedilmeli: %v", err)
	}
	noVer := v2TestState()
	noVer.Version = 0
	if validateV2States([]rollout.V2WorkloadState{noVer}) == nil {
		t.Fatal("version 0 reddedilmeli (açık istemci version'ı şart)")
	}
}

func TestRolloutWorkerRunArgs(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	r := rollout.WorkerRun{Worker: rollout.WorkerRolloutDetector, StartedAt: t0, FinishedAt: t0.Add(1500 * time.Millisecond),
		Host: "pod-1", Status: rollout.RunPartial, ScopesTotal: 70000, ScopesOK: -1, SeriesRead: 12,
		Truncated: true, RowsWritten: 3, APICalls: 7, DurationMs: 1500, Error: strings.Repeat("x", 9000)}
	args := rolloutWorkerRunArgs(r)
	if len(args) != 17 {
		t.Fatalf("17 argüman: %d", len(args))
	}
	if args[5].(uint16) != 65535 || args[6].(uint16) != 0 {
		t.Errorf("UInt16 kelepçe: %v %v", args[5], args[6])
	}
	if args[8].(uint8) != 1 || args[9].(uint8) != 0 {
		t.Errorf("bool → UInt8: %v %v", args[8], args[9])
	}
	if n := len([]rune(args[15].(string))); n > rolloutWorkerRunErrorMax {
		t.Errorf("error kırpılmalı: %d", n)
	}
	if v := args[16].(uint64); v != uint64(t0.Add(1500*time.Millisecond).UnixNano()) {
		t.Errorf("version = finished_at ns: %d", v)
	}
}
