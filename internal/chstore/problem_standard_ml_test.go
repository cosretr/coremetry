package chstore

// problem_standard_ml_test.go — v0.10.2383 (triage mockup dilim 2). SÖZLEŞME:
// ML servis düzeyi olayının sahip olduğu Problem'de standart DOĞRUYU söyler —
//
//   - kaynak, canlı yolun açıklamaya yazdığı işaretten okunur (VM örneklenmemiş
//     / log kayıtları); işaretsiz ESKİ Problem bugünkü çıktısını bayt bayt korur;
//   - güven YENİ kural olmadan, mevcut kapılardan o doğru kaynakla çıkar;
//   - bildirim politikası kararı DEĞİŞMEZ (PolicyLevel; etki testte açık);
//   - "Neden" etki kuralı olayının gerçeklerini satır satır söyler (TR + anahtar);
//   - manşet "normalde X, şimdi Y (N kat)" (taban 0 → kat yok).
//
// Saat ve dilim bağımsız: zaman sabit unix an, yerel saat okunmaz.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

var mlStdNow = time.Unix(1_790_000_000, 0)

// mlSvcProblem — `anomaly-auto:svc:orders` satırı (ML oran birimi 0–1; eşik
// %0,5 tabanlı — svc_shadow.go svcValueThreshold).
func mlSvcProblem(desc string) Problem {
	return Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", RuleName: MLPromotedRuleNamePrefix + "servis anomalisi",
		Severity: "critical", Service: "orders", Metric: "error_rate", Comparator: ">=", Value: 0.068, Threshold: 0.005, Status: "open",
		Description: desc, StartedAt: mlStdNow.Add(-14 * time.Minute).UnixNano()}
}

const (
	mlOldDesc  = "orders servisinde hata oranı %6,8, olağanı %0,4. Dakikada 210 başarısız, 2.890 başarılı istek (olağanı 12 başarısız, 3.090 başarılı)."
	mlSplitTxt = " Dakikada 210 başarısız, 2.890 başarılı istek (olağanı 12 başarısız, 3.090 başarılı)."
	mlNewHead  = "orders servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat)."
)

func mlNewDesc() string { return mlNewHead + mlSplitTxt + " " + REDEvidenceVM + "." }

func mlRule() *StdImpactRule {
	return &StdImpactRule{Window: 5, Violations: 5, MinViolations: 3, Threshold: 0.014, Floor: 0.01, Rel: 1.5, Abs: 0.01,
		LoadPerMin: 3100, MinLoad: 10, Unsampled: true}
}

func gateStatus(s ProblemStandard, id string) string {
	for _, g := range s.Confidence.Gates {
		if g.ID == id {
			return g.Status
		}
	}
	return ""
}

// TestMLOldProblemUnchanged — işaretsiz eski Problem: v0.10.2381 çıktısı bayt
// bayt (gövde v0.10.2381 kodundan alındı). Etki kuralı gerçekleri verilmezse
// "Neden" alanı JSON'a hiç çıkmaz.
func TestMLOldProblemUnchanged(t *testing.T) {
	const want = `{"family":"error","kind":"anomaly_auto","subject":"orders","headline":"orders servisinde hata oranı %6,8, olağanı %0,4.","impact":{"primary":{"text":"Dakikada 210 başarısız · 2.890 başarılı (isteklerin %6,8'i)","value":0.068,"normal":0.005,"unit":"%","ratio":13.600000000000001,"msg":{"k":"imp.reqSplit","a":{"f":"210","o":"2,890","share":"6.8%"}}},"known":true,"floor":{},"requests":{"failedPerMin":210,"okPerMin":2890,"normalFailedPerMin":12,"normalOkPerMin":3090}},"since":{"startedAt":1789999160000000000,"durationSec":840,"ongoing":true},"evidence":{"sentence":"Dedektör sürdürme koşulunu geçti; ölçü span'lerden (örneklenmiş).","msg":{"k":"ev.sentence","a":{"baseK":"ev.base.dwell","srcK":"ev.src.span"}},"source":"span","sampled":true},"confidence":{"level":"medium","gates":[{"id":"G1","name":"trafik","status":"na"},{"id":"G2","name":"sürdürme","status":"pass","note":"dedektörün sürdürme kuralı","noteMsg":{"k":"gate.dwellPass"}},{"id":"G3","name":"VM karar","status":"na"},{"id":"G4","name":"topoloji","status":"na"},{"id":"G5","name":"deploy","status":"na"},{"id":"G6","name":"takvim","status":"na"},{"id":"G7","name":"batch","status":"na"}]},"nextStep":{"text":"Servisin hata kırılımına bak","href":"/service?name=orders","msg":{"k":"next.errBreakdown"}},"notify":{"notifiable":false}}`
	std := StandardizeProblem(mlSvcProblem(mlOldDesc), StdFacts{Now: mlStdNow, BigBreachRatio: 2})
	got, err := json.Marshal(std)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("işaretsiz eski Problem'in standardı değişti:\n%s\n%s", got, want)
	}
	if std.Confidence.PolicyLevel != "" {
		t.Fatalf("eski Problem'de politika seviyesi ayrışmamalı: %q", std.Confidence.PolicyLevel)
	}
}

// TestMLSvcProblemTruth — işaret + kanıt: kaynak, güven, kapılar, "Neden",
// manşet (Türkçe metin + metin anahtarı).
func TestMLSvcProblemTruth(t *testing.T) {
	f := StdFacts{Now: mlStdNow, BigBreachRatio: 2, ImpactRule: mlRule()}
	std := StandardizeProblem(mlSvcProblem(mlNewDesc()), f)

	if std.Evidence.Source != StdSourceVM || std.Evidence.Sampled {
		t.Fatalf("kaynak = %s örneklenmiş=%v, istenen vm / örneklenmemiş", std.Evidence.Source, std.Evidence.Sampled)
	}
	if std.Evidence.Sentence != "Dedektör sürdürme koşulunu geçti; ölçü VictoriaMetrics'ten (örneklenmemiş)." {
		t.Errorf("kanıt cümlesi: %q", std.Evidence.Sentence)
	}
	// Kapılar: yalnız G3 değişir (uygulanmaz → geçti); diğerleri eskiyle aynı.
	old := StandardizeProblem(mlSvcProblem(mlOldDesc), StdFacts{Now: mlStdNow, BigBreachRatio: 2})
	for i, g := range std.Confidence.Gates {
		o := old.Confidence.Gates[i]
		if g.ID == "G3" {
			if o.Status != GateNA || g.Status != GatePass {
				t.Errorf("G3: %s → %s, istenen na → pass", o.Status, g.Status)
			}
			continue
		}
		if !reflect.DeepEqual(g, o) {
			t.Errorf("%s değişmemeli: %+v → %+v", g.ID, o, g)
		}
	}
	if old.Confidence.Level != ConfidenceMedium || std.Confidence.Level != ConfidenceHigh {
		t.Fatalf("güven %s → %s, istenen medium → high", old.Confidence.Level, std.Confidence.Level)
	}

	if std.Headline != mlNewHead {
		t.Errorf("manşet: %q", std.Headline)
	}
	wantHead := &StdMsg{K: "h.mlErr", A: map[string]any{"svc": "orders", "n": "0.4%", "v": "6.8%", "x": "17"}}
	if !reflect.DeepEqual(std.HeadlineMsg, wantHead) {
		t.Errorf("manşet anahtarı: %+v", std.HeadlineMsg)
	}

	wantLines := []string{
		"Son 5 dakikanın 5'inde eşik aşıldı (en az 3 gerekir)",
		"Eşik %1,4: en az %1, normalin 1,5 katı ve 1 puan üstü",
		"Yük yeterli: dakikada yaklaşık 3.100 istek (en az 10 gerekir)",
		"Kaynak örneklenmemiş sunucu metriği",
	}
	if !reflect.DeepEqual(std.Evidence.Reasons, wantLines) {
		t.Fatalf("neden satırları:\n%q\n%q", std.Evidence.Reasons, wantLines)
	}
	wantMsgs := []StdMsg{
		{K: "why.viol", A: map[string]any{"n": "5", "w": "5", "min": "3"}},
		{K: "why.threshold", A: map[string]any{"t": "1.4%", "floor": "1%", "rel": "1.5", "abs": "1"}},
		{K: "why.load", A: map[string]any{"load": "3,100", "min": "10"}},
		{K: "why.srcVM"},
	}
	if !reflect.DeepEqual(std.Evidence.ReasonMsgs, wantMsgs) {
		t.Fatalf("neden anahtarları:\n%+v\n%+v", std.Evidence.ReasonMsgs, wantMsgs)
	}

	// Öncelik / büyüklük / etki ölçüleri bu dilimde değişmez.
	if !reflect.DeepEqual(std.Impact, old.Impact) || StdMagnitude(std) != StdMagnitude(old) {
		t.Errorf("etki değişmemeli:\n%+v\n%+v", std.Impact, old.Impact)
	}
}

// TestMLSvcProblemEvidenceMissing — kanıt yok (eski olay / başka dedektör):
// bugünkü tek cümle kalır, satır üretilmez; kaynak yine işaretten.
func TestMLSvcProblemEvidenceMissing(t *testing.T) {
	std := StandardizeProblem(mlSvcProblem(mlNewDesc()), StdFacts{Now: mlStdNow, BigBreachRatio: 2})
	if std.Evidence.Reasons != nil || std.Evidence.ReasonMsgs != nil {
		t.Fatalf("kanıt yokken satır üretildi: %q", std.Evidence.Reasons)
	}
	if std.Evidence.Sentence != "Dedektör sürdürme koşulunu geçti; ölçü VictoriaMetrics'ten (örneklenmemiş)." || std.Evidence.Source != StdSourceVM {
		t.Errorf("cümle / kaynak: %q %s", std.Evidence.Sentence, std.Evidence.Source)
	}
	// Etki kuralı gerçekleri başka aileye verilse de kullanılmaz.
	other := StandardizeProblem(stdP("p", "anomaly:orders:error_rate", "orders", "error_rate", 6, 1), StdFacts{Now: mlStdNow, ImpactRule: mlRule()})
	if other.Evidence.Reasons != nil {
		t.Errorf("ML dışı ailede neden satırı: %q", other.Evidence.Reasons)
	}
}

// TestImpactRuleReasons — satırların dalları.
func TestImpactRuleReasons(t *testing.T) {
	texts := func(r StdImpactRule, vm bool) []string {
		out, msgs := impactRuleReasons(r, vm)
		if len(out) != len(msgs) {
			t.Fatalf("satır %d, anahtar %d", len(out), len(msgs))
		}
		return out
	}
	t.Run("ihlal eşiğin altında (olay açık, düzeliyor)", func(t *testing.T) {
		r := *mlRule()
		r.Violations = 2
		got := texts(r, true)
		if got[0] != "Son 5 dakikanın 2'sinde eşik aşıldı (açılış için en az 3 gerekir; ihlal sürdüğü için açık)" {
			t.Errorf("%q", got[0])
		}
		_, msgs := impactRuleReasons(r, true)
		if msgs[0].K != "why.violOpen" {
			t.Errorf("anahtar %q", msgs[0].K)
		}
	})
	t.Run("iyelik eki sayının okunuşundan", func(t *testing.T) {
		for n, want := range map[int]string{1: "1'inde", 2: "2'sinde", 3: "3'ünde", 4: "4'ünde", 5: "5'inde", 6: "6'sında", 9: "9'unda", 10: "10'unda"} {
			if got := trCountLoc(n); got != want {
				t.Errorf("%d → %q, istenen %q", n, got, want)
			}
		}
	})
	t.Run("kural sabiti bilinmiyor → eşik tek başına", func(t *testing.T) {
		r := *mlRule()
		r.Floor, r.Rel, r.Abs = 0, 0, 0
		if got := texts(r, true); got[1] != "Eşik %1,4" {
			t.Errorf("%q", got[1])
		}
	})
	t.Run("yük bilinmiyor / kaynak işaretsiz → o satırlar yok", func(t *testing.T) {
		r := *mlRule()
		r.LoadPerMin = 0
		if got := texts(r, false); len(got) != 2 {
			t.Errorf("%q", got)
		}
		r = *mlRule()
		r.Unsampled = false
		if got := texts(r, true); len(got) != 3 {
			t.Errorf("%q", got)
		}
		// Kanıt örneklenmemiş dese de Problem'in kaynağı VM değilse (eski,
		// işaretsiz satır) kaynak satırı yazılmaz: kart ile çelişmesin.
		if got := texts(*mlRule(), false); len(got) != 3 {
			t.Errorf("%q", got)
		}
	})
	t.Run("geçersiz gerçek → satır yok", func(t *testing.T) {
		for _, r := range []StdImpactRule{{}, {Window: 5, Violations: 0, MinViolations: 3, Threshold: 0.014}, {Window: 5, Violations: 6, MinViolations: 3, Threshold: 0.014},
			{Window: 5, Violations: 3, MinViolations: 3}} {
			if got, _ := impactRuleReasons(r, true); got != nil {
				t.Errorf("%+v → %q", r, got)
			}
		}
	})
}

// TestMLErrRateSentence — manşet biçimleri + anahtarın geri okunması.
func TestMLErrRateSentence(t *testing.T) {
	cases := []struct {
		name      string
		obs, base float64
		want      string
		key       string
		args      map[string]any
	}{
		{"kat tam sayı", 0.0683, 0.0041, "orders servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat).", "h.mlErr",
			map[string]any{"svc": "orders", "n": "0.4%", "v": "6.8%", "x": "17"}},
		{"kat tek ondalık", 0.034, 0.021, "orders servisinde hata oranı normalde %2,1, şimdi %3,4 (1,6 kat).", "h.mlErr",
			map[string]any{"svc": "orders", "n": "2.1%", "v": "3.4%", "x": "1.6"}},
		{"taban 0 → kat yok", 0.068, 0, "orders servisinde hata oranı normalde neredeyse hiç yok, şimdi %6,8.", "h.mlErrZero",
			map[string]any{"svc": "orders", "v": "6.8%"}},
		{"taban yuvarlanınca 0 → hiç yok", 0.068, 0.00001, "orders servisinde hata oranı normalde neredeyse hiç yok, şimdi %6,8.", "h.mlErrZero",
			map[string]any{"svc": "orders", "v": "6.8%"}},
		{"küçük taban iki ondalık, binlik ayırıcı", 0.9, 0.0005, "orders servisinde hata oranı normalde %0,05, şimdi %90 (1.800 kat).", "h.mlErr",
			map[string]any{"svc": "orders", "n": "0.05%", "v": "90%", "x": "1,800"}},
		{"gözlenen tabanın altında → kat yok", 0.015, 0.02, "orders servisinde hata oranı normalde %2, şimdi %1,5.", "h.mlErrFlat",
			map[string]any{"svc": "orders", "n": "2%", "v": "1.5%"}},
		{"bozuk taban → hiç yok", 0.068, -1, "orders servisinde hata oranı normalde neredeyse hiç yok, şimdi %6,8.", "h.mlErrZero",
			map[string]any{"svc": "orders", "v": "6.8%"}},
	}
	for _, c := range cases {
		got := MLErrRateSentence("orders", c.obs, c.base)
		if got != c.want {
			t.Errorf("%s:\n%q\n%q", c.name, got, c.want)
			continue
		}
		m := mlErrRateMsg(got)
		if m == nil || m.K != c.key || !reflect.DeepEqual(m.A, c.args) {
			t.Errorf("%s: anahtar %+v", c.name, m)
		}
	}
	// Tek cümle değilse (ikinci cümle / kapanış eki) anahtar yok: serbest metin.
	for _, h := range []string{"", mlNewHead + " orders servisinde 3 operasyon birden yavaşladı.", mlNewHead + " · kendiliğinden kapandı: servis anomalisi bitti",
		"orders servisinde hata oranı %6,8, olağanı %0,4."} {
		if m := mlErrRateMsg(h); m != nil {
			t.Errorf("%q → %+v", h, m)
		}
	}
}

// TestMLHeadlineStripsMarkers — manşet açıklamanın cümlesidir: istek bölümü ve
// kaynak işareti manşete girmez; kapanış eki eskisi gibi kalır.
func TestMLHeadlineStripsMarkers(t *testing.T) {
	for desc, want := range map[string]string{
		mlNewDesc(): mlNewHead,
		mlNewHead + " orders servisinde 3 operasyon birden yavaşladı. " + REDEvidenceVM + ".":   mlNewHead + " orders servisinde 3 operasyon birden yavaşladı.",
		"orders: aynı anda 2 farklı sinyalde anomali var (log ve trace). " + EvidenceLogs + ".": "orders: aynı anda 2 farklı sinyalde anomali var (log ve trace).",
		mlNewHead + " " + REDEvidenceVM + ". · kendiliğinden kapandı: servis anomalisi bitti":   mlNewHead + " · kendiliğinden kapandı: servis anomalisi bitti",
		"orders: özet cümlesi": "orders: özet cümlesi",
	} {
		h, ok := MLPromotedHeadline(mlSvcProblem(desc))
		if !ok || h != want {
			t.Errorf("%q →\n%q\n%q", desc, h, want)
		}
	}
	zero := StandardizeProblem(mlSvcProblem(MLErrRateSentence("orders", 0.068, 0)+" "+REDEvidenceVM+"."), StdFacts{Now: mlStdNow})
	if zero.Headline != "orders servisinde hata oranı normalde neredeyse hiç yok, şimdi %6,8." || zero.HeadlineMsg == nil || zero.HeadlineMsg.K != "h.mlErrZero" {
		t.Errorf("taban 0 manşeti: %q %+v", zero.Headline, zero.HeadlineMsg)
	}
}

// TestEvidenceLogsMarker — log işareti mevcut "logs" kaynağına okunur; eski
// işaretler aynen.
func TestEvidenceLogsMarker(t *testing.T) {
	if s, hold := ParseEvidenceSource("x " + EvidenceLogs + "."); s != StdSourceLogs || hold {
		t.Fatalf("log işareti: %q %v", s, hold)
	}
	p := mlSvcProblem("orders: aynı anda 2 farklı sinyalde anomali var (log ve trace). " + EvidenceLogs + ".")
	p.RuleID, p.ID, p.Metric, p.Value, p.Threshold = "anomaly-auto:group:ev1", "anomaly-auto:group:ev1:orders", "ml_group_signals", 2, 2
	std := StandardizeProblem(p, StdFacts{Now: mlStdNow, BigBreachRatio: 2})
	if std.Evidence.Source != StdSourceLogs || std.Evidence.Sampled {
		t.Errorf("kaynak %s örneklenmiş=%v", std.Evidence.Source, std.Evidence.Sampled)
	}
	if std.Evidence.Sentence != "Dedektör sürdürme koşulunu geçti; log kayıtlarından." {
		t.Errorf("cümle %q", std.Evidence.Sentence)
	}
	// Sayım satırı (oran 1) büyük ihlal değil → güven orta; G3 RED ailesi değil → uygulanmaz.
	if std.Confidence.Level != ConfidenceMedium || gateStatus(std, "G3") != GateNA || std.Confidence.PolicyLevel != "" {
		t.Errorf("güven %s G3 %s politika %q", std.Confidence.Level, gateStatus(std, "G3"), std.Confidence.PolicyLevel)
	}
}

// TestMarkerOutsideMLLiveUnchanged — ML canlı ailesi dışındaki anomaly-auto
// satırında (terfi kopyası) işaretin etkisi v0.10.2381 ile aynı: politika
// seviyesi ayrışmaz, G3 uygulanmaz kalır.
func TestMarkerOutsideMLLiveUnchanged(t *testing.T) {
	p := mlSvcProblem("orders: özet cümlesi. " + REDEvidenceVM + ".")
	p.RuleID, p.ID = "anomaly-auto:7f3a", "anomaly-auto:7f3a:orders"
	std := StandardizeProblem(p, StdFacts{Now: mlStdNow, BigBreachRatio: 2})
	if std.Evidence.Source != StdSourceVM || std.Confidence.Level != ConfidenceHigh || std.Confidence.PolicyLevel != "" || gateStatus(std, "G3") != GateNA {
		t.Errorf("kaynak %s güven %s politika %q G3 %s", std.Evidence.Source, std.Confidence.Level, std.Confidence.PolicyLevel, gateStatus(std, "G3"))
	}
}

// TestMLMarkerConfidenceBranches — güven yalnız mevcut kuraldan: büyük ihlal
// değilse (P2 olayı) ya da takvim işaretliyse yüksek olmaz.
func TestMLMarkerConfidenceBranches(t *testing.T) {
	p2 := mlSvcProblem(mlNewDesc())
	p2.Value, p2.Threshold = 0.034, 0.021 // 1,6 kat
	if got := StandardizeProblem(p2, StdFacts{Now: mlStdNow, BigBreachRatio: 2}).Confidence; got.Level != ConfidenceMedium || got.PolicyLevel != "" {
		t.Errorf("1,6 kat: %s / %q", got.Level, got.PolicyLevel)
	}
	hol := StandardizeProblem(mlSvcProblem(mlNewDesc()), StdFacts{Now: mlStdNow, BigBreachRatio: 2, CalendarKind: CalendarHoliday})
	if hol.Confidence.Level != ConfidenceMedium || gateStatus(hol, "G6") != GateFlag {
		t.Errorf("tatil: %s G6 %s", hol.Confidence.Level, gateStatus(hol, "G6"))
	}
}

// TestMLMarkerNotifyPolicyUnchanged — BİLDİRİM ETKİSİ, açıkça: doğru kaynak
// ekrandaki güveni ORTA → YÜKSEK yapar; "yüksek güvenli P1" baypası (D3) bu
// yüzden yeni kararlar üretirdi. v0.10.2383 politikayı eski seviyede tutar
// (PolicyLevel): kararlar işaretsiz Problem'inkiyle AYNI. Alt testin ikinci
// yarısı, tutma kaldırılırsa (operatör kararı) neyin değişeceğini pinler.
func TestMLMarkerNotifyPolicyUnchanged(t *testing.T) {
	pol := DefaultNotifyPolicy()
	if pol.Mode != NotifyModeShadow {
		t.Fatalf("varsayılan kip %q, istenen shadow", pol.Mode)
	}
	before := StandardizeProblem(mlSvcProblem(mlOldDesc), StdFacts{Now: mlStdNow, BigBreachRatio: 2})
	after := StandardizeProblem(mlSvcProblem(mlNewDesc()), StdFacts{Now: mlStdNow, BigBreachRatio: 2})
	if before.Confidence.Level != ConfidenceMedium || after.Confidence.Level != ConfidenceHigh || after.Confidence.PolicyLevel != ConfidenceMedium {
		t.Fatalf("güven %s → %s (politika %q)", before.Confidence.Level, after.Confidence.Level, after.Confidence.PolicyLevel)
	}
	lifted := after
	lifted.Confidence.PolicyLevel = "" // tutma kaldırılırsa

	mag := StdMagnitude(after)
	recent := mlStdNow.Add(-time.Hour).UnixNano()
	cases := []struct {
		name       string
		hist       NotifyHistory
		wantHold   string // bugün (önce = sonra)
		liftBypass string // tutma kaldırılırsa baypas ("" = aynı karar)
	}{
		{"ilk açılış", NotifyHistory{}, "", ""},
		{"12 sa içinde aynı imza gönderilmiş", NotifyHistory{LastSend: &NotifyRecord{At: recent, Priority: "P1", Magnitude: mag}}, NotifyHoldDedupe, "high_conf_p1"},
		{"\"problem değil\" işareti penceresinde", NotifyHistory{Noise: &NoiseMark{At: recent, Priority: "P1", Magnitude: mag}}, NotifyHoldNoise, "high_conf_p1"},
		{"kalıcı gizleme", NotifyHistory{Noise: &NoiseMark{At: recent, Permanent: true}}, NotifyHoldHidden, ""},
	}
	for _, c := range cases {
		b := NotifiableAt(before, "P1", c.hist, pol, mlStdNow)
		a := NotifiableAt(after, "P1", c.hist, pol, mlStdNow)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: karar değişti:\nönce  %+v\nsonra %+v", c.name, b, a)
		}
		if a.Hold != c.wantHold || a.Notifiable != (c.wantHold == "") || a.Confidence != ConfidenceMedium {
			t.Errorf("%s: karar %+v", c.name, a)
		}
		l := NotifiableAt(lifted, "P1", c.hist, pol, mlStdNow)
		if c.liftBypass == "" {
			if l.Hold != a.Hold || l.Notifiable != a.Notifiable {
				t.Errorf("%s: tutma kalkınca karar değişmemeli: %+v", c.name, l)
			}
			continue
		}
		if !l.Notifiable || l.Bypass != c.liftBypass || l.Confidence != ConfidenceHigh {
			t.Errorf("%s: tutma kalkınca beklenen baypas %q: %+v", c.name, c.liftBypass, l)
		}
	}
	// Canlı susturma kapısı (muteNotifications açıkken, notify verdict_silence).
	mark := &NoiseMark{At: recent, Priority: "P1", Magnitude: mag}
	if !NoiseSilences(mark, before, "P1", pol, mlStdNow) || !NoiseSilences(mark, after, "P1", pol, mlStdNow) {
		t.Error("\"problem değil\" işareti ML servis Problem'ini önce de sonra da susturmalı")
	}
	if NoiseSilences(mark, lifted, "P1", pol, mlStdNow) {
		t.Error("tutma kalkınca yüksek güvenli P1 işareti baypas ederdi (pin)")
	}
	// P2 (1,5–2 kat) Problem'inde güven zaten orta: tutma devreye girmez.
	p2 := mlSvcProblem(mlNewDesc())
	p2.Value, p2.Threshold = 0.034, 0.021
	if s := StandardizeProblem(p2, StdFacts{Now: mlStdNow, BigBreachRatio: 2}); s.Confidence.Level != ConfidenceMedium || s.Confidence.PolicyLevel != "" {
		t.Errorf("P2: %+v", s.Confidence)
	}
}

// v0.10.2385 (etki olayı I4) — servis yanıt süresi Problem'i: cümle bir kez
// tanımlanır, manşet açıklamadan okunur, İngilizce arayüz için anahtara geri
// okunur; aile gecikme, kategori yavaşlama (medyan p50_ms, en yavaş %10 p90_ms).
func TestMLResponseTimeSentenceAndHeadline(t *testing.T) {
	cases := []struct {
		name, measure  string
		obs, base      float64
		want, key      string
		wantN, wantV   string
		wantX, metric  string
		wantHeadlineTR bool
	}{
		{"medyan, kat tek ondalık", MLRespMedian, 920, 180, "orders servisinde yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms (5,1 kat).", "h.mlRtMed", "180 ms", "920 ms", "5.1", "p50_ms", true},
		{"en yavaş %10, binlik ayırıcı", MLRespSlow, 2900, 800, "orders servisinde yanıt süresi (en yavaş %10) normalde 800 ms, şimdi 2.900 ms (3,6 kat).", "h.mlRtSlow", "800 ms", "2,900 ms", "3.6", "p90_ms", true},
		{"kat tam sayı", MLRespMedian, 12400, 40, "orders servisinde yanıt süresi (medyan) normalde 40 ms, şimdi 12.400 ms (310 kat).", "h.mlRtMed", "40 ms", "12,400 ms", "310", "p50_ms", true},
		{"gözlenen tabanın altında → kat yok (uzatma tiki)", MLRespMedian, 170, 180, "orders servisinde yanıt süresi (medyan) normalde 180 ms, şimdi 170 ms.", "h.mlRtMedFlat", "180 ms", "170 ms", "", "p50_ms", true},
		{"en yavaş %10, kat yok", MLRespSlow, 800, 800, "orders servisinde yanıt süresi (en yavaş %10) normalde 800 ms, şimdi 800 ms.", "h.mlRtSlowFlat", "800 ms", "800 ms", "", "p90_ms", true},
		{"taban 1 ms'nin altında → 1 ms, kat yok", MLRespMedian, 300, 0.2, "orders servisinde yanıt süresi (medyan) normalde 1 ms, şimdi 300 ms.", "h.mlRtMedFlat", "1 ms", "300 ms", "", "p50_ms", true},
	}
	for _, c := range cases {
		got := MLResponseTimeSentence("orders", c.measure, c.obs, c.base)
		if got != c.want {
			t.Errorf("%s: cümle\n got %q\nwant %q", c.name, got, c.want)
			continue
		}
		p := Problem{RuleID: MLSvcRulePrefix + "orders", RuleName: MLPromotedRuleNamePrefix + "servis anomalisi", Severity: "critical", Service: "orders",
			Metric: c.metric, Comparator: ">=", Value: c.obs, Threshold: c.base, Status: "open", StartedAt: 1,
			Description: got + " " + REDEvidenceVM + "."}
		std := StandardizeProblem(p, StdFacts{Now: mlStdNow})
		if std.Family != StdFamilyLatency || std.Headline != c.want || std.HeadlineMsg == nil || std.HeadlineMsg.K != c.key {
			t.Errorf("%s: standart aile=%s manşet=%q anahtar=%+v", c.name, std.Family, std.Headline, std.HeadlineMsg)
			continue
		}
		a := std.HeadlineMsg.A
		if a["svc"] != "orders" || a["n"] != c.wantN || a["v"] != c.wantV || (c.wantX != "" && a["x"] != c.wantX) || (c.wantX == "" && a["x"] != nil) {
			t.Errorf("%s: argümanlar %+v", c.name, a)
		}
		if std.Evidence.Source != StdSourceVM {
			t.Errorf("%s: kaynak işareti VM okunmalı: %q", c.name, std.Evidence.Source)
		}
		if ProblemCategory(p) != CategorySlowdown {
			t.Errorf("%s: kategori %s", c.name, ProblemCategory(p))
		}
	}
	// serbest metin (ikinci cümle, başka cümle) anahtarsız kalır; hata oranı cümlesi kendi anahtarında
	if mlRespTimeMsg("orders servisinde yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms (5,1 kat). Ek cümle.") != nil ||
		mlRespTimeMsg("orders servisinde yanıt süresi (p99) normalde 180 ms, şimdi 920 ms.") != nil {
		t.Error("tam eşleşmeyen cümle anahtarlanmamalı")
	}
	if m := mlPromotedMsg(MLErrRateSentence("orders", 0.068, 0.004)); m == nil || m.K != "h.mlErr" {
		t.Errorf("hata oranı cümlesi kendi anahtarında kalmalı: %+v", m)
	}
}

// TestImpactRuleReasonsResponseTimeAndLag — v0.10.2391: yanıt süresi ve
// consumer lag etki olaylarının "Neden" satırları (TR metin + EN anahtar /
// argüman). Ölçüsüz gerçek (hata oranı) v0.10.2383 satırlarını bayt bayt korur.
func TestImpactRuleReasonsResponseTimeAndLag(t *testing.T) {
	med := StdImpactRule{Window: 5, Violations: 4, MinViolations: 3, Threshold: 270, Rel: 1.5, Abs: 100, LoadPerMin: 3100, MinLoad: 10,
		Unsampled: true, Measure: MLRespMedian}
	slow := StdImpactRule{Window: 5, Violations: 5, MinViolations: 3, Threshold: 1750, Rel: 2, Abs: 1000, LoadPerMin: 640, MinLoad: 10,
		Unsampled: true, Measure: MLRespSlow}
	slowZero := slow // taban 0: eşik = taban + 1 sn
	slowZero.Threshold = 1000
	lag := StdImpactRule{Window: 5, Violations: 4, MinViolations: 3, Threshold: 1200, Floor: 1000, Rel: 2, Unsampled: true, Measure: MLImpactLag}
	lagZero := lag // taban 0: eşik etki tabanı
	lagZero.Threshold = 1000
	lagOld := lag // kanıttaki eşik bugünkü sabitlerle uyuşmuyor
	lagOld.Floor, lagOld.Rel = 0, 0
	medOpen := med
	medOpen.Violations = 2
	type msg struct {
		k string
		a map[string]any
	}
	cases := []struct {
		name string
		r    StdImpactRule
		vm   bool
		want []string
		msgs []msg
	}{
		{"medyan", med, true, []string{
			"Son 5 dakikanın 4'ünde medyan eşiği aşıldı (en az 3 gerekir)",
			"Eşik 270 ms: normalin 1,5 katı ve 100 ms üstü",
			"Yük yeterli: dakikada yaklaşık 3.100 istek (en az 10 gerekir)",
			"Kaynak örneklenmemiş sunucu metriği",
		}, []msg{
			{"why.violMed", map[string]any{"n": "4", "w": "5", "min": "3"}},
			{"why.thrMs", map[string]any{"tMs": 270.0, "rel": "1.5", "absMs": 100.0}},
			{"why.load", map[string]any{"load": "3,100", "min": "10"}},
			{"why.srcVM", nil},
		}},
		{"en yavaş %10", slow, true, []string{
			"Son 5 dakikanın 5'inde en yavaş %10 eşiği aşıldı (en az 3 gerekir)",
			"Eşik 1,75 sn: normalin 2 katı ve 1 sn üstü",
			"Yük yeterli: dakikada yaklaşık 640 istek (en az 10 gerekir)",
			"Kaynak örneklenmemiş sunucu metriği",
		}, []msg{
			{"why.violSlow", map[string]any{"n": "5", "w": "5", "min": "3"}},
			{"why.thrMs", map[string]any{"tMs": 1750.0, "rel": "2", "absMs": 1000.0}},
			{"why.load", map[string]any{"load": "640", "min": "10"}},
			{"why.srcVM", nil},
		}},
		{"en yavaş %10, taban 0", slowZero, false, []string{
			"Son 5 dakikanın 5'inde en yavaş %10 eşiği aşıldı (en az 3 gerekir)",
			"Eşik 1 sn: normalin 2 katı ve 1 sn üstü",
			"Yük yeterli: dakikada yaklaşık 640 istek (en az 10 gerekir)",
		}, nil},
		{"medyan, açık koşu uzuyor", medOpen, true, []string{
			"Son 5 dakikanın 2'sinde medyan eşiği aşıldı (açılış için en az 3 gerekir; ihlal sürdüğü için açık)",
		}, []msg{{"why.violOpenMed", map[string]any{"n": "2", "w": "5", "min": "3"}}}},
		{"consumer lag", lag, true, []string{
			"Son 5 dakikanın 4'ünde eşik aşıldı (en az 3 gerekir)",
			"Eşik 1.200 kayıt: en az 1.000 kayıt ve normalin 2 katı",
			"Kaynak örneklenmemiş sunucu metriği",
		}, []msg{
			{"why.viol", map[string]any{"n": "4", "w": "5", "min": "3"}},
			{"why.thrLag", map[string]any{"t": "1,200", "floor": "1,000", "rel": "2"}},
			{"why.srcVM", nil},
		}},
		{"consumer lag, taban 0, kaynak işaretsiz", lagZero, false, []string{
			"Son 5 dakikanın 4'ünde eşik aşıldı (en az 3 gerekir)",
			"Eşik 1.000 kayıt: en az 1.000 kayıt ve normalin 2 katı",
		}, nil},
		{"consumer lag, katsayı bilinmiyor", lagOld, false, []string{
			"Son 5 dakikanın 4'ünde eşik aşıldı (en az 3 gerekir)",
			"Eşik 1.200 kayıt",
		}, []msg{{"why.viol", map[string]any{"n": "4", "w": "5", "min": "3"}}, {"why.thrOnlyLag", map[string]any{"t": "1,200"}}}},
	}
	for _, c := range cases {
		got, msgs := impactRuleReasons(c.r, c.vm)
		if len(got) != len(msgs) {
			t.Fatalf("%s: satır %d, anahtar %d", c.name, len(got), len(msgs))
		}
		if c.name == "medyan, açık koşu uzuyor" {
			got, msgs = got[:1], msgs[:1]
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n%q\n%q", c.name, got, c.want)
		}
		for i, m := range c.msgs {
			if msgs[i].K != m.k || (m.a != nil && !reflect.DeepEqual(msgs[i].A, m.a)) {
				t.Errorf("%s: anahtar %d = %+v, istenen %+v", c.name, i, msgs[i], m)
			}
		}
	}
	// Ölçüsüz (hata oranı) gerçek: v0.10.2383 metni aynen.
	got, _ := impactRuleReasons(*mlRule(), true)
	want := []string{"Son 5 dakikanın 5'inde eşik aşıldı (en az 3 gerekir)", "Eşik %1,4: en az %1, normalin 1,5 katı ve 1 puan üstü",
		"Yük yeterli: dakikada yaklaşık 3.100 istek (en az 10 gerekir)", "Kaynak örneklenmemiş sunucu metriği"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hata oranı satırları değişti: %q", got)
	}
}
