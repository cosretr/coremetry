package evalrubric

// stream_contract_test.go — v0.10.2107 (CoSRE kalite döngüsü D2,
// docs/cosre/quality-loop-audit.md §3.2): stream_contract boyutu.
//
// Sözleşme: ağırlık 0,10, mevcut beş boyutun (toplam 1,0) ÜSTÜNE eklenir;
// toplam uygulanabilir ağırlıklara bölünerek normalize edilir. StreamContract
// nil (v1 koşusu) → boyut HİÇ eklenmez: v1 Result'ı bayt bayt eskisi
// (aşağıdaki literaller v0.10.2106 kodunun çıktısı). Eşik 0,8 değişmez.

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func v1Inputs(tr, fa *bool) []Input {
	return []Input{
		{Answer: "checkout servisinde hata oranı arttı çünkü bağımlılık zaman aşımı veriyor.", MustContain: []string{"checkout", "hata"}, ExpectTurkish: true, MaxChars: 500},
		{Answer: "checkout yavaş", MustContain: []string{"checkout", "payment"}, UnknownCount: 2, MaxUnknown: 1},
		{Answer: "grafana panosuna bak", MustContain: []string{"grafana"}, MustNotContain: []string{"grafana"}, ExpectTurkish: true},
		{Answer: "{\"x\":1}"},
		{Answer: "uzun bir cevap metni burada", ExpectTurkish: true, MaxChars: 5, ToolCallsValid: fa},
		{Answer: "servis için bir cevap ve sonuç", ExpectTurkish: true, ToolCallsValid: tr},
		{Err: errors.New("x"), ExpectTurkish: true, MaxChars: 10, ToolCallsValid: tr},
	}
}

// TestV1ScoreBytesUnchanged — v1 girdisi (StreamContract nil) v0.10.2106
// çıktısının AYNISINI üretir.
func TestV1ScoreBytesUnchanged(t *testing.T) {
	want := []string{
		`{"dims":[{"name":"grounded","score":1,"max":1,"weight":0.4},{"name":"answers_question","score":2,"max":2,"weight":0.3},{"name":"language_tr","score":1,"max":1,"weight":0.1},{"name":"length_ok","score":1,"max":1,"weight":0.1},{"name":"tool_calls_valid","score":0,"max":0,"weight":0.1}],"total":1,"applicable":4}`,
		`{"dims":[{"name":"grounded","score":0,"max":1,"weight":0.4,"note":"kanıtsız ad/sayı: 2 \u003e 1"},{"name":"answers_question","score":1,"max":2,"weight":0.3,"note":"1/2 beklenen ifade"},{"name":"language_tr","score":0,"max":0,"weight":0.1},{"name":"length_ok","score":0,"max":0,"weight":0.1},{"name":"tool_calls_valid","score":0,"max":0,"weight":0.1}],"total":0.214,"applicable":2}`,
		`{"dims":[{"name":"grounded","score":1,"max":1,"weight":0.4},{"name":"answers_question","score":0,"max":2,"weight":0.3,"note":"yasak ifade: grafana"},{"name":"language_tr","score":0,"max":1,"weight":0.1,"note":"Türkçe değil"},{"name":"length_ok","score":0,"max":0,"weight":0.1},{"name":"tool_calls_valid","score":0,"max":0,"weight":0.1}],"total":0.5,"applicable":3}`,
		`{"dims":[{"name":"grounded","score":1,"max":1,"weight":0.4},{"name":"answers_question","score":2,"max":2,"weight":0.3},{"name":"language_tr","score":0,"max":0,"weight":0.1},{"name":"length_ok","score":0,"max":0,"weight":0.1},{"name":"tool_calls_valid","score":0,"max":0,"weight":0.1}],"total":1,"applicable":2}`,
		`{"dims":[{"name":"grounded","score":1,"max":1,"weight":0.4},{"name":"answers_question","score":2,"max":2,"weight":0.3},{"name":"language_tr","score":1,"max":1,"weight":0.1},{"name":"length_ok","score":0,"max":1,"weight":0.1,"note":"27 \u003e 5 karakter"},{"name":"tool_calls_valid","score":0,"max":1,"weight":0.1,"note":"geçersiz tool çağrısı"}],"total":0.8,"applicable":5}`,
		`{"dims":[{"name":"grounded","score":1,"max":1,"weight":0.4},{"name":"answers_question","score":2,"max":2,"weight":0.3},{"name":"language_tr","score":1,"max":1,"weight":0.1},{"name":"length_ok","score":0,"max":0,"weight":0.1},{"name":"tool_calls_valid","score":1,"max":1,"weight":0.1}],"total":1,"applicable":4}`,
		`{"dims":[{"name":"grounded","score":0,"max":1,"weight":0.4},{"name":"answers_question","score":0,"max":2,"weight":0.3},{"name":"language_tr","score":0,"max":1,"weight":0.1},{"name":"length_ok","score":0,"max":1,"weight":0.1},{"name":"tool_calls_valid","score":0,"max":1,"weight":0.1}],"total":0,"applicable":5}`,
	}
	tr, fa := true, false
	for i, in := range v1Inputs(&tr, &fa) {
		b, _ := json.Marshal(Score(in))
		if string(b) != want[i] {
			t.Errorf("v1 girdi %d değişti:\n got %s\nwant %s", i, b, want[i])
		}
	}
}

func TestStreamContractDim(t *testing.T) {
	tr, fa := true, false
	base := Input{Answer: "servis için bir cevap ve sonuç", ExpectTurkish: true, ToolCallsValid: &tr}
	ok, bad := base, base
	ok.StreamContract = &tr
	bad.StreamContract, bad.StreamContractNote = &fa, "data-links: title var, kart kuralı tutmadı"
	ro, rb := Score(ok), Score(bad)
	if d := dim(ro, "stream_contract"); d.Max != 1 || d.Score != 1 || d.Weight != 0.10 {
		t.Fatalf("geçerli akış: %+v", d)
	}
	if d := dim(rb, "stream_contract"); d.Score != 0 || d.Note == "" {
		t.Fatalf("ihlal: %+v", d)
	}
	if ro.Total != 1 || ro.Applicable != 5 {
		t.Fatalf("tam puan: %+v", ro)
	}
	// length n/a: uygulanabilir ağırlık 0,40+0,30+0,10(dil)+0,10(araç)+0,10(akış)
	// = 1,00 → ihlal 0,10 düşürür. Uzunluk da uygulanabilirken 0,10/1,10.
	if want := math.Round((1-0.10/1.00)*1000) / 1000; rb.Total != want {
		t.Fatalf("ihlal toplamı %v, want %v", rb.Total, want)
	}
	full, fullBad := ok, bad
	full.MaxChars, fullBad.MaxChars = 500, 500
	if got, want := Score(fullBad).Total, math.Round((1-0.10/1.10)*1000)/1000; Score(full).Total != 1 || got != want {
		t.Fatalf("altı boyut: ihlal %v, want %v", got, want)
	}
	// Hata: uygulanabilir boyut 0 (kayboldu değil).
	e := ok
	e.Err = errors.New("x")
	if d := dim(Score(e), "stream_contract"); d.Max != 1 || d.Score != 0 {
		t.Fatalf("hata: %+v", d)
	}
}

func TestStreamContractWeightOnTop(t *testing.T) {
	if wStream != 0.10 || Threshold != 0.8 {
		t.Fatalf("ağırlık %v eşik %v", wStream, Threshold)
	}
}
