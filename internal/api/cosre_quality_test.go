package api

// CoSRE öğrenme Faz 2 — örtük sinyal ucunun girdi sözleşmesi.

import (
	"strings"
	"testing"
)

func TestValidateCosreSignal(t *testing.T) {
	ok := []cosreSignalRequest{
		{ExchangeID: "ab12", Kind: "copy"},
		{ExchangeID: " ab12 ", Kind: "link_click", Detail: "/traces/x"},
		{ExchangeID: "ab12", Kind: "evidence_click"},
		{ExchangeID: "ab12", Kind: "problem_note"},
	}
	for _, r := range ok {
		if _, err := validateCosreSignal(r); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []cosreSignalRequest{
		{ExchangeID: "", Kind: "copy"},
		{ExchangeID: strings.Repeat("a", aiFeedbackMaxIDLen+1), Kind: "copy"},
		{ExchangeID: "ab", Kind: "rephrase"}, // türetilen sinyal istemciden GELEMEZ
		{ExchangeID: "ab", Kind: "thumbs_up"},
	}
	for _, r := range bad {
		if _, err := validateCosreSignal(r); err == nil {
			t.Errorf("%+v kabul edildi", r)
		}
	}
	r, _ := validateCosreSignal(cosreSignalRequest{ExchangeID: "a", Kind: "copy", Detail: strings.Repeat("ş", 600)})
	if len(r.Detail) > cosreSignalDetailMax {
		t.Errorf("detail tavanı: %d", len(r.Detail))
	}
}

func TestCosreQualityDays(t *testing.T) {
	for in, want := range map[string]int{"": 7, "x": 7, "0": 7, "-3": 7, "1": 1, "14": 14, "99": 30} {
		if got := cosreQualityDays(in); got != want {
			t.Errorf("%q → %d", in, got)
		}
	}
}

// v0.10.1597 (CoSRE v2 Faz 1, R7) — kart başına "ilgili / ilgisiz" ve mesaj
// düzeyinde serbest not (operatör kararı §0-2, 👍/👎 yerine). evidence_*
// ayrıntısı kartın adım kimliği s<i> olmak ZORUNDA (sinyal hangi kanıta
// düştü); not 2 KiB'a dek, diğer türler 512 B.
func TestValidateCosreSignalV2Kinds(t *testing.T) {
	ok := []cosreSignalRequest{
		{ExchangeID: "ab", Kind: "evidence_relevant", Detail: "s3"},
		{ExchangeID: "ab", Kind: "evidence_irrelevant", Detail: " s12 "},
		{ExchangeID: "ab", Kind: "note", Detail: "kanıt yanlış servisten"},
		{ExchangeID: "ab", Kind: "note"},
	}
	for _, r := range ok {
		if _, err := validateCosreSignal(r); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []cosreSignalRequest{
		{ExchangeID: "ab", Kind: "evidence_relevant"},
		{ExchangeID: "ab", Kind: "evidence_relevant", Detail: "b1"},
		{ExchangeID: "ab", Kind: "evidence_irrelevant", Detail: "s"},
		{ExchangeID: "ab", Kind: "evidence_irrelevant", Detail: "s12345"},
		{ExchangeID: "ab", Kind: "evidence_relevant", Detail: "s1; DROP"},
		{ExchangeID: "ab", Kind: "relevant"},
	}
	for _, r := range bad {
		if _, err := validateCosreSignal(r); err == nil {
			t.Errorf("%+v kabul edildi", r)
		}
	}
	n, _ := validateCosreSignal(cosreSignalRequest{ExchangeID: "a", Kind: "note", Detail: strings.Repeat("ş", 1500)})
	if len(n.Detail) > cosreNoteDetailMax || len(n.Detail) <= cosreSignalDetailMax {
		t.Errorf("not tavanı: %d (512 < x ≤ %d bekleniyordu)", len(n.Detail), cosreNoteDetailMax)
	}
	e, _ := validateCosreSignal(cosreSignalRequest{ExchangeID: "a", Kind: "evidence_relevant", Detail: " s7 "})
	if e.Detail != "s7" {
		t.Errorf("evidence detail %q", e.Detail)
	}
}
