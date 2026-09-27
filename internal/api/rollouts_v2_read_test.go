package api

// rollouts_v2_read_test.go — v0.10.984 — Rollouts v2 P2.3 okuma yolu
// (rollouts_v2_read.go başlığı). Sahte depo (rolloutReaderOf dikişi), canlı
// CH YOK:
//
//   - source=v1 (varsayılan): uçlar v1 okumalarını çağırır, v2 okumasına
//     HİÇ dokunmaz ve gövde bugünkü şekilde (ek anahtar yok) — "deploy
//     davranış değiştirmez" pini.
//   - source=v2: rollout_events okumaları, v1 sözlüğünde durum, eklemeli
//     "v2" işareti, dedektör notu / koşuları.
//   - 6 parçalı anahtar yalnız v2'de; 5 parçalı eski bağlantı v2'de de
//     workload_rollouts'tan açılır (karar 14); v1'de 6 parçalı istek
//     bugünkü 400.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
	"github.com/cilcenk/coremetry/internal/thanos"
)

type fakeRolloutReader struct {
	calls   []string
	v1Rows  []chstore.RolloutRow
	v2Rows  []chstore.RolloutRow
	v2ID    chstore.RolloutV2ID
	v1ID    chstore.RolloutID
	v2Stats *chstore.RolloutStats
	v1Stats *chstore.RolloutStats
	wRuns   []rollout.WorkerRun
	v1Runs  []rollout.Run
	lastW   *rollout.WorkerRun
	lastV1  *rollout.Run
	filter  chstore.RolloutFilter
	// tail: dolu ise sonraki kursör ve hata (rows.Err() ikizi: ilerlemiş
	// kursör + hata birlikte döner).
	tailRows  []chstore.RolloutRow
	tailNext  *chstore.RolloutCursor
	tailNext2 *chstore.RolloutV2Cursor
	tailErr   error
}

func (f *fakeRolloutReader) hit(n string) { f.calls = append(f.calls, n) }

func (f *fakeRolloutReader) RolloutList(_ context.Context, fl chstore.RolloutFilter, _, _ time.Time, _ int) ([]chstore.RolloutRow, error) {
	f.hit("v1.list")
	f.filter = fl
	return f.v1Rows, nil
}
func (f *fakeRolloutReader) RolloutV2List(_ context.Context, fl chstore.RolloutFilter, _, _ time.Time, _ int) ([]chstore.RolloutRow, error) {
	f.hit("v2.list")
	f.filter = fl
	return f.v2Rows, nil
}
func (f *fakeRolloutReader) RolloutByID(_ context.Context, id chstore.RolloutID) (*chstore.RolloutRow, error) {
	f.hit("v1.one")
	f.v1ID = id
	if len(f.v1Rows) == 0 {
		return nil, nil
	}
	return &f.v1Rows[0], nil
}
func (f *fakeRolloutReader) RolloutV2ByID(_ context.Context, id chstore.RolloutV2ID) (*chstore.RolloutRow, error) {
	f.hit("v2.one")
	f.v2ID = id
	if len(f.v2Rows) == 0 {
		return nil, nil
	}
	return &f.v2Rows[0], nil
}
func (f *fakeRolloutReader) RolloutStats(context.Context, string, string, time.Time, time.Time, int) (*chstore.RolloutStats, error) {
	f.hit("v1.stats")
	return f.v1Stats, nil
}
func (f *fakeRolloutReader) RolloutV2Stats(context.Context, string, string, time.Time, time.Time, int) (*chstore.RolloutStats, error) {
	f.hit("v2.stats")
	return f.v2Stats, nil
}
func (f *fakeRolloutReader) RolloutRuns(context.Context, int) ([]rollout.Run, error) {
	f.hit("v1.runs")
	return f.v1Runs, nil
}
func (f *fakeRolloutReader) RolloutWorkerRuns(_ context.Context, worker string, _ int) ([]rollout.WorkerRun, error) {
	f.hit("v2.runs:" + worker)
	return f.wRuns, nil
}
func (f *fakeRolloutReader) RolloutLastRun(context.Context) (*rollout.Run, error) {
	f.hit("v1.last")
	return f.lastV1, nil
}
func (f *fakeRolloutReader) RolloutWorkerLastRun(_ context.Context, worker string) (*rollout.WorkerRun, error) {
	f.hit("v2.last:" + worker)
	return f.lastW, nil
}
func (f *fakeRolloutReader) RolloutTail(_ context.Context, c chstore.RolloutCursor, _ time.Duration, _ int) ([]chstore.RolloutRow, chstore.RolloutCursor, error) {
	f.hit("v1.tail")
	if f.tailNext != nil {
		return f.tailRows, *f.tailNext, f.tailErr
	}
	return nil, c, nil
}
func (f *fakeRolloutReader) RolloutV2Tail(_ context.Context, c chstore.RolloutV2Cursor, _ time.Duration, _ int) ([]chstore.RolloutRow, chstore.RolloutV2Cursor, error) {
	f.hit("v2.tail")
	if f.tailNext2 != nil {
		return f.tailRows, *f.tailNext2, f.tailErr
	}
	return nil, c, nil
}
func (f *fakeRolloutReader) RolloutsForWorkloads(context.Context, []rollout.Key, time.Time, time.Time) ([]chstore.RolloutRow, error) {
	f.hit("v1.workloads")
	return f.v1Rows, nil
}
func (f *fakeRolloutReader) RolloutV2ForWorkloads(context.Context, []rollout.Key, time.Time, time.Time) ([]chstore.RolloutRow, error) {
	f.hit("v2.workloads")
	return f.v2Rows, nil
}

func (f *fakeRolloutReader) touched(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

var rv2T0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func rv2V1Row() chstore.RolloutRow {
	return chstore.RolloutRow{Rollout: rollout.Rollout{ClusterID: "c-1", Namespace: "pay", Workload: "api", Kind: "Deployment",
		Revision: "api-7f", StartedAt: rv2T0, Status: rollout.StatusCompleted, DetectedBy: "spans", SpanCount: 12}, UpdatedAt: rv2T0.Add(time.Minute)}
}

func rv2V2Row() chstore.RolloutRow {
	return chstore.RolloutRowFromV2(rollout.V2Event{ClusterID: "c-1", Namespace: "pay", WorkloadKind: "Deployment", Workload: "api",
		IncarnationAt: rv2T0.Add(-24 * time.Hour), Generation: 7, StartedAt: rv2T0, Status: rollout.V2StatusSucceeded,
		ChangeType: rollout.V2ChangeRollout, NewRevision: "api-8a", OldRevision: "api-7f",
		Images: []string{"reg/api:2.0"}, PrevImages: []string{"reg/api:1.9"}, SucceededAt: rv2T0.Add(90 * time.Second),
		FinishedAt: rv2T0.Add(90 * time.Second), UpdatedAt: rv2T0.Add(2 * time.Minute), Version: 1})
}

func newRolloutReadEnv(t *testing.T, src string, fr *fakeRolloutReader) *http.ServeMux {
	t.Helper()
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats()}
	cfg := rollout.NewSettingsService()
	st := rollout.DefaultSettings()
	st.Enabled, st.Source = true, src
	cfg.Configure(st)
	s.SetRollout(cfg)
	prev := rolloutReaderOf
	rolloutReaderOf = func(*Server) rolloutReader { return fr }
	t.Cleanup(func() { rolloutReaderOf = prev })
	mux := http.NewServeMux()
	s.registerRolloutRoutes(mux)
	return mux
}

func rv2Get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Email: "a@example.test", Role: auth.RoleAdmin}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func rv2Keys(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("gövde JSON değil: %v\n%s", err, body)
	}
	return m
}

const rv2Window = "from=1790000000000000000&to=1790003600000000000" // ns (parseFromTo)

func TestRolloutListV1Unchanged(t *testing.T) {
	fr := &fakeRolloutReader{v1Rows: []chstore.RolloutRow{rv2V1Row()}}
	mux := newRolloutReadEnv(t, rollout.SourceV1, fr)
	w := rv2Get(mux, "/api/rollouts?"+rv2Window+"&status=completed")
	if w.Code != http.StatusOK {
		t.Fatalf("v1 liste %d %s", w.Code, w.Body)
	}
	if fr.touched("v2.") {
		t.Fatalf("v1 kaynağında v2 okuması çağrıldı: %v", fr.calls)
	}
	// Bayt eşliği: bugünkü gövde (rollouts/from/to/limit, v2 anahtarı yok).
	want, _ := json.Marshal(map[string]any{"rollouts": fr.v1Rows, "from": int64(1790000000000), "to": int64(1790003600000), "limit": rolloutListLimitDefault})
	if !bytes.Equal(bytes.TrimSpace(w.Body.Bytes()), want) {
		t.Fatalf("v1 gövdesi değişmiş:\n got %s\nwant %s", w.Body.Bytes(), want)
	}
	if fr.filter.Status != "completed" {
		t.Fatalf("v1 süzgeci aynen geçmeli: %+v", fr.filter)
	}
}

func TestRolloutListV2ReadsRolloutEvents(t *testing.T) {
	fr := &fakeRolloutReader{v2Rows: []chstore.RolloutRow{rv2V2Row()}}
	mux := newRolloutReadEnv(t, rollout.SourceV2, fr)
	w := rv2Get(mux, "/api/rollouts?"+rv2Window+"&status=completed")
	if w.Code != http.StatusOK {
		t.Fatalf("v2 liste %d %s", w.Code, w.Body)
	}
	if fr.touched("v1.") || !fr.touched("v2.list") {
		t.Fatalf("v2 kaynağı yalnız rollout_events okumalı: %v", fr.calls)
	}
	// Süzgeç v1 sözlüğünde gelir; store v2'ye kendisi çevirir (rolloutV2Where).
	if fr.filter.Status != "completed" {
		t.Fatalf("süzgeç: %+v", fr.filter)
	}
	m := rv2Keys(t, w.Body.Bytes())
	if string(m["v2"]) != "true" {
		t.Fatalf("v2 işareti yok: %s", w.Body)
	}
	var body struct {
		Rollouts []map[string]any `json:"rollouts"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Rollouts) != 1 {
		t.Fatalf("satır: %s", w.Body)
	}
	r := body.Rollouts[0]
	if r["status"] != "completed" || r["v2Status"] != "succeeded" || r["detectedBy"] != "ksm" || r["generation"] != float64(7) ||
		r["imageTag"] != "2.0" || r["prevImageTag"] != "1.9" || r["changeType"] != "rollout" {
		t.Fatalf("v2 satırı eşlenmemiş: %v", r)
	}
}

func TestRolloutListEmptyNotePerSource(t *testing.T) {
	fr := &fakeRolloutReader{}
	w := rv2Get(newRolloutReadEnv(t, rollout.SourceV2, fr), "/api/rollouts?"+rv2Window)
	m := rv2Keys(t, w.Body.Bytes())
	if !strings.Contains(string(m["note"]), "rollout-detector") || fr.touched("v1.last") || !fr.touched("v2.last:rollout-detector") {
		t.Fatalf("v2 boş liste notu dedektörün koşusunu söylemeli: %s %v", m["note"], fr.calls)
	}
	fr = &fakeRolloutReader{lastW: &rollout.WorkerRun{Status: rollout.RunPartial}}
	w = rv2Get(newRolloutReadEnv(t, rollout.SourceV2, fr), "/api/rollouts?"+rv2Window)
	if !strings.Contains(w.Body.String(), "dedektör koşusu kısmi") {
		t.Fatalf("kısmi koşu notu: %s", w.Body)
	}
	fr = &fakeRolloutReader{}
	w = rv2Get(newRolloutReadEnv(t, rollout.SourceV1, fr), "/api/rollouts?"+rv2Window)
	if !strings.Contains(w.Body.String(), "reconciler henüz koşmadı") || fr.touched("v2.") {
		t.Fatalf("v1 notu değişmemeli: %s %v", w.Body, fr.calls)
	}
}

func TestRolloutOneKeyDispatch(t *testing.T) {
	six := "/api/rollout?cluster=c-1&namespace=pay&kind=Deployment&workload=api&incarnationAt=1790000000000&generation=7"
	five := "/api/rollout?cluster=c-1&namespace=pay&workload=api&revision=api-7f&startedAt=1790000000000"

	fr := &fakeRolloutReader{v2Rows: []chstore.RolloutRow{rv2V2Row()}, v1Rows: []chstore.RolloutRow{rv2V1Row()}}
	mux := newRolloutReadEnv(t, rollout.SourceV2, fr)
	if w := rv2Get(mux, six); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"generation":7`) {
		t.Fatalf("v2 + 6 parça: %d %s", w.Code, w.Body)
	}
	want := chstore.RolloutV2ID{ClusterID: "c-1", Namespace: "pay", Kind: "Deployment", Workload: "api", IncarnationAt: time.UnixMilli(1790000000000).UTC(), Generation: 7}
	if !reflect.DeepEqual(fr.v2ID, want) {
		t.Fatalf("6 parçalı anahtar: %+v", fr.v2ID)
	}
	// Eski 5 parçalı bağlantı v2'de de workload_rollouts'tan açılır (karar 14).
	if w := rv2Get(mux, five); w.Code != http.StatusOK || !fr.touched("v1.one") {
		t.Fatalf("v2 + 5 parça: %d %s %v", w.Code, w.Body, fr.calls)
	}
	if w := rv2Get(mux, strings.Replace(six, "generation=7", "generation=0", 1)); w.Code != http.StatusBadRequest {
		t.Fatalf("bozuk 6 parça 400 olmalı: %d", w.Code)
	}

	// v1: 6 parçalı istek bugünkü gibi 400 (revizyon yok), v2 okuması yok.
	fr = &fakeRolloutReader{v2Rows: []chstore.RolloutRow{rv2V2Row()}}
	w := rv2Get(newRolloutReadEnv(t, rollout.SourceV1, fr), six)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "cluster, workload, revision, startedAt (ms) zorunlu") || fr.touched("v2.") {
		t.Fatalf("v1 + 6 parça: %d %s %v", w.Code, w.Body, fr.calls)
	}
}

func TestRolloutDetailV2NoCluster(t *testing.T) {
	fr := &fakeRolloutReader{v2Rows: []chstore.RolloutRow{rv2V2Row()}}
	w := rv2Get(newRolloutReadEnv(t, rollout.SourceV2, fr),
		"/api/rollout/detail?cluster=c-1&namespace=pay&kind=Deployment&workload=api&incarnationAt=1790000000000&generation=7")
	if w.Code != http.StatusOK || !fr.touched("v2.one") || fr.touched("v1.") {
		t.Fatalf("v2 çekmece: %d %s %v", w.Code, w.Body, fr.calls)
	}
	var det struct {
		Rollout map[string]any `json:"rollout"`
		Note    string         `json:"note"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &det)
	if det.Rollout["generation"] != float64(7) || !strings.Contains(det.Note, "servisler çözülemez") {
		t.Fatalf("çekmece gövdesi: %s", w.Body)
	}
}

func TestRolloutStatsAndRunsPerSource(t *testing.T) {
	st := &chstore.RolloutStats{Total: 3, Completed: 2, TopRollback: []chstore.RolloutWorkloadN{}, TopDeploy: []chstore.RolloutWorkloadN{}, ByDay: []chstore.RolloutDayCount{}}
	fr := &fakeRolloutReader{v1Stats: st, v2Stats: st, v1Runs: []rollout.Run{{Status: "ok"}},
		wRuns: []rollout.WorkerRun{{Worker: rollout.WorkerRolloutDetector, StartedAt: rv2T0, FinishedAt: rv2T0.Add(time.Second), Host: "p1",
			Status: rollout.RunPartial, ScopesTotal: 2, ScopesOK: 1, RowsWritten: 4, DurationMs: 900}}}
	v1 := newRolloutReadEnv(t, rollout.SourceV1, fr)
	w := rv2Get(v1, "/api/rollouts/stats?"+rv2Window)
	want, _ := json.Marshal(st)
	if !bytes.Equal(bytes.TrimSpace(w.Body.Bytes()), want) || fr.touched("v2.") {
		t.Fatalf("v1 istatistik gövdesi değişmiş: %s %v", w.Body, fr.calls)
	}
	w = rv2Get(v1, "/api/rollouts/runs")
	if !strings.Contains(w.Body.String(), `"ksmMs"`) || strings.Contains(w.Body.String(), `"worker"`) || fr.touched("v2.") {
		t.Fatalf("v1 koşuları: %s %v", w.Body, fr.calls)
	}

	fr.calls = nil
	v2 := newRolloutReadEnv(t, rollout.SourceV2, fr)
	w = rv2Get(v2, "/api/rollouts/stats?"+rv2Window)
	m := rv2Keys(t, w.Body.Bytes())
	if string(m["v2"]) != "true" || string(m["completed"]) != "2" || fr.touched("v1.") {
		t.Fatalf("v2 istatistik: %s %v", w.Body, fr.calls)
	}
	w = rv2Get(v2, "/api/rollouts/runs")
	var runs struct {
		Runs []map[string]any `json:"runs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &runs)
	if len(runs.Runs) != 1 || runs.Runs[0]["worker"] != "rollout-detector" || runs.Runs[0]["status"] != "partial" ||
		runs.Runs[0]["clusters"] != float64(2) || runs.Runs[0]["durationMs"] != float64(900) || !fr.touched("v2.runs:rollout-detector") {
		t.Fatalf("v2 koşuları: %s %v", w.Body, fr.calls)
	}
}

func TestParseRolloutV2ID(t *testing.T) {
	q := func(s string) url.Values { v, _ := url.ParseQuery(s); return v }
	if _, present, _ := parseRolloutV2ID(q("cluster=c&workload=w&revision=r&startedAt=1")); present {
		t.Fatal("5 parçalı istek v2 sayılmamalı")
	}
	id, present, err := parseRolloutV2ID(q("cluster=c&namespace=n&kind=StatefulSet&workload=w&incarnationAt=1000&generation=3"))
	if !present || err != nil || id.Kind != "StatefulSet" || id.Generation != 3 || id.IncarnationAt.UnixMilli() != 1000 {
		t.Fatalf("geçerli 6 parça: %+v %v %v", id, present, err)
	}
	for _, bad := range []string{
		"cluster=c&namespace=n&kind=K&workload=w&incarnationAt=1000",              // generation yok
		"cluster=c&namespace=n&kind=K&workload=w&generation=3",                    // incarnation yok
		"cluster=c&namespace=n&workload=w&incarnationAt=1000&generation=3",        // kind yok
		"cluster=c&kind=K&workload=w&incarnationAt=1000&generation=3",             // namespace yok
		"cluster=c&namespace=n&kind=K&workload=w&incarnationAt=-5&generation=3",   // negatif
		"cluster=c&namespace=n&kind=K&workload=w&incarnationAt=1000&generation=x", // sayı değil
	} {
		if _, present, err := parseRolloutV2ID(q(bad)); !present || err == nil {
			t.Errorf("bozuk v2 anahtarı kabul edildi: %s", bad)
		}
	}
}

func TestServiceRolloutsFromV2(t *testing.T) {
	mk := func(wl, ct, status string, at time.Time, img, prev, vtag string) chstore.RolloutRow {
		return chstore.RolloutRowFromV2(rollout.V2Event{ClusterID: "c-a", Namespace: "pay", WorkloadKind: "Deployment", Workload: wl,
			IncarnationAt: rv2T0.Add(-time.Hour), Generation: 2, StartedAt: at, Status: status, ChangeType: ct,
			Images: []string{img}, PrevImages: []string{prev}, VersionTag: vtag, SpecReplicas: 3, UpdatedReplicas: 3, AvailableReplicas: 2,
			UpdatedAt: at, Version: 1})
	}
	// store DESC döner
	rows := []chstore.RolloutRow{
		mk("api", rollout.V2ChangeConfig, rollout.V2StatusProgressing, rv2T0.Add(2*time.Hour), "reg/api:2.0", "reg/api:2.0", ""),
		mk("api", rollout.V2ChangeRollout, rollout.V2StatusSucceeded, rv2T0, "reg/api:2.0", "reg/api:1.9", "v2.0-release"),
	}
	res := serviceRolloutsFromV2("pay-api", rows, map[string]string{"c-a": "prod-a"})
	if res.Source != "ksm" || !res.InstancesTracked || len(res.Rollouts) != 2 {
		t.Fatalf("sonuç: %+v", res)
	}
	first, second := res.Rollouts[0], res.Rollouts[1]
	if first.TimeUnixNs != rv2T0.UnixNano() || second.TimeUnixNs <= first.TimeUnixNs {
		t.Fatal("pod-churn sözleşmesi: zaman ARTAN (strip deploys[len-1])")
	}
	if first.Kind != "deploy" || first.VersionAfter != "v2.0-release" || first.VersionBefore != "1.9" || first.Status != "completed" {
		t.Fatalf("rollout satırı: %+v", first)
	}
	if second.Kind != "restart" || second.Status != "in_progress" {
		t.Fatalf("config satırı restart olmalı (sürüm çipi saymaz): %+v", second)
	}
	if first.Cluster != "prod-a" || first.Workload != "api" || first.WorkloadKind != "Deployment" || first.PodsAdded != 3 || first.PodsRemoved != 0 || first.ActivePods != 2 {
		t.Fatalf("iş yükü / replika alanları: %+v", first)
	}
	if res.VersionConstant {
		t.Fatal("1.9 → v2.0-release: sürüm sabit değil")
	}
	if one := serviceRolloutsFromV2("s", rows[:1], nil); !one.VersionConstant || one.Rollouts[0].Cluster != "c-a" {
		t.Fatalf("tek sürüm sabit; eşleme yoksa kimlik: %+v", one)
	}
	if empty := serviceRolloutsFromV2("s", nil, nil); empty.Rollouts == nil || len(empty.Rollouts) != 0 {
		t.Fatal("boş sonuç [] olmalı (FE .map)")
	}
	// v1 pod-churn JSON'u ek anahtar taşımaz (omitempty).
	b, _ := json.Marshal(chstore.RolloutsResult{Service: "s", Rollouts: []chstore.Rollout{{TimeUnixNs: 1, Kind: "deploy"}}})
	for _, k := range []string{"source", "workload", "status", "specReplicas", "note"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Fatalf("pod-churn gövdesine %q sızmış: %s", k, b)
		}
	}
}

func TestSpanClusterOf(t *testing.T) {
	refs := []chstore.WorkloadRevisionRef{{Cluster: "prod-b"}, {Cluster: "prod-a2"}, {Cluster: "prod-a"}, {Cluster: "ghost"}}
	got := spanClusterOf(refs, map[string]string{"prod-a": "c-a", "prod-a2": "c-a", "prod-b": "c-b"})
	if !reflect.DeepEqual(got, map[string]string{"c-a": "prod-a", "c-b": "prod-b"}) {
		t.Fatalf("temsilci span değeri: %v", got)
	}
	if got := spanClusterOf(refs[:1], nil); got["prod-b"] != "prod-b" {
		t.Fatalf("tek küme: %v", got)
	}
}

// TestRolloutTailPagesCursorOnlyOnSuccess — İnceleme (v0.10.984): akış
// ortasında hata (rows.Err()) ilerlemiş kursörle birlikte döner; kursör
// saklanırsa o satırların SSE olayı kalıcı kaybolur. Kursör yalnız başarıda
// ilerler — her iki kaynakta.
func TestRolloutTailPagesCursorOnlyOnSuccess(t *testing.T) {
	ctx := context.Background()
	c0 := chstore.RolloutCursor{UpdatedAt: rv2T0}
	c20 := chstore.RolloutV2Cursor{UpdatedAt: rv2T0}
	adv := chstore.RolloutCursor{UpdatedAt: rv2T0.Add(time.Minute), Workload: "api"}
	adv2 := chstore.RolloutV2Cursor{UpdatedAt: rv2T0.Add(time.Minute), ID: chstore.RolloutV2ID{Workload: "api"}}
	rows := []chstore.RolloutRow{rv2V1Row(), rv2V1Row()}

	for _, src := range []string{rollout.SourceV1, rollout.SourceV2} {
		f := &fakeRolloutReader{tailRows: rows, tailNext: &adv, tailNext2: &adv2, tailErr: errors.New("stream reset")}
		c, c2 := c0, c20
		if n := rolloutTailPages(ctx, f, src, &c, &c2); n != 0 || !c.UpdatedAt.Equal(c0.UpdatedAt) || c.Workload != "" || !c2.UpdatedAt.Equal(c20.UpdatedAt) || c2.ID != c20.ID {
			t.Fatalf("%s hata: n=%d kursör ilerledi %+v %+v", src, n, c, c2)
		}
		f.tailErr = nil
		n := rolloutTailPages(ctx, f, src, &c, &c2)
		if n != 2 {
			t.Fatalf("%s başarı: n=%d", src, n)
		}
		if src == rollout.SourceV1 && (!c.UpdatedAt.Equal(adv.UpdatedAt) || c.Workload != "api" || c2.ID != c20.ID) {
			t.Fatalf("v1 başarı: yalnız v1 kursörü ilerler %+v %+v", c, c2)
		}
		if src == rollout.SourceV2 && (c2.ID != adv2.ID || !c.UpdatedAt.Equal(c0.UpdatedAt) || c.Workload != "") {
			t.Fatalf("v2 başarı: yalnız v2 kursörü ilerler %+v %+v", c, c2)
		}
	}
}

// Servis kapsamlı okumaların (service-rollouts, annotations) anahtar parçası:
// v2'de Remote Cluster eşlemesinin özetini taşır (span değeri eklenince
// anahtar değişir), v1'de eşlemeden bağımsız "churn" kalır.
func TestServiceRolloutsSrcCarriesClusterMap(t *testing.T) {
	th := thanos.New()
	conf := func(span string) {
		th.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
			{ID: "c-a", Name: "cluster-a", URL: "http://t.invalid", SpanClusterValue: span, Enabled: true},
		}})
	}
	conf("span-a")
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(), thanos: th}
	cfg := rollout.NewSettingsService()
	st := rollout.DefaultSettings()
	st.Enabled, st.Source = true, rollout.SourceV2
	cfg.Configure(st)
	s.SetRollout(cfg)
	k1 := s.serviceRolloutsSrc()
	if !strings.HasPrefix(k1, "ksm:cl=") || s.serviceRolloutsSrc() != k1 {
		t.Fatalf("v2 anahtar parçası kararlı ve özetli olmalı: %q", k1)
	}
	conf("span-b")
	if k2 := s.serviceRolloutsSrc(); k2 == k1 {
		t.Fatalf("Remote Cluster eşlemesi değişince v2 anahtarı değişmeli: %q", k2)
	}
	st.Source = rollout.SourceV1
	cfg.Configure(st)
	v1 := s.serviceRolloutsSrc()
	conf("span-a")
	if v1 != "churn" || s.serviceRolloutsSrc() != "churn" {
		t.Fatalf("v1 anahtarı eşlemeden bağımsız: %q", v1)
	}
}
