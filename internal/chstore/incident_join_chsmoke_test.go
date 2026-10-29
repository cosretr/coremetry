//go:build chsmoke

// v0.10.1588 — Davis paritesi FAZ 0.5 / D: JoinOpenIncident yalnız AÇIK
// incident'a katılır, ASLA yeni incident açmaz (operatör kararı 7: tekil
// exception grubu kendi incident'ını açmaz). Aynı servis, birden çok
// servisten biri ya da 1-hop komşu eşler; çözülmüş / 30 dk sessiz incident
// eşlemez; mevcut bağ (çözülmemiş incident) yeniden kullanılır.
//
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/chstore/ -run TestJoinOpenIncidentNeverCreates -v
package chstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestJoinOpenIncidentNeverCreates(t *testing.T) {
	st := smokeStore(t)
	ctx := context.Background()
	run := time.Now().UnixNano()
	seed := func(t *testing.T, id, svc, status string, activeAgo time.Duration) {
		t.Helper()
		now := time.Now()
		if err := st.conn.Exec(ctx, `INSERT INTO incidents
			(id, title, severity, status, service, summary, assignee, postmortem,
			 started_at, ack_at, resolved_at, updated_at, version)
			VALUES (?, 'mevcut', 'warning', ?, ?, '', '', '', ?, NULL, NULL, ?, ?)`,
			id, status, svc, now.Add(-activeAgo).UTC(), now.Add(-activeAgo).UTC(),
			uint64(now.Add(-activeAgo).UnixNano())); err != nil {
			t.Fatalf("incident tohumu: %v", err)
		}
	}
	count := func(t *testing.T, svcs ...string) uint64 {
		t.Helper()
		var n uint64
		if err := st.conn.QueryRow(ctx, `SELECT count() FROM incidents FINAL WHERE service IN ?`, svcs).Scan(&n); err != nil {
			t.Fatalf("say: %v", err)
		}
		return n
	}
	cases := []struct {
		name       string
		incSvc     string // "" = tohum yok
		status     string
		activeAgo  time.Duration
		services   []string
		neighbours map[string][]string
		wantJoin   bool
	}{
		{name: "incident yok → katılmaz, YENİ AÇMAZ", services: []string{"svc-a"}},
		{name: "aynı serviste açık incident → katılır", incSvc: "svc-a", status: "open", activeAgo: 5 * time.Minute, services: []string{"svc-a"}, wantJoin: true},
		{name: "servislerden birinde açık incident → katılır", incSvc: "svc-b", status: "open", activeAgo: 5 * time.Minute, services: []string{"svc-a", "svc-b"}, wantJoin: true},
		{name: "komşuda açık incident → katılır", incSvc: "svc-n", status: "open", activeAgo: 5 * time.Minute, services: []string{"svc-a"}, neighbours: map[string][]string{"svc-a": {"svc-n"}}, wantJoin: true},
		{name: "çözülmüş incident → katılmaz", incSvc: "svc-a", status: "resolved", activeAgo: 5 * time.Minute, services: []string{"svc-a"}},
		{name: "90 dk sessiz incident → katılmaz", incSvc: "svc-a", status: "open", activeAgo: 90 * time.Minute, services: []string{"svc-a"}},
		{name: "servis listesi boş → hiçbir şey", services: nil},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pre := fmt.Sprintf("join-%d-%d-", run, i)
			var svcs []string
			for _, s := range tc.services {
				svcs = append(svcs, pre+s)
			}
			incID := pre + "inc"
			if tc.incSvc != "" {
				seed(t, incID, pre+tc.incSvc, tc.status, tc.activeAgo)
			}
			var np NeighborProvider
			if tc.neighbours != nil {
				m := staticNeighbours{}
				for k, v := range tc.neighbours {
					for _, x := range v {
						m[pre+k] = append(m[pre+k], pre+x)
					}
				}
				np = m
			}
			all := append(append([]string{}, svcs...), pre+"svc-n", pre+"svc-a", pre+"svc-b", "")
			before := count(t, all...)
			p := Problem{ID: pre + "p", RuleID: "exception:fatal-infrastructure", RuleName: "Bağımlılık adı çözülemiyor",
				Severity: "critical", Service: "", Status: "open", StartedAt: time.Now().UnixNano()}
			if len(svcs) > 0 {
				p.Service = svcs[0]
			}
			inc, joined, err := st.JoinOpenIncidentWith(ctx, p, svcs, np)
			if err != nil {
				t.Fatalf("join: %v", err)
			}
			if joined != tc.wantJoin || (joined && (inc == nil || inc.ID != incID)) {
				t.Fatalf("joined=%v inc=%v, want join=%v (%s)", joined, inc, tc.wantJoin, incID)
			}
			if after := count(t, all...); after != before {
				t.Fatalf("incident sayısı değişti %d → %d — JoinOpenIncident ASLA incident açmaz", before, after)
			}
			if !joined {
				if id, _, _ := st.GetProblemIncident(ctx, p.ID); id != "" {
					t.Fatalf("katılmayan problem bağlandı: %s", id)
				}
				return
			}
			if id, _, _ := st.GetProblemIncident(ctx, p.ID); id != incID {
				t.Fatalf("bağ yazılmadı: %q", id)
			}
			// Mevcut bağ yeniden kullanılır (kural 1).
			again, ok, err := st.JoinOpenIncidentWith(ctx, p, svcs, np)
			if err != nil || !ok || again.ID != incID {
				t.Fatalf("ikinci çağrı mevcut bağı döndürmeli: %v %v %v", again, ok, err)
			}
		})
	}
}
