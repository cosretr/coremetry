package chstore

import (
	"testing"
	"time"
)

// v0.10.2341 (S4): sabit kapalı gelir; test kapısı sabiti düzenlemeden açar.
func TestProblemGraphMergeEnforceShipsOff(t *testing.T) {
	if problemGraphMergeEnforce {
		t.Fatal("S4 kapalı gelir: problemGraphMergeEnforce = false (açma ölçütü tasarım §9)")
	}
	if ProblemGraphMergeEnforced() {
		t.Fatal("varsayılan kip kapalı")
	}
	restore := SetProblemGraphMergeEnforceForTest(true)
	if !ProblemGraphMergeEnforced() {
		t.Fatal("test kapısı açmalı")
	}
	restore()
	if ProblemGraphMergeEnforced() {
		t.Fatal("geri alma sabite dönmeli")
	}
}

func mergeStd() ProblemStandard {
	return ProblemStandard{Family: StdFamilyError, Confidence: StdConfidence{Level: ConfidenceMedium}}
}

func TestNotifiableAtMergedStep(t *testing.T) {
	pol := DefaultNotifyPolicy()
	pol.Mode = NotifyModeEnforce
	now := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	m := &MergeHold{Rule: GraphRuleCall, OwnerID: "p-db", RefPriority: "P2"}
	// Merged nil: karar bugünkü (aynı girdiyle iki kez aynı sonuç, adım yok).
	base := NotifiableAt(mergeStd(), "P2", NotifyHistory{}, pol, now)
	if !base.Notifiable || base.Hold != "" {
		t.Fatalf("üye değil: gider (%+v)", base)
	}
	if d := NotifiableAt(mergeStd(), "P2", NotifyHistory{Merged: m}, pol, now); d.Notifiable || d.Hold != NotifyHoldMerged {
		t.Fatalf("sonradan bağlanan P2 üye tutulur: %+v", d)
	}
	if d := NotifiableAt(mergeStd(), "P1", NotifyHistory{Merged: m}, pol, now); !d.Notifiable || d.Bypass != "p1" {
		t.Fatalf("P1 her zaman geçer: %+v", d)
	}
	if d := NotifiableAt(mergeStd(), "P2", NotifyHistory{Merged: &MergeHold{RefPriority: "P3"}}, pol, now); !d.Notifiable || d.Bypass != "priority_up" {
		t.Fatalf("kümenin önceliğini aşan üye geçer: %+v", d)
	}
	if d := NotifiableAt(mergeStd(), "P2", NotifyHistory{Merged: m, HeldPriority: "P3"}, pol, now); !d.Notifiable || d.Bypass != "escalated" {
		t.Fatalf("tutulduktan sonra önceliği yükselen üye geçer: %+v", d)
	}
	// Önceki kapılar önce: P3 özete, merged adımına ulaşmaz.
	if d := NotifiableAt(mergeStd(), "P3", NotifyHistory{Merged: m}, pol, now); d.Hold != NotifyHoldP3Digest {
		t.Fatalf("P3 özet kapısı önce: %+v", d)
	}
	// S3 gölge satırı: merged tutması "daha önceki kapı" sayılmaz.
	if c := FrequentShadowCode(NotifyDecision{Hold: NotifyHoldMerged}, FrequentVerdict{Frequent: true, Kind: FrequentKindHold}); c != FrequentShadowHold {
		t.Fatalf("frequent gölge kodu = %q", c)
	}
}

// v0.10.2342 (S5): sabit kapalı gelir; `frequent` adımı yalnız Frequent dolu iken.
func TestProblemGraphFrequentEnforceShipsOff(t *testing.T) {
	if problemGraphFrequentEnforce || ProblemGraphFrequentEnforced() {
		t.Fatal("S5 kapalı gelir: problemGraphFrequentEnforce = false")
	}
	restore := SetProblemGraphFrequentEnforceForTest(true)
	if !ProblemGraphFrequentEnforced() {
		t.Fatal("test kapısı açmalı")
	}
	restore()
	if ProblemGraphFrequentEnforced() {
		t.Fatal("geri alma sabite dönmeli")
	}
}

func TestNotifiableAtFrequentStep(t *testing.T) {
	pol := DefaultNotifyPolicy()
	pol.Mode = NotifyModeEnforce
	now := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	v := &FrequentVerdict{Frequent: true, Kind: FrequentKindHold, MaxPriority: "P2", Prior24h: 3}
	if d := NotifiableAt(mergeStd(), "P2", NotifyHistory{Frequent: v}, pol, now); d.Notifiable || d.Hold != NotifyHoldFrequent {
		t.Fatalf("sık tekrar eden açılış tutulur: %+v", d)
	}
	if d := NotifiableAt(mergeStd(), "P1", NotifyHistory{Frequent: v}, pol, now); !d.Notifiable || d.Bypass != "priority_up" {
		t.Fatalf("penceredeki en yüksek önceliği aşan geçer: %+v", d)
	}
	if d := NotifiableAt(mergeStd(), "P2", NotifyHistory{Frequent: &FrequentVerdict{Frequent: true, MaxPriority: "P1"}, HeldPriority: "P3"}, pol, now); !d.Notifiable || d.Bypass != "escalated" {
		t.Fatalf("tutulduktan sonra yükselen geçer: %+v", d)
	}
	// Sık değil ya da imza tekrar penceresinde gönderilmiş: adım yok (D3 karar verir).
	if d := NotifiableAt(mergeStd(), "P2", NotifyHistory{Frequent: &FrequentVerdict{}}, pol, now); !d.Notifiable {
		t.Fatalf("sık değil: gider %+v", d)
	}
	last := &NotifyRecord{At: now.Add(-time.Hour).UnixNano(), Priority: "P2"}
	if d := NotifiableAt(mergeStd(), "P2", NotifyHistory{Frequent: v, LastSend: last}, pol, now); d.Hold != NotifyHoldDedupe {
		t.Fatalf("açılış değil: tekrar penceresi karar verir %+v", d)
	}
	if c := FrequentShadowCode(NotifyDecision{Hold: NotifyHoldFrequent}, *v); c != FrequentShadowHold {
		t.Fatalf("S5 tutması S3 gölge satırında 'earlier' sayılmaz: %q", c)
	}
}
