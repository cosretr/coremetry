package chstore

// v0.10.1588 — Davis paritesi FAZ 0.5 / D: terfi / fırtına / tekil exception
// incident bağlama bayrakları varsayılan AÇIK (*bool nil), açıkça false
// kapatır, JSON + Normalize gidiş-dönüşünde korunur.

import (
	"encoding/json"
	"testing"
)

func TestAnomalyPromotionAttachDefaultOn(t *testing.T) {
	if !DefaultAnomalyPromotion().AttachesToIncident() || !(AnomalyPromotionConfig{}).AttachesToIncident() {
		t.Fatal("varsayılan / alan yok → açık")
	}
	off := false
	c := NormalizeAnomalyPromotion(AnomalyPromotionConfig{AttachToIncident: &off})
	b, _ := json.Marshal(c)
	var back AnomalyPromotionConfig
	_ = json.Unmarshal(b, &back)
	if back.AttachesToIncident() {
		t.Fatalf("açıkça false korunmalı: %s", b)
	}
}

func TestExceptionTriageAttachFlagsDefaultOn(t *testing.T) {
	d := DefaultExceptionTriage()
	if !d.StormAttachesToIncident() || !d.SingleGroupJoinsIncident() {
		t.Fatal("varsayılan açık")
	}
	var legacy ExceptionTriageConfig
	_ = json.Unmarshal([]byte(`{"p1FreshHours":4,"stormMinServices":5}`), &legacy)
	n := NormalizeExceptionTriage(legacy)
	if !n.StormAttachesToIncident() || !n.SingleGroupJoinsIncident() {
		t.Fatal("eski blob açık okunmalı")
	}
	off := false
	n = NormalizeExceptionTriage(ExceptionTriageConfig{StormAttachToIncident: &off, SingleGroupJoinIncident: &off})
	b, _ := json.Marshal(n)
	var back ExceptionTriageConfig
	_ = json.Unmarshal(b, &back)
	if back.StormAttachesToIncident() || back.SingleGroupJoinsIncident() {
		t.Fatalf("açıkça false korunmalı: %s", b)
	}
}
