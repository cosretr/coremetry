package chstore

// problem_standard_test.go — v0.10.1523 (Problem standardı R1). Türkçe manşet /
// etki altın tablosu (tür başına) + güven pinleri.

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var stdNow = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)

func stdP(id, rule, svc, metric string, v, thr float64) Problem {
	return Problem{ID: id, RuleID: rule, Service: svc, Metric: metric, Value: v, Threshold: thr,
		Severity: "critical", Status: "open", StartedAt: stdNow.Add(-20 * time.Minute).UnixNano()}
}

// TestStandardizeProblemGolden — her türün Türkçe manşeti, aile ve etki
// metni birebir.
func TestStandardizeProblemGolden(t *testing.T) {
	ramp := stdP("p-ramp", "anomaly:bff-web:error_rate", "bff-web", "error_rate", 3.2, 0.4)
	ramp.Description = "Teknik hata (5xx) oranı sürekli yükseldi on bff-web — current 3.20% vs baseline 0.40% (eşik 0.80%, 2.5 hata/sn, 6 kova ≈ 30 dk boyunca eşiğin üstünde). " + REDEvidenceVM + " · HTTP sunucu histogramı."
	zarm := stdP("p-z", "anomaly:orders-api:error_rate", "orders-api", "error_rate", 4.5, 0.5)
	zarm.Description = "Error rate up on orders-api — current 4.50% vs baseline 0.50% (7.1σ, sustained 3 buckets). " + REDEvidenceSpan + "."
	rrDown := stdP("p-rr", "anomaly:checkout:request_rate", "checkout", "request_rate", 10, 40)
	rrDown.Comparator = "<"
	rrUp := stdP("p-rr2", "anomaly:checkout:request_rate", "checkout", "request_rate", 120, 40)
	rrUp.Comparator = ">"
	builtin := stdP("p-b", "builtin-http-p99-5s", "svc-beta", "http_p99_ms", 6100, 5000)
	builtin.RuleName = "HTTP P99 latency >5s (5 min)"
	user := stdP("p-u", "user-checkout-errors", "checkout", "error_rate", 12, 5)
	user.RuleName = "Checkout errors"
	slow := stdP("svc-slowdown:crm-svc", "svc-slowdown:crm-svc", "crm-svc", "p99_ms", 15000, 120)
	slow.RuleName = "Yaygın yavaşlama"
	collapse := stdP("svc-slowdown:crm-svc", "svc-slowdown:crm-svc", "crm-svc", "p99_ms", 6000, 900)
	collapse.RuleName = "Yaygın yavaşlama · trafik çöküşü"
	dbErr := stdP("db-health:oracle@db-host-1/orders", "db-health:oracle@db-host-1/orders", "db:oracle@orders", DBHealthMetricErrorPct, 12.5, 5)
	dbErr.Kind = ProblemKindDB
	dbCnt := stdP("db-health:oracle@db-host-1/orders", "db-health:oracle@db-host-1/orders", "db:oracle@orders", DBHealthMetricErrorCount, 120, 50)
	dbCnt.Kind = ProblemKindDB
	capP := stdP("cap", "db-capacity:oracle-tablespace", "db:oracle@orders", "db.capacity", 92, 90)
	capP.Kind = ProblemKindDB
	stmt := stdP("st", "db-slow-stmt", "db:oracle@orders", "db.statement_p95_ms", 2400, 1000)
	gc := stdP("gc", "runtime:jvm-gc", "pay-svc", "runtime.jvm_gc", 450, 300)
	gcShare := stdP("gcs", "runtime:jvm-gc-share", "pay-svc", "runtime.jvm_gc_share", 23.5, 15)
	mon := stdP("m", "monitor:m1", "login-page", "uptime", 0, 1)
	mon.RuleName = "Monitor: login-page"
	self := stdP("s", "self-ingest-stall", "", "self.ingest", 0, 1)
	storm := stdP("st", "exception-storm", "", "exception_storm", 9, 5)
	shared := stdP("sh", "exception:shared-dependency:1729", "", "exception.shared_dependency", 4, 3)
	fatal := stdP("f", "exception:fatal-infrastructure", "orders-api", "exception.fatal_infrastructure", 1, 1)
	ora := Problem{ID: "exception-group:ora:abc123", RuleID: "exception-group:new", Service: "loan-svc",
		RuleName: "Teknik hata · ORA-00060 · UPDATE ORDERS", Severity: "critical", Status: "open", Priority: "P1", Metric: "exception",
		Description: "ORA-00060 — 6200 occurrence · P1 olayı. UPDATE ORDERS"}
	http5 := Problem{ID: "exception-group:f00d", RuleID: "exception-group:new", Service: "bff-web",
		RuleName: "HTTP hatası · 503", Severity: "warning", Status: "open", Priority: "P2", Metric: "exception"}
	http4 := Problem{ID: "exception-group:f00e", RuleID: "exception-group:new", Service: "bff-web",
		RuleName: "HTTP hatası · 404", Severity: "warning", Status: "open", Priority: "P2", Metric: "exception"}
	exc := Problem{ID: "exception-group:beef", RuleID: "exception-group:regressed", Service: "orders-api",
		RuleName: "Exception · NullPointerException", Severity: "warning", Status: "open", Priority: "P2", Metric: "exception"}
	inc := Problem{ID: "i1", RuleID: "incident:i1", Kind: NotifyKindIncident, RuleName: "Incident · Ödeme hatası", Severity: "critical", Status: "open"}
	slo := stdP("slo", "slo:slo-a:critical", "svc-checkout", "burn_rate", 20, 14.4)
	ext := stdP("e", "anomaly:ext:oracle-prod/OP1/E1:ext:error_count", "ext:oracle-prod/OP1/E1", "ext:error_count", 40, 5)
	extDown := stdP("ed", "anomaly:ext-down:oracle-prod", "oracle-prod", "ext.down", 3, 3)
	extCap := stdP("ec", "anomaly:ext-cap:ext:extsrc:ext:fail_count", "ext:extsrc", "ext:fail_count", 25, 20)
	cluster := stdP("cl", "anomaly-cluster:shop-db", "shop-db", "cluster", 4, 3)
	auto := stdP("au", "anomaly-auto:abc", "cart-svc", "p99_ms", 900, 300)
	// v0.10.1875 — ML olayının terfi kopyası: manşet = kartın özeti (bildirim konusu).
	mlAuto := stdP("ml", "anomaly-auto:e9", "orders-api", "anomaly_ratio", 9.4, 2)
	mlAuto.RuleName, mlAuto.Description = MLPromotedRuleNamePrefix+"_error", "orders-api: hata logları normalin yaklaşık 9 katı, 13 dakikadır (14:31'den beri)."
	silent := stdP("si", "anomaly:cart-svc:service_silent", "cart-svc", "service_silent", 0, 1)
	p99 := stdP("p99", "anomaly:payments:p99_ms", "payments", "p99_ms", 1250, 300)

	cases := []struct {
		name           string
		p              Problem
		family, kind   string
		headline, impc string
	}{
		{"error_rate z kolu", zarm, StdFamilyError, "anomaly.error_rate", "orders-api: hata oranı arttı — %4,5, normal %0,5", "hata oranı %4,5 (normal %0,5)"},
		{"error_rate rampa", ramp, StdFamilyError, "anomaly.error_rate.ramp", "bff-web: teknik hata (5xx) sürekli artıyor — %3,2, normal %0,4", "hata oranı %3,2 (normal %0,4)"},
		{"p99 artış", p99, StdFamilyLatency, "anomaly.p99_ms", "payments: yanıt süresi uzadı — p99 1,25 sn, normal 300 ms", "p99 1,25 sn (normal 300 ms)"},
		{"trafik düşüşü", rrDown, StdFamilyTraffic, "anomaly.request_rate", "checkout: trafik düştü — %75 azaldı (10/sn)", "trafik 10/sn (normal 40/sn)"},
		{"trafik artışı", rrUp, StdFamilyTraffic, "anomaly.request_rate", "checkout: trafik arttı — 120/sn, normal 40/sn", "trafik 120/sn (normal 40/sn)"},
		{"servis sustu", silent, StdFamilyAvailability, "anomaly.service_silent", "cart-svc: servis sustu — son kovalarda hiç istek yok", "değer 0 (eşik 1)"},
		{"anomali kümesi", cluster, StdFamilyCluster, "anomaly_cluster", "shop-db: yayılan sorun — 4 sinyal birlikte", "4 sinyal birlikte"},
		{"terfi anomali", auto, StdFamilyLatency, "anomaly_auto", "cart-svc: yanıt süresi olağandışı — normalin 3×", "değer 900 ms (eşik 300 ms)"},
		{"ML terfi (özet)", mlAuto, StdFamilyRule, "anomaly_auto", "orders-api: hata logları normalin yaklaşık 9 katı, 13 dakikadır (14:31'den beri).", "değer 9,4 (eşik 2)"},
		{"dış seri", ext, StdFamilyExternal, "anomaly.ext", "ext:oracle-prod/OP1/E1: dış kaynak hata sayısı arttı — 40, normal 5", "değer 40 (eşik 5)"},
		{"dış kaynak düştü", extDown, StdFamilyExternal, "anomaly.ext_down", "oracle-prod: dış kaynak erişilemiyor — ardışık sorgu hatası", "değer 3 (eşik 3)"},
		{"dış tavan özeti", extCap, StdFamilyExternal, "anomaly.ext_cap", "ext:extsrc: dış kaynakta çok sayıda eşzamanlı anomali (özet)", "değer 25 (eşik 20)"},
		{"yerleşik kural", builtin, StdFamilyLatency, "rule.builtin", "svc-beta: kural ihlali — HTTP P99 latency >5s (5 min)", "değer 6,1 sn (eşik 5 sn)"},
		{"kullanıcı kuralı", user, StdFamilyError, "rule.user", "checkout: kural ihlali — Checkout errors", "değer %12 (eşik %5)"},
		{"yaygın yavaşlama", slow, StdFamilyLatency, "svc_slowdown", "crm-svc: yaygın yavaşlama — p99 15 sn, taban 120 ms", "p99 15 sn (taban 120 ms)"},
		{"trafik çöküşü", collapse, StdFamilyTraffic, "svc_slowdown.collapse", "crm-svc: trafik çöktü ve yavaşladı — p99 6 sn", "p99 6 sn (taban 900 ms)"},
		{"db hata oranı", dbErr, StdFamilyDB, "db_health", "oracle@db-host-1/orders: veritabanı hata oranı yüksek — %12,5", "hata oranı %12,5 (eşik %5)"},
		{"db hata sayısı", dbCnt, StdFamilyDB, "db_health", "oracle@db-host-1/orders: veritabanı hataları arttı — 5 dk'da 120 hata", "120 hata / 5 dk (eşik 50)"},
		{"db kapasite", capP, StdFamilyDB, "db_capacity", "db:oracle@orders: kapasite %92 dolu (eşik %90)", "değer %92 (eşik %90)"},
		{"yavaş ifade", stmt, StdFamilyDB, "db_slow_stmt", "db:oracle@orders: yavaş SQL ifadesi — p95 2,4 sn", "değer 2,4 sn (eşik 1 sn)"},
		{"jvm gc", gc, StdFamilyRuntime, "runtime.gc", "pay-svc: JVM GC duraklaması uzun — ort. 450 ms", StdImpactUnverified},
		{"jvm gc payı", gcShare, StdFamilyRuntime, "runtime.gc", "pay-svc: JVM zamanının %23,5'i GC'de", StdImpactUnverified},
		{"sentetik kontrol", mon, StdFamilyAvailability, "monitor", "login-page: sentetik kontrol başarısız", "sentetik kontrol erişemiyor"},
		{"self-health", self, StdFamilySelf, "self", "Coremetry: veri alımı durdu", "Coremetry iç sağlığı"},
		{"exception fırtınası", storm, StdFamilyException, "exception_storm", "Exception fırtınası — 9 serviste yeni hata", "9 serviste aynı anda"},
		{"paylaşılan bağımlılık", shared, StdFamilyException, "exception_shared", "paylaşılan bağımlılık hatası — 4 serviste aynı anda", "4 serviste aynı anda"},
		{"ölümcül", fatal, StdFamilyException, "exception_fatal", "orders-api: ölümcül altyapı hatası", "1 oluşum"},
		{"oracle grubu (D13)", ora, StdFamilyException, "exception_group.oracle", "loan-svc: Oracle hata · ORA-00060 · UPDATE ORDERS", "6200 oluşum"},
		{"5xx grubu = teknik hata", http5, StdFamilyException, "exception_group.http", "bff-web: Teknik hata · HTTP 503", "hata grubu"},
		{"4xx grubu", http4, StdFamilyException, "exception_group.http", "bff-web: HTTP hatası · 404", "hata grubu"},
		{"exception grubu", exc, StdFamilyException, "exception_group", "orders-api: Exception · NullPointerException", "hata grubu"},
		{"incident", inc, StdFamilyIncident, "incident", "Olay: Ödeme hatası", "olay ilan edildi"},
		{"slo", slo, StdFamilyRule, "slo", "svc-checkout: SLO hata bütçesi hızla tükeniyor — 1,39×", "değer 20 (eşik 14,4)"},
	}
	if len(cases) < 20 {
		t.Fatalf("altın tablo ≥ 20 satır olmalı, %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			std := StandardizeProblem(c.p, StdFacts{Now: stdNow, BigBreachRatio: 2})
			if std.Family != c.family || std.Kind != c.kind {
				t.Errorf("aile/tür = %s/%s, istenen %s/%s", std.Family, std.Kind, c.family, c.kind)
			}
			if std.Headline != c.headline {
				t.Errorf("manşet\n got %q\nwant %q", std.Headline, c.headline)
			}
			if std.Impact.Primary.Text != c.impc {
				t.Errorf("etki\n got %q\nwant %q", std.Impact.Primary.Text, c.impc)
			}
			if utf8.RuneCountInString(std.Headline) > StdHeadlineMax {
				t.Errorf("manşet tavanı aşıldı: %d", utf8.RuneCountInString(std.Headline))
			}
			if len(std.Confidence.Gates) != 7 {
				t.Errorf("kapı sayısı %d, istenen 7", len(std.Confidence.Gates))
			}
			if std.NextStep.Text == "" {
				t.Error("sonraki adım boş")
			}
		})
	}
}

func TestStandardizeHeadlineClipped(t *testing.T) {
	p := stdP("x", "user-long", "orders-api", "custom", 1, 2)
	p.RuleName = strings.Repeat("çok uzun kural adı ", 10)
	h := StandardizeProblem(p, StdFacts{Now: stdNow}).Headline
	if utf8.RuneCountInString(h) != StdHeadlineMax || !strings.HasSuffix(h, "…") {
		t.Fatalf("kırpma: %d rune, %q", utf8.RuneCountInString(h), h)
	}
}

// TestNotifiableBFFRamp1518 — v0.10.1518 olayı: BFF'te 5xx rampası (VM
// kaynaklı, 2,5 hata/sn ≈ 750 / 5 dk). Bildirilebilir VE yüksek güvenli olmalı.
func TestNotifiableBFFRamp1518(t *testing.T) {
	p := stdP("p-ramp", "anomaly:bff-web:error_rate", "bff-web", "error_rate", 3.2, 0.4)
	p.Description = "Teknik hata (5xx) oranı sürekli yükseldi on bff-web — current 3.20% vs baseline 0.40% (eşik 0.80%, 2.5 hata/sn, 6 kova ≈ 30 dk boyunca eşiğin üstünde). " + REDEvidenceVM + " · HTTP sunucu histogramı."
	p.Priority = "P1"
	std := StandardizeProblem(p, StdFacts{Now: stdNow, BigBreachRatio: 2})
	if std.Evidence.Source != StdSourceVM || std.Evidence.Sampled {
		t.Fatalf("kaynak = %s sampled=%v, istenen vm örneklenmemiş", std.Evidence.Source, std.Evidence.Sampled)
	}
	if std.Confidence.Level != ConfidenceHigh {
		t.Fatalf("güven = %s, istenen high; kapılar %+v", std.Confidence.Level, std.Confidence.Gates)
	}
	if std.Impact.FailedPerMin != 150 || std.Impact.Floor.Failed5m == nil || *std.Impact.Floor.Failed5m != 750 {
		t.Fatalf("etki: dk başına %v, 5 dk %v", std.Impact.FailedPerMin, std.Impact.Floor.Failed5m)
	}
	prio := NotifyViewPriority(p, DefaultInboxKeepSourcePriority())
	if prio != "P1" {
		t.Fatalf("görünüm önceliği %s, istenen P1 (anomaly:*:error_rate istisna listesinde)", prio)
	}
	d := NotifiableAt(std, prio, NotifyHistory{}, DefaultNotifyPolicy(), stdNow)
	if !d.Notifiable {
		t.Fatalf("rampa bildirilebilir olmalı, tutma %q", d.Hold)
	}
	// Bir saat önce "problem değil" işareti olsa bile yüksek güvenli P1 baypas eder (D3).
	d = NotifiableAt(std, prio, NotifyHistory{Noise: &NoiseMark{At: stdNow.Add(-time.Hour).UnixNano()}}, DefaultNotifyPolicy(), stdNow)
	if !d.Notifiable || d.Bypass != "high_conf_p1" {
		t.Fatalf("işaretli rampa: notifiable=%v bypass=%q hold=%q", d.Notifiable, d.Bypass, d.Hold)
	}
}

// TestRuntimeGCImpactUnverifiedIsMedium — GC sinyali kullanıcı etkisini
// kanıtlamaz: büyük ihlal + örneklenmemiş kaynak olsa da en çok medium;
// aynı serviste açık hata/p99 problemi varsa etki doğrulanmış sayılır.
func TestRuntimeGCImpactUnverifiedIsMedium(t *testing.T) {
	p := stdP("gc", "runtime:jvm-gc", "pay-svc", "runtime.jvm_gc", 2400, 500)
	std := StandardizeProblem(p, StdFacts{Now: stdNow, BigBreachRatio: 2})
	if std.Impact.Known || std.Impact.Primary.Text != StdImpactUnverified {
		t.Fatalf("etki = %+v", std.Impact)
	}
	if std.Confidence.Level != ConfidenceMedium {
		t.Fatalf("güven = %s, istenen medium", std.Confidence.Level)
	}
	std = StandardizeProblem(p, StdFacts{Now: stdNow, BigBreachRatio: 2, SameServiceErrOrP99Open: true})
	if !std.Impact.Known || std.Confidence.Level != ConfidenceHigh {
		t.Fatalf("aynı serviste açık problem varken: known=%v güven=%s", std.Impact.Known, std.Confidence.Level)
	}
}

// TestUnknownGateNeverLowers — ölçülmüş "pass" yerine "unknown" (gerçek
// okunamadı) güveni düşürmez ve bildirimi tutmaz.
func TestUnknownGateNeverLowers(t *testing.T) {
	base := stdP("p", "anomaly:orders-api:error_rate", "orders-api", "error_rate", 6, 1)
	base.Description = "x " + REDEvidenceVM + "."
	base.Priority = "P1"
	known := StandardizeProblem(base, StdFacts{Now: stdNow, BigBreachRatio: 2,
		Traffic: &StdTraffic{Calls5m: 5000, Errors5m: 300}})
	unknown := StandardizeProblem(base, StdFacts{Now: stdNow, BigBreachRatio: 2})
	if g := unknown.Confidence.Gates[0]; g.ID != "G1" || g.Status != GateUnknown {
		t.Fatalf("G1 = %+v, istenen unknown", g)
	}
	if ConfidenceRank(unknown.Confidence.Level) < ConfidenceRank(known.Confidence.Level) {
		t.Fatalf("unknown güveni düşürdü: %s < %s", unknown.Confidence.Level, known.Confidence.Level)
	}
	pol := DefaultNotifyPolicy()
	if a, b := NotifiableAt(known, "P1", NotifyHistory{}, pol, stdNow), NotifiableAt(unknown, "P1", NotifyHistory{}, pol, stdNow); a.Notifiable && !b.Notifiable {
		t.Fatalf("unknown bildirimi tuttu: %q", b.Hold)
	}
	// Kapı düzeyinde: her kapıda pass → unknown değişimi seviyeyi düşürmez.
	gates := ComputeSignalGates(GateInput{Family: StdFamilyError, Traffic: true, VMDecides: true, Dwell: GatePass,
		Source: StdSourceVM, Floor: StdFloorMeasures{Calls5m: f64(500)}})
	in := ConfidenceInput{Ratio: 6, BigBreach: 2}
	ref := ConfidenceFromGates(gates, in)
	for i := range gates {
		if gates[i].Status != GatePass {
			continue
		}
		mod := append([]SignalGate(nil), gates...)
		mod[i].Status = GateUnknown
		if got := ConfidenceFromGates(mod, in); ConfidenceRank(got) < ConfidenceRank(ref) {
			t.Errorf("%s unknown → %s (önce %s)", gates[i].ID, got, ref)
		}
	}
	// Ölçülmüş başarısızlık ise düşürür (karşı örnek — kapı gerçekten çalışıyor).
	failed := StandardizeProblem(base, StdFacts{Now: stdNow, BigBreachRatio: 2, Traffic: &StdTraffic{Calls5m: 3, Errors5m: 1}})
	if failed.Confidence.Level != ConfidenceLow {
		t.Fatalf("3 çağrılık kova: güven %s, istenen low", failed.Confidence.Level)
	}
}

// TestConfidenceIgnoresVotesAndVerdicts — güven yalnız kapılardan: kararlar
// (gerçek / problem değil) NotifiableAt'ın sonucunu değiştirebilir ama güveni
// DEĞİŞTİREMEZ; çekirdek dosyalar oy / karar / LLM tiplerini hiç içe almaz.
func TestConfidenceIgnoresVotesAndVerdicts(t *testing.T) {
	p := stdP("p", "anomaly:orders-api:error_rate", "orders-api", "error_rate", 6, 1)
	p.Description = "x " + REDEvidenceVM + "."
	std := StandardizeProblem(p, StdFacts{Now: stdNow, BigBreachRatio: 2})
	pol := DefaultNotifyPolicy()
	ref := NotifiableAt(std, "P1", NotifyHistory{}, pol, stdNow).Confidence
	for _, h := range []NotifyHistory{
		{Noise: &NoiseMark{At: stdNow.Add(-time.Minute).UnixNano()}},
		{Noise: &NoiseMark{At: stdNow.Add(-time.Minute).UnixNano(), Permanent: true}},
		{LastSend: &NotifyRecord{At: stdNow.Add(-time.Minute).UnixNano(), Priority: "P1", Magnitude: 6}},
	} {
		if got := NotifiableAt(std, "P1", h, pol, stdNow).Confidence; got != ref {
			t.Errorf("geçmiş %+v güveni değiştirdi: %s → %s", h, ref, got)
		}
	}
	for _, f := range []string{"signal_gates.go", "problem_standard.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		code := stripGoComments(string(src))
		for _, bad := range []string{"ProblemVerdict", "AIFeedback", "Feedback", "Vote", "vote", "copilot", "LLM"} {
			if strings.Contains(code, bad) {
				t.Errorf("%s güven çekirdeğinde %q kullanıyor", f, bad)
			}
		}
	}
}

func TestParseEvidenceSource(t *testing.T) {
	cases := []struct {
		desc string
		src  string
		hold bool
	}{
		{"x " + REDEvidenceVM + " · HTTP sunucu histogramı.", StdSourceVM, false},
		{"x " + REDEvidenceSpan + " — VM kullanılmadı: seri yok.", StdSourceSpan, false},
		{"x " + REDEvidenceVMHold + "ısınma.", StdSourceVM, true},
		{"düz metin", "", false},
	}
	for _, c := range cases {
		s, h := ParseEvidenceSource(c.desc)
		if s != c.src || h != c.hold {
			t.Errorf("%q → (%s,%v), istenen (%s,%v)", c.desc, s, h, c.src, c.hold)
		}
	}
	// VM beklemede = G3 ölçülmüş başarısızlık → low.
	p := stdP("p", "anomaly:a:error_rate", "a", "error_rate", 5, 1)
	p.Description = "x " + REDEvidenceVMHold + "ısınma."
	if std := StandardizeProblem(p, StdFacts{Now: stdNow}); std.Confidence.Level != ConfidenceLow {
		t.Fatalf("VM beklemede: %s", std.Confidence.Level)
	}
}

func TestStandardizeSampledCappedAtMedium(t *testing.T) {
	// D12: örneklenmiş span-yalnız sinyal en çok medium, büyüklük ne olursa olsun.
	p := stdP("p", "anomaly:a:error_rate", "a", "error_rate", 50, 1)
	p.Description = "x " + REDEvidenceSpan + "."
	if std := StandardizeProblem(p, StdFacts{Now: stdNow, BigBreachRatio: 2}); std.Confidence.Level != ConfidenceMedium {
		t.Fatalf("span kaynaklı: %s", std.Confidence.Level)
	}
	// Takvim günü (G6) yüksek güveni bir basamak indirir, bastırmaz.
	q := p
	q.Description = "x " + REDEvidenceVM + "."
	std := StandardizeProblem(q, StdFacts{Now: stdNow, BigBreachRatio: 2, CalendarKind: CalendarMonthEnd})
	if std.Confidence.Level != ConfidenceMedium {
		t.Fatalf("ay sonu: %s", std.Confidence.Level)
	}
	if d := NotifiableAt(std, "P1", NotifyHistory{}, DefaultNotifyPolicy(), stdNow); !d.Notifiable {
		t.Fatalf("takvim bastırdı: %q", d.Hold)
	}
}

func TestStandardizeExceptionEventIncident(t *testing.T) {
	g := ExceptionGroup{Fingerprint: "ora:abc", Type: "ORA-00060", Message: "UPDATE ORDERS", Service: "loan-svc",
		State: ExStateNew, FirstSeen: stdNow.Add(-time.Hour).UnixNano(), LastSeen: stdNow.UnixNano(), Occurrences: 1200, Priority: "P1"}
	std := StandardizeException(g, StdFacts{Now: stdNow})
	if std.Headline != "loan-svc: Oracle hata · ORA-00060 · UPDATE ORDERS" || std.Evidence.Source != StdSourceOracle {
		t.Fatalf("oracle: %q %s", std.Headline, std.Evidence.Source)
	}
	if std.Confidence.Level != ConfidenceHigh {
		t.Fatalf("oracle P1 güveni %s", std.Confidence.Level)
	}
	h := StandardizeException(ExceptionGroup{Fingerprint: "f1", Type: "503", Service: "bff-web", Occurrences: 40, State: ExStateRegressed}, StdFacts{Now: stdNow})
	if h.Headline != "bff-web: Teknik hata · HTTP 503 (geri döndü)" || h.Kind != "exception_group.http" {
		t.Fatalf("503: %q %s", h.Headline, h.Kind)
	}
	low := StandardizeException(ExceptionGroup{Fingerprint: "f2", Type: "NPE", Service: "a", Occurrences: 2}, StdFacts{Now: stdNow})
	if low.Confidence.Level != ConfidenceLow {
		t.Fatalf("2 oluşum: %s", low.Confidence.Level)
	}
	ev := StandardizeAnomalyEvent(AnomalyEvent{ID: "a1", Kind: "trace_op_latency", Pattern: "GET /orders", Service: "orders-api",
		StartedAt: stdNow.Add(-10 * time.Minute).UnixNano(), LastSeen: stdNow.UnixNano(), PeakRatio: 4.2, Status: "active"}, StdFacts{Now: stdNow})
	if ev.Headline != "orders-api: operasyon yavaşladı — GET /orders (4,2×)" || ev.Family != StdFamilyLatency || ev.Since.DurationSec != 600 {
		t.Fatalf("olay: %q %s %d", ev.Headline, ev.Family, ev.Since.DurationSec)
	}
	in := StandardizeIncident(Incident{ID: "i 1", Title: "Ödeme hatası", Severity: "critical", Status: "open",
		StartedAt: stdNow.Add(-time.Hour).UnixNano()}, StdFacts{Now: stdNow, IncidentProblems: 3})
	if in.Headline != "Olay: Ödeme hatası" || in.Impact.Primary.Text != "3 bağlı problem" || in.NextStep.Href != "/incident?id=i+1" {
		t.Fatalf("incident: %+v", in)
	}
}

func TestTrNumber(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{{0.4, "0,4"}, {3.2, "3,2"}, {1.25, "1,25"}, {12.5, "12,5"}, {120, "120"}, {75, "75"}, {math.Inf(1), "—"}} {
		if got := trNum(c.v); got != c.want {
			t.Errorf("trNum(%v) = %q, istenen %q", c.v, got, c.want)
		}
	}
	if trMs(300) != "300 ms" || trMs(15000) != "15 sn" || trMs(1250) != "1,25 sn" {
		t.Errorf("trMs: %q %q %q", trMs(300), trMs(15000), trMs(1250))
	}
}
