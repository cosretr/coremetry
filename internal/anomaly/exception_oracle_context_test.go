package anomaly

// exception_oracle_context_test.go — v0.10.1100 (operatör onaylı: Oracle hata
// grubu için AI açıklaması span yerine Oracle bağlamı). Pinler: bağlam
// alanları (kaynak, kod, operasyon, saatlik akış + oran, kanal %, servisler,
// host/instance, trace'ler, örnek satır alanları), SINIRLAR (satır tavanı,
// ≤5 trace, ≤3 örnek, ≤8 kolon), eksik kırılımda zarif düşüş ve otomatik
// özetin kapanmış-dakika gecikmeli aday kuralı.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var oraNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func oraGroup() *chstore.ExceptionGroup {
	return &chstore.ExceptionGroup{
		Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_042", "OP_TRANSFER"),
		Type:        "APP_ERR_042", Message: "OP_TRANSFER", Service: "svc-payments", State: chstore.ExStateNew,
		Occurrences: 12000, FirstSeen: oraNow.Add(-3 * time.Hour).UnixNano(), LastSeen: oraNow.Add(-17 * time.Minute).UnixNano(),
	}
}

func oraFacts() OracleExplainFacts {
	return OracleExplainFacts{
		SourceID: "o-1", SourceName: "core-errlog", Lag: 16 * time.Minute, LastHour: 9000, PrevHour: 2000, BlobOK: true,
		Channels: []OracleNamedCount{{Name: "MOB", Count: 600}, {Name: "WEB", Count: 300}, {Name: "ATM", Count: 60}, {Name: "IVR", Count: 40}},
		Services: []OracleNamedCount{{Name: "svc-payments", Count: 9}, {Name: "svc-ledger", Count: 5}, {Name: "svc-gw", Count: 3}, {Name: "svc-x", Count: 1}},
	}
}

// oraRows — n satır, en yeni önce; trace id'ler i%traces ile döner (tekrar eder).
func oraRows(n, traces int) []chstore.OracleErrorRow {
	out := make([]chstore.OracleErrorRow, 0, n)
	for i := 0; i < n; i++ {
		host := "db-host-a"
		if i%4 == 3 {
			host = "db-host-b"
		}
		r := chstore.OracleErrorRow{
			SourceID: "o-1", Time: oraNow.Add(-17*time.Minute - time.Duration(i)*time.Second),
			SeverityText: "ERROR", Body: "islem reddedildi: limit asimi", TraceID: fmt.Sprintf("%032x", i%traces+1),
			HostName: host, InstanceID: "pod-7", OperationCode: "OP_TRANSFER", ErrorCode: "APP_ERR_042",
			ExternalCode: "EXT-91", ErrorType: "BUSINESS", ChannelCode: "MOB", TaskCode: "T1",
		}
		if i == 0 {
			// 10 eşlenmeyen kolon + ağırlık attribute'u: ≤8 kolon basılır, ağırlık Adet alanına.
			for k := 0; k < 10; k++ {
				r.AttrKeys = append(r.AttrKeys, fmt.Sprintf("COL_%02d", k))
				r.AttrValues = append(r.AttrValues, "v")
			}
			r.AttrKeys = append(r.AttrKeys, "SONUC", chstore.OracleWeightAttr)
			r.AttrValues = append(r.AttrValues, "RED", "4")
		}
		out = append(out, r)
	}
	return out
}

func TestOracleFlow(t *testing.T) {
	cases := []struct {
		known      bool
		last, prev uint64
		want       string
	}{
		{false, 9000, 1, "bilinmiyor"},
		{true, 0, 0, "sessiz"},
		{true, 500, 0, "yeni akış"},
		{true, 9000, 2000, "patlama"},
		{true, 6000, 2000, "patlama"}, // tam 3× sınır dahil (öncelik kuralıyla aynı)
		{true, 5000, 4000, "sürekli akış"},
		{true, 1000, 3000, "sönüyor"},
	}
	for _, c := range cases {
		if got := oracleFlow(c.known, c.last, c.prev); got != c.want {
			t.Errorf("oracleFlow(%v,%d,%d)=%q, beklenen %q", c.known, c.last, c.prev, got, c.want)
		}
	}
}

func TestAssembleOracleExplainContext(t *testing.T) {
	g := oraGroup()
	tf := map[string]chstore.TraceFact{fmt.Sprintf("%032x", 1): {Service: "svc-ledger", ExType: "java.sql.SQLException"}}
	cases := []struct {
		name    string
		facts   OracleExplainFacts
		factsOK bool
		rows    []chstore.OracleErrorRow
		check   func(t *testing.T, c OracleExplainContext, prompt string)
	}{
		{"tam bağlam + sınırlar", oraFacts(), true, oraRows(oracleExplainRowLimit, 9), func(t *testing.T, c OracleExplainContext, p string) {
			if c.SourceName != "core-errlog" || c.Code != "APP_ERR_042" || c.Operation != "OP_TRANSFER" || !c.SourceKnown {
				t.Fatalf("kimlik: %+v", c)
			}
			if c.SummaryLine() != "Oracle · core-errlog · APP_ERR_042 · OP_TRANSFER" {
				t.Fatalf("özet satırı: %q", c.SummaryLine())
			}
			if !c.HourKnown || c.Flow != "patlama" || c.LagMin != 16 {
				t.Fatalf("akış: %+v", c)
			}
			if len(c.Channels) != 3 || c.Channels[0].Name != "MOB" || c.Channels[0].Pct != 60 || c.ChannelCount != 4 {
				t.Fatalf("kanal: %+v (toplam %d)", c.Channels, c.ChannelCount)
			}
			if len(c.Services) != 3 || c.ServiceCount != 4 || c.Services[0].Name != "svc-payments" {
				t.Fatalf("servis: %+v", c.Services)
			}
			if len(c.Hosts) != 2 || c.Hosts[0].Name != "db-host-a" || len(c.Instances) != 1 {
				t.Fatalf("host/instance: %+v %+v", c.Hosts, c.Instances)
			}
			if len(c.Traces) != oracleExplainTraceMax || c.Traces[0].Service != "svc-ledger" || c.Traces[1].Service != "" {
				t.Fatalf("trace'ler (≤5, çözülen servisli): %+v", c.Traces)
			}
			if len(c.Samples) != oracleExplainSamples || !c.RowsCapped || c.RowsRead != oracleExplainRowLimit {
				t.Fatalf("örnek/tavan: %d örnek, capped=%v, read=%d", len(c.Samples), c.RowsCapped, c.RowsRead)
			}
			s0 := c.Samples[0]
			if s0.Message == "" || s0.ExternalCode != "EXT-91" || s0.Weight != 4 || len(s0.Columns) != oracleExplainAttrMax {
				t.Fatalf("örnek alanları: %+v", s0)
			}
			if s0.Columns["SONUC"] != "RED" {
				t.Fatalf("anlam taşıyan kolon (SONUC) tavanın içinde kalmalı: %+v", s0.Columns)
			}
			if _, leaked := s0.Columns[chstore.OracleWeightAttr]; leaked {
				t.Fatal("ağırlık attribute'u kolon olarak basılmamalı")
			}
			for _, want := range []string{"Oracle HATA GRUBU", "stacktrace yok", "son 1 sa=9000 · önceki 1 sa=2000 · oran 4.5× → patlama",
				"son ~16 dk henüz sayılmadı", "MOB %60 (600)", "toplam 4 kanal", "svc-payments (9)", "toplam 4 servis",
				"host db-host-a", "instance pod-7", "Çözülen trace'ler", "EXT-91", `"timezone":"UTC"`} {
				if !strings.Contains(p, want) {
					t.Errorf("prompt %q içermeli:\n%s", want, p)
				}
			}
			if strings.Contains(p, "STACKTRACE") {
				t.Error("Oracle prompt'u stack bölümü basmamalı")
			}
		}},
		{"kırılım yok (blob okunamadı) → zarif", OracleExplainFacts{SourceID: "o-1", SourceName: "core-errlog"}, true, nil, func(t *testing.T, c OracleExplainContext, p string) {
			if c.HourKnown || c.Flow != "bilinmiyor" || len(c.Channels) != 0 || len(c.Services) != 0 || len(c.Traces) != 0 || len(c.Samples) != 0 {
				t.Fatalf("boş bağlam: %+v", c)
			}
			for _, want := range []string{"bilinmiyor (kırılım blob'u okunamadı)", "Kanal kırılımı: yok", "servis çözümü, oy): çözülemedi", "Örnek satır: yok"} {
				if !strings.Contains(p, want) {
					t.Errorf("prompt %q içermeli:\n%s", want, p)
				}
			}
			if strings.Contains(p, "Host / instance") || strings.Contains(p, "Çözülen trace'ler") {
				t.Errorf("boş blok başlık bırakmamalı:\n%s", p)
			}
		}},
		{"kaynak bilinmiyor → sentetik servisten ad", OracleExplainFacts{}, false, oraRows(2, 1), func(t *testing.T, c OracleExplainContext, p string) {
			if c.SourceKnown || c.SourceName != "" {
				t.Fatalf("gerçek servis adı kaynak adı sayılmamalı: %+v", c)
			}
			if !strings.Contains(p, `"sourceResolved":false`) || len(c.Samples) != 2 || c.RowsCapped {
				t.Fatalf("kaynaksız: %+v\n%s", c, p)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := assembleOracleExplainContext(g, tc.facts, tc.factsOK, tc.rows, oracleExplainRowLimit, tf, time.UTC)
			tc.check(t, c, renderOracleExplainPrompt(g, c, time.UTC, "", ""))
		})
	}
	// Sentetik servis (`oracle:<ad>`) kaynak adına çözülür.
	syn := oraGroup()
	syn.Service = chstore.OracleGroupFallbackService("core-errlog")
	if c := assembleOracleExplainContext(syn, OracleExplainFacts{}, false, nil, oracleExplainRowLimit, nil, nil); c.SourceName != "core-errlog" {
		t.Fatalf("sentetik servis → kaynak adı: %q", c.SourceName)
	}
}

// fakeOracleReader — kurucunun okuma yüzü; çağrıları ve argümanları kaydeder.
type fakeOracleReader struct {
	sourceCalls int
	source      string
	rows        []chstore.OracleErrorRow
	gotLimit    int
	gotFrom     time.Time
	gotTo       time.Time
	gotSrc      string
	gotIDs      []string
	traceErr    error
	// v0.10.1103 — found nil: yalnız ids[0] Coremetry'de (eski davranış);
	// dolu: yalnız bu id'ler. spans: GetTrace cevabı (id → span'ler).
	found       []string
	spans       map[string][]chstore.SpanRow
	getTraceErr error
	gotTraces   []string
}

func (f *fakeOracleReader) GetTrace(_ context.Context, id string) ([]chstore.SpanRow, error) {
	f.gotTraces = append(f.gotTraces, id)
	if f.getTraceErr != nil {
		return nil, f.getTraceErr
	}
	return f.spans[id], nil
}

func (f *fakeOracleReader) OracleGroupSource(_ context.Context, _ *chstore.ExceptionGroup) (string, error) {
	f.sourceCalls++
	return f.source, nil
}

func (f *fakeOracleReader) OracleErrorsByOpCode(_ context.Context, src, op, code string, from, to time.Time, limit int) ([]chstore.OracleErrorRow, error) {
	f.gotSrc, f.gotFrom, f.gotTo, f.gotLimit = src, from, to, limit
	if op != "OP_TRANSFER" || code != "APP_ERR_042" {
		return nil, fmt.Errorf("op/kod karıştı: %s %s", op, code)
	}
	if len(f.rows) > limit {
		return f.rows[:limit], nil
	}
	return f.rows, nil
}

func (f *fakeOracleReader) TraceFactsByIDs(_ context.Context, ids []string, _, _ time.Time) (map[string]chstore.TraceFact, error) {
	f.gotIDs = ids
	if f.traceErr != nil {
		return nil, f.traceErr
	}
	if f.found != nil {
		out := map[string]chstore.TraceFact{}
		for _, id := range f.found {
			out[id] = chstore.TraceFact{Service: "svc-orders"}
		}
		return out, nil
	}
	return map[string]chstore.TraceFact{ids[0]: {Service: "svc-ledger"}}, nil
}

func TestBuildOracleExceptionExplainInput(t *testing.T) {
	t.Cleanup(func() { oracleFactsFn.Store(nil) })
	g := oraGroup()

	// Gerçekler enjekte → kaynak oradan (geri çözüm sorgusu YOK), okumalar sınırlı.
	SetOracleExplainFacts(func(chstore.ExceptionGroup) (OracleExplainFacts, bool) { return oraFacts(), true })
	rd := &fakeOracleReader{rows: oraRows(200, 12)}
	in := BuildOracleExceptionExplainInput(context.Background(), rd, nil, g, time.UTC)
	if rd.sourceCalls != 0 || rd.gotSrc != "o-1" || rd.gotLimit != oracleExplainRowLimit {
		t.Fatalf("kaynak/tavan: calls=%d src=%q limit=%d", rd.sourceCalls, rd.gotSrc, rd.gotLimit)
	}
	last := time.Unix(0, g.LastSeen).UTC()
	if !rd.gotFrom.Equal(last.Add(-oracleExplainLookback)) || !rd.gotTo.Equal(last.Add(2*time.Minute)) {
		t.Fatalf("pencere: %s → %s", rd.gotFrom, rd.gotTo)
	}
	if len(rd.gotIDs) != oracleExplainTraceMax {
		t.Fatalf("trace çözümü ≤5 id: %d", len(rd.gotIDs))
	}
	if in.Oracle == nil || len(in.EvTraces) != oracleExplainTraceMax || in.TraceID != in.EvTraces[0] || in.Stack != "" || len(in.EvSpans) != 0 {
		t.Fatalf("girdi: oracle=%v ev=%v trace=%q stack=%q", in.Oracle != nil, in.EvTraces, in.TraceID, in.Stack)
	}
	if !strings.Contains(in.User, "Oracle HATA GRUBU") || !strings.Contains(in.User, "svc-ledger") {
		t.Fatalf("User Oracle bağlamı taşımalı:\n%s", in.User)
	}

	// Gerçek yok → kaynak parmak izinden geri çözülür; trace çözümü düşerse servis boş.
	oracleFactsFn.Store(nil)
	rd2 := &fakeOracleReader{source: "o-1", rows: oraRows(3, 3), traceErr: fmt.Errorf("ch down")}
	in2 := BuildOracleExceptionExplainInput(context.Background(), rd2, nil, g, time.UTC)
	if rd2.sourceCalls != 1 || rd2.gotSrc != "o-1" || in2.Oracle == nil || in2.Oracle.Traces[0].Service != "" || len(in2.Oracle.Samples) != 3 {
		t.Fatalf("geri çözüm/düşüş: %+v", in2.Oracle)
	}

	// Kaynak hiç bulunamaz → satır okunmaz, girdi yine kurulur.
	rd3 := &fakeOracleReader{}
	in3 := BuildOracleExceptionExplainInput(context.Background(), rd3, nil, g, time.UTC)
	if rd3.gotLimit != 0 || in3.Oracle == nil || !strings.Contains(in3.User, "Örnek satır: yok") {
		t.Fatalf("kaynaksız: limit=%d\n%s", rd3.gotLimit, in3.User)
	}
	// nil okuyucu panik yapmaz.
	if in4 := BuildOracleExceptionExplainInput(context.Background(), nil, nil, g, nil); in4.Oracle == nil {
		t.Fatal("nil okuyucu")
	}
}

// Otomatik özet adayı — Oracle grubunun last_seen'i kapanmış dakika gecikmesi
// (≥16 dk) kadar geriden gelir; yaş o kadar geriden ölçülmezse HİÇ aday olamaz.
func TestIsExceptionExplainCandidateAtLag(t *testing.T) {
	now := oraNow
	mk := func(ago time.Duration, occ uint64, summary string) chstore.ExceptionGroup {
		g := *oraGroup()
		g.LastSeen, g.Occurrences, g.AISummary = now.Add(-ago).UnixNano(), occ, summary
		return g
	}
	lag := 16 * time.Minute
	cases := []struct {
		name string
		g    chstore.ExceptionGroup
		lag  time.Duration
		want bool
	}{
		{"Oracle: 18 dk önce, gecikme 16 dk → taze", mk(18*time.Minute, 12000, ""), lag, true},
		{"Oracle: gecikmesiz kural (eski davranış) → asla", mk(18*time.Minute, 12000, ""), 0, false},
		{"Oracle: 25 dk önce, gecikme 16 dk → bayat", mk(25*time.Minute, 12000, ""), lag, false},
		{"Oracle: occurrences < 500", mk(17*time.Minute, 400, ""), lag, false},
		{"Oracle: özet zaten var → bir kez", mk(17*time.Minute, 12000, "hazır"), lag, false},
		{"negatif gecikme 0 sayılır", mk(2*time.Minute, 600, ""), -time.Hour, true},
	}
	for _, c := range cases {
		if got := isExceptionExplainCandidateAt(c.g, now, c.lag); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

func TestExceptionExplainLag(t *testing.T) {
	t.Cleanup(func() { oracleFactsFn.Store(nil) })
	span := chstore.ExceptionGroup{Fingerprint: "0a1b2c3d4e5f6a7b"}
	if lag, ok := exceptionExplainLag(span); lag != 0 || !ok {
		t.Fatalf("span grubu: %s %v", lag, ok)
	}
	ora := *oraGroup()
	oracleFactsFn.Store(nil)
	if _, ok := exceptionExplainLag(ora); ok {
		t.Fatal("enjekte edilmemiş / kaynaksız Oracle grubu aday olmamalı")
	}
	SetOracleExplainFacts(func(chstore.ExceptionGroup) (OracleExplainFacts, bool) { return oraFacts(), true })
	if lag, ok := exceptionExplainLag(ora); lag != 16*time.Minute || !ok {
		t.Fatalf("Oracle gecikmesi: %s %v", lag, ok)
	}
}
