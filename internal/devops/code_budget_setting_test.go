package devops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/stackparse"
)

// code_budget_setting_test.go — v0.10.1038. KOD BÜTÇESİ AYAR OLDU.
//
// Operatör (prod, "Kodu da incele"): "kod bütçesi (4000 karakter) doldu — 1
// pencere düştü, kalanlar hata satırı çevresinde kısaltıldı" notu rutindi.
// "Kod bütçesi daha fazla karakter olabilir bence, default 10k gibi,
// performans sorunu olmayacaksa." Bütçe Settings.CodeBudgetRunes oldu:
// 0 → 10.000, aralık [2.000, 20.000], tek normalizasyon ClampCodeBudgetRunes.
// Pencere sayısı (3) ve ±30 satır değişmedi; çekilen dosya sayısı da —
// bütçe yalnız modele gideni kırpar.

// realisticJava — gerçekçi satır boylu sentetik bir Java servis sınıfı
// (ortalama satır ~35 karakter, Spring/JBoss tarzı girinti). Metot gövdesi
// tekrar eder; her tekrar kendi indeksini taşır ki satırlar birebir aynı
// olmasın. Dönen ikinci değer "throw" satırlarının numaraları.
func realisticJava(pkg, class string, methods int) (string, []int) {
	var b strings.Builder
	n := 0
	line := func(s string) {
		b.WriteString(s)
		b.WriteByte('\n')
		n++
	}
	line("package " + pkg + ";")
	line("")
	line("import java.math.BigDecimal;")
	line("import java.util.List;")
	line("import java.util.Optional;")
	line("import org.slf4j.Logger;")
	line("import org.slf4j.LoggerFactory;")
	line("")
	line("public class " + class + " {")
	line("    private static final Logger LOG =")
	line("        LoggerFactory.getLogger(" + class + ".class);")
	line("    private final OrderRepository orders;")
	line("    private final PaymentGateway payments;")
	line("")
	var throws []int
	for i := 0; i < methods; i++ {
		line("    /**")
		line(fmt.Sprintf("     * Adım %d: siparişi doğrular ve tutarı hesaplar.", i))
		line("     */")
		line(fmt.Sprintf("    public Receipt process%02d(String customerNo,", i))
		line("            List<OrderLine> lines) {")
		line("        if (lines == null || lines.isEmpty()) {")
		throws = append(throws, n+1)
		line("            throw new IllegalStateException(\"no lines: \" + customerNo);")
		line("        }")
		line("        BigDecimal total = BigDecimal.ZERO;")
		line("        for (OrderLine l : lines) {")
		line("            BigDecimal qty = BigDecimal.valueOf(l.getQuantity());")
		line("            total = total.add(l.getUnitPrice().multiply(qty));")
		line("        }")
		line("        Optional<Order> existing = orders.findOpen(customerNo);")
		line("        if (existing.isPresent()) {")
		line(fmt.Sprintf("            LOG.warn(\"open order {} step %d\", customerNo);", i))
		line("            return Receipt.duplicate(existing.get());")
		line("        }")
		line("        Order order = orders.create(customerNo, lines, total);")
		line("        payments.reserve(order.getId(), total);")
		line("        return Receipt.of(order);")
		line("    }")
		line("")
	}
	line("}")
	return b.String(), throws
}

// windowRunes — pencerelerin toplam rune'u (bütçe ile aynı sayım).
func windowRunes(ws []CodeWindow) int {
	n := 0
	for _, w := range ws {
		n += utf8.RuneCountInString(w.Content)
	}
	return n
}

// TestCodeBudgetNormalisation — tek normalizasyon (ClampCodeBudgetRunes) +
// yürürlükteki değer (codeBudget); iki sabit tek kaynak.
func TestCodeBudgetNormalisation(t *testing.T) {
	if defaultCodeBudgetRunes != DefaultCodeBudgetRunes || DefaultCodeBudgetRunes != 10000 {
		t.Fatalf("varsayılan ayrıştı: code.go=%d client.go=%d (istenen 10000)", defaultCodeBudgetRunes, DefaultCodeBudgetRunes)
	}
	if MinCodeBudgetRunes != 2000 || MaxCodeBudgetRunes != 20000 {
		t.Fatalf("aralık [%d, %d], istenen [2000, 20000]", MinCodeBudgetRunes, MaxCodeBudgetRunes)
	}
	for _, c := range []struct{ in, clamp, effective int }{
		{0, 0, 10000},   // yok → varsayılan
		{-5, 0, 10000},  // bozuk → varsayılan
		{1, 2000, 2000}, // alt sınırın altı → alt sınır
		{1999, 2000, 2000},
		{2000, 2000, 2000}, // sınır dahil
		{4000, 4000, 4000}, // eski davranış ayarla korunur
		{10000, 10000, 10000},
		{20000, 20000, 20000}, // sınır dahil
		{20001, 20000, 20000}, // üst sınırın üstü → üst sınır
		{999999, 20000, 20000},
	} {
		if got := ClampCodeBudgetRunes(c.in); got != c.clamp {
			t.Errorf("ClampCodeBudgetRunes(%d)=%d, istenen %d", c.in, got, c.clamp)
		}
		if got := (Settings{CodeBudgetRunes: c.in}).codeBudget(); got != c.effective {
			t.Errorf("codeBudget(%d)=%d, istenen %d", c.in, got, c.effective)
		}
	}
}

// memDevOpsStore — system_settings'in devops satırı (bellekte).
type memDevOpsStore struct{ raw []byte }

func (m *memDevOpsStore) GetDevOpsSettingsRaw(context.Context) ([]byte, error) { return m.raw, nil }
func (m *memDevOpsStore) PutDevOpsSettingsRaw(_ context.Context, raw []byte) error {
	m.raw = append([]byte(nil), raw...)
	return nil
}

// TestCodeBudgetSettingsRoundTrip — eski blob (alan yok) varsayılana düşer;
// kaydedilen değer blob'a yazılır ve okunur; aralık dışı elle yazılmış değer
// yürürlükte sıkışır; PAT sözleşmesi değişmez.
func TestCodeBudgetSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	const pat = "PAT-ROUNDTRIP-PIN"
	store := &memDevOpsStore{raw: []byte(`{"baseUrl":"https://devops.example.local/tfs","collection":"DefaultCollection","pat":"` + pat + `","codeLookupLimit":8}`)}
	svc := New()
	if err := svc.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if got := svc.CurrentSettings().CodeBudgetRunes; got != 0 {
		t.Fatalf("eski blob: CodeBudgetRunes=%d, istenen 0", got)
	}
	snap := svc.Snapshot()
	if snap.CodeBudgetRunes != 0 || snap.EffectiveCodeBudgetRunes != DefaultCodeBudgetRunes || snap.EffectiveLookupLimit != 8 {
		t.Fatalf("eski blob snapshot: %+v", snap)
	}
	if blob := mustJSON(t, snap); strings.Contains(blob, pat) || !strings.Contains(blob, `"effectiveCodeBudgetRunes":10000`) {
		t.Fatalf("snapshot JSON: %s", blob)
	}
	// Varsayılan (0) blob'a yazılmaz — eski okuyucular aynı JSON'u görür.
	if b, _ := json.Marshal(Settings{BaseURL: "https://x"}); strings.Contains(string(b), "codeBudgetRunes") {
		t.Errorf("0 değeri blob'a yazıldı: %s", b)
	}

	cfg := svc.CurrentSettings()
	cfg.CodeBudgetRunes = 6000
	if err := svc.SavePersisted(ctx, store, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(store.raw), `"codeBudgetRunes":6000`) {
		t.Fatalf("kayıt blob'a yazılmadı: %s", store.raw)
	}
	svc2 := New()
	if err := svc2.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if s := svc2.Snapshot(); s.CodeBudgetRunes != 6000 || s.EffectiveCodeBudgetRunes != 6000 || !s.HasPAT {
		t.Fatalf("geri okuma: %+v", s)
	}
	if svc2.CurrentSettings().PAT != pat {
		t.Error("PAT gidiş-dönüşte kayboldu")
	}

	// Elle yazılmış aralık dışı değer: kayıtlı hâli olduğu gibi, yürürlükteki sıkışık.
	store.raw = []byte(`{"baseUrl":"https://devops.example.local/tfs","codeBudgetRunes":500}`)
	svc3 := New()
	if err := svc3.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if s := svc3.Snapshot(); s.CodeBudgetRunes != 500 || s.EffectiveCodeBudgetRunes != MinCodeBudgetRunes {
		t.Fatalf("aralık dışı blob: kayıtlı=%d yürürlükte=%d", s.CodeBudgetRunes, s.EffectiveCodeBudgetRunes)
	}
}

// budgetFixture — üç dosyalı gerçekçi Java fikstürü + üç uygulama frame'li
// stack (en derin "Caused by" önce): Validator → Service → Controller.
func budgetFixture(t *testing.T) (*fakeTFS, []stackparse.Frame) {
	t.Helper()
	f := newFakeTFS(t)
	const dir = "/src/main/java/com/acme/orders/"
	classes := []string{"OrderValidator", "OrderService", "OrderController"}
	var stack strings.Builder
	stack.WriteString("java.lang.IllegalStateException: no lines: C-1001\n")
	for i, cls := range classes {
		body, throws := realisticJava("com.acme.orders", cls, 8)
		p := dir + cls + ".java"
		f.tree = append(f.tree, p)
		f.files[p] = body
		ln := throws[3+i] // dosyanın ortası: ±30 satır tam pencere
		fmt.Fprintf(&stack, "\tat com.acme.orders.%s.process%02d(%s.java:%d)\n", cls, 3+i, cls, ln)
	}
	return f, stackparse.ParseJava(stack.String())
}

// TestFetchCodeHonoursCodeBudgetSetting — AYAR GERÇEKTEN BAĞLI: aynı
// gerçekçi fikstür 4000'de (eski davranış, ayarla) bir pencere düşürür ve
// operatörün gördüğü notu üretir; varsayılanda (10.000) üç pencere tam
// sığar, not yok. Git çağrısı sayısı İKİSİNDE AYNI — bütçe çekimi değil,
// gönderileni kırpar. Pencere boyları -v ile basılır (karar girdisi).
func TestFetchCodeHonoursCodeBudgetSetting(t *testing.T) {
	f, frames := budgetFixture(t)
	run := func(budget int) (CodeContext, int) {
		svc := New() // taze Service: ağaç cache'i paylaşılmaz, çağrı sayısı kıyaslanabilir
		cfg := f.settings()
		cfg.CodeBudgetRunes = budget
		svc.Configure(cfg)
		f.mu.Lock()
		before := len(f.seen)
		f.mu.Unlock()
		cc := svc.FetchCode(context.Background(), "core-service", ProjectHint{}, frames, nil, nil)
		f.mu.Lock()
		defer f.mu.Unlock()
		return cc, len(f.seen) - before
	}

	old, oldCalls := run(4000)
	def, defCalls := run(0)

	// Ham pencereler (bütçe öncesi): varsayılanda hiçbiri kırpılmadığı için
	// def.Windows bütçe öncesi boyların kendisi.
	if len(def.Windows) != 3 {
		t.Fatalf("varsayılan: pencere=%d, istenen 3 (%s)", len(def.Windows), def.Reason)
	}
	for i, w := range def.Windows {
		n := utf8.RuneCountInString(w.Content)
		t.Logf("pencere %d: %s satır %d-%d (%d satır) = %d rune", i+1, w.Path, w.FromLine, w.ToLine, w.ToLine-w.FromLine+1, n)
		if w.ToLine-w.FromLine+1 != 2*codeWindowRadius+1 {
			t.Errorf("pencere %d tam ±30 değil: %d-%d", i+1, w.FromLine, w.ToLine)
		}
	}
	t.Logf("toplam: varsayılan %d rune (bütçe %d) · eski 4000: %d pencere, %d rune", windowRunes(def.Windows), def.Budget, len(old.Windows), windowRunes(old.Windows))

	// Eski davranış ayarla: not ve kayıp aynen.
	if old.Budget != 4000 || len(old.Windows) != 2 || windowRunes(old.Windows) > 4000 {
		t.Fatalf("4000: budget=%d pencere=%d rune=%d", old.Budget, len(old.Windows), windowRunes(old.Windows))
	}
	const oldNote = "kod bütçesi (4000 karakter) doldu — 1 pencere düştü, kalanlar hata satırı çevresinde kısaltıldı"
	if !strings.Contains(old.Reason, oldNote) || old.Trimmed != oldNote || old.Outcome != CodePartial {
		t.Fatalf("4000: reason=%q trimmed=%q outcome=%s", old.Reason, old.Trimmed, old.Outcome)
	}
	// Kırpılan ikinci pencere hata satırını taşır (merkezden kesme, v0.9.1239).
	if w := old.Windows[1]; w.FromLine > w.Line || w.ToLine < w.Line {
		t.Errorf("kırpılan pencere hata satırını kaybetti: %d ∉ %d-%d", w.Line, w.FromLine, w.ToLine)
	}

	// Varsayılan: not yok, kayıp yok.
	if def.Budget != DefaultCodeBudgetRunes || def.Trimmed != "" || strings.Contains(def.Reason, "kod bütçesi") || def.Outcome != CodeOK {
		t.Fatalf("varsayılan: budget=%d trimmed=%q reason=%q outcome=%s", def.Budget, def.Trimmed, def.Reason, def.Outcome)
	}

	// Çağrı sayısı değişmez.
	if oldCalls != defCalls || defCalls == 0 {
		t.Errorf("git çağrısı: 4000'de %d, 10.000'de %d — bütçe çekimi değiştirmemeli", oldCalls, defCalls)
	}
	if old.Stats.Fetched != def.Stats.Fetched || old.Stats.Resolved != def.Stats.Resolved {
		t.Errorf("çekim sayıları ayrıştı: %+v vs %+v", old.Stats, def.Stats)
	}
	t.Logf("git çağrısı (taze cache): %d — iki bütçede aynı", defCalls)
}

// fixedWindow — 61 satırlık (100-160, hata satırı 130) sabit genişlikli
// pencere: rune boyu 61×(5+width)+60, yani hedef boy tam ayarlanabilir.
func fixedWindow(path string, width int) CodeWindow {
	lines := make([]string, 0, 61)
	for i := 100; i <= 160; i++ {
		lines = append(lines, fmt.Sprintf("%d| %s", i, strings.Repeat("x", width)))
	}
	return CodeWindow{Path: path, Line: 130, FromLine: 100, ToLine: 160, Content: strings.Join(lines, "\n")}
}

// TestClampAndHalvedUseEffectiveBudget — saf yarı: ClampCodeWindows
// yürürlükteki bütçeyle (4000'de kırpılan/düşen, 10.000'de sığar); Halved
// (v0.10.1038) modelin az önce taştığı kodu, min(bütçe, gönderilen)'i yarıya
// indirir. Yarısı halvedMinRunes'ın altında kalan minik blok küçültülmez
// (aynı blok → çağıran kodsuz denemeye düşer).
func TestClampAndHalvedUseEffectiveBudget(t *testing.T) {
	var real []CodeWindow
	for i, cls := range []string{"OrderValidator", "OrderService", "OrderController"} {
		body, throws := realisticJava("com.acme.orders", cls, 8)
		w := WindowAround(body, throws[3+i], codeWindowRadius)
		w.Path, w.Line = "/"+cls+".java", throws[3+i]
		real = append(real, w)
	}
	raw := windowRunes(real)

	at4000, trimmed := ClampCodeWindows(real, 4000)
	if !trimmed || len(at4000) != 2 {
		t.Fatalf("4000: trimmed=%v pencere=%d", trimmed, len(at4000))
	}
	if out, trimmed := ClampCodeWindows(real, DefaultCodeBudgetRunes); trimmed || len(out) != 3 || windowRunes(out) != raw {
		t.Fatalf("10.000: trimmed=%v pencere=%d (ham %d rune sığmalıydı)", trimmed, len(out), raw)
	}

	for _, c := range []struct {
		name           string
		budget         int
		ws             []CodeWindow
		sentLo, sentHi int  // fikstürün senaryoyu gerçekten kurduğunun sınaması
		shrinks        bool // false → blok AYNEN döner (çağıran kodsuza düşer)
	}{
		{"varsayılanda 4.500 gönderildi → ≤ 2.250 (eskiden kodsuza düşerdi)", 10000,
			[]CodeWindow{fixedWindow("/A.java", 31), fixedWindow("/B.java", 31)}, 4400, 4600, true},
		{"varsayılanda 9.900 gönderildi → ≤ 4.950", 10000,
			[]CodeWindow{fixedWindow("/A.java", 48), fixedWindow("/B.java", 48), fixedWindow("/C.java", 48)}, 9800, 10000, true},
		{"eski 4000: ~3.998 gönderildi → ~2.000", 4000, at4000, 3900, 4000, true},
		{"damga yok, 7.821 gönderildi → yarısı", 0, real, raw, raw, true},
		{"bütçe 20.000, 7.821 gönderildi → yine gönderilenin yarısı", 20000, real, raw, raw, true},
		{"minik blok (~1.500) → küçültülmez", 10000, []CodeWindow{fixedWindow("/A.java", 19)}, 1400, 1600, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			sent := windowRunes(c.ws)
			if sent < c.sentLo || sent > c.sentHi {
				t.Fatalf("fikstür: gönderilen %d ∉ [%d, %d]", sent, c.sentLo, c.sentHi)
			}
			in := CodeContext{Repo: "r", Budget: c.budget, Windows: c.ws}
			h := in.Halved()
			if !c.shrinks {
				if h.PromptBlock() != in.PromptBlock() || h.Trimmed != "" || h.Budget != c.budget {
					t.Fatalf("minik blok değişti: trimmed=%q budget=%d", h.Trimmed, h.Budget)
				}
				return
			}
			half := sent / 2
			if b := in.budgetRunes(); b < sent {
				half = b / 2
			}
			got := windowRunes(h.Windows)
			if len(h.Windows) == 0 || h.PromptBlock() == "" || got > half || h.Budget != half {
				t.Fatalf("yarı=%d gönderilen=%d → kalan %d rune, %d pencere, Budget=%d", half, sent, got, len(h.Windows), h.Budget)
			}
			if h.PromptBlock() == in.PromptBlock() {
				t.Fatal("blok küçülmedi — çağıran kodsuz denemeye düşerdi")
			}
			if note := fmt.Sprintf("gönderilen kod yarıya indirildi (%d karakter)", half); !strings.Contains(h.Trimmed, note) {
				t.Errorf("not gerçek yarıyı söylemiyor: %q (istenen %q)", h.Trimmed, note)
			}
			// Kalan her pencere hata satırını taşır (merkezden kesme).
			for _, w := range h.Windows {
				if w.FromLine > w.Line || w.ToLine < w.Line {
					t.Errorf("%s: hata satırı %d ∉ %d-%d", w.Path, w.Line, w.FromLine, w.ToLine)
				}
			}
			t.Logf("gönderilen %d → yarı %d → kalan %d rune (%d pencere)", sent, half, got, len(h.Windows))
		})
	}
}
