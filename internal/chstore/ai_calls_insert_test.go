package chstore

// v0.10.409 — ai_calls iki-boot sözleşmesi. İnceleme bulgusu: edit
// betiğinin çift koşumu genişletilmiş INSERT bloğunu üç kez ekleyip eski
// kolon dalını ULAŞILMAZ yapmıştı — ilk boot'ta (kolonlar yokken) her
// AI çağrısı kaydı Distributed tarafından reddedilecekti. Her iki dal
// saf yardımcılarla pinlenir: SQL kolon sayısı == arg sayısı.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAICallsInsertBranches(t *testing.T) {
	c := AICall{ID: "x", PromptVersion: "v", ErrorClass: "timeout", TTFTMs: 12, StreamFallback: true, ShieldHits: 1, CachedTokens: 77,
		TraceID: "0af7651916cd43dd8448eb211c80319c"}
	// v0.10.807 — ext × cached; v0.10.1463 — × traced (üç ayrı probe, üç
	// bayrak → sekiz kombinasyon; her biri ilk boot'ta tek başına false
	// olabilir).
	sqls := map[string]bool{}
	for _, ext := range []bool{false, true} {
		for _, cached := range []bool{false, true} {
			for _, traced := range []bool{false, true} {
				sql := aiCallsInsertSQL(ext, cached, traced, false, false, false)
				sqls[sql] = true
				inner := sql[strings.Index(sql, "(")+1 : strings.LastIndex(sql, ")")]
				n := len(strings.Split(inner, ","))
				args := aiCallsInsertArgs(c, time.Unix(0, 0).UTC(), ext, cached, traced, false, false, false)
				if n != len(args) {
					t.Fatalf("ext=%v cached=%v traced=%v: SQL %d kolon, %d arg", ext, cached, traced, n, len(args))
				}
				for _, col := range strings.Split(aiCallsExtCols, ", ") {
					if strings.Contains(inner, col) != ext {
						t.Errorf("ext=%v: kolon %q beklenmedik durumda", ext, col)
					}
				}
				if strings.Contains(inner, "cached_tokens") != cached {
					t.Errorf("cached=%v: cached_tokens kolonu beklenmedik durumda", cached)
				}
				if strings.Contains(inner, "trace_id") != traced {
					t.Errorf("traced=%v: trace_id kolonu beklenmedik durumda", traced)
				}
				last := args[len(args)-1]
				switch {
				case traced:
					if last != c.TraceID {
						t.Errorf("trace_id son arg olmalı, %v", last)
					}
				case cached:
					if last != uint32(77) {
						t.Errorf("cached_tokens son arg olmalı, %v", last)
					}
				case ext:
					if args[len(args)-2] != uint8(1) {
						t.Errorf("stream_fallback UInt8(1) olmalı, %v", args[len(args)-2])
					}
				}
			}
		}
	}
	if len(sqls) != 8 {
		t.Fatalf("sekiz dal sekiz ayrı SQL üretmeli, %d", len(sqls))
	}
	// Satır okuması da bayrağa uyar: SELECT kolon sayısı == Scan hedefi.
	for _, cached := range []bool{false, true} {
		for _, traced := range []bool{false, true} {
			sel := aiCallsRowSelect(cached, traced, false, false, false)
			n := len(strings.Split(strings.TrimPrefix(sel, "SELECT "), ","))
			var row AICall
			if got := len(aiCallsRowScan(&row, cached, traced, false, false, false)); got != n {
				t.Errorf("cached=%v traced=%v: SELECT %d kolon, Scan %d hedef", cached, traced, n, got)
			}
			if strings.Contains(sel, "trace_id") != traced {
				t.Errorf("traced=%v: SELECT trace_id beklenmedik durumda", traced)
			}
		}
	}
}

// Probe yalnız INSERT hedefine bakar: yerel parçada olup Distributed'da
// henüz olmayan kolon "var" sayılırsa INSERT kırılır.
func TestAICallsProbeTargetsInsertTable(t *testing.T) {
	b, err := os.ReadFile("ai_calls.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "'ai_calls_local'") {
		t.Fatal("probe yerel parçaya bakmamalı — INSERT hedefi ai_calls")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'error_class'") {
		t.Fatal("probe `table = 'ai_calls'` yüklemini taşımalı")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'cached_tokens'") { // v0.10.807
		t.Fatal("cached_tokens probe'u da INSERT hedefine bakmalı")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'trace_id'") { // v0.10.1463
		t.Fatal("trace_id probe'u da INSERT hedefine bakmalı")
	}
}

// v0.10.423 — E5 nokta okuması da iki-boot sözleşmesine uyar: genişletilmiş
// kolonlar yalnız bayrakla; SQL kolon sayısı == Scan hedefi sayısı.
func TestAICallEvalSelectBranches(t *testing.T) {
	base := aiCallEvalSelect(false)
	ext := aiCallEvalSelect(true)
	if strings.Contains(base, "prompt_version") || !strings.Contains(ext, "prompt_version, profile_id, error_class") {
		t.Fatalf("dallar yanlış: base=%q ext=%q", base, ext)
	}
	for _, q := range []string{base, ext} {
		if !strings.Contains(q, "WHERE exchange_id = ?") || !strings.Contains(q, "LIMIT 1") || !strings.Contains(q, "max_execution_time") {
			t.Fatalf("nokta okuma sınırları eksik: %q", q)
		}
	}
	inner := ext[len("SELECT "):strings.Index(ext, " FROM")]
	if n := len(strings.Split(inner, ",")); n != 11 {
		t.Fatalf("ext 11 kolon (8 + 3) olmalı, %d", n)
	}
}

// v0.10.2297 — model_rounds / peak_input_tokens kendi bayrağıyla: kolon
// yokken (ilk boot, küme kipinde ertelenen ALTER) INSERT ve SELECT onları
// ANMAZ; varken son iki kolon/arg ve Scan hedefi onlar.
func TestAICallsRoundsColumnGate(t *testing.T) {
	c := AICall{ID: "x", TraceID: "t", ModelRounds: 3, PeakInputTokens: 34100}
	for _, rounds := range []bool{false, true} {
		sql := aiCallsInsertSQL(true, true, true, rounds, false, false)
		inner := sql[strings.Index(sql, "(")+1 : strings.LastIndex(sql, ")")]
		args := aiCallsInsertArgs(c, time.Unix(0, 0).UTC(), true, true, true, rounds, false, false)
		if n := len(strings.Split(inner, ",")); n != len(args) {
			t.Fatalf("rounds=%v: SQL %d kolon, %d arg", rounds, n, len(args))
		}
		if strings.Contains(inner, "model_rounds") != rounds || strings.Contains(inner, "peak_input_tokens") != rounds {
			t.Fatalf("rounds=%v: INSERT kolonları beklenmedik: %s", rounds, inner)
		}
		if rounds && (args[len(args)-2] != uint16(3) || args[len(args)-1] != uint32(34100)) {
			t.Fatalf("son iki arg tur/peak olmalı: %v", args[len(args)-2:])
		}
		sel := aiCallsRowSelect(true, true, rounds, false, false)
		var row AICall
		dest := aiCallsRowScan(&row, true, true, rounds, false, false)
		if n := len(strings.Split(strings.TrimPrefix(sel, "SELECT "), ",")); n != len(dest) {
			t.Fatalf("rounds=%v: SELECT %d kolon, Scan %d hedef", rounds, n, len(dest))
		}
		if strings.Contains(sel, "model_rounds") != rounds {
			t.Fatalf("rounds=%v: SELECT model_rounds beklenmedik", rounds)
		}
		ex := aiExchangeCallsSQL(rounds, false)
		if strings.Contains(ex, "model_rounds, peak_input_tokens") != rounds || strings.Count(ex, "?") != 4 {
			t.Fatalf("rounds=%v: etkileşim çağrı okuması: %s", rounds, ex)
		}
	}
	// Kolon yokken okunan satır: alanlar 0 = bilinmiyor (asla "1 tur" değil).
	var old AICall
	_ = aiCallsRowScan(&old, true, true, false, false, false)
	if old.ModelRounds != 0 || old.PeakInputTokens != 0 {
		t.Fatal("eski satır 0 (bilinmiyor) okunmalı")
	}
}

// v0.10.2297 — DDL: iki kolon ADD COLUMN IF NOT EXISTS + DEFAULT 0, probe her ikisini ister.
func TestAICallsRoundsDDL(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ALTER TABLE ai_calls ADD COLUMN IF NOT EXISTS model_rounds UInt16 DEFAULT 0",
		"ALTER TABLE ai_calls ADD COLUMN IF NOT EXISTS peak_input_tokens UInt32 DEFAULT 0",
		"s.probeAICallsRoundsColumns(ctx)",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("store.go %q içermiyor", want)
		}
	}
	dd, err := os.ReadFile("ddl_defer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dd), "s.probeAICallsRoundsColumns(ctx)") {
		t.Error("ertelenen DDL yeniden probe'u rounds kolonlarını yoklamıyor")
	}
}

// v0.10.2318 — döngü süre kolonları kendi bayrağıyla: kolon yokken (eski
// şema / küme kipinde ertelenen ALTER) INSERT ve SELECT onları ANMAZ ve
// satır yine yazılır/okunur; varken son dört arg ve beş Scan hedefi onlar.
func TestAICallsTimingColumnGate(t *testing.T) {
	c := AICall{ID: "x", TTFTMs: 2100, LoopModelMs: 9400, LoopToolMs: 1800, LoopToolCalls: 4, LoopMaxRoundToolCalls: 2}
	for _, timing := range []bool{false, true} {
		sql := aiCallsInsertSQL(true, true, true, true, timing, false)
		inner := sql[strings.Index(sql, "(")+1 : strings.LastIndex(sql, ")")]
		args := aiCallsInsertArgs(c, time.Unix(0, 0).UTC(), true, true, true, true, timing, false)
		if n := len(strings.Split(inner, ",")); n != len(args) {
			t.Fatalf("timing=%v: SQL %d kolon, %d arg", timing, n, len(args))
		}
		if strings.Contains(inner, "loop_model_ms") != timing || strings.Contains(inner, "loop_max_round_tool_calls") != timing {
			t.Fatalf("timing=%v: INSERT kolonları beklenmedik: %s", timing, inner)
		}
		if strings.Count(inner, "ttft_ms") != 1 {
			t.Fatalf("timing=%v: ttft_ms INSERT'te tam bir kez (ext kolonu) olmalı: %s", timing, inner)
		}
		if timing {
			tail := args[len(args)-4:]
			if tail[0] != uint32(9400) || tail[1] != uint32(1800) || tail[2] != uint16(4) || tail[3] != uint16(2) {
				t.Fatalf("son dört arg döngü kırılımı olmalı: %v", tail)
			}
		}
		// Eski şema (yalnız base kolonlar) da yazılabilir kalır.
		old := aiCallsInsertSQL(false, false, false, false, timing, false)
		oldInner := old[strings.Index(old, "(")+1 : strings.LastIndex(old, ")")]
		if n, a := len(strings.Split(oldInner, ",")), len(aiCallsInsertArgs(c, time.Unix(0, 0).UTC(), false, false, false, false, timing, false)); n != a {
			t.Fatalf("eski şema timing=%v: %d kolon, %d arg", timing, n, a)
		}
		sel := aiCallsRowSelect(true, true, true, timing, false)
		var row AICall
		dest := aiCallsRowScan(&row, true, true, true, timing, false)
		if n := len(strings.Split(strings.TrimPrefix(sel, "SELECT "), ",")); n != len(dest) {
			t.Fatalf("timing=%v: SELECT %d kolon, Scan %d hedef", timing, n, len(dest))
		}
		if strings.Contains(sel, "ttft_ms") != timing || strings.Contains(sel, "loop_tool_ms") != timing {
			t.Fatalf("timing=%v: SELECT kolonları beklenmedik: %s", timing, sel)
		}
		for _, rounds := range []bool{false, true} {
			ex := aiExchangeCallsSQL(rounds, timing)
			if strings.Contains(ex, "loop_tool_calls") != timing || strings.Contains(ex, "ttft_ms") != timing || strings.Count(ex, "?") != 4 {
				t.Fatalf("rounds=%v timing=%v: etkileşim çağrı okuması: %s", rounds, timing, ex)
			}
		}
	}
	var old AICall
	_ = aiCallsRowScan(&old, true, true, true, false, false)
	if old.TTFTMs != 0 || old.LoopModelMs != 0 || old.LoopToolCalls != 0 {
		t.Fatal("kolon yokken alanlar 0 (bilinmiyor) okunmalı")
	}
}

// v0.10.2318 — DDL: dört kolon ADD COLUMN IF NOT EXISTS + DEFAULT 0; probe
// ttft_ms dahil beşini ister; boot ve ertelenen DDL yeniden probe'u çağırır.
func TestAICallsTimingDDL(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ALTER TABLE ai_calls ADD COLUMN IF NOT EXISTS loop_model_ms UInt32 DEFAULT 0",
		"ALTER TABLE ai_calls ADD COLUMN IF NOT EXISTS loop_tool_ms UInt32 DEFAULT 0",
		"ALTER TABLE ai_calls ADD COLUMN IF NOT EXISTS loop_tool_calls UInt16 DEFAULT 0",
		"ALTER TABLE ai_calls ADD COLUMN IF NOT EXISTS loop_max_round_tool_calls UInt16 DEFAULT 0",
		"s.probeAICallsTimingColumns(ctx)",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("store.go %q içermiyor", want)
		}
	}
	dd, err := os.ReadFile("ddl_defer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dd), "s.probeAICallsTimingColumns(ctx)") {
		t.Error("ertelenen DDL yeniden probe'u timing kolonlarını yoklamıyor")
	}
	ac, err := os.ReadFile("ai_calls.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ac), "table = 'ai_calls'\n\t\t   AND name IN ('ttft_ms', 'loop_model_ms', 'loop_tool_ms', 'loop_tool_calls', 'loop_max_round_tool_calls')") ||
		!strings.Contains(string(ac), "n == 5") {
		t.Error("timing probe'u INSERT hedefinde beş kolonu birden istemeli")
	}
}

// v0.10.2321 — loop_gateway_retry kendi bayrağıyla: kolon yokken INSERT ve
// SELECT onu anmaz (eski şemada satır yine yazılır); varken son arg UInt8.
func TestAICallsRetryColumnGate(t *testing.T) {
	c := AICall{ID: "x", LoopGatewayRetry: true}
	for _, retry := range []bool{false, true} {
		sql := aiCallsInsertSQL(true, true, true, true, true, retry)
		inner := sql[strings.Index(sql, "(")+1 : strings.LastIndex(sql, ")")]
		args := aiCallsInsertArgs(c, time.Unix(0, 0).UTC(), true, true, true, true, true, retry)
		if n := len(strings.Split(inner, ",")); n != len(args) {
			t.Fatalf("retry=%v: SQL %d kolon, %d arg", retry, n, len(args))
		}
		if strings.Contains(inner, "loop_gateway_retry") != retry {
			t.Fatalf("retry=%v: INSERT: %s", retry, inner)
		}
		if retry && args[len(args)-1] != uint8(1) {
			t.Fatalf("son arg UInt8(1) olmalı: %v", args[len(args)-1])
		}
		sel := aiCallsRowSelect(true, true, true, true, retry)
		var row AICall
		if n, d := len(strings.Split(strings.TrimPrefix(sel, "SELECT "), ",")), len(aiCallsRowScan(&row, true, true, true, true, retry)); n != d {
			t.Fatalf("retry=%v: SELECT %d kolon, Scan %d hedef", retry, n, d)
		}
	}
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ALTER TABLE ai_calls ADD COLUMN IF NOT EXISTS loop_gateway_retry UInt8 DEFAULT 0", "s.probeAICallsRetryColumn(ctx)"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("store.go %q içermiyor", want)
		}
	}
	dd, _ := os.ReadFile("ddl_defer.go")
	if !strings.Contains(string(dd), "s.probeAICallsRetryColumn(ctx)") {
		t.Error("ertelenen DDL yeniden probe'u retry kolonunu yoklamıyor")
	}
}
