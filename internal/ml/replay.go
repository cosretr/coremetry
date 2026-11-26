package ml

// replay.go — v0.10.2252, anomali kartı Dilim E (docs/ml/anomaly-card-audit.md
// §7.1, §10 E): geçmiş olayların ÇEVRİMDIŞI, deterministik yeniden oynatımı.
// Dışa aktarılmış ml_events satırları (§7.1 biçimi: olay başına bir dizin,
// dosya başına bir tablo, JSONEachRow, .jsonl ya da .jsonl.gz) BUGÜNKÜ kayıt
// sözleşmesinden (ValidateRecord) ve gölge yayın kapılarından (EvaluateGates +
// gateVerdict; contractVerdict üzerinden — yazım yoluyla aynı karar) geçirilir;
// dedektör başına rapor döner.
//
// Geliştirici / operatör aracı ve regresyon koşumu: `go test` (replay_test.go)
// ve `make eval-ml` (cmd/ml-replay). Üretim kod yolu ÇAĞIRMAZ (replay_test.go
// pinler): ClickHouse'a bağlanmaz, yazmaz, sorgu yükü yok; yalnız dosya okur.
// Gölge sayacına (global) dokunmaz.
//
// Kapsam: kaydı (ml_events.record) olan olay yeniden yargılanır. Kaydı olmayan
// eski olay "kayıt yok" (değerlendirilemez) sayılır — dedektörü ham seriden
// (ml_series / spanmetrics_1m) yeniden koşmak bu dilimin dışında.

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Replay sonuçları — olay başına tek sonuç.
const (
	ReplayPublish         = "publish"          // tüm kapılar geçti; sınıf değişmez
	ReplayDrop            = "drop"             // bir yayın kapısı kaldı (A2'de yayımlanmaz)
	ReplayInfo            = "info"             // kanıt / etki kaldı → sınıf info
	ReplayTelemetryVolume = "telemetry_volume" // hacim kaydı, servis etkisi yok
	ReplayInvalid         = "invalid"          // kayıt çözülemedi ya da doğrulayıcıdan kaldı
	ReplayNoRecord        = "no_record"        // sözleşmesiz eski olay; değerlendirilemez
)

// ReplayExpect — fikstür dizinindeki expect.json girdisi (olay id → beklenti).
// Boş alan denetlenmez.
type ReplayExpect struct {
	Outcome     string   `json:"outcome"`
	FailedGates []string `json:"failedGates,omitempty"`
	Violations  []string `json:"violations,omitempty"` // doğrulayıcı kural adları
}

// ReplayEvent — bir olayın yeniden oynatım sonucu.
type ReplayEvent struct {
	Fixture     string            `json:"fixture"` // dizin (köke göre)
	ID          string            `json:"id"`
	Detector    string            `json:"detector"`
	Outcome     string            `json:"outcome"`
	FailedGates []string          `json:"failedGates,omitempty"`
	NotEvalGate []string          `json:"notEvaluableGates,omitempty"`
	Violations  []RecordViolation `json:"violations,omitempty"`
	Mismatch    []string          `json:"mismatch,omitempty"` // expect.json ile fark
	Expected    bool              `json:"expected"`           // expect.json girdisi vardı
}

// ReplayReport — dizinin raporu. ByDetector gölge raporuyla aynı sayaçlar
// (chstore.MLGateShadow; NotEvaluated = kayıt yok + invalid).
type ReplayReport struct {
	Dir        string                          `json:"dir"`
	Events     []ReplayEvent                   `json:"events"`
	ByDetector map[string]chstore.MLGateShadow `json:"byDetector"`
	Mismatches int                             `json:"mismatches"`
}

// replayRow — ml_events dışa aktarım satırından okunan alanlar (diğerleri yok sayılır).
type replayRow struct {
	ID       string `json:"id"`
	Detector string `json:"detector"`
	Record   string `json:"record"`
}

// ReplayRecord — SAF: tek olayın bugünkü sözleşme + kapı sonucu.
func ReplayRecord(id, detector, record string) ReplayEvent {
	e := ReplayEvent{ID: id, Detector: detector}
	_, vs, o := contractVerdict(record, true)
	e.FailedGates, e.NotEvalGate, e.Violations = o.Failed, o.NotEvaluable, vs
	e.Outcome = replayOutcome(record, o)
	return e
}

func replayOutcome(record string, o gateOutcome) string {
	switch {
	case record == "":
		return ReplayNoRecord
	case o.Invalid:
		return ReplayInvalid
	case o.Drop:
		return ReplayDrop
	case o.WouldClass == ClassInfo:
		return ReplayInfo
	case o.WouldClass == ClassTelemetryVolume:
		return ReplayTelemetryVolume
	}
	return ReplayPublish
}

// outcomeOf — rapor sayacı için olay sonucundan gateOutcome (foldGateShadow girdisi).
func outcomeOf(e ReplayEvent) gateOutcome {
	o := gateOutcome{Failed: e.FailedGates, NotEvaluable: e.NotEvalGate}
	switch e.Outcome {
	case ReplayNoRecord:
		return gateOutcome{}
	case ReplayInvalid:
		return gateOutcome{Invalid: true}
	case ReplayDrop:
		o.Drop = true
	case ReplayInfo:
		o.WouldClass = ClassInfo
	case ReplayTelemetryVolume:
		o.WouldClass = ClassTelemetryVolume
	}
	o.Evaluated = true
	return o
}

// ReplayDir — dizin ağacındaki her fikstür dizinini (ml_events.jsonl[.gz]
// taşıyan) okur, olayları yeniden oynatır, expect.json varsa karşılaştırır.
// Sıra deterministik (fikstür, id). Dizin yoksa hata.
func ReplayDir(root string) (ReplayReport, error) {
	rep := ReplayReport{Dir: root}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return rep, fmt.Errorf("fikstür dizini yok: %s (dışa aktarım: docs/ml/anomaly-card-audit.md §7.1)", root)
	}
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (d.Name() == "ml_events.jsonl" || d.Name() == "ml_events.jsonl.gz") {
			dirs = append(dirs, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	sort.Strings(dirs)
	for i, d := range dirs {
		if i > 0 && dirs[i-1] == d {
			continue
		}
		evs, err := replayFixture(root, d)
		if err != nil {
			return rep, err
		}
		rep.Events = append(rep.Events, evs...)
	}
	entries := map[string]gateEntry{}
	for _, e := range rep.Events {
		entries[e.Fixture+"\x00"+e.ID] = gateEntry{detector: e.Detector, o: outcomeOf(e)}
		if len(e.Mismatch) > 0 {
			rep.Mismatches++
		}
	}
	rep.ByDetector = foldGateShadow(entries)
	return rep, nil
}

// replayFixture — tek fikstür dizini: ml_events satırları + isteğe bağlı expect.json.
func replayFixture(root, dir string) ([]ReplayEvent, error) {
	rows, err := readReplayRows(dir)
	if err != nil {
		return nil, err
	}
	var expect map[string]ReplayExpect
	if b, err := os.ReadFile(filepath.Join(dir, "expect.json")); err == nil {
		if err := json.Unmarshal(b, &expect); err != nil {
			return nil, fmt.Errorf("%s/expect.json: %w", dir, err)
		}
	}
	rel, _ := filepath.Rel(root, dir)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	var out []ReplayEvent
	for i, r := range rows {
		if i > 0 && rows[i-1].ID == r.ID {
			continue // FINAL'siz dışa aktarımda çift satır: ilki
		}
		e := ReplayRecord(r.ID, r.Detector, r.Record)
		e.Fixture = filepath.ToSlash(rel)
		if x, ok := expect[r.ID]; ok {
			e.Expected = true
			e.Mismatch = replayMismatch(e, x)
		}
		out = append(out, e)
	}
	for id := range expect {
		found := false
		for _, r := range rows {
			found = found || r.ID == id
		}
		if !found {
			return nil, fmt.Errorf("%s/expect.json: olay %q ml_events'te yok", dir, id)
		}
	}
	return out, nil
}

// replayMismatch — SAF: sonuç ile beklenti farkları (boş = tuttu).
func replayMismatch(e ReplayEvent, x ReplayExpect) []string {
	var out []string
	if x.Outcome != "" && x.Outcome != e.Outcome {
		out = append(out, fmt.Sprintf("sonuç %s, beklenen %s", e.Outcome, x.Outcome))
	}
	if x.FailedGates != nil && !sameSet(x.FailedGates, e.FailedGates) {
		out = append(out, fmt.Sprintf("kalan kapılar %v, beklenen %v", e.FailedGates, x.FailedGates))
	}
	if x.Violations != nil {
		var got []string
		for _, v := range e.Violations {
			got = append(got, v.Rule)
		}
		if !sameSet(x.Violations, got) {
			out = append(out, fmt.Sprintf("ihlaller %v, beklenen %v", got, x.Violations))
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	n := map[string]bool{}
	for _, x := range b {
		if !m[x] {
			return false
		}
		n[x] = true
	}
	return len(m) == len(n)
}

// readReplayRows — ml_events.jsonl ya da .jsonl.gz (JSONEachRow) satırları.
func readReplayRows(dir string) ([]replayRow, error) {
	p := filepath.Join(dir, "ml_events.jsonl")
	f, err := os.Open(p)
	gz := false
	if err != nil {
		p += ".gz"
		if f, err = os.Open(p); err != nil {
			return nil, err
		}
		gz = true
	}
	defer f.Close()
	var r io.Reader = f
	if gz {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		defer zr.Close()
		r = zr
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	var rows []replayRow
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row replayRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", p, ln, err)
		}
		if row.ID == "" {
			return nil, fmt.Errorf("%s:%d: id yok", p, ln)
		}
		rows = append(rows, row)
	}
	return rows, sc.Err()
}

// WriteText — insan okur rapor: dedektör tablosu + olay satırları + beklenti özeti.
func (rep ReplayReport) WriteText(w io.Writer) error {
	fmt.Fprintf(w, "ML replay — %s — %d olay (gölge kapılar; liste / bildirim / Problem değişmez)\n\n", rep.Dir, len(rep.Events))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "detector\tevaluated\twould_publish\twould_drop\twould_info\twould_telemetry_volume\tnot_evaluable\tinvalid\tfailed_by_gate")
	dets := make([]string, 0, len(rep.ByDetector))
	for d := range rep.ByDetector {
		dets = append(dets, d)
	}
	sort.Strings(dets)
	for _, d := range dets {
		r := rep.ByDetector[d]
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", d, r.Evaluated, r.WouldPublish, r.WouldDrop, r.WouldInfo,
			r.WouldTelemetryVolume, r.NotEvaluated, r.Invalid, kvLine(r.FailedByGate))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w, "\nolaylar:")
	checked := 0
	for _, e := range rep.Events {
		line := fmt.Sprintf("  %s  %s  %s → %s", e.Fixture, e.ID, e.Detector, e.Outcome)
		if len(e.FailedGates) > 0 {
			line += "  kalan=" + strings.Join(e.FailedGates, ",")
		}
		if len(e.Violations) > 0 {
			line += fmt.Sprintf("  ihlal=%s (%s)", e.Violations[0].Rule, e.Violations[0].Reason)
			if len(e.Violations) > 1 {
				line += fmt.Sprintf(" +%d", len(e.Violations)-1)
			}
		}
		if e.Expected {
			checked++
			if len(e.Mismatch) > 0 {
				line += "  BEKLENTİ TUTMADI: " + strings.Join(e.Mismatch, "; ")
			}
		}
		fmt.Fprintln(w, line)
	}
	_, err := fmt.Fprintf(w, "\nbeklenti: %d olayda denetlendi, %d tutmadı\n", checked, rep.Mismatches)
	return err
}
