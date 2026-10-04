package anomaly

// log_patterns_es_prefix_test.go — v0.10.1087 (operatör, prod ES: "Elastic'te
// eksik."). ES'e bağlanmadan ölçüm: standart çözümleyicinin (UAX#29 sözcük
// sınırları + küçük harf) küçük bir taklidi sentetik gövdeleri terimlere
// böler; küratörlü desenlerin ES planı (logstore.ESPatternTerms) bu terimlere
// karşı değerlendirilir. Eski plan (her token `match_phrase`) kod benzeri
// sözcüğün içindeki token'ı KAÇIRIR — eksik sayımın kaydı; yeni plan (prefix
// + esPrefixForms) aynı satırı yakalar. Gövdeler SENTETİK.

import (
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// standardAnalyze — ES `standard` çözümleyicisinin taklidi (yalnız bu
// fikstürün ihtiyacı): harf / rakam / `_` bitişikliği tek terim (WB5, WB8–10,
// WB13a/b); `.` ve `'` iki HARF arasında, `:` iki harf arasında (MidLetter)
// birleştirir (WB6/7); `.` `,` `;` iki RAKAM arasında birleştirir (WB11/12);
// geri kalan her şey (boşluk, tire, `/`, `$`, `@`, `=`, …) böler. Terimler
// küçük harf. Gerçek tokenizer'ın Unicode ayrıntılarını (Katakana, emoji, …)
// taklit etmez.
func standardAnalyze(s string) []string {
	rs := []rune(s)
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 && strings.TrimFunc(string(cur), func(r rune) bool { return r == '_' }) != "" {
			out = append(out, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if word(r) {
			cur = append(cur, r)
			continue
		}
		if len(cur) > 0 && i+1 < len(rs) {
			prev, next := cur[len(cur)-1], rs[i+1]
			letters := unicode.IsLetter(prev) && unicode.IsLetter(next)
			digits := unicode.IsDigit(prev) && unicode.IsDigit(next)
			if (letters && (r == '.' || r == '\'' || r == ':')) || (digits && (r == '.' || r == ',' || r == ';')) {
				cur = append(cur, r)
				continue
			}
		}
		flush()
	}
	flush()
	return out
}

// Taklidin kendisi ES belgelerindeki örneklerle sabitlenir.
func TestStandardAnalyzeStub_MatchesESDocs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		// ES "standard tokenizer" belge örneği.
		{"The 2 QUICK Brown-Foxes jumped over the lazy dog's bone.",
			[]string{"the", "2", "quick", "brown", "foxes", "jumped", "over", "the", "lazy", "dog's", "bone"}},
		// ES "uax_url_email tokenizer" belgesindeki standart karşılaştırma.
		{"Email me at john.smith@global-international.com",
			[]string{"email", "me", "at", "john.smith", "global", "international.com"}},
		// Bu düzeltmenin dayandığı üç gözlem:
		{"java.lang.NullPointerException: x", []string{"java.lang.nullpointerexception", "x"}},
		{"MQJCA1011: Failed", []string{"mqjca1011", "failed"}},
		{"ORA-12541: TNS:no listener", []string{"ora", "12541", "tns:no", "listener"}},
		{"HikariPool-1 - Connection", []string{"hikaripool", "1", "connection"}},
		{"took 1,250.5 ms", []string{"took", "1,250.5", "ms"}},
	}
	for _, c := range cases {
		if got := standardAnalyze(c.in); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q:\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// esPlanHits — bir ES planının (bool.should) çözümlenmiş gövdeye eşleşmesi:
// prefix → herhangi bir terim değerle başlar; match_phrase → değerin
// çözümlenmiş terim dizisi gövdede bitişik geçer.
func esPlanHits(plan []logstore.ESPatternTerm, doc []string) bool {
	for _, t := range plan {
		if t.Prefix {
			for _, d := range doc {
				if strings.HasPrefix(d, strings.ToLower(t.Value)) {
					return true
				}
			}
			continue
		}
		seq := standardAnalyze(t.Value)
		if len(seq) == 0 {
			continue
		}
		for i := 0; i+len(seq) <= len(doc); i++ {
			ok := true
			for j := range seq {
				if doc[i+j] != seq[j] {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

// phrasePlan — düzeltme ÖNCESİ ES planı: her token `message:"token"`.
func phrasePlan(p logPattern) []logstore.ESPatternTerm {
	out := make([]logstore.ESPatternTerm, len(p.Tokens))
	for i, tok := range p.Tokens {
		out[i] = logstore.ESPatternTerm{Value: tok}
	}
	return out
}

func chTokensHit(p logPattern, body string) bool {
	low := strings.ToLower(body)
	for _, tok := range p.Tokens {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}

// esFixture — sentetik gövde; old = düzeltme öncesi ES eşleşmesi (false =
// belgelenen eksik sayım), regex = desenin regex'i eşler mi (CH sayar mı).
// Yeni plan her satırda regex'le aynı sonucu vermeli.
var esFixture = []struct {
	pattern, body string
	old, regex    bool
}{
	// ── eksik sayım: kod benzeri sözcüğün içindeki token ──
	{"Null pointer", `java.lang.NullPointerException: Cannot invoke "String.length()" because "s" is null`, false, true},
	{"Null pointer", "Unexpected NullPointerException in orders-svc handler", false, true},
	{"Out of memory", "Terminating due to java.lang.OutOfMemoryError: Java heap space", false, true},
	{"Read / write timeout", "java.util.concurrent.TimeoutException: inventory-svc did not answer in 30000 ms", false, true},
	{"Database deadlock", "org.springframework.dao.DeadlockLoserDataAccessException: PreparedStatementCallback; SQL [update orders]", false, true},
	{"Database deadlock", "DeadlockLoserDataAccessException on demo-db", false, true},
	{"SQL exception", "Caused by: java.sql.SQLException: Connection closed", false, true},
	{"JDBC pool exhausted", "MQJCA1011: Failed to allocate a JMS connection.", false, true},
	{"JBoss / WildFly errors", "JBAS014777: Services which failed to start: service jboss.web.deployment", false, true},
	// v0.10.1098 — gerçek biçimli (sentetik) WildFly / JBoss AS kodları:
	// önek + alt sistem harfleri + rakam. `wflyctl0013` tek terim, yalnız
	// `wfly` öneki bulur; eski regex hiçbirini eşlemiyordu.
	{"JBoss / WildFly errors", "WFLYCTL0013: Operation (\"deploy\") failed - address: ([(\"deployment\" => \"orders.war\")])", false, true},
	{"JBoss / WildFly errors", "WFLYEJB0034: Jakarta Enterprise Beans Invocation failed on component OrdersBean for method public void com.example.Orders.place()", false, true},
	{"JBoss / WildFly errors", "WFLYUT0012: Unable to start listener demo-https on 0.0.0.0:8443", false, true},
	{"JBoss / WildFly errors", "WFLYSRV0026: WildFly Full 26.1.3.Final (WildFly Core 18.1.2.Final) started (with errors) in 9123ms", false, true},
	{"JBoss / WildFly errors", "WFLYCTL0180: Services with missing/unavailable dependencies", false, true},
	{"JBoss / WildFly errors", "WFLYMSGAMQ0090: Could not create queue jms.queue.DemoOrders", false, true},
	{"JBoss / WildFly errors", "JBAS014612: Operation (\"add\") failed - address: ([(\"subsystem\" => \"datasources\")])", false, true},
	{"Class init / load failure", "Caused by: java.lang.ClassNotFoundException: com.example.orders.Handler from [Module \"deployment.orders.war\"]", false, true},
	{"Class init / load failure", "java.lang.NoClassDefFoundError: Could not initialize class com.example.Cfg", false, true},
	{"Class init / load failure", "java.lang.ExceptionInInitializerError", false, true},
	{"Class init / load failure", "ClassNotFoundException while loading plugin demo-plugin", false, true},
	{"JNDI / lookup failure", "javax.naming.NameNotFoundException: java:/jdbc/OrdersDS", false, true},
	{"JNDI / lookup failure", "lookup failed: NameNotFoundException", false, true},
	{"Spring bean failure", "org.springframework.beans.factory.BeanCreationException: Error creating bean with name 'ordersService'", false, true},
	{"Spring bean failure", "org.springframework.beans.factory.NoSuchBeanDefinitionException: No qualifying bean of type 'com.example.Repo'", false, true},
	{"Spring bean failure", "org.springframework.beans.BeanInstantiationException: Failed to instantiate [com.example.Cfg]", false, true},
	{"Spring bean failure", "org.springframework.beans.factory.UnsatisfiedDependencyException: Error creating bean", false, true},
	{"Spring bean failure", "Caught BeanCreationException during refresh", false, true},
	{"Hibernate / JPA", "org.hibernate.LazyInitializationException: could not initialize proxy - no Session", false, true},
	{"Hibernate / JPA", "org.hibernate.StaleObjectStateException: Row was updated or deleted by another transaction", false, true},
	{"Hibernate / JPA", "javax.persistence.OptimisticLockException: Row was updated", false, true},
	{"Hibernate / JPA", "jakarta.persistence.OptimisticLockException: Row was updated", false, true},
	{"Hibernate / JPA", "org.springframework.transaction.TransactionTimedOutException: Transaction timed out", false, true},
	{"Hibernate / JPA", "javax.persistence.TransactionRequiredException: No EntityManager with actual transaction available", false, true},
	{"Hibernate / JPA", "jakarta.persistence.TransactionRequiredException: No EntityManager", false, true},
	{"DB constraint violation", "org.springframework.dao.DataIntegrityViolationException: could not execute statement", false, true},
	{"DB constraint violation", "java.sql.SQLIntegrityConstraintViolationException: ORA-00001: unique constraint violated", false, true},
	{"DB constraint violation", "org.hibernate.exception.ConstraintViolationException: could not execute statement", false, true},
	{"DB constraint violation", "javax.validation.ConstraintViolationException: name: must not be blank", false, true},
	{"DB constraint violation", "jakarta.validation.ConstraintViolationException: name: must not be blank", false, true},
	{"Java exceptions", "java.lang.ClassCastException: class A cannot be cast to class B", false, true},
	{"Java exceptions", "java.lang.IllegalStateException: Connection pool shut down", false, true},
	{"Java exceptions", "java.lang.IllegalArgumentException: id must be positive", false, true},
	{"Java exceptions", "java.lang.UnsupportedOperationException", false, true},
	{"Java exceptions", "java.lang.ArrayIndexOutOfBoundsException: Index 5 out of bounds for length 3", false, true},
	{"Java exceptions", "java.util.ConcurrentModificationException", false, true},
	{"Java exceptions", `java.lang.NumberFormatException: For input string: "abc"`, false, true},
	{"Java exceptions", "ClassCastException in orders mapper", false, true},
	{"Java exceptions", "IllegalStateException during shutdown", false, true},
	{"Java exceptions", "IllegalArgumentException for demo-user", false, true},
	{"Java exceptions", "UnsupportedOperationException from legacy client", false, true},
	{"Java exceptions", "ArrayIndexOutOfBoundsException in parser", false, true},
	{"Java exceptions", "ConcurrentModificationException in cache sweep", false, true},
	{"Java exceptions", "NumberFormatException for header x-retry", false, true},
	{"Java exceptions", "StackOverflowException in recursive resolver", false, true},

	// ── kontrol: eski plan da yakalıyordu, yeni plan bozmaz ──
	{"Connection refused", "connect ECONNREFUSED 10.0.0.7:5432", true, true},
	{"Oracle errors (ORA-)", "ORA-00942: table or view does not exist", true, true},
	{"Oracle TNS errors", "TNS-12541: TNS:no listener", true, true},
	{"SQL exception", "SQLException while committing batch 42", true, true},
	{"Read / write timeout", "read timeout on demo-db socket", true, true},
	{"Out of memory", "container orders-svc OOMKilled", true, true},
	{"Disk full", "write /data/wal: no space left on device", true, true},
	{"Connection refused", "dial tcp 10.0.0.7:5432: connection refused", true, true},
	{"JDBC pool exhausted", "IJ000655: No managed connections available within configured blocking timeout", true, true},
	{"External system rejected", "com.example.core.exception.ExternalSystemException: Request not allowed for URI :", true, true},

	// ── kısa token'lar İFADE kalır: önek genişlemesi yok ──
	{"Oracle errors (ORA-)", "oracle pool warmed up for demo-db", false, false},
	{"Oracle TNS errors", "tnsnames.ora reloaded for demo-db", false, false},
	{"Auth failures", "response 4012 bytes from orders-svc", false, false},
	{"Go panic", "worker panicked and recovered", false, false},
	{"TLS / certificate", "parsed x5091 field", false, false},
}

func TestESPrefixFixture_UndercountFixed(t *testing.T) {
	byName := map[string]logPattern{}
	for _, p := range patterns {
		byName[p.Name] = p
	}
	var oldHits, newHits, regexHits int
	for _, c := range esFixture {
		p, ok := byName[c.pattern]
		if !ok {
			t.Fatalf("desen yok: %q", c.pattern)
		}
		doc := standardAnalyze(c.body)
		re := regexp.MustCompile(p.Regex)
		if got := re.MatchString(c.body); got != c.regex {
			t.Errorf("%s / %q: regex=%v, fikstür %v diyor", c.pattern, c.body, got, c.regex)
		}
		oldHit := esPlanHits(phrasePlan(p), doc)
		newHit := esPlanHits(logstore.ESPatternTerms(p.spec()), doc)
		if oldHit != c.old {
			t.Errorf("%s / %q: eski ifade planı=%v, fikstür %v diyor (terimler %q)", c.pattern, c.body, oldHit, c.old, doc)
		}
		if newHit != c.regex {
			t.Errorf("%s / %q: yeni plan=%v, regex=%v (terimler %q)", c.pattern, c.body, newHit, c.regex, doc)
		}
		// Regex'in eşlediği satırı CH token ön süzgeci de geçirir: yeni ES
		// planı CH'nin saydığı satırı sayar.
		if c.regex && !chTokensHit(p, c.body) {
			t.Errorf("%s / %q: CH token'ı yok", c.pattern, c.body)
		}
		if oldHit {
			oldHits++
		}
		if newHit {
			newHits++
		}
		if c.regex {
			regexHits++
		}
	}
	// Ölçüm: regex'in (= CH'nin) eşlediği satırlardan eski ES planı kaçını
	// sayıyordu, yeni plan kaçını sayıyor.
	t.Logf("sentetik fikstür: regex/CH %d satır · eski ES ifade planı %d · yeni ES planı %d", regexHits, oldHits, newHits)
	if newHits != regexHits {
		t.Errorf("yeni plan regex'le aynı sayıda satır saymalı: %d ≠ %d", newHits, regexHits)
	}
}

// esPrefixForms sözleşmesi: anahtar gerçek desen; girdi küçük harf, standart
// çözümleyicide TEK terim (yoksa prefix hiçbir terimle eşleşmez) ve
// fikstürde onu gerektiren en az bir satır var (yalnız o biçimle bulunur ya da
// regex'le doğrulanır) — kanıtsız ek biçim, ES'e kanıtsız genişleme demek.
func TestESPrefixForms_Contract(t *testing.T) {
	byName := map[string]logPattern{}
	for _, p := range patterns {
		byName[p.Name] = p
	}
	for name, forms := range esPrefixForms {
		p, ok := byName[name]
		if !ok {
			t.Errorf("esPrefixForms anahtarı desen değil: %q", name)
			continue
		}
		for _, f := range forms {
			if f != strings.ToLower(f) {
				t.Errorf("%s: %q küçük harf değil", name, f)
			}
			if got := standardAnalyze(f); len(got) != 1 || got[0] != f {
				t.Errorf("%s: %q tek terim değil: %q", name, f, got)
			}
			covered := false
			for _, c := range esFixture {
				if c.pattern != name || !c.regex {
					continue
				}
				for _, d := range standardAnalyze(c.body) {
					if strings.HasPrefix(d, f) {
						covered = true
					}
				}
			}
			if !covered {
				t.Errorf("%s: %q için regex'in eşlediği fikstür satırı yok", name, f)
			}
		}
		if s := p.spec(); len(s.ESPrefixes) != len(forms) {
			t.Errorf("%s: spec ESPrefixes taşımıyor", name)
		}
	}
	// 1071 sözleşmesi: kurum içi sınıfın paketi bilinmiyor; ES tanımı yalnız
	// Tokens.
	if _, ok := esPrefixForms["External system rejected"]; ok {
		t.Error("External system rejected ES biçimi taşımamalı")
	}
}

// Dedektör, grafik (1060), pivot (1071) ve örneklem (1080) aynı spec'i görür:
// LogPatternSpecByName ESPrefixes'i taşır.
func TestLogPatternSpecByName_CarriesESPrefixes(t *testing.T) {
	got, ok := LogPatternSpecByName("Null pointer")
	if !ok || len(got.ESPrefixes) != 1 || got.ESPrefixes[0] != "java.lang.nullpointer" {
		t.Fatalf("spec=%+v", got)
	}
	if got, _ := LogPatternSpecByName("Disk full"); got.ESPrefixes != nil {
		t.Fatalf("ES biçimi olmayan desen: %+v", got)
	}
}
