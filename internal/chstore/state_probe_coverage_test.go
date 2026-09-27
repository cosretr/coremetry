// v0.10.971 — boot probe'unun KAPSAM denetimi (kural 3 kaldırıldıktan sonraki
// inceleme, bulgu F1 bölüm 1-2).
//
// KÖR NOKTA. Kural 4 "tablo hiçbir node'da yok → birleşik yol" der. Probe'un
// küme geneli okuması `skip_unavailable_shards=1` ile koşar; clusterAllReplicas
// her replikayı ayrı shard sayar, yani bağlantıyı reddeden bir replika HATASIZ
// düşer ve dönen (tablo, yol) satırları kısmi cevabı tam cevaptan ayırt
// ettirmez. Eskiden yalnız okuma HATASI loglanıyordu; atlanan replika sessizdi.
//
// Bu dosya şunu çiviler: cevap veren replika sayısı roster'la (system.clusters)
// karşılaştırılır, eksikse ⚠ loglanır ve obs.complete=false olur — DAVRANIŞ
// DEĞİŞMEZ (Keeper'dan yokluk kanıtı ayrı operatör kararı). Canlı ClickHouse
// yok: sahte driver.Conn (sprRows/sprRow, state_path_rebuild_test.go). Host
// adları sentetik (host-1..4, uptrace_all).
package chstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/cilcenk/coremetry/internal/config"
)

// probeConn — boot probe'unun dört okumasını taklit eder: yerel
// system.replicas, küme geneli system.replicas, roster ve cevap sayımı.
type probeConn struct {
	driver.Conn
	local       [][]any // yerel system.replicas: (table, zookeeper_path)
	cluster     [][]any // clusterAllReplicas(…, system.replicas) — yalnız cevap verenlerin satırları
	clusterErr  error
	roster      uint64 // system.clusters satır sayısı
	answered    uint64 // clusterAllReplicas(…, system.one) satır sayısı
	rosterErr   error
	answeredErr error
	queries     []string
}

func (c *probeConn) Query(_ context.Context, q string, _ ...any) (driver.Rows, error) {
	c.queries = append(c.queries, q)
	switch {
	case strings.Contains(q, "clusterAllReplicas('uptrace_all', system.replicas)"):
		if c.clusterErr != nil {
			return nil, c.clusterErr
		}
		return &sprRows{vals: c.cluster}, nil
	case strings.Contains(q, "FROM system.replicas WHERE"):
		return &sprRows{vals: c.local}, nil
	}
	return nil, errors.New("beklenmeyen Query: " + q)
}

func (c *probeConn) QueryRow(_ context.Context, q string, args ...any) driver.Row {
	c.queries = append(c.queries, q)
	switch {
	case strings.Contains(q, "FROM system.clusters WHERE cluster = ?"):
		if len(args) != 1 || args[0] != "uptrace_all" {
			return sprRow{err: errors.New("roster okuması küme adını bağlamadı")}
		}
		return sprRow{vals: []any{c.roster}, err: c.rosterErr}
	case strings.Contains(q, "clusterAllReplicas('uptrace_all', system.one)"):
		return sprRow{vals: []any{c.answered}, err: c.answeredErr}
	}
	return sprRow{err: errors.New("beklenmeyen QueryRow: " + q)}
}

// host1Rows — host-1'in (shard 01) yerel görüşü: ai_eval_runs YOK, problems
// eski yolda, users birleşik yolda.
func host1Rows() [][]any {
	return [][]any{
		{"problems", "/clickhouse/tables/01/problems"},
		{"users", "/clickhouse/tables/state/users"},
	}
}

// TestResolveStateReplicaPathsCoverage — F1 senaryosu (sentetik 2×2): pod
// host-1'e bağlı, host-2..4 kapalı. Küme okuması HATASIZ döner ama yalnız
// host-1'in satırlarını taşır.
func TestResolveStateReplicaPathsCoverage(t *testing.T) {
	cases := []struct {
		name         string
		conn         *probeConn
		wantComplete bool
		wantWarn     string // "" = ⚠ satırı YOK
		wantCounted  bool   // cevap sayımı (system.one) koştu mu
	}{
		{
			name:         "dört replika da cevap verdi → tam, uyarı yok",
			conn:         &probeConn{local: host1Rows(), cluster: host1Rows(), roster: 4, answered: 4},
			wantComplete: true, wantCounted: true,
		},
		{
			name:     "ÜÇ replika sessizce atlandı → eksik, gürültülü",
			conn:     &probeConn{local: host1Rows(), cluster: host1Rows(), roster: 4, answered: 1},
			wantWarn: "yalnız 1/4 replika cevap verdi", wantCounted: true,
		},
		{
			// Küme okuması düştü → kapsam okuması hiç koşmaz (gözlem zaten yerel).
			name:     "küme okuması hata → yalnız yerel, gürültülü (eski dal)",
			conn:     &probeConn{local: host1Rows(), clusterErr: errors.New("code: 279, message: All connection tries failed"), roster: 4, answered: 1},
			wantWarn: "küme geneli state yolu okunamadı",
		},
		{
			// Roster okunamadı → sayım boşuna koşmaz.
			name:     "roster okunamadı → kapsam doğrulanamadı, gürültülü",
			conn:     &probeConn{local: host1Rows(), cluster: host1Rows(), rosterErr: errors.New("code: 159, Timeout exceeded"), answered: 4},
			wantWarn: "kapsamı doğrulanamadı",
		},
		{
			name:     "cevap sayımı okunamadı → kapsam doğrulanamadı, gürültülü",
			conn:     &probeConn{local: host1Rows(), cluster: host1Rows(), roster: 4, answeredErr: errors.New("code: 159, Timeout exceeded")},
			wantWarn: "kapsamı doğrulanamadı", wantCounted: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := captureLog(t)
			s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: c.conn}
			s.resolveStateReplicaPaths(context.Background())
			logged := buf.String()

			if !s.stateObs.ok {
				t.Fatalf("yerel okuma başarılı, probe koşmuş sayılmalı; log:\n%s", logged)
			}
			if s.stateObs.complete != c.wantComplete {
				t.Errorf("complete = %v, beklenen %v; log:\n%s", s.stateObs.complete, c.wantComplete, logged)
			}
			if c.wantWarn == "" {
				if strings.Contains(logged, "⚠") {
					t.Errorf("tam gözlemde ⚠ satırı olmamalı:\n%s", logged)
				}
			} else {
				if !strings.Contains(logged, "⚠") || !strings.Contains(logged, c.wantWarn) {
					t.Errorf("⚠ %q loglanmalıydı:\n%s", c.wantWarn, logged)
				}
				if !strings.Contains(logged, stateProbeBlindTail) {
					t.Errorf("⚠ satırı kör noktanın ortak kuyruğunu taşımalı:\n%s", logged)
				}
			}
			counted := false
			for _, q := range c.conn.queries {
				if strings.Contains(q, "system.one)") {
					counted = true
				}
			}
			if counted != c.wantCounted {
				t.Errorf("cevap sayımı koştu mu = %v, beklenen %v (sorgular: %q)", counted, c.wantCounted, c.conn.queries)
			}

			// DAVRANIŞ DEĞİŞMEZ: kapsam yalnız bilgi. Gözlenmeyen tablo kural 4
			// ile birleşik yola, gözlenen eski tablo kural 2 ile kendi yoluna.
			pfx := s.zkPrefix()
			if got, reason := useUnifiedStatePath(s.stateObs, pfx, "ai_eval_runs"); !got || reason != statePathFreshReason {
				t.Errorf("gözlenmeyen ai_eval_runs = (%v, %q), kural 4 bekleniyordu", got, reason)
			}
			if got, _ := useUnifiedStatePath(s.stateObs, pfx, "problems"); got {
				t.Error("gözlenen eski yollu problems komşularına (kural 2) katılmalıydı")
			}
			if got, _ := useUnifiedStatePath(s.stateObs, pfx, "users"); !got {
				t.Error("gözlenen birleşik users birleşik kalmalıydı")
			}
		})
	}
}

// TestStateProbeCoverage — SAF hüküm; complete=false iken warn asla boş değil.
func TestStateProbeCoverage(t *testing.T) {
	cases := []struct {
		name             string
		answered, roster int
		err              error
		wantComplete     bool
		wantWarn         string
	}{
		{name: "hepsi cevap verdi", answered: 4, roster: 4, wantComplete: true},
		{name: "roster'dan fazla cevap (yarış) → tam", answered: 5, roster: 4, wantComplete: true},
		{name: "tek düğüm küme", answered: 1, roster: 1, wantComplete: true},
		{name: "üç replika atlandı", answered: 1, roster: 4, wantWarn: "yalnız 1/4 replika cevap verdi"},
		{name: "hiçbiri cevap vermedi", answered: 0, roster: 4, wantWarn: "yalnız 0/4 replika"},
		{name: "roster boş", answered: 1, roster: 0, wantWarn: "system.clusters'ta bu küme için replika yok"},
		{name: "okuma hatası", answered: 4, roster: 4, err: errors.New("code: 159, Timeout exceeded"), wantWarn: "Timeout exceeded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			complete, warn := stateProbeCoverage(c.answered, c.roster, c.err)
			if complete != c.wantComplete {
				t.Errorf("complete = %v, beklenen %v", complete, c.wantComplete)
			}
			if c.wantComplete {
				if warn != "" {
					t.Errorf("tam kapsamda uyarı olmamalı: %q", warn)
				}
				return
			}
			if warn == "" || !strings.Contains(warn, c.wantWarn) {
				t.Errorf("uyarı %q, %q içermeli", warn, c.wantWarn)
			}
		})
	}
}

// TestStateProbeAnsweredSQL — cevap sayımı probe'un kendi okumasıyla AYNI
// atlama ayarında ve tavanlı; küme adı string literal olarak kaçırılır.
func TestStateProbeAnsweredSQL(t *testing.T) {
	q := stateProbeAnsweredSQL("uptrace_all")
	for _, want := range []string{"SELECT count() FROM clusterAllReplicas('uptrace_all', system.one)", "skip_unavailable_shards = 1", "max_execution_time = 10"} {
		if !strings.Contains(q, want) {
			t.Errorf("%q içermeli: %s", want, q)
		}
	}
	if q := stateProbeAnsweredSQL("a'b"); !strings.Contains(q, "clusterAllReplicas('a''b', system.one)") {
		t.Errorf("küme adı kaçırılmadı: %s", q)
	}
}
