package templater

// v0.10.1030 regresyon testi — "yeni log şablonu" seli (operatör: "Çok fazla
// problem geliyor."). Puller her tikte örnekten ağacı soğuk kurar ve küme
// kimliği her inceltmede yeniden hesaplanır; aynı satır ailesi tikten tike
// farklı `<*>` konumlarıyla, yani FARKLI kimlikle biter. SameTemplateFamily
// bu iki şablonu Drain'in kendi kuralıyla "aynı aile" saymalı; gerçekten
// farklı biçimleri ise saymamalı.

import (
	"reflect"
	"strings"
	"testing"
)

// jsonUserLine — operatörün ekranındaki biçime benzeyen sentetik yapısal
// satır: sıkışık JSON öneki ilk belirtece yapışır, işlenmiş mesajın serbest
// metin kelimeleri sonraki belirteçler.
func jsonUserLine(user, region, channel string) string {
	return `{"Timestamp":"2026-10-02T10:00:01.123+03:00","Level":"Warning",` +
		`"MessageTemplate":"User {UserName} logged in from {Region} via {Channel}",` +
		`"RenderedMessage":"User ` + user + ` logged in from ` + region + ` via ` + channel + `"}`
}

// jsonOrderLine — aynı JSON önekli, aynı belirteç sayılı (15) ama BAŞKA
// mesaj şablonu: yönlendirme belirteci 0 farklı.
func jsonOrderLine(order, region, carrier string) string {
	return `{"Timestamp":"2026-10-02T10:00:02.456+03:00","Level":"Warning",` +
		`"MessageTemplate":"Order {OrderId} was shipped to {Region} via {Carrier}",` +
		`"RenderedMessage":"Order ` + order + ` was shipped to ` + region + ` via ` + carrier + `"}`
}

const jsonPrefix = `{"Timestamp":"<*>","Level":"Warning","MessageTemplate":"User {UserName} logged in from {Region} via {Channel}","RenderedMessage":"User`

func TestSameTemplateFamily_Table(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"birebir aynı", "User <*> logged in from <*>", "User <*> logged in from <*>", true},
		{"bir fazla joker", "User <*> logged in from eu-west", "User <*> logged in from <*>", true},
		{"farklı belirteç sayısı", "User <*> logged in", "User <*> logged in from <*>", false},
		{"açıkça farklı biçim (yönlendirme belirteci farklı)", "Payment <*> declined by acquirer", "User <*> logged in today", false},
		{
			"aynı önek ama benzerlik eşik altında (3/10 < 0.4)",
			"payments-api worker started alpha beta gamma delta epsilon zeta eta",
			"payments-api worker started one two three four five six seven",
			false,
		},
		{
			"eşik sınırı tam 0.4 (4/10) → aynı küme",
			"payments-api worker started job beta gamma delta epsilon zeta eta",
			"payments-api worker started job two three four five six seven",
			true,
		},
		{
			"uzun JSON önekli aile, mesaj kelimeleri farklı",
			jsonPrefix + ` alice logged in from eu-west via web"}`,
			jsonPrefix + ` <*> logged in from us-east via mobile"}`,
			true,
		},
		{
			"JSON önekli ama başka mesaj şablonu (belirteç 0 farklı)",
			`{"Timestamp":"<*>","Level":"Warning","MessageTemplate":"User <*> logged in from <*>`,
			`{"Timestamp":"<*>","Level":"Warning","MessageTemplate":"Order <*> was shipped to <*>`,
			false,
		},
		{"yönlendirme konumunda joker ↔ değişmez (MaxChildren taşması)", "User <*> logged in from <*>", "User alice logged in from <*>", true},
		// Yönlendirme kuralını tek başına sınar: benzerlik 6/7 ama belirteç 2
		// (yönlendirme öneki, Depth-1 = 3 belirteç) farklı → Drain bu ikisini
		// hiç karşılaştırmaz. Bir konum sonrası (belirteç 3) farkı ise yaprakta
		// benzerliğe kalır.
		{"yönlendirme belirteci farklı, benzerlik yüksek", "payments-api worker started job queue drained ok", "payments-api worker stopped job queue drained ok", false},
		{"yönlendirme sonrası fark, benzerlik yüksek", "payments-api worker started job queue drained ok", "payments-api worker started task queue drained ok", true},
		{"boş ↔ boş", "", "", false},
		{"boş ↔ dolu", "", "User <*> logged in", false},
		{"yalnız boşluk", "   ", " \t ", false},
		{"sekme ayırıcı = boşluk (Tokenize kuralı)", "User\t<*> logged in", "User <*> logged in", true},
		{"satır sonu belirteç İÇİNDE kalır (sayı 3 ≠ 4)", "caused\nby <*> retry", "caused by <*> retry", false},
		{"satır sonlu belirteç kendi ailesinde", "caused\nby <*> retry", "caused\nby <*> <*>", true},
		// Drain değişmez belirteci KENDİ çocuğuna yollar: yönlendirme öneki
		// tümü `<*>` olan şablon, değişmez önekli bir şablonla aile değildir.
		{"tümü joker ↔ değişmezler", "<*> <*> <*>", "alpha beta gamma", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFamily(t, tc.a, tc.b, tc.want)
		})
	}
}

// assertFamily — dizge yolu, ayrılmış yol ve simetri aynı cevabı vermeli.
func assertFamily(t *testing.T, a, b string, want bool) {
	t.Helper()
	if got := SameTemplateFamily(a, b); got != want {
		t.Errorf("SameTemplateFamily(%q, %q) = %v, want %v", a, b, got, want)
	}
	if got := SameTemplateFamily(b, a); got != want {
		t.Errorf("simetri bozuk: SameTemplateFamily(%q, %q) = %v, want %v", b, a, got, want)
	}
	if got := ParseTemplate(a).SameFamily(ParseTemplate(b)); got != want {
		t.Errorf("ParseTemplate yolu ayrışmış: (%q, %q) = %v, want %v", a, b, got, want)
	}
}

// TestSameTemplateFamily_NotLooserThanDrain — v0.10.1030 gözden geçirme:
// yönlendirme konumunda `<*>` her değişmezle eşleşseydi, öneki joker olan
// bir şablon aynı boydaki HER şablonu yutardı. Drain değişmezi kendi
// çocuğuna yollar; `<*>` ↔ değişmez yalnız taşmada (ortak bir değişmez
// yönlendirme belirteciyle) mümkündür.
func TestSameTemplateFamily_NotLooserThanDrain(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"bilinen `<*> <*>` yeni `Shutting down`'ı yutmaz", "<*> <*>", "Shutting down", false},
		{"joker önekli aday değişmez önekli 5 belirteçliyle aile değil", "<*> <*> <*> upstream unavailable", "Connection pool exhausted for checkout", false},
		{"önek tümü joker, n=7", "<*> <*> <*> <*> <*> <*> <*>", "alpha beta gamma delta epsilon zeta eta", false},
		{"tek belirteç: `<*>` ↔ değişmez", "<*>", "Ready", false},
		{"yalnız joker ortak, karışık konum var", "<*> <*> <*>", "<*> job <*>", false},
		// Pozitif kontroller — Drain'le eşdeğer olanlar korunur.
		{"ikisi de */*/* (Drain'in aynı yaprağı)", "<*> <*> <*> upstream unavailable", "<*> <*> <*> upstream timeout", true},
		{"taşma: konum 0 karışık, konum 1-2 ortak değişmez", "<*> worker started job alpha", "payments-api worker started job alpha", true},
		{"taşma: ortak değişmez son yönlendirme konumunda", "<*> <*> started", "<*> job started", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFamily(t, tc.a, tc.b, tc.want)
		})
	}
}

// drainTick — bir puller tikinin aynısı: soğuk ağaç, örneği besle, anlık
// görüntüyü al, sıfırla.
func drainTick(t *testing.T, d *Drain, lines []string) []*Cluster {
	t.Helper()
	for _, l := range lines {
		if d.Add(l, "payments-api", 1) == nil {
			t.Fatalf("Add nil döndü: %q", l)
		}
	}
	snap := d.Snapshot()
	d.Reset()
	return snap
}

// TestSameTemplateFamily_ReproducesReportedMechanism — bildirilen mekanizma:
// aynı aile iki ayrı tikte, farklı örnek alt kümeleriyle, farklı inceltme
// düzeyinde → FARKLI kimlik. Yüklem ikisini aynı aile saymalı.
func TestSameTemplateFamily_ReproducesReportedMechanism(t *testing.T) {
	d := NewDrain()

	// Tik 1: kullanıcı değişiyor, bölge/kanal sabit.
	tick1 := drainTick(t, d, []string{
		jsonUserLine("alice", "eu-west", "web"),
		jsonUserLine("bob", "eu-west", "web"),
		jsonUserLine("carol", "eu-west", "web"),
		jsonUserLine("dave", "eu-west", "web"),
	})
	// Tik 2: kullanıcı sabit, bölge/kanal değişiyor.
	tick2 := drainTick(t, d, []string{
		jsonUserLine("alice", "eu-west", "web"),
		jsonUserLine("alice", "us-east", "mobile"),
		jsonUserLine("alice", "ap-south", "web"),
		jsonUserLine("alice", "eu-west", "kiosk"),
	})
	if len(tick1) != 1 || len(tick2) != 1 {
		t.Fatalf("her tik tek küme vermeli: tik1=%d tik2=%d", len(tick1), len(tick2))
	}
	a, b := tick1[0], tick2[0]
	if a.ID == b.ID {
		t.Fatalf("mekanizma yeniden üretilemedi: iki tik aynı kimliği verdi (%s)\n%s", a.ID, a.TemplateString())
	}
	t.Logf("tik1 %s %q", a.ID, a.TemplateString())
	t.Logf("tik2 %s %q", b.ID, b.TemplateString())
	if !SameTemplateFamily(a.TemplateString(), b.TemplateString()) {
		t.Errorf("aynı ailenin iki tik varyantı aile sayılmadı:\n  tik1 %s %q\n  tik2 %s %q",
			a.ID, a.TemplateString(), b.ID, b.TemplateString())
	}

	// Gerçekten farklı biçim (aynı JSON öneki, aynı belirteç sayısı, başka
	// mesaj şablonu) aile DEĞİL.
	other := drainTick(t, d, []string{
		jsonOrderLine("ord-a", "eu-west", "carrier-a"),
		jsonOrderLine("ord-b", "us-east", "carrier-a"),
	})
	if len(other) != 1 {
		t.Fatalf("sipariş ailesi tek küme vermeli: %d", len(other))
	}
	if countTemplateTokens(other[0].TemplateString()) != countTemplateTokens(a.TemplateString()) {
		t.Fatalf("test kurgusu: iki biçimin belirteç sayısı eşit olmalı (yalnız yönlendirme ayırsın)")
	}
	for _, c := range []*Cluster{a, b} {
		if SameTemplateFamily(c.TemplateString(), other[0].TemplateString()) {
			t.Errorf("farklı biçim aile sayıldı:\n  %q\n  %q", c.TemplateString(), other[0].TemplateString())
		}
	}

	// Aynı tikte iki aile birlikte → Drain iki küme tutar; yüklem de ayırır.
	mixed := drainTick(t, d, []string{
		jsonUserLine("erin", "eu-west", "web"),
		jsonOrderLine("ord-c", "eu-west", "carrier-b"),
		jsonUserLine("frank", "eu-west", "web"),
		jsonOrderLine("ord-d", "eu-west", "carrier-b"),
	})
	if len(mixed) != 2 {
		t.Fatalf("karışık tik iki küme vermeli: %d", len(mixed))
	}
	if SameTemplateFamily(mixed[0].TemplateString(), mixed[1].TemplateString()) {
		t.Errorf("Drain'in ayrı tuttuğu iki biçim aile sayıldı")
	}
}

// TestSameTemplateFamily_MemberLinesBelongToCluster — tutarlılık: Drain'in
// bir kümeye koyduğu HER satır (maskelenmiş, birleştirilmiş hâli) o kümenin
// şablonuyla aynı ailedir. Yüklem Add'den daha sıkı olsaydı burada düşerdi.
func TestSameTemplateFamily_MemberLinesBelongToCluster(t *testing.T) {
	d := NewDrain()
	lines := []string{
		jsonUserLine("alice", "eu-west", "web"),
		jsonUserLine("bob", "us-east", "mobile"),
		jsonUserLine("carol", "ap-south", "kiosk"),
		"Connection to db-primary established in 12 ms",
		"Connection to db-replica established in 40 ms",
		"payments-api worker started job alpha",
		"payments-api worker started job beta",
	}
	members := map[*Cluster][]string{}
	for _, l := range lines {
		c := d.Add(l, "payments-api", 1)
		members[c] = append(members[c], strings.Join(Tokenize(l), " "))
	}
	for c, ms := range members {
		for _, m := range ms {
			if !SameTemplateFamily(c.TemplateString(), m) {
				t.Errorf("küme üyesi aile dışı sayıldı:\n  şablon %q\n  satır  %q", c.TemplateString(), m)
			}
		}
	}
}

// TestSplitTemplate_RoundTrip — saklanan dizge (TemplateString) belirteçlere
// KAYIPSIZ geri ayrılmalı: sekme ve satır sonu içeren ham satırlar dahil.
func TestSplitTemplate_RoundTrip(t *testing.T) {
	d := NewDrain()
	for _, l := range []string{
		"User alice\tlogged  in from eu-west",
		"panic: boom\ngoroutine 7 [running]: main.run()",
		jsonUserLine("alice", "eu-west", "web"),
	} {
		d.Add(l, "payments-api", 1)
	}
	for _, c := range d.Snapshot() {
		s := c.TemplateString()
		if got := splitTemplate(s); !reflect.DeepEqual(got, c.Template) {
			t.Errorf("gidiş-dönüş bozuk:\n  got  %q\n  want %q", got, c.Template)
		}
		if got := countTemplateTokens(s); got != len(c.Template) {
			t.Errorf("countTemplateTokens(%q) = %d, want %d", s, got, len(c.Template))
		}
		if got := ParseTemplate(s).TokenCount(); got != len(c.Template) {
			t.Errorf("ParseTemplate(%q).TokenCount() = %d, want %d", s, got, len(c.Template))
		}
		// v0.10.1030 — dedektörün SQL ön süzgeci belirteç sayısını
		// `countSubstrings(template, ' ') + 1` ile hesaplar. Bu ancak saklanan
		// dizgede belirteçler TEK boşlukla ayrılmış, belirteçlerde boşluk/sekme
		// yok ve baş/son boşluk yoksa kesindir — burada sabitlenir.
		if got := strings.Count(s, " ") + 1; got != len(c.Template) {
			t.Errorf("boşluk sayısı + 1 = %d, belirteç %d (%q)", got, len(c.Template), s)
		}
		if strings.Contains(s, "\t") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") || strings.Contains(s, "  ") {
			t.Errorf("saklanan şablon biçimi bozuk: %q", s)
		}
	}
}

// TestNewDrainUsesFamilyDefaults — yüklem ile ağaç aynı ayarları okur.
func TestNewDrainUsesFamilyDefaults(t *testing.T) {
	d := NewDrain()
	if d.Depth != defaultDepth || d.SimThreshold != defaultSimThreshold || d.MaxChildren != defaultMaxChildren {
		t.Errorf("NewDrain varsayılanları sabitlerden ayrışmış: %+v", d)
	}
}
