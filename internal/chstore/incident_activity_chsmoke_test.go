//go:build chsmoke

// v0.10.1581 — uzun süren incident ilgili problemleri emmeyi bırakıyordu.
//
// Kod denetimi bulgusu: kural 2 (aynı servis) ve kural 3 (1-hop komşu)
// yalnız started_at >= now−30m incident'larını eşliyordu; 30 dk'dan uzun
// açık kalan incident yeni aynı-servis / komşu problemleri alamıyor,
// her biri için yeni incident açılıyordu. Beklenen: başlangıca değil
// ETKİNLİĞE bakılır — çözülmemiş VE son 30 dk'da etkinliği olan incident
// emer; 30 dk'dır sessiz olan sonsuz kova olmaz.
//
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/chstore/ -run TestAttachMatchesActiveLongRunningIncident
package chstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type staticNeighbors map[string][]string

func (n staticNeighbors) Neighbors(s string) []string { return n[s] }

func TestAttachMatchesActiveLongRunningIncident(t *testing.T) {
	st := smokeStore(t)
	ctx := context.Background()
	run := time.Now().UnixNano()

	cases := []struct {
		name string
		// mevcut incident'ın başlangıcı ve son etkinliği (updated_at) şimdiden önce
		startedAgo, activeAgo time.Duration
		neighbour             bool // yeni problem komşu serviste mi (kural 3)
		wantAbsorb            bool
	}{
		{name: "taze incident aynı servis", startedAgo: 5 * time.Minute, activeAgo: 5 * time.Minute, wantAbsorb: true},
		{name: "2 sa önce başlamış, 5 dk önce etkin — aynı servis", startedAgo: 2 * time.Hour, activeAgo: 5 * time.Minute, wantAbsorb: true},
		{name: "2 sa önce başlamış, 5 dk önce etkin — komşu", startedAgo: 2 * time.Hour, activeAgo: 5 * time.Minute, neighbour: true, wantAbsorb: true},
		{name: "2 sa önce başlamış, 90 dk sessiz — aynı servis", startedAgo: 2 * time.Hour, activeAgo: 90 * time.Minute, wantAbsorb: false},
		{name: "2 sa önce başlamış, 90 dk sessiz — komşu", startedAgo: 2 * time.Hour, activeAgo: 90 * time.Minute, neighbour: true, wantAbsorb: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := fmt.Sprintf("act-svc-%d-%d", run, i)
			nb := svc + "-nb"
			now := time.Now()
			id := fmt.Sprintf("act-inc-%d-%d", run, i)
			// Geçmiş updated_at'li satır doğrudan yazılır (UpsertIncident
			// updated_at'i şimdiye basar).
			if err := st.conn.Exec(ctx, `INSERT INTO incidents
				(id, title, severity, status, service, summary, assignee, postmortem,
				 started_at, ack_at, resolved_at, updated_at, version)
				VALUES (?, 'mevcut', 'critical', 'open', ?, '', '', '', ?, NULL, NULL, ?, ?)`,
				id, svc, now.Add(-tc.startedAgo).UTC(), now.Add(-tc.activeAgo).UTC(),
				uint64(now.Add(-tc.activeAgo).UnixNano())); err != nil {
				t.Fatalf("incident tohumu: %v", err)
			}
			pSvc := svc
			var np NeighborProvider
			if tc.neighbour {
				pSvc = nb
				np = staticNeighbors{nb: {svc}}
			}
			p := Problem{
				ID: fmt.Sprintf("act-p-%d-%d", run, i), RuleID: "rule-act", RuleName: "latency",
				Severity: "warning", Service: pSvc, Status: "open", StartedAt: now.UnixNano(),
			}
			got, err := st.AttachProblemToIncidentWith(ctx, p, np)
			if err != nil || got == nil {
				t.Fatalf("bağlama: %v", err)
			}
			if absorbed := got.ID == id; absorbed != tc.wantAbsorb {
				t.Fatalf("emildi=%v, istenen %v (incident %s, dönen %s)", absorbed, tc.wantAbsorb, id, got.ID)
			}
			if !tc.wantAbsorb {
				return
			}
			// Etkinlik sinyali bağlanmayla tazelenir: aynı incident 30 dk
			// sonra hâlâ "etkin" sayılabilsin.
			after, err := st.GetIncident(ctx, id)
			if err != nil || after == nil {
				t.Fatalf("okuma: %v", err)
			}
			if time.Duration(time.Now().UnixNano()-after.UpdatedAt) > time.Minute {
				t.Fatalf("bağlanma etkinliği tazelemedi: updated_at %s önce",
					time.Duration(time.Now().UnixNano()-after.UpdatedAt))
			}
			if after.StartedAt/int64(time.Second) != now.Add(-tc.startedAgo).UnixNano()/int64(time.Second) {
				t.Fatalf("started_at değişti")
			}
		})
	}
}
