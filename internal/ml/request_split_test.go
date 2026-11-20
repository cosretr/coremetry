package ml

// request_split_test.go — v0.10.2380: başarısız / başarılı istek bölümü
// dedektörden kayda. Sözleşme: bölüm tikin 1 dakikalık örneklerinden (ek
// sorgu yok) bir kez üretilir; kanıt, Problem açıklaması ve olay kaydı AYNI
// sayıları taşır; doğrulayıcı ve yayın kapıları çalışmaya devam eder; alanı
// olmayan eski kayıt aynen geçer; op_error_rate kapsam dışıdır.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSvcRequestSplit(t *testing.T) {
	first := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC).Unix()
	for _, c := range []struct {
		name        string
		samples     []impactSample
		median      float64
		baseRate    float64 // istek/sn
		ok          bool
		minute      int64
		failed, okN float64
		normal      []float64
	}{
		{"en yeni dakika ölçülmüş", []impactSample{impS(0.004, 3000), impS(0.05, 3050), impS(0.06, 3080), impS(0.065, 3090), impS(0.0677, 3100)},
			0.004, 51.7, true, first + 240, 210, 2890, []float64{12, 3090}},
		{"en yeni dakika ölçülmemiş: son ölçülen okunur", []impactSample{impS(0.004, 3000), impS(0.05, 3000), impS(0.06, 2000), impGap, impGap},
			0.004, 50, true, first + 120, 120, 1880, []float64{12, 2988}},
		{"başarısız yok", []impactSample{impS(0.05, 400), impS(0.05, 400), impS(0.05, 400), impS(0.02, 400), impS(0, 400)},
			0.01, 6, true, first + 240, 0, 400, []float64{4, 356}},
		{"taban oranı 0", []impactSample{impGap, impGap, impS(0.5, 20), impS(0.5, 20), impS(0.5, 20)}, 0, 0.5, true, first + 240, 10, 10, []float64{0, 30}},
		{"taban trafiği yok: olağan düzey yazılmaz", []impactSample{impGap, impGap, impS(0.5, 20), impS(0.5, 20), impS(0.5, 20)}, 0.004, 0, true, first + 240, 10, 10, nil},
		{"ölçülmüş dakika yok", []impactSample{impGap, impGap, impGap, impGap, {Load: 5}}, 0.004, 10, false, 0, 0, 0, nil},
	} {
		v := svcImpactVerdict{Median: c.median, BaseRate: c.baseRate}
		rs, ok := svcRequestSplit(c.samples, v, first)
		if ok != c.ok {
			t.Fatalf("%s: ok=%v", c.name, ok)
		}
		if !ok {
			continue
		}
		if rs.Minute != c.minute || rs.FailedPerMin != c.failed || rs.OKPerMin != c.okN {
			t.Errorf("%s: %+v", c.name, rs)
		}
		if (c.normal == nil) != (rs.NormalFailedPerMin == nil) || (c.normal != nil && (*rs.NormalFailedPerMin != c.normal[0] || *rs.NormalOKPerMin != c.normal[1])) {
			t.Errorf("%s: olağan düzey %v / %v, beklenen %v", c.name, rs.NormalFailedPerMin, rs.NormalOKPerMin, c.normal)
		}
	}
}

// Olay kanıtı bölümü taşır; Problem cümlesi aynı sayıları söyler; kanıtta
// bölüm yoksa (eski olay) cümle bugünkü haliyle kalır.
func TestSvcImpactEventCarriesRequestSplit(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 19, 12, 5, 0, 0, time.UTC)
	samples := []impactSample{impS(0.004, 3000), impS(0.05, 3050), impS(0.06, 3080), impS(0.065, 3090), impS(0.0677, 3100)}
	v := svcImpactDecide(samples, repeat(0.004, 30), repeat(51.7, 30), 20, false)
	ev := svcImpactEvent("orders", samples, v, end, 0, cfg, "weekday")
	rs := chstore.MLRequestSplitOf(ev.Evidence)
	if rs == nil || rs.Minute != end.Add(-time.Minute).Unix() || rs.FailedPerMin != 210 || rs.OKPerMin != 2890 || *rs.NormalFailedPerMin != 12 || *rs.NormalOKPerMin != 3090 {
		t.Fatalf("kanıttaki bölüm: %+v (%s)", rs, ev.Evidence)
	}
	// mevcut kanıt alanları yerinde (dakika dizileri bölümün kaynağı)
	var m struct {
		Ratios []*float64 `json:"ratios"`
		Loads  []float64  `json:"loads"`
	}
	if err := json.Unmarshal([]byte(ev.Evidence), &m); err != nil || len(m.Ratios) != 5 || m.Loads[4] != 3100 {
		t.Fatalf("dakika dizileri: %v %s", err, ev.Evidence)
	}
	// v0.10.2383 — cümle "normalde X, şimdi Y (N kat)" (chstore.MLErrRateSentence).
	want := "orders servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat). Dakikada 210 başarısız, 2.890 başarılı istek (olağanı 12 başarısız, 3.090 başarılı)."
	if got := svcEventSentence(ev); got != want {
		t.Errorf("Problem cümlesi %q, beklenen %q", got, want)
	}
	if back, ok := chstore.ParseRequestSplit(svcEventSentence(ev)); !ok || back.FailedPerMin != rs.FailedPerMin || back.OKPerMin != rs.OKPerMin ||
		*back.NormalFailedPerMin != *rs.NormalFailedPerMin || *back.NormalOKPerMin != *rs.NormalOKPerMin {
		t.Errorf("cümleden geri okunan bölüm kanıttakiyle aynı olmalı: %+v", back)
	}
	// gölge damgası (mergeEvidence) bölümü silmez
	stamped := []chstore.MLEvent{ev}
	stampSvcShadow(stamped, map[string]SvcShadow{ev.ID: {WouldOpen: true, RuleID: MLSvcRulePrefix + "orders"}})
	if chstore.MLRequestSplitOf(stamped[0].Evidence) == nil {
		t.Error("gölge damgası istek bölümünü korumalı")
	}
	old := ev
	old.Evidence = `{"rule":"impact","ratios":[0.05],"loads":[200]}`
	if got := svcEventSentence(old); got != "orders servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat)." {
		t.Errorf("bölümsüz olayda cümleye istek bölümü eklenmemeli: %q", got)
	}
	// servis başına Problem girdisi cümleyi taşır (svcProblemInputs → Desc);
	// v0.10.2383: sonda lider olayın kaynak işareti.
	in := svcProblemInputs([]chstore.MLEvent{ev}, map[string]SvcShadow{ev.ID: {WouldOpen: true}})
	if len(in) != 1 || in[0].Desc != want+" "+chstore.REDEvidenceVM+"." {
		t.Errorf("Problem açıklaması: %+v", in)
	}
}

// Kayıt: bölüm kanıttan aynen; doğrulayıcı + kapılar çalışır; bölümsüz (eski)
// kayıt da geçer; bozuk bölüm kaydı geçersiz kılar; op_error_rate taşımaz.
func TestErrorRateRecordRequests(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 8, 30, 0, time.UTC)
	det := now.Add(-30 * time.Second)
	cfg := chstore.DefaultMLConfig()
	const ids = `"rule":"impact","redFamily":"http","svcLabel":"service_name","statusLabel":"http_response_status_code"`
	const req = `,"requests":{"minute":1795089960,"failedPerMin":30,"okPerMin":570,"normalFailedPerMin":2,"normalOkPerMin":598}`
	build := func(detector, evidence string) (chstore.MLEvent, EventRecord) {
		t.Helper()
		evs := []chstore.MLEvent{{ID: "ep1", Detector: detector, Signal: chstore.MLSignalTrace, Service: "orders", Stage: chstore.MLStageShadow,
			Baseline: 0.004, Observed: 0.05, StartedAt: det.Add(-4 * time.Minute).UnixNano(), LastSeen: det.UnixNano(), WindowSec: 300, Evidence: evidence}}
		budget := 5
		attachErrorRateRecords(context.Background(), Deps{VM: &errRecVM{det: det}}, cfg, evs, false, func(string) time.Time { return det }, nil, &budget, now)
		ev := applyRecordContract(evs[0])
		var rec EventRecord
		if ev.Record != "" {
			if err := json.Unmarshal([]byte(ev.Record), &rec); err != nil {
				t.Fatal(err)
			}
		}
		return ev, rec
	}
	ev, rec := build(detSvcErrorRate, "{"+ids+req+"}")
	if ev.PublishState != PublishPublished || rec.Requests == nil || rec.Requests.FailedPerMin != 30 || rec.Requests.OKPerMin != 570 ||
		*rec.Requests.NormalFailedPerMin != 2 || rec.Requests.Minute != 1795089960 {
		t.Fatalf("kayıt bölümü kanıttan aynen taşımalı: state=%q %+v", ev.PublishState, rec.Requests)
	}
	if len(rec.Gates) == 0 {
		t.Fatal("yayın kapıları bölümlü kayıtta da değerlendirilmeli")
	}
	if !RequestsCapable(detSvcErrorRate, rec) {
		t.Fatal("1 dk oran + hacim: istek görünümü çizilebilir olmalı")
	}
	// eski olay: kanıtta bölüm yok → kayıt bölümsüz, yine yayımlanır
	evOld, recOld := build(detSvcErrorRate, "{"+ids+"}")
	if evOld.PublishState != PublishPublished || recOld.Requests != nil || strings.Contains(evOld.Record, `"requests"`) {
		t.Fatalf("bölümsüz kayıt aynen geçmeli: state=%q", evOld.PublishState)
	}
	if recOld.Title != rec.Title || !reflect.DeepEqual(recOld.Snapshot, rec.Snapshot) || len(recOld.Gates) != len(rec.Gates) {
		t.Fatal("bölüm kaydın başlığını / serisini / kapılarını değiştirmemeli")
	}
	// dakikası olmayan bölüm kayda GİRMEZ (kayıt geçersiz olmaz)
	if evNoMin, recNoMin := build(detSvcErrorRate, "{"+ids+`,"requests":{"failedPerMin":30,"okPerMin":570}}`); evNoMin.PublishState != PublishPublished || recNoMin.Requests != nil {
		t.Fatalf("dakikasız bölüm kayda girmemeli: state=%q %+v", evNoMin.PublishState, recNoMin.Requests)
	}
	// op_error_rate: kanıtında bölüm olsa bile kayda yazılmaz (kapsam dışı)
	evOp, recOp := build(detOpErrorRate, "{"+ids+`,"routeLabel":"http_route"`+req+"}")
	if evOp.Record == "" || recOp.Requests != nil || RequestsCapable(detOpErrorRate, recOp) {
		t.Fatalf("op_error_rate istek bölümü taşımamalı: %+v", recOp.Requests)
	}
}

func TestValidateRecordRequestsRule(t *testing.T) {
	rec, ok := BuildREDRecord(errTestInput(detSvcErrorRate, true, func(m int) float64 {
		if m >= 40 && m < 55 {
			return 4.2
		}
		return 0.4
	}))
	if !ok || len(ValidateRecord(rec)) != 0 {
		t.Fatal("taban kayıt geçmeli")
	}
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		name string
		q    chstore.StdRequestSplit
		bad  bool
	}{
		{"geçerli", chstore.StdRequestSplit{Minute: 600, FailedPerMin: 4, OKPerMin: 96, NormalFailedPerMin: f(0), NormalOKPerMin: f(100)}, false},
		{"başarısız 0", chstore.StdRequestSplit{Minute: 600, FailedPerMin: 0, OKPerMin: 96}, false},
		{"dakika yok", chstore.StdRequestSplit{FailedPerMin: 4, OKPerMin: 96}, true},
		{"istek yok", chstore.StdRequestSplit{Minute: 600}, true},
		{"negatif", chstore.StdRequestSplit{Minute: 600, FailedPerMin: -1, OKPerMin: 96}, true},
		{"olağan düzey yarım", chstore.StdRequestSplit{Minute: 600, FailedPerMin: 4, OKPerMin: 96, NormalOKPerMin: f(100)}, true},
	} {
		r := rec
		q := c.q
		r.Requests = &q
		vs := ValidateRecord(r)
		if bad := len(vs) == 1 && vs[0].Rule == RuleRequests; bad != c.bad || (!c.bad && len(vs) != 0) {
			t.Errorf("%s: ihlaller %+v", c.name, vs)
		}
	}
	// Problem detayına giden görünüm: yalnız çizilebilen kayıt, metin alanları atılmış
	rec.Requests = &chstore.StdRequestSplit{Minute: 600, FailedPerMin: 4, OKPerMin: 96}
	raw, _ := json.Marshal(rec)
	ev := chstore.MLEvent{ID: "e1", Detector: detSvcErrorRate, Record: string(raw), PublishState: PublishPublished}
	v := RequestsRecordView(&ev)
	if v == nil || v.Detector != detSvcErrorRate || v.Requests == nil || len(v.Evidence) != 0 || len(v.Gates) != 0 || len(v.Snapshot.N) != len(v.Snapshot.V) {
		t.Fatalf("istek görünümü: %+v", v)
	}
	for name, mut := range map[string]func(*chstore.MLEvent, *EventRecord){
		"geçersiz kayıt":        func(e *chstore.MLEvent, _ *EventRecord) { e.PublishState = PublishInvalid },
		"başka dedektör":        func(e *chstore.MLEvent, _ *EventRecord) { e.Detector = detOpErrorRate },
		"5 dk'lık eski seri":    func(_ *chstore.MLEvent, r *EventRecord) { r.Series.StepSec = 300 },
		"hacim sütunu yok":      func(_ *chstore.MLEvent, r *EventRecord) { r.Snapshot.N = nil },
		"oran yüzde değil":      func(_ *chstore.MLEvent, r *EventRecord) { r.Series.Unit = "ms" },
		"kayıt yok (eski olay)": func(e *chstore.MLEvent, r *EventRecord) { *r = EventRecord{} },
	} {
		e, r := ev, rec
		mut(&e, &r)
		b, _ := json.Marshal(r)
		e.Record = string(b)
		if name == "kayıt yok (eski olay)" {
			e.Record = ""
		}
		if RequestsRecordView(&e) != nil {
			t.Errorf("%s: istek görünümü dönmemeli", name)
		}
	}
}
