package chstore

// notify_policy_test.go — v0.10.1523 (Problem standardı R1): NotifiableAt tablosu
// (D2/D3/D5/D6/D10), politika blobu doğrulama/normalize ve görünüm önceliği.

import (
	"strings"
	"testing"
	"time"
)

func npStd(family, conf string, fl StdFloorMeasures, ratio float64) ProblemStandard {
	return ProblemStandard{Family: family,
		Impact:     StdImpact{Primary: StdMeasure{Ratio: ratio, Value: ratio}, Known: true, Floor: fl},
		Confidence: StdConfidence{Level: conf, Gates: []SignalGate{{ID: "G1", Status: GatePass}}}}
}

func TestNotifiableAtTable(t *testing.T) {
	now := time.Date(2026, 10, 24, 14, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	pol := DefaultNotifyPolicy()
	quiet := DefaultNotifyPolicy()
	quiet.QuietHours = &NotifyQuietHours{From: "22:00", To: "07:00"}
	night := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)
	p3imm := DefaultNotifyPolicy()
	p3imm.P3 = NotifyP3Immediate
	p3off := DefaultNotifyPolicy()
	p3off.P3 = NotifyP3Off
	lowStd := npStd(StdFamilyError, ConfidenceLow, StdFloorMeasures{}, 5)
	lowStd.Confidence.Gates = []SignalGate{{ID: "G1", Status: GatePass}, {ID: "G2", Status: GateFail}}

	cases := []struct {
		name   string
		std    ProblemStandard
		prio   string
		hist   NotifyHistory
		pol    NotifyPolicy
		at     time.Time
		ok     bool
		hold   string
		bypass string
	}{
		// D6 hata tabanı: sayı YA DA yüzde (yüzde kolu yalnız çağrı tabanı ölçülüyken).
		{"hata: 30 başarısız / 5 dk yeter", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(30), ErrorPct: f64(0.2), Calls5m: f64(15000)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"hata: %1 + ≥30 çağrı yeter (sayı az)", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(3), ErrorPct: f64(10), Calls5m: f64(30)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"hata: iki kol da tutmadı", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(5), ErrorPct: f64(0.5), Calls5m: f64(1000)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactErr, ""},
		{"hata: asla yalnız yüzde (%50 ama 4 çağrı)", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(2), ErrorPct: f64(50), Calls5m: f64(4)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactErr, ""},
		{"hata: yüzde var, trafik bilinmiyor → geçer", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{ErrorPct: f64(0.3)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"hata: sayı az, çağrı bilinmiyor → geçer", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(4), ErrorPct: f64(2)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		// D6 gecikme: +300 ms VE ≥30 çağrı.
		{"gecikme: +300 ms ve 30 çağrı", npStd(StdFamilyLatency, ConfidenceMedium, StdFloorMeasures{P99DeltaMs: f64(300), Calls5m: f64(30)}, 3), "P2", NotifyHistory{}, pol, now, true, "", ""},
		{"gecikme: +250 ms", npStd(StdFamilyLatency, ConfidenceMedium, StdFloorMeasures{P99DeltaMs: f64(250), Calls5m: f64(5000)}, 3), "P2", NotifyHistory{}, pol, now, false, NotifyHoldImpactLat, ""},
		{"gecikme: +2 sn ama 10 çağrı", npStd(StdFamilyLatency, ConfidenceMedium, StdFloorMeasures{P99DeltaMs: f64(2000), Calls5m: f64(10)}, 3), "P2", NotifyHistory{}, pol, now, false, NotifyHoldImpactLat, ""},
		// D6 trafik düşüşü: ≥%40 VE ≥100 taban çağrı; artışa taban yok.
		{"trafik: %60 düşüş, 500 taban", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{DropPct: f64(60), BaselineCalls5m: f64(500)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"trafik: %30 düşüş", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{DropPct: f64(30), BaselineCalls5m: f64(500)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactTrf, ""},
		{"trafik: %90 düşüş ama 50 taban", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{DropPct: f64(90), BaselineCalls5m: f64(50)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactTrf, ""},
		{"trafik artışı tabansız", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		// D2 / D7: P3 → özet (varsayılan), kapalı ya da anında.
		{"P3 özet", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P3", NotifyHistory{}, pol, now, false, NotifyHoldP3Digest, ""},
		{"P3 kapalı", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P3", NotifyHistory{}, p3off, now, false, NotifyHoldP3Off, ""},
		{"P3 anında", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P3", NotifyHistory{}, p3imm, now, true, "", ""},
		// D10: düşük güven görünür ama bildirilmez — tutma kodu kapıyı söyler.
		{"low güven", lowStd, "P1", NotifyHistory{}, pol, now, false, NotifyHoldGatePrefix + "G2", ""},
		// D3 / D5: "problem değil" penceresi baypaslı. v0.10.1531 (R3): pencere
		// noiseMuteHours (168 sa) — 13 sa'lik işaret artık hâlâ susturur.
		{"işaret 1 sa önce", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{Noise: &NoiseMark{At: ago(time.Hour)}}, pol, now, false, NotifyHoldNoise, ""},
		{"işaret 13 sa önce → hâlâ susar (R3: 7 gün)", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{Noise: &NoiseMark{At: ago(13 * time.Hour)}}, pol, now, false, NotifyHoldNoise, ""},
		{"işaret 8 gün önce → bildirir (zaman sınırlı)", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{Noise: &NoiseMark{At: ago(8 * 24 * time.Hour)}}, pol, now, true, "", ""},
		{"işaret gelecekte (replay) sayılmaz", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{Noise: &NoiseMark{At: now.Add(time.Hour).UnixNano()}}, pol, now, true, "", ""},
		{"işaret + öncelik yükseldi", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{Noise: &NoiseMark{At: ago(time.Hour), Priority: "P2"}}, pol, now, true, "", "priority_up"},
		{"kalıcı gizleme (yönetici)", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 9), "P1", NotifyHistory{Noise: &NoiseMark{At: ago(30 * 24 * time.Hour), Permanent: true}}, pol, now, false, NotifyHoldHidden, ""},
		// D3 tekrar penceresi ve üç baypas.
		{"tekrar 2 sa önce", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(2 * time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, false, NotifyHoldDedupe, ""},
		{"tekrar 12 sa sonra açık", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(12 * time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, true, "", ""},
		{"tekrar + öncelik P2→P1", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, true, "", "priority_up"},
		{"tekrar + etki ≥ 2×", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 6), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, true, "", "impact_2x"},
		{"tekrar + etki 1,9× (baypas yok)", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 5.7), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, false, NotifyHoldDedupe, ""},
		{"tekrar + yüksek güvenli P1", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P1", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P1", Magnitude: 3}}, pol, now, true, "", "high_conf_p1"},
		// Sessiz saat yalnız P2.
		{"sessiz saatte P2", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{}, quiet, night, false, NotifyHoldQuiet, ""},
		{"sessiz saatte P1", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{}, quiet, night, true, "", ""},
		{"sessiz saat dışında P2", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{}, quiet, now, true, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := NotifiableAt(c.std, c.prio, c.hist, c.pol, c.at)
			if d.Notifiable != c.ok || d.Hold != c.hold || d.Bypass != c.bypass {
				t.Fatalf("got notifiable=%v hold=%q bypass=%q; want %v %q %q", d.Notifiable, d.Hold, d.Bypass, c.ok, c.hold, c.bypass)
			}
			if d.Notifiable && d.Hold != "" {
				t.Fatal("bildirilebilir kararın tutma kodu olamaz")
			}
		})
	}
}

func TestNotifyPolicyDefaultsAndValidation(t *testing.T) {
	d := DefaultNotifyPolicy()
	if d.Mode != NotifyModeShadow || d.P3 != NotifyP3Digest || strings.Join(d.DigestTimes, ",") != "09:00,16:00" || d.NoiseDedupeHours != 12 {
		t.Fatalf("varsayılan: %+v", d)
	}
	f := d.ImpactFloors
	if f.ErrorFailed5m != 30 || f.ErrorPct != 1 || f.MinCalls5m != 30 || f.LatencyP99DeltaMs != 300 || f.TrafficDropPct != 40 || f.TrafficMinBaselineCalls5m != 100 {
		t.Fatalf("D6 tabanları: %+v", f)
	}
	if err := ValidateNotifyPolicy(d); err != nil {
		t.Fatalf("varsayılan geçersiz: %v", err)
	}
	if CurrentNotifyPolicy().Mode != NotifyModeShadow {
		t.Fatal("hidrate edilmemiş süreç shadow görmeli")
	}
	bad := []func(*NotifyPolicy){
		func(p *NotifyPolicy) { p.Mode = "on" },
		func(p *NotifyPolicy) { p.P3 = "mail" },
		func(p *NotifyPolicy) { p.DigestTimes = []string{"25:00"} },
		func(p *NotifyPolicy) { p.DigestTimes = []string{"1", "2", "3", "4", "5", "6", "7"} },
		func(p *NotifyPolicy) { p.QuietHours = &NotifyQuietHours{From: "22:00", To: "x"} },
		func(p *NotifyPolicy) { p.NoiseDedupeHours = 0 },
		func(p *NotifyPolicy) { p.NoiseDedupeHours = 1000 },
		func(p *NotifyPolicy) { p.ImpactFloors.ErrorPct = 101 },
		func(p *NotifyPolicy) { p.ImpactFloors.TrafficDropPct = -1 },
	}
	for i, mut := range bad {
		p := DefaultNotifyPolicy()
		mut(&p)
		if ValidateNotifyPolicy(p) == nil {
			t.Errorf("bozuk girdi %d kabul edildi: %+v", i, p)
		}
	}
	// decode: eksik alan varsayılanda; bozuk değer normalize'da varsayılana döner.
	p, err := decodeNotifyPolicy([]byte(`{"mode":"enforce","noiseDedupeHours":0,"digestTimes":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != NotifyModeEnforce || p.P3 != NotifyP3Digest || p.NoiseDedupeHours != 12 || p.DigestTimes == nil || len(p.DigestTimes) != 0 {
		t.Fatalf("decode: %+v", p)
	}
	if p.ImpactFloors != d.ImpactFloors {
		t.Fatalf("tabanlar korunmadı: %+v", p.ImpactFloors)
	}
	if _, err := decodeNotifyPolicy([]byte(`{`)); err == nil {
		t.Fatal("bozuk JSON hata vermeli")
	}
	if p, err := decodeNotifyPolicy(nil); err != nil || p.Mode != NotifyModeShadow {
		t.Fatalf("boş blob: %+v %v", p, err)
	}
}

func TestLoadNotifyPolicyKeepsLastGood(t *testing.T) {
	t.Cleanup(func() { notifyPolicyCfg.Store(nil) })
	on := DefaultNotifyPolicy()
	on.Mode = NotifyModeOff
	LoadNotifyPolicyWith(func() (NotifyPolicy, error) { return on, nil })
	if CurrentNotifyPolicy().Mode != NotifyModeOff {
		t.Fatal("yayınlanmadı")
	}
	LoadNotifyPolicyWith(func() (NotifyPolicy, error) { return NotifyPolicy{}, errTestRead })
	if CurrentNotifyPolicy().Mode != NotifyModeOff {
		t.Fatal("okuma hatası son iyi değeri ezdi")
	}
}

type testReadErr struct{}

func (testReadErr) Error() string { return "ch down" }

var errTestRead = testReadErr{}

// TestNotifyViewPriority — D2: bildirim önceliği = /inbox görünüm önceliği.
func TestNotifyViewPriority(t *testing.T) {
	keep := DefaultInboxKeepSourcePriority()
	cases := []struct {
		p    Problem
		want string
	}{
		{Problem{RuleID: "anomaly:orders-api:error_rate", Priority: "P1"}, "P1"},
		{Problem{RuleID: "anomaly:orders-api:p99_ms", Priority: "P1"}, "P3"},
		{Problem{RuleID: "svc-slowdown:crm", Priority: "P1"}, "P1"},
		// v0.10.2056 — ML anomali grubu kaynak önceliğini korur; diğer
		// anomaly-auto kimlikleri P3 (inbox ile aynı).
		{Problem{RuleID: "anomaly-auto:group:ev-1", Priority: "P2"}, "P2"},
		{Problem{RuleID: "anomaly-auto:group:ev-1", Priority: "P1"}, "P1"},
		{Problem{RuleID: "anomaly-auto:subject:orders-api", Priority: "P2"}, "P3"},
		{Problem{RuleID: "anomaly-auto:ev-2", Priority: "P1"}, "P3"},
		{Problem{RuleID: "db-health:oracle@h/d", Priority: "P2"}, "P2"},
		{Problem{RuleID: "user-rule", Priority: "P1"}, "P3"},
		{Problem{RuleID: "builtin-http-p99-5s", Priority: "P2"}, "P2"},
		{Problem{RuleID: "exception-group:new", Priority: "P2"}, "P2"},
		{Problem{RuleID: "incident:i1", Kind: NotifyKindIncident, Severity: "critical", Priority: "P1"}, "P1"},
		{Problem{RuleID: "incident:i2", Kind: NotifyKindIncident, Severity: "warning", Priority: "P2"}, "P3"},
		{Problem{RuleID: "anomaly:a:error_rate", Priority: "P3"}, "P3"},
		{Problem{RuleID: "anomaly:a:error_rate"}, "P3"},
	}
	for _, c := range cases {
		if got := NotifyViewPriority(c.p, keep); got != c.want {
			t.Errorf("%s/%s → %s, istenen %s", c.p.RuleID, c.p.Priority, got, c.want)
		}
	}
	if got := NotifyViewPriority(Problem{RuleID: "anomaly:a:error_rate", Priority: "P1"}, []string{}); got != "P3" {
		t.Errorf("boş liste: %s", got)
	}
}

// v0.10.1531 (Problem standardı R3, D5) — "problem değil" susturması zaman
// sınırlı (noiseMuteHours, varsayılan 7 gün) + baypaslı; kalıcı yalnız yönetici.
func TestNoiseSilencesR3(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	pol := DefaultNotifyPolicy()
	at := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	hi := ProblemStandard{Confidence: StdConfidence{Level: ConfidenceHigh}, Impact: StdImpact{Primary: StdMeasure{Ratio: 3}}}
	cases := []struct {
		name string
		m    *NoiseMark
		std  ProblemStandard
		prio string
		want bool
	}{
		{"işaret yok", nil, hi, "P2", false},
		{"taze işaret, aynı öncelik", &NoiseMark{At: at(time.Hour), Priority: "P2", Magnitude: 3}, hi, "P2", true},
		{"6 gün: hâlâ pencere içinde", &NoiseMark{At: at(6 * 24 * time.Hour), Priority: "P2", Magnitude: 3}, hi, "P2", true},
		{"8 gün: pencere bitti", &NoiseMark{At: at(8 * 24 * time.Hour), Priority: "P2", Magnitude: 3}, hi, "P2", false},
		{"kalıcı: 90 gün sonra da", &NoiseMark{At: at(90 * 24 * time.Hour), Permanent: true}, hi, "P1", true},
		{"öncelik yükseldi → baypas", &NoiseMark{At: at(time.Hour), Priority: "P3", Magnitude: 3}, ProblemStandard{Impact: StdImpact{Primary: StdMeasure{Ratio: 3}}}, "P2", false},
		{"etki 2× → baypas", &NoiseMark{At: at(time.Hour), Priority: "P2", Magnitude: 1.4}, hi, "P2", false},
		{"yüksek güvenli P1 → baypas", &NoiseMark{At: at(time.Hour), Priority: "P1", Magnitude: 3}, hi, "P1", false},
		{"gelecekteki işaret (saat kayması) susturmaz", &NoiseMark{At: now.Add(time.Hour).UnixNano()}, hi, "P2", false},
	}
	for _, c := range cases {
		if got := NoiseSilences(c.m, c.std, c.prio, pol, now); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
	// noiseMuteHours vidası: 24 sa'e indirilince 2 günlük işaret susturmaz.
	pol.NoiseMuteHours = 24
	if NoiseSilences(&NoiseMark{At: at(48 * time.Hour), Priority: "P2", Magnitude: 3}, hi, "P2", pol, now) {
		t.Error("noiseMuteHours=24: 48 sa'lik işaret susturmamalı")
	}
}

func TestNoiseMuteHoursValidateNormalize(t *testing.T) {
	p := DefaultNotifyPolicy()
	if p.NoiseMuteHours != 168 {
		t.Fatalf("varsayılan 168 sa: %v", p.NoiseMuteHours)
	}
	p.NoiseMuteHours = 0 // R3 öncesi istemci alanı göndermez
	if err := ValidateNotifyPolicy(p); err != nil {
		t.Fatalf("0 = varsayılan kabul edilmeli: %v", err)
	}
	if NormalizeNotifyPolicy(p).NoiseMuteHours != 168 {
		t.Fatal("0 → 168'e normalize")
	}
	p.NoiseMuteHours = 721
	if ValidateNotifyPolicy(p) == nil {
		t.Fatal("721 sa reddedilmeli (30 gün tavan)")
	}
}

func TestNoiseMarkOf(t *testing.T) {
	if NoiseMarkOf(nil) != nil || NoiseMarkOf(&ProblemVerdict{Verdict: ProblemVerdictReal}) != nil {
		t.Fatal("gerçek karar / nil işaret üretmez")
	}
	m := NoiseMarkOf(&ProblemVerdict{Verdict: ProblemVerdictNoise, At: 5, Permanent: true, Priority: "P2", Magnitude: 3})
	if m == nil || m.At != 5 || !m.Permanent || m.Priority != "P2" || m.Magnitude != 3 {
		t.Fatalf("işaret alanları taşınmalı: %+v", m)
	}
}

// v0.10.1533 (Problem standardı R5, D7) — P3 özet slotu: bugünün geçmiş en yeni
// slotu, son gönderilen değilse ve ≤ 2 sa geride kaldıysa.
func TestDigestSlotDue(t *testing.T) {
	times := []string{"16:00", "09:00"}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 25, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name string
		now  time.Time
		last string
		slot string
		due  bool
	}{
		{"ilk slottan önce", at(8, 59), "", "", false},
		{"09:00 tam", at(9, 0), "", "2026-10-25 09:00", true},
		{"09:30, gönderilmemiş", at(9, 30), "2026-10-24 16:00", "2026-10-25 09:00", true},
		{"09:30, gönderilmiş", at(9, 30), "2026-10-25 09:00", "2026-10-25 09:00", false},
		{"11:30 — 2 sa'ten fazla geride, kovalanmaz", at(11, 30), "2026-10-24 16:00", "2026-10-25 09:00", false},
		{"16:05", at(16, 5), "2026-10-25 09:00", "2026-10-25 16:00", true},
		{"23:00", at(23, 0), "2026-10-25 09:00", "2026-10-25 16:00", false},
	}
	for _, c := range cases {
		slot, due := DigestSlotDue(times, c.last, c.now)
		if slot != c.slot || due != c.due {
			t.Errorf("%s: %q %v, beklenen %q %v", c.name, slot, due, c.slot, c.due)
		}
	}
	if _, due := DigestSlotDue(nil, "", at(9, 0)); due {
		t.Error("slot yoksa özet yok")
	}
}

func TestDigestItemFromRow(t *testing.T) {
	q := `{"signature":"p:r|s","headline":"x"}`
	if _, ok := digestItemFromRow("nd:p:r|s", "", "pending", NotifyDigestPage, q); !ok {
		t.Fatal("geçerli sistem satırı")
	}
	if _, ok := digestItemFromRow("random", "", "pending", NotifyDigestPage, q); ok {
		t.Fatal("kimlik imzadan türemeli")
	}
	if _, ok := digestItemFromRow("nd:p:r|s", "user1", "pending", NotifyDigestPage, q); ok {
		t.Fatal("kişisel satır kuyruk sayılmaz")
	}
	if _, ok := digestItemFromRow("nd:p:r|s", "", "", NotifyDigestPage, q); ok {
		t.Fatal("mezar taşı bekleyen sayılmaz")
	}
}
