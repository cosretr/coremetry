package ml

// publish_gate_a2_pin_test.go — v0.10.2343 (anomali kartı A2, kapalı gelir):
// anahtar (mlPublishGatesEnforce) KAPALIYKEN yazım, Problem terfisi ve
// bildirim kararlarının bu dilimden ÖNCEKİ kodla bayt bayt aynı olduğunun
// kanıtı. a2OffDigest aynı fikstürü (replay fikstürlerinin kayıtları +
// sentetik grup / servis düzeyi olaylar) bugünkü yoldan geçirir ve çıktının
// SHA-256'sını döner; beklenen değer v0.10.2340 (A2 öncesi) koduyla
// üretilip buraya sabitlendi. Saat dilimi / günün saatinden bağımsız
// (sabit UTC zamanlar). Servis adları SENTETİK.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// a2FixtureRow — replay fikstürü satırının gereken alanları.
type a2FixtureRow struct {
	ID           string  `json:"id"`
	Detector     string  `json:"detector"`
	Service      string  `json:"service"`
	Class        string  `json:"class"`
	PublishState string  `json:"publish_state"`
	Record       string  `json:"record"`
	Baseline     float64 `json:"baseline"`
	Observed     float64 `json:"observed"`
}

// a2Fixture — SAF (dosya okur): olaylar, id sırasıyla. Kayıtlı olaylar
// (yayımlanır / tutulur / info / geçersiz / hacim / kayıtsız) tek sinyalli
// güçlü kötüleşme olarak kurulur (tek sinyal kolunu bugün açarlar); ek
// olarak iki sinyalli grup (checkout) ve servis düzeyi olay (orders).
func a2Fixture(t *testing.T) []chstore.MLEvent {
	t.Helper()
	dirs := []string{"latency_publish", "latency_one_bucket_blip", "error_rate_single_evidence", "invalid_record", "volume_only", "legacy_no_record"}
	var out []chstore.MLEvent
	for _, d := range dirs {
		f, err := os.Open(filepath.Join("testdata", "replay", d, "ml_events.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			var r a2FixtureRow
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Fatal(err)
			}
			e := gateEv(r.ID, r.Detector, r.Service, 0, 30, 100, 500)
			e.Class, e.PublishState, e.Record = r.Class, r.PublishState, r.Record
			out = append(out, e)
		}
		f.Close()
	}
	// iki sinyalli grup: trace gecikme + log hata oranı, aynı servis
	out = append(out, gateEv("b1", detOpLatency, "checkout", 0, 30, 100, 300), gateEv("b2", detErrorRate, "checkout", 5, 30, 10, 30))
	// servis düzeyi olay (kendi kapısı)
	svc := gateEv("c1", detSvcErrorRate, "orders", 0, 30, 0.01, 0.05)
	out = append(out, svc)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// a2Outputs — fikstürü yazım + terfi + bildirim yolundan geçirir; JSON çıktı.
// in.Published nil değilse A2 kipinin terfi girdisi (yalnız açık-kip testi).
func a2Outputs(t *testing.T, evs []chstore.MLEvent, in GroupGateInput) []byte {
	t.Helper()
	now := gBase.Add(40 * time.Minute)
	written := make([]chstore.MLEvent, len(evs))
	for i, e := range evs {
		written[i] = applyRecordContract(e)
	}
	kept := make([]chstore.MLEvent, len(evs))
	copy(kept, evs)
	groups, counts := decideGroups(kept, nil, 10*time.Minute, in, now)
	svcDec := decideSvcShadow(publishedCandidates(kept, in.Published), nil, groups, chstore.DefaultProblemPriority(), now)
	plan := planMLProblems(groups, svcProblemInputs(kept, svcDec), nil, true, func(g decidedGroup) string { return g.D.Reason })
	stampGroupDecisions(kept, groups)
	stampSvcShadow(kept, svcDec)
	var open []string
	for _, p := range plan.Open {
		open = append(open, p.ID+" | "+p.Severity+" | "+p.Description)
	}
	b, err := json.Marshal(map[string]any{
		"written": written, "stamped": kept, "counts": counts,
		"open": open, "refresh": len(plan.Refresh), "resolve": len(plan.Resolve),
		"single": plan.SingleOpen, "svcOpen": plan.SvcOpen, "liveGroup": plan.LiveGroup, "liveSvc": plan.LiveSvc,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func a2Digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// a2OffWant — v0.10.2340 (A2 öncesi) koduyla üretilen çıktı özeti.
const a2OffWant = "581d8fb36ef19b6a2e472d2fc58d4b14a82834de32ff9a9beb56ab399fb55eff"

// a2SvcSentence — v0.10.2383: servis hata oranı Problem cümlesi BİLİNÇLİ
// değişti ("normalde X, şimdi Y (N kat)" + kaynak işareti). Pinin gücü
// korunur: özet, fikstürdeki o TEK cümle v0.10.2340 metnine geri çevrilerek
// alınır — geri kalan her bayt A2 öncesiyle aynı olmak zorunda.
const (
	a2SvcSentenceNow = "orders servisinde hata oranı normalde %1, şimdi %5 (5 kat). " + chstore.REDEvidenceVM + "."
	a2SvcSentenceOld = "orders servisinde hata oranı %5, olağanı %1."
)

func a2PreSentence(t *testing.T, out []byte) []byte {
	t.Helper()
	if n := bytes.Count(out, []byte(a2SvcSentenceNow)); n != 1 {
		t.Fatalf("servis cümlesi çıktıda %d kez (1 beklenir)", n)
	}
	return bytes.Replace(out, []byte(a2SvcSentenceNow), []byte(a2SvcSentenceOld), 1)
}

func TestPublishGatesA2OffMatchesPreA2(t *testing.T) {
	got := a2Digest(a2PreSentence(t, a2Outputs(t, a2Fixture(t), GroupGateInput{})))
	if got != a2OffWant {
		t.Fatalf("A2 kapalıyken yazım / terfi / bildirim çıktısı A2 öncesinden farklı: %s", got)
	}
}
