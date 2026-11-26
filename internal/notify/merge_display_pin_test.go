package notify

// merge_display_pin_test.go — v0.10.2473 PİN: servisler arası birleştirmenin
// GÖRÜNÜM kipi (problem_graph_merge: off | shadow | on, varsayılan on)
// bildirim davranışını DEĞİŞTİRMEZ.
//
// Kip ne olursa olsun, notify_policy hangi kipte olursa olsun:
//   - sonradan bağlanan küme üyesinin gönderim kararı (enforceGate) ve
//     politika gölge satırı, tutma kaynağı hiç bağlanmamış bir bildiriciyle
//     AYNIDIR;
//   - tutma kaynağı (evaluator.MergeHold) hiç sorulmaz.
//
// Bildirim tutması ayrı anahtardadır (chstore.problemGraphMergeEnforce, kod
// sabiti, kapalı; TestProblemGraphMergeEnforceShipsOff). Bu test kırılırsa
// liste ayarı bildirimi etkilemeye başlamış demektir — operatör kararı
// olmadan birleştirilmez.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMergeDisplayModeNeverChangesNotifications(t *testing.T) {
	if chstore.ProblemGraphMergeEnforced() {
		t.Fatal("ön koşul: bildirim tutması sabiti kapalı gelir")
	}
	if chstore.DefaultProblemGraphMerge().Mode != chstore.GraphMergeOn {
		t.Fatal("ön koşul: görünüm kipi varsayılanı on")
	}
	ctx := context.Background()
	for _, display := range []string{chstore.GraphMergeOff, chstore.GraphMergeShadow, chstore.GraphMergeOn} {
		restore := chstore.SetProblemGraphMergeModeForTest(display)
		for _, policy := range []string{chstore.NotifyModeOff, chstore.NotifyModeShadow, chstore.NotifyModeEnforce} {
			withPolicyMode(t, policy)
			src := &fakeMergeSrc{}
			// Kaynak "bu üye tutulur" diyecek şekilde kurulu; sahibin açılışı gitmiş.
			withSrc, _ := mergeNotifier(src, true)
			without := enforceNotifier()
			without.threads.m = withSrc.threads.m
			for _, prio := range []string{"P1", "P2", "P3"} {
				p := excProblem("fp-pin-"+display+"-"+policy+"-"+prio, prio)
				ok1, h1 := withSrc.enforceGate(ctx, p)
				ok2, h2 := without.enforceGate(ctx, p)
				if ok1 != ok2 || h1 != h2 {
					t.Fatalf("görünüm %s / politika %s / %s: gönderim kararı değişti: %v %q vs %v %q", display, policy, prio, ok1, h1, ok2, h2)
				}
				if h1 == chstore.NotifyHoldMerged {
					t.Fatalf("görünüm %s / politika %s / %s: `merged` tutması uygulanmamalı", display, policy, prio)
				}
				var rows [2][]chstore.NotificationLog
				for i, n := range []*Notifier{withSrc, without} {
					i := i
					n.shadow.sink = func(_ context.Context, e chstore.NotificationLog) error { rows[i] = append(rows[i], e); return nil }
					n.recordShadowDecision(ctx, p, chstore.CurrentNotifyPolicy(), time.Now())
				}
				if !reflect.DeepEqual(rows[0], rows[1]) {
					t.Fatalf("görünüm %s / politika %s / %s: politika gölge satırı değişti: %+v vs %+v", display, policy, prio, rows[0], rows[1])
				}
			}
			if src.calls != 0 {
				t.Fatalf("görünüm %s / politika %s: tutma kaynağı sorulmamalı (%d çağrı)", display, policy, src.calls)
			}
		}
		restore()
	}
}

// Kaynak pini: bildirim paketi ve evaluator'ın bildirim anı kararı görünüm
// kipini HİÇ okumaz; tek kapıları bildirim tutması sabitidir.
func TestMergeDisplayModeNotReadByNotifyPath(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"ProblemGraphMergeOn(", "ProblemGraphMergeMode(", "CurrentProblemGraphMerge("} {
			if strings.Contains(string(b), banned) {
				t.Fatalf("notify/%s görünüm kipini okuyor (%s): bildirim davranışı liste ayarına bağlanmamalı", f, banned)
			}
		}
	}
	b, err := os.ReadFile(filepath.Join("..", "evaluator", "problem_graph_merge.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (e *Evaluator) MergeHold(")
	if i < 0 {
		t.Fatal("evaluator MergeHold bulunamadı")
	}
	body := src[i:]
	if !strings.Contains(body, "!chstore.ProblemGraphMergeEnforced()") {
		t.Fatal("MergeHold bildirim tutması sabitiyle kapılı kalmalı")
	}
	if strings.Contains(body, "ProblemGraphMergeOn(") || strings.Contains(body, "ProblemGraphMergeMode(") {
		t.Fatal("MergeHold görünüm kipini okumamalı")
	}
}
