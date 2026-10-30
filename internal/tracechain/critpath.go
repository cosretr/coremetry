package tracechain

// critpath.go — v0.10.1625 — CoSRE kritik yol planı R1 (operatör: trace açıklamasında
// "en yavaş bileşen" sunucunun hesapladığı kritik yoldan gelsin; en büyük
// paylı adım CLIENT ise cevap "dış bağımlılık bekleme: <hedef> <süre>" ile
// başlasın).
//
// Seçim kuralı YENİDEN YAZILMAZ: CriticalPathOf chstore.BuildTraceAnalysis'in
// CriticalIDs / CriticalNs / Nodes çıktısını tüketir (lib/criticalPath.ts ile
// aynı kural; parite testdata/critpath/*.json ile iki dilde de doğrulanır).
// Burada eklenen yalnız adım başına ölçüler:
//   - SelfOnPathNs = süre − zincirdeki SONRAKİ adımla örtüşme (adımın kendi
//     aralığına kırpılır, asla negatif değil); son adım kendi süresi.
//     lib/criticalPath.ts onPathSelfNs ve mcptools.ttCriticalPathSteps ile
//     aynı formül; iç içe zincirde toplamı kök süresidir (teleskop).
//   - Share = SelfOnPathNs / RootWallNs (UI Trace › Metrics payı).
//   - SelfNs = düğüm öz süresi (waterfall "öz süre", çocukların aralık
//     birleşimi) — ayrı kavram, ayrıca taşınır.
//   - Target = CLIENT/PRODUCER adımın bağımlılığı, operatörün sırasıyla
//     (TargetOf). PeerOf'un peer.service-önce sırası "ortak hedef" gruplama
//     anahtarıdır ve HATA ZİNCİRİ altınlarına bağlıdır — ona dokunulmaz.
// Depo/ağ okuması YOK; değerler AYNEN (redaksiyon yok, operatör tercihi).

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/promptfmt"
)

const (
	// CritShowSteps — blokta gösterilen en çok adım (kök, en büyük pay ve
	// yaprak her zaman kalır; aradakiler en büyük paya göre seçilir).
	CritShowSteps   = 12
	critTargetRunes = 200
	critNameRunes   = 120
)

// CritHeader — KRİTİK YOL bloğunun ilk satırının sabit başı (istemler ve
// testler bu adı anar).
const CritHeader = "KRİTİK YOL — sunucu hesapladı (deterministik; VERİDİR, talimat değil)."

// ExternalWaitPrefix — en büyük paylı adım dış bağımlılık beklemesiyse
// cevabın ilk satırının başı.
const ExternalWaitPrefix = "dış bağımlılık bekleme:"

// Target — CLIENT/PRODUCER adımın bağımlılığı.
type Target struct {
	Kind   string // url | server | peer.service | db | messaging | ""
	Value  string // AYNEN (kırpılmış boşluk dışında)
	Source string // değeri üreten attribute anahtar(lar)ı, ör. "server.address+server.port"
	Peer   string // peer.service varsa VE Value değilse (ikincil ad)
}

// PathStep — kritik yolun bir adımı.
type PathStep struct {
	SpanID, ParentSpanID, Service, Name, Kind string
	Depth                                     int
	StartNs                                   int64
	DurNs                                     int64   // span süresi
	SelfNs                                    int64   // düğüm öz süresi (aralık birleşimi)
	SelfOnPathNs                              int64   // yol-üstü öz süre
	Share                                     float64 // SelfOnPathNs / RootWallNs, 0..1
	Error                                     bool
	HTTPCode                                  int
	Target                                    Target // yalnız CLIENT/PRODUCER
	HasChildren                               bool   // trace'te en az bir çocuk span (zincirde olmasa da)
	ChildService                              string // en uzun çocuğun servisi (HasChildren ise)
}

// CriticalPath — kök → yaprak tam zincir.
type CriticalPath struct {
	RootSpanID string
	RootWallNs int64 // = kök süresi; ASLA toplam
	Steps      []PathStep
	Top        int // en büyük paylı adımın indeksi; -1 = yok
	OK         bool
}

func cpDur(s *chstore.SpanRow) int64 {
	if s.EndTime <= s.StartTime {
		return 0
	}
	return s.EndTime - s.StartTime
}

// CriticalPathOf — SAF: span'ler → kritik yol.
func CriticalPathOf(spans []chstore.SpanRow) CriticalPath {
	if len(spans) == 0 {
		return CriticalPath{Top: -1}
	}
	return CriticalPathFrom(spans, chstore.BuildTraceAnalysis(spans, false))
}

// CriticalPathFrom — SAF: çağıranın zaten hesapladığı analizle (mcptools
// get_trace aynı analizi başka alanlar için de kullanır; iki kez kurulmasın).
func CriticalPathFrom(spans []chstore.SpanRow, an chstore.TraceAnalysis) CriticalPath {
	out := CriticalPath{Top: -1, RootSpanID: an.RootSpanID, RootWallNs: an.CriticalNs}
	if len(spans) == 0 || len(an.CriticalIDs) == 0 {
		return out
	}
	byID := make(map[string]int, len(spans))
	for i := range spans {
		if id := spans[i].SpanID; id != "" {
			if _, dup := byID[id]; !dup {
				byID[id] = i
			}
		}
	}
	onPath := make(map[string]bool, len(an.CriticalIDs))
	for _, id := range an.CriticalIDs {
		onPath[id] = true
	}
	// Yol adımlarının çocukları (zincirde olsun olmasın): en uzun çocuk.
	bestChild := make(map[string]int, len(an.CriticalIDs))
	for i := range spans {
		p := spans[i].ParentSpanID
		if p == "" || !onPath[p] || spans[i].SpanID == "" || spans[i].SpanID == p || byID[spans[i].SpanID] != i {
			continue
		}
		if b, ok := bestChild[p]; !ok || cpDur(&spans[i]) > cpDur(&spans[b]) {
			bestChild[p] = i
		}
	}
	node := make(map[string]chstore.TraceNode, len(an.CriticalIDs))
	for _, n := range an.Nodes {
		if onPath[n.SpanID] {
			node[n.SpanID] = n
		}
	}
	ids := an.CriticalIDs
	for i, id := range ids {
		idx, ok := byID[id]
		if !ok {
			continue
		}
		s := &spans[idx]
		dur := cpDur(s)
		on := dur
		if i+1 < len(ids) {
			if nidx, ok := byID[ids[i+1]]; ok {
				n := &spans[nidx]
				a, b := n.StartTime, n.StartTime+cpDur(n)
				if a < s.StartTime {
					a = s.StartTime
				}
				if end := s.StartTime + dur; b > end {
					b = end
				}
				if b > a {
					on -= b - a
				}
			}
		}
		if on < 0 {
			on = 0
		}
		st := PathStep{SpanID: s.SpanID, ParentSpanID: s.ParentSpanID, Service: s.ServiceName, Name: s.Name,
			Kind: strings.ToLower(strings.TrimSpace(s.Kind)), Depth: int(node[id].Depth), StartNs: s.StartTime,
			DurNs: dur, SelfNs: node[id].SelfNs, SelfOnPathNs: on, Error: s.StatusCode == "error", HTTPCode: httpCodeOf(*s)}
		if out.RootWallNs > 0 {
			st.Share = float64(on) / float64(out.RootWallNs)
		}
		if KindRank(st.Kind) == 1 {
			st.Target = TargetOf(*s)
		}
		if c, ok := bestChild[id]; ok {
			st.HasChildren, st.ChildService = true, spans[c].ServiceName
		}
		out.Steps = append(out.Steps, st)
	}
	if len(out.Steps) == 0 {
		return out
	}
	out.OK = true
	out.Top = 0
	for i := 1; i < len(out.Steps); i++ {
		if topBefore(out.Steps[i], out.Steps[out.Top]) {
			out.Top = i
		}
	}
	return out
}

// topBefore — a, b'den önce mi: pay → hata → CLIENT/PRODUCER → en erken
// başlangıç; hepsi eşitse yol sırası (önceki kalır).
func topBefore(a, b PathStep) bool {
	if a.SelfOnPathNs != b.SelfOnPathNs {
		return a.SelfOnPathNs > b.SelfOnPathNs
	}
	if a.Error != b.Error {
		return a.Error
	}
	if ra, rb := KindRank(a.Kind), KindRank(b.Kind); ra != rb {
		return ra > rb
	}
	return a.StartNs < b.StartNs
}

func httpCodeOf(sp chstore.SpanRow) int {
	if sp.HTTPStatus > 0 {
		return int(sp.HTTPStatus)
	}
	var c int
	if _, err := fmt.Sscanf(SpanAttr(sp, "http.response.status_code", "http.status_code"), "%d", &c); err == nil {
		return c
	}
	return 0
}

// attrFirst — ilk dolu attribute'un anahtarı ve değeri.
func attrFirst(sp chstore.SpanRow, keys ...string) (string, string) {
	for _, k := range keys {
		if v := strings.TrimSpace(sp.Attributes[k]); v != "" {
			return k, v
		}
	}
	return "", ""
}

// TargetOf — SAF: operatörün sırası. DB span'i (db.system kolonu, sonra
// db.system.name|db.system attribute'u) ÖNCE ve kendi kuralıyla çözülür
// (dbTargetOf, v0.10.1639); diğerleri url.full|http.url → server.address|
// net.peer.name|http.host (+ :server.port|net.peer.port) → peer.service
// (kolon, sonra attribute) → messaging.system +
// /messaging.destination.name|messaging.destination. Hiçbiri yoksa sıfır.
func TargetOf(sp chstore.SpanRow) Target {
	peer := strings.TrimSpace(sp.PeerService)
	if peer == "" {
		_, peer = attrFirst(sp, "peer.service")
	}
	withPeer := func(t Target) Target {
		if peer != "" && peer != t.Value {
			t.Peer = peer
		}
		return t
	}
	if t, ok := dbTargetOf(sp); ok {
		return withPeer(t)
	}
	if k, v := attrFirst(sp, "url.full", "http.url"); v != "" {
		return withPeer(Target{Kind: "url", Value: v, Source: k})
	}
	if k, host := attrFirst(sp, "server.address", "net.peer.name", "http.host"); host != "" {
		t := Target{Kind: "server", Value: host, Source: k}
		portKeys := []string{"server.port"}
		if k == "net.peer.name" {
			portKeys = []string{"net.peer.port", "server.port"}
		} else {
			portKeys = append(portKeys, "net.peer.port")
		}
		if pk, port := attrFirst(sp, portKeys...); port != "" && !strings.Contains(host, ":") {
			t.Value += ":" + port
			t.Source += "+" + pk
		}
		return withPeer(t)
	}
	if peer != "" {
		return Target{Kind: "peer.service", Value: peer, Source: "peer.service"}
	}
	if _, ms := attrFirst(sp, "messaging.system"); ms != "" {
		t := Target{Kind: "messaging", Value: ms, Source: "messaging.system"}
		if k, d := attrFirst(sp, "messaging.destination.name", "messaging.destination"); d != "" {
			t.Value += "/" + d
			t.Source += "+" + k
		}
		return t
	}
	return Target{}
}

// dbTargetOf — v0.10.1639 (CoSRE kritik yol takibi, operatör onaylı): DB
// CLIENT span'inin hedefi "<veritabanı>@<sunucu>"; url.full DB'de geçmez
// (HTTP tabanlı DB istemcisinin uç adresi veritabanını söylemez), port
// eklenmez (hedef veritabanı, soket değil). Kısmi veri: yalnız ad → ad;
// yalnız sunucu → <db.system>@sunucu; ikisi de yok → db.system. Span ADINA
// ASLA düşülmez (SQL özeti hedef değildir) — değer en az db.system'dir.
// Takma adlar yeni → eski (internal/otlp/semconv.go sırası): db.system.name|
// db.system, db.namespace|db.name, server.address|net.peer.name.
func dbTargetOf(sp chstore.SpanRow) (Target, bool) {
	sysKey, sys := "db.system", strings.TrimSpace(sp.DBSystem)
	if sys == "" {
		sysKey, sys = attrFirst(sp, "db.system.name", "db.system")
	}
	if sys == "" {
		return Target{}, false
	}
	nameKey, name := attrFirst(sp, "db.namespace", "db.name")
	hostKey, host := attrFirst(sp, "server.address", "net.peer.name")
	switch {
	case name != "" && host != "":
		return Target{Kind: "db", Value: name + "@" + host, Source: nameKey + "+" + hostKey}, true
	case name != "":
		return Target{Kind: "db", Value: name, Source: nameKey}, true
	case host != "":
		return Target{Kind: "db", Value: sys + "@" + host, Source: sysKey + "+" + hostKey}, true
	}
	return Target{Kind: "db", Value: sys, Source: sysKey}, true
}

// TopStep — en büyük paylı adım.
func (c CriticalPath) TopStep() (PathStep, bool) {
	if !c.OK || c.Top < 0 || c.Top >= len(c.Steps) {
		return PathStep{}, false
	}
	return c.Steps[c.Top], true
}

// TopIsExternalWait — en büyük paylı adım CLIENT/PRODUCER VE trace'te hiç
// çocuğu yok (izlenmeyen bağımlılık). İzlenen aşağı akış span'i olan (zincirde
// ya da saat kaymasıyla zincir dışında) çağrı dış bağımlılık sayılmaz.
func (c CriticalPath) TopIsExternalWait() bool {
	t, ok := c.TopStep()
	return ok && KindRank(t.Kind) == 1 && !t.HasChildren
}

// closingKind — "external" | "dependent" | "internal" | "".
func (c CriticalPath) closingKind() string {
	t, ok := c.TopStep()
	switch {
	case !ok:
		return ""
	case KindRank(t.Kind) == 1 && !t.HasChildren:
		return "external"
	case KindRank(t.Kind) == 1:
		return "dependent"
	}
	return "internal"
}

// TargetLabel — adımın hedefi (blokta ve önek satırında AYNI yazım); hedef
// yoksa span adı.
func (s PathStep) TargetLabel() string {
	if s.Target.Value != "" {
		return OneLine(s.Target.Value, critTargetRunes)
	}
	return OneLine(s.Name, critNameRunes)
}

// ExternalWaitLine — "dış bağımlılık bekleme: <hedef> <süre>"; dış bekleme
// değilse "". Süre adımın KENDİ süresi (kökün değil).
func (c CriticalPath) ExternalWaitLine() string {
	if !c.TopIsExternalWait() {
		return ""
	}
	t, _ := c.TopStep()
	return ExternalWaitPrefix + " " + t.TargetLabel() + " " + FmtMs(float64(t.DurNs)/1e6)
}

func pct(share float64) string { return fmt.Sprintf("%%%.0f", share*100) }

func nsMs(ns int64) string { return FmtMs(float64(ns) / 1e6) }

// stepLine — tek adım satırı.
func (c CriticalPath) stepLine(i int) string {
	s := c.Steps[i]
	var tags []string
	if i == 0 {
		tags = append(tags, "kök")
	}
	if i == c.Top {
		tags = append(tags, "en büyük pay")
	}
	prefix := fmt.Sprintf("%d. ", i+1)
	if len(tags) > 0 {
		prefix += "[" + strings.Join(tags, " · ") + "] "
	}
	var parts []string
	if svc := OneLine(s.Service, serviceMaxRunes); svc != "" {
		parts = append(parts, svc)
	}
	if k := strings.ToUpper(s.Kind); k != "" {
		parts = append(parts, k)
	}
	parts = append(parts, OneLine(s.Name, critNameRunes),
		"süre "+nsMs(s.DurNs), "öz "+nsMs(s.SelfOnPathNs)+" ("+pct(s.Share)+")")
	if s.Target.Value != "" {
		parts = append(parts, "hedef "+OneLine(s.Target.Value, critTargetRunes))
	}
	if s.Target.Peer != "" {
		parts = append(parts, "peer "+OneLine(s.Target.Peer, serviceMaxRunes))
	}
	switch {
	case s.HTTPCode > 0:
		parts = append(parts, fmt.Sprintf("HTTP %d", s.HTTPCode))
	case s.Error:
		parts = append(parts, "status error")
	}
	return prefix + strings.Join(parts, " · ")
}

// closingLine — "En büyük pay: …" kapanışı.
func (c CriticalPath) closingLine() string {
	t, ok := c.TopStep()
	if !ok {
		return ""
	}
	svc := OneLine(t.Service, serviceMaxRunes)
	kind := strings.ToUpper(t.Kind)
	switch c.closingKind() {
	case "external", "dependent":
		what := t.TargetLabel()
		if t.Target.Value != "" {
			what = "hedef " + what
		}
		l := fmt.Sprintf("En büyük pay: %s · %s · %s · %s (%s) — ", svc, kind, what, nsMs(t.DurNs), pct(t.Share))
		if c.closingKind() == "external" {
			return l + "dış bağımlılık beklemesi (çağıranın altında izlenen çocuk span yok)"
		}
		l += "bağımlı servis beklemesi: " + OneLine(t.ChildService, serviceMaxRunes)
		if c.Top == len(c.Steps)-1 {
			l += " (çocuk span zincire girmedi: saat kayması ya da ebeveynden sonra bitti)"
		}
		return l
	}
	return fmt.Sprintf("En büyük pay: %s · %s · %s · öz %s (%s) — servis içi süre", svc, kind, OneLine(t.Name, critNameRunes), nsMs(t.SelfOnPathNs), pct(t.Share))
}

// Shown — gösterilecek adım indeksleri (artan): kök, en büyük pay, yaprak her
// zaman; kalan yer en büyük yol-üstü öz süreye göre.
func (c CriticalPath) Shown(max int) []int {
	n := len(c.Steps)
	if max < 3 {
		max = 3
	}
	if n <= max {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	keep := map[int]bool{0: true, n - 1: true}
	if c.Top >= 0 {
		keep[c.Top] = true
	}
	rest := make([]int, 0, n)
	for i := 1; i < n-1; i++ {
		if !keep[i] {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool { return c.Steps[rest[a]].SelfOnPathNs > c.Steps[rest[b]].SelfOnPathNs })
	for _, i := range rest {
		if len(keep) >= max {
			break
		}
		keep[i] = true
	}
	out := make([]int, 0, len(keep))
	for i := range keep {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// Block — SAF: düz metin (çitsiz) KRİTİK YOL bloğu; yol yoksa "". Baş/son
// satır sonu YOK. ≤ BlockMaxRunes: sığmazsa gösterilen adım sayısı azalır,
// kapanış satırı her zaman kalır.
func (c CriticalPath) Block() string {
	if !c.OK || len(c.Steps) == 0 {
		return ""
	}
	head := CritHeader + " Kök duvar saati " + nsMs(c.RootWallNs) +
		"; adımlar kök → yaprak; öz = adımın yolda tek başına geçen süresi, pay = kök süresine oranı; süreler TOPLANMAZ:"
	closing := c.closingLine()
	var out string
	for max := CritShowSteps; max >= 3; max-- {
		lines := []string{head}
		prev := -1
		for _, i := range c.Shown(max) {
			if prev >= 0 && i-prev > 1 {
				lines = append(lines, fmt.Sprintf("… %d adım atlandı", i-prev-1))
			}
			lines = append(lines, TruncRunes(c.stepLine(i), HopMaxRunes))
			prev = i
		}
		lines = append(lines, closing)
		out = strings.Join(lines, "\n")
		if utf8.RuneCountInString(out) <= BlockMaxRunes {
			break
		}
	}
	return promptfmt.FenceSafe(TruncRunes(out, BlockMaxRunes))
}

const (
	critClosingPrefix   = "En büyük pay: "
	critExternalSuffix  = " — dış bağımlılık beklemesi"
	critClosingPartsMin = 4 // servis · TÜR · hedef/ad · süre
)

// ExternalWaitLineFromBlock — SAF (v0.10.1630): kanıt METNİNDEKİ KRİTİK YOL
// bloğunun kapanış satırından ExternalWaitLine'ı geri okur ("" = blok yok ya
// da dış bekleme değil). Evalset yalnız metni görür; prod da aynı yolu
// kullanır ki puanlanan ile operatörün gördüğü aynı olsun. Biçim closingLine
// ile birlikte değişir (round-trip testi).
func ExternalWaitLineFromBlock(text string) string {
	i := strings.Index(text, CritHeader)
	if i < 0 {
		return ""
	}
	rest := text[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	for _, ln := range strings.Split(rest, "\n") {
		if !strings.HasPrefix(ln, critClosingPrefix) {
			continue
		}
		k := strings.Index(ln, critExternalSuffix)
		if k < 0 {
			return ""
		}
		body := ln[len(critClosingPrefix):k]
		if p := strings.LastIndex(body, " (%"); p >= 0 {
			body = body[:p]
		}
		parts := strings.Split(body, " · ")
		if len(parts) < critClosingPartsMin {
			return ""
		}
		what := strings.TrimPrefix(strings.Join(parts[2:len(parts)-1], " · "), "hedef ")
		return ExternalWaitPrefix + " " + what + " " + parts[len(parts)-1]
	}
	return ""
}
