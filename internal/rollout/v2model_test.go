package rollout

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v2model_test.go — v0.10.963 — Rollouts v2 P2.1 veri modeli sözleşmesi
// (docs/rollouts/v2-audit.md §10.3.1–10.3.2, §10.2 yazıcı sözleşmesi v0.10.959):
//
//   - V2Event / V2WorkloadState, migrations/0015'teki kolon listesini SIRASIYLA,
//     adıyla (`ch` etiketi) ve tipiyle birebir aynalar. 0015 ile boot DDL'i
//     bayt-eşliği ayrıca pinli (TestRolloutV2MigrationByteIdenticalToBootDDL),
//     bu yüzden 0015'i okumak Go DDL'ini okumakla aynıdır (chstore → rollout
//     içe aktarımı döngü kurardı).
//   - Saf doğrulayıcı: TTL çapası (rollout_events.started_at,
//     rollout_workload_state.last_seen_at) sıfır, epoch ya da epoch-öncesiyse
//     satır REDDEDİLİR; anahtar zamanları, sözlük değerleri ve version da.
//   - V2CHTime: DEFAULT 0 kolonuna Go sıfırı DEĞİL epoch bağlanır.

// detDDLColumns — 0015'ten bir tablonun (ad, tip) listesi, DDL sırasıyla.
func detDDLColumns(t *testing.T, table string) [][2]string {
	t.Helper()
	b, err := os.ReadFile("../../migrations/0015_rollouts_v2.sql")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	head := "CREATE TABLE IF NOT EXISTS " + table + " "
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("0015'te %s yok", table)
	}
	body := src[i:]
	body = body[strings.Index(body, "(\n")+2:]
	body = body[:strings.Index(body, ") ENGINE")]
	var cols [][2]string
	for _, ln := range strings.Split(body, "\n") {
		if c := strings.Index(ln, "--"); c >= 0 {
			ln = ln[:c]
		}
		ln = strings.TrimSuffix(strings.TrimSpace(ln), ",")
		if ln == "" {
			continue
		}
		f := strings.Fields(ln)
		typ := f[1]
		cols = append(cols, [2]string{f[0], typ})
	}
	return cols
}

func detGoColumns(v any) [][2]string {
	rt := reflect.TypeOf(v)
	var cols [][2]string
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		var typ string
		switch f.Type {
		case reflect.TypeOf(""):
			typ = "string"
		case reflect.TypeOf(uint64(0)):
			typ = "UInt64"
		case reflect.TypeOf(uint32(0)):
			typ = "UInt32"
		case reflect.TypeOf(time.Time{}):
			typ = "DateTime64(3)"
		case reflect.TypeOf([]string(nil)):
			typ = "Array(String)"
		default:
			typ = f.Type.String()
		}
		cols = append(cols, [2]string{f.Tag.Get("ch"), typ})
	}
	return cols
}

func TestV2RowsMirrorDDL(t *testing.T) {
	for _, tc := range []struct {
		table string
		row   any
	}{
		{"rollout_events", V2Event{}},
		{"rollout_workload_state", V2WorkloadState{}},
	} {
		ddl := detDDLColumns(t, tc.table)
		got := detGoColumns(tc.row)
		if len(ddl) != len(got) {
			t.Fatalf("%s: DDL %d kolon, Go %d alan\nDDL %v\nGo  %v", tc.table, len(ddl), len(got), ddl, got)
		}
		for i := range ddl {
			want := ddl[i][1]
			if want == "String" || want == "LowCardinality(String)" {
				want = "string"
			}
			if ddl[i][0] != got[i][0] || want != got[i][1] {
				t.Fatalf("%s kolon %d: DDL %v, Go %v", tc.table, i, ddl[i], got[i])
			}
		}
	}
}

func detValidEvent() V2Event {
	at := time.Date(2026, 9, 27, 10, 0, 30, 0, time.UTC)
	return V2Event{ClusterID: "cluster-a", Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "checkout-api",
		IncarnationAt: at.Truncate(time.Minute), Generation: 2, StartedAt: at, Status: V2StatusProgressing,
		ChangeType: V2ChangeRollout, UpdatedAt: at, Version: uint64(at.UnixNano())}
}

func detValidState() V2WorkloadState {
	at := time.Date(2026, 9, 27, 10, 0, 30, 0, time.UTC)
	return V2WorkloadState{ClusterID: "cluster-a", Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "checkout-api",
		IncarnationAt: at.Truncate(time.Minute), Generation: 2, FirstSeenAt: at, LastSeenAt: at, Version: uint64(at.UnixNano())}
}

func TestValidateV2Event(t *testing.T) {
	pre := time.Date(1969, 12, 31, 23, 59, 0, 0, time.UTC)
	epoch := time.Unix(0, 0).UTC()
	cases := []struct {
		name string
		mut  func(*V2Event)
		bad  string // hata metninde geçmesi gereken kolon; "" = geçerli
	}{
		{"geçerli", func(*V2Event) {}, ""},
		{"started_at sıfır (TTL çapası)", func(e *V2Event) { e.StartedAt = time.Time{} }, "started_at"},
		{"started_at epoch", func(e *V2Event) { e.StartedAt = epoch }, "started_at"},
		{"started_at epoch öncesi", func(e *V2Event) { e.StartedAt = pre }, "started_at"},
		{"incarnation_at sıfır (anahtar)", func(e *V2Event) { e.IncarnationAt = time.Time{} }, "incarnation_at"},
		{"updated_at sıfır (SSE kursörü)", func(e *V2Event) { e.UpdatedAt = time.Time{} }, "updated_at"},
		{"succeeded_at epoch öncesi (sıfır olmayan)", func(e *V2Event) { e.SucceededAt = pre }, "succeeded_at"},
		{"finished_at epoch öncesi", func(e *V2Event) { e.FinishedAt = pre }, "finished_at"},
		{"stuck_at epoch öncesi", func(e *V2Event) { e.StuckAt = pre }, "stuck_at"},
		{"cluster_id boş", func(e *V2Event) { e.ClusterID = "" }, "cluster_id"},
		{"namespace boş", func(e *V2Event) { e.Namespace = "" }, "namespace"},
		{"workload boş", func(e *V2Event) { e.Workload = "" }, "workload"},
		{"tür sözlük dışı", func(e *V2Event) { e.WorkloadKind = "CronJob" }, "workload_kind"},
		{"status sözlük dışı", func(e *V2Event) { e.Status = "in_progress" }, "status"},
		{"change_type scale YAZILMAZ", func(e *V2Event) { e.ChangeType = "scale" }, "change_type"},
		{"stuck_reason sözlük dışı", func(e *V2Event) { e.StuckReason = "slow" }, "stuck_reason"},
		{"generation 0", func(e *V2Event) { e.Generation = 0 }, "generation"},
		{"version 0 (açık istemci version'ı şart)", func(e *V2Event) { e.Version = 0 }, "version"},
		{"isteğe bağlı zamanlar sıfır olabilir", func(e *V2Event) { e.SucceededAt, e.StuckAt, e.FinishedAt = time.Time{}, time.Time{}, time.Time{} }, ""},
		// v0.10.963 — P2.1 (P21-1): V2CHTime'ın kendi sentinel'i (dilimden bağımsız) geçerli.
		{"isteğe bağlı zamanlar epoch sentinel'i olabilir (V2CHTime sonrası)", func(e *V2Event) {
			e.SucceededAt, e.StuckAt, e.FinishedAt = V2CHTime(time.Time{}), epoch.In(time.FixedZone("x", 3*3600)), V2CHTime(time.Time{})
		}, ""},
		{"started_at epoch sentinel'i yine reddedilir (TTL çapası)", func(e *V2Event) { e.StartedAt = V2CHTime(time.Time{}) }, "started_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := detValidEvent()
			tc.mut(&e)
			err := ValidateV2Event(e)
			if tc.bad == "" {
				if err != nil {
					t.Fatalf("geçerli satır reddedildi: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.bad) {
				t.Fatalf("%s için hata bekleniyordu, alınan: %v", tc.bad, err)
			}
		})
	}
}

func TestValidateV2State(t *testing.T) {
	pre := time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		mut  func(*V2WorkloadState)
		bad  string
	}{
		{"geçerli", func(*V2WorkloadState) {}, ""},
		{"last_seen_at sıfır (TTL çapası)", func(s *V2WorkloadState) { s.LastSeenAt = time.Time{} }, "last_seen_at"},
		{"last_seen_at epoch", func(s *V2WorkloadState) { s.LastSeenAt = time.Unix(0, 0) }, "last_seen_at"},
		{"last_seen_at epoch öncesi", func(s *V2WorkloadState) { s.LastSeenAt = pre }, "last_seen_at"},
		{"first_seen_at sıfır", func(s *V2WorkloadState) { s.FirstSeenAt = time.Time{} }, "first_seen_at"},
		{"incarnation_at sıfır", func(s *V2WorkloadState) { s.IncarnationAt = time.Time{} }, "incarnation_at"},
		{"pending_started_at epoch öncesi", func(s *V2WorkloadState) { s.PendingGeneration, s.PendingStartedAt = 3, pre }, "pending_started_at"},
		{"bekleyen nesil zamansız (failover'da started_at kaybolur)", func(s *V2WorkloadState) { s.PendingGeneration = 3 }, "pending_started_at"},
		// v0.10.963 — P2.1 (P21-1): epoch sentinel'i de "zamansız"dır.
		{"bekleyen nesil epoch sentinel'iyle", func(s *V2WorkloadState) {
			s.PendingGeneration, s.PendingStartedAt = 3, time.Unix(0, 0).In(time.FixedZone("x", 3*3600))
		}, "pending_started_at"},
		{"bekleyen nesil yokken pending_started_at epoch sentinel'i geçerli", func(s *V2WorkloadState) { s.PendingStartedAt = V2CHTime(time.Time{}) }, ""},
		{"tür sözlük dışı", func(s *V2WorkloadState) { s.WorkloadKind = "Rollout" }, "workload_kind"},
		{"workload boş", func(s *V2WorkloadState) { s.Workload = "" }, "workload"},
		{"generation 0", func(s *V2WorkloadState) { s.Generation = 0 }, "generation"},
		{"version 0", func(s *V2WorkloadState) { s.Version = 0 }, "version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := detValidState()
			tc.mut(&s)
			err := ValidateV2State(s)
			if tc.bad == "" {
				if err != nil {
					t.Fatalf("geçerli satır reddedildi: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.bad) {
				t.Fatalf("%s için hata bekleniyordu, alınan: %v", tc.bad, err)
			}
		})
	}
}

func TestV2CHTime(t *testing.T) {
	if got := V2CHTime(time.Time{}); !got.Equal(time.Unix(0, 0)) {
		t.Fatalf("sıfır → epoch beklenir, alınan %v", got)
	}
	at := time.Date(2026, 9, 27, 10, 0, 30, 123456789, time.UTC)
	if got := V2CHTime(at); !got.Equal(at) {
		t.Fatalf("sıfır olmayan değer değişmemeli: %v", got)
	}
}

// v0.10.963 — P2.1 (R2 / P21-1): V2FromCHTime, V2CHTime'ın tersi; FINAL
// okumadaki 1970 (herhangi bir dilimde) ve 1900'e kırpılmış değer → Go sıfırı.
func TestV2FromCHTime(t *testing.T) {
	at := time.Date(2026, 9, 27, 10, 0, 30, 0, time.UTC)
	for _, tc := range []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"Go sıfırı", time.Time{}, time.Time{}},
		{"epoch UTC", time.Unix(0, 0).UTC(), time.Time{}},
		{"epoch UTC dışı dilim", time.Unix(0, 0).In(time.FixedZone("x", 3*3600)), time.Time{}},
		{"1900 (kırpılmış Go sıfırı)", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}},
		{"gerçek zaman aynen", at, at},
		{"gidiş-dönüş", V2CHTime(time.Time{}), time.Time{}},
	} {
		if got := V2FromCHTime(tc.in); !got.Equal(tc.want) || got.IsZero() != tc.want.IsZero() {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
