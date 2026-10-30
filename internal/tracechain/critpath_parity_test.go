package tracechain

// critpath_parity_test.go — v0.10.1625 — CoSRE kritik yol planı R1.
//
// Sözleşme: "Go ikizi lib/criticalPath.ts ile aynı kural" bir YORUMDU, bir
// kontrol değil. testdata/critpath/*.json her iki tarafın okuduğu AYNI
// baytlardır: bu test CriticalPathOf'un sırasını / kök duvar saatini /
// yol-üstü öz süresini, frontend/src/lib/criticalPath.parity.test.ts aynı
// beklentiyi computeCriticalPath için doğrular. Beklenti elle yazılır (üretici
// YOK — beklentinin kendisi sözleşmedir). expectGo yalnız Go'nun hesapladığı
// alanlar: en büyük pay, dış bağımlılık beklemesi, hedef, kapanış türü.
// Fikstürler sentetiktir (orders-api, *.ext.example, com.acme.*).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type cpFixSpan struct {
	SpanID     string            `json:"spanId"`
	ParentID   string            `json:"parentId"`
	StartNs    int64             `json:"startNs"`
	DurNs      int64             `json:"durNs"`
	Kind       string            `json:"kind"`
	Service    string            `json:"service"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes"`
}

type cpFixture struct {
	Name   string      `json:"name"`
	Spans  []cpFixSpan `json:"spans"`
	Expect struct {
		Order        []string         `json:"order"`
		RootWallNs   int64            `json:"rootWallNs"`
		OnPathSelfNs map[string]int64 `json:"onPathSelfNs"`
	} `json:"expect"`
	ExpectGo struct {
		Top             string `json:"top"`
		TopExternalWait bool   `json:"topExternalWait"`
		Closing         string `json:"closing"`
		TopTarget       *struct {
			Kind, Value, Source string
		} `json:"topTarget"`
		// ExternalWaitLine — v0.10.1639: sunucunun mekanik önek satırı
		// (api traceAnswerWithLatencyOwner) bayt bayt; boş = pinlenmez.
		ExternalWaitLine string `json:"externalWaitLine"`
	} `json:"expectGo"`
}

func (f cpFixture) rows() []chstore.SpanRow {
	out := make([]chstore.SpanRow, 0, len(f.Spans))
	for _, s := range f.Spans {
		out = append(out, chstore.SpanRow{SpanID: s.SpanID, ParentSpanID: s.ParentID, StartTime: s.StartNs,
			EndTime: s.StartNs + s.DurNs, Kind: s.Kind, ServiceName: s.Service, Name: s.Name,
			StatusCode: s.Status, Attributes: s.Attributes})
	}
	return out
}

func loadCritPathFixtures(t *testing.T) []cpFixture {
	t.Helper()
	files, err := filepath.Glob("../../testdata/critpath/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("testdata/critpath fikstürü yok (%v) — taşındıysa test yüksek sesle kırılsın", err)
	}
	sort.Strings(files)
	out := make([]cpFixture, 0, len(files))
	for _, fn := range files {
		raw, err := os.ReadFile(fn)
		if err != nil {
			t.Fatal(err)
		}
		var f cpFixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		out = append(out, f)
	}
	return out
}

func TestCriticalPathParityFixtures(t *testing.T) {
	for _, f := range loadCritPathFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			cp := CriticalPathOf(f.rows())
			if !cp.OK {
				t.Fatal("OK=false")
			}
			var order []string
			self := map[string]int64{}
			for _, st := range cp.Steps {
				order = append(order, st.SpanID)
				self[st.SpanID] = st.SelfOnPathNs
			}
			if !reflect.DeepEqual(order, f.Expect.Order) {
				t.Errorf("sıra = %v, beklenen %v", order, f.Expect.Order)
			}
			if cp.RootWallNs != f.Expect.RootWallNs {
				t.Errorf("rootWallNs = %d, beklenen %d", cp.RootWallNs, f.Expect.RootWallNs)
			}
			if !reflect.DeepEqual(self, f.Expect.OnPathSelfNs) {
				t.Errorf("onPathSelfNs = %v, beklenen %v", self, f.Expect.OnPathSelfNs)
			}
			top, ok := cp.TopStep()
			if !ok || top.SpanID != f.ExpectGo.Top {
				t.Errorf("en büyük pay = %q (ok=%v), beklenen %q", top.SpanID, ok, f.ExpectGo.Top)
			}
			if got := cp.TopIsExternalWait(); got != f.ExpectGo.TopExternalWait {
				t.Errorf("TopIsExternalWait = %v, beklenen %v", got, f.ExpectGo.TopExternalWait)
			}
			if got := cp.closingKind(); got != f.ExpectGo.Closing {
				t.Errorf("kapanış = %q, beklenen %q", got, f.ExpectGo.Closing)
			}
			if tt := f.ExpectGo.TopTarget; tt != nil {
				if top.Target.Kind != tt.Kind || top.Target.Value != tt.Value || top.Target.Source != tt.Source {
					t.Errorf("hedef = %+v, beklenen %+v", top.Target, *tt)
				}
				// v0.10.1639 — blokta da aynı hedef; span adı hedef yerine geçmez.
				if b := cp.Block(); !strings.Contains(b, "· hedef "+tt.Value+" ·") {
					t.Errorf("blokta hedef %q yok:\n%s", tt.Value, b)
				}
			}
			if want := f.ExpectGo.ExternalWaitLine; want != "" {
				if got := cp.ExternalWaitLine(); got != want {
					t.Errorf("önek satırı = %q, beklenen %q", got, want)
				}
			}
		})
	}
}

// TestExternalWaitLineFromBlockRoundTrip — v0.10.1630 (R3): sunucunun mekanik
// öneki (api traceAnswerWithLatencyOwner) evalset'te yalnız kanıt METNİNİ
// görür; bloktan geri okunan satır yapıdan kurulanla bayt bayt aynı olmalı.
func TestExternalWaitLineFromBlockRoundTrip(t *testing.T) {
	for _, f := range loadCritPathFixtures(t) {
		cp := CriticalPathOf(f.rows())
		evidence := "önsöz\n\n" + cp.Block() + "\n\nTrace x with 3 spans:\n```json\n[]\n```"
		if got, want := ExternalWaitLineFromBlock(evidence), cp.ExternalWaitLine(); got != want {
			t.Errorf("%s: bloktan %q, yapıdan %q", f.Name, got, want)
		}
	}
	if ExternalWaitLineFromBlock("En büyük pay: a · CLIENT · hedef x · 1 s (%9) — dış bağımlılık beklemesi") != "" {
		t.Error("KRİTİK YOL başlığı olmadan satır okundu")
	}
}
