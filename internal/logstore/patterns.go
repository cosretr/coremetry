package logstore

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// patterns.go — v0.10.296 (docs/audit/log-search.md Dilim 2, B3):
// /logs "Desenler" — pencere içindeki log mesajlarını NormalizeSignature
// imzasıyla gruplar.
//
// ÖRNEKLEME ZORUNLU (INCIDENTS "Drain templating … sample-based";
// templater/puller.go 1000/5 dk disiplini): pencere ne kadar büyük olursa
// olsun en çok PatternsSampleCap satır okunur — Search sayfaları (cursor)
// ile, en yeniden eskiye. Sayımlar bu örneğe GÖREdir; sonuç `Sampled` /
// `Total` / `Truncated` ile bunu ilan eder ("~%12" değil "2000 örnekte
// 240"). ES'te terms agg KULLANILAMAZ (imza indekste yok) — iki backend de
// aynı yoldan geçer, ayrı uygulama yok (bir backend'in uyguladığı, ötekinin
// yok saydığı metot bu repo'nun sessiz no-op sınıfıdır).
//
// İmza: logstore.NormalizeSignature — Problem zenginleştirmesiyle (influx
// enrich groupLogSignatures) AYNI fonksiyon; iki yüzey aynı "aynı log"
// tanımını gösterir. Drain (templater) ayrı uzaydır.
// v0.10.1504 — istisna: JSON gövdede kısa kategorik alanlar (status, level,
// result…) örneklemde düşük kardinaliteliyse desen imzasında TUTULUR
// (buildPatternGroups); Problem zenginleştirmesi satır başına imzayı
// (tümü `<x>`) kullanmaya devam eder. Kategorik alanı olmayan gövdelerde iki
// imza bayt bayt aynıdır.

const (
	PatternsSampleCap = 2000
	patternsPageSize  = 500
	patternsMaxGroups = 500
	patternsTopSvc    = 3
)

// SignatureGroup — bir desen satırı.
type SignatureGroup struct {
	Hash         string   `json:"hash"`
	Template     string   `json:"template"`
	Count        int64    `json:"count"`
	Sample       string   `json:"sample"` // ilk görülen mesaj, VERBATİM
	Severity     uint8    `json:"severity"`
	SeverityText string   `json:"severityText,omitempty"`
	FirstSeen    int64    `json:"firstSeen"` // unix ns (örnek içinde)
	LastSeen     int64    `json:"lastSeen"`
	Services     []string `json:"services"`     // en çok görülen ≤3
	ServiceCount int      `json:"serviceCount"` // farklı servis sayısı
	// Query — v0.10.297: şablondan türetilen arama dizesi (PatternSearchQuery);
	// UI "Ara" ile bunu serbest metne koyar. Boş = aranabilir literal yok.
	Query string `json:"query"`
	// v0.10.508 (log arama denetimi C6) — yalnız baseline istenince dolar:
	// PrevCount = hemen önceki eşit pencerenin ÖRNEKLEMESİNDE aynı imzanın
	// sayısı; Ratio = Count/PrevCount (PrevCount 0 → 0); New = önceki
	// örneklemede hiç yok (örneklemede yeni — kanıt değil ipucu; tavan
	// dolduysa taban da alt pencere anlatır).
	PrevCount int64   `json:"prevCount,omitempty"`
	Ratio     float64 `json:"ratio,omitempty"`
	New       bool    `json:"new,omitempty"`
}

// PatternsBaseline — v0.10.508 (C6): taban örneklemesinin dürüstlük zarfı.
type PatternsBaseline struct {
	FromNs   int64 `json:"fromNs"`
	ToNs     int64 `json:"toNs"`
	Sampled  int   `json:"sampled"`
	Distinct int   `json:"distinct"`
	// Truncated — taban örneklemesi tavana dayandı: "yeni" ipucu zayıflar.
	Truncated bool `json:"truncated,omitempty"`
	// Degraded — taban okunamadı; gruplarda prevCount/ratio/new YOK.
	Degraded bool   `json:"degraded,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// JoinPatternBaseline — v0.10.508 (C6) SAF: mevcut pencerenin grupları
// tabanın gruplarıyla HASH üzerinden birleşir. Taban nil/boşsa hiçbir
// alan yazılmaz (UI "—" çizer). Tabanda örnek satır yoksa (Sampled 0)
// "yeni" DENMEZ: yokluk kanıt değildir.
func JoinPatternBaseline(cur *PatternsResult, base *PatternsResult) {
	if cur == nil || base == nil {
		return
	}
	base = AlignPatternBaseline(cur, base)
	prev := make(map[string]int64, len(base.Groups))
	for _, g := range base.Groups {
		prev[g.Hash] = g.Count
	}
	for i := range cur.Groups {
		pc := prev[cur.Groups[i].Hash]
		cur.Groups[i].PrevCount = pc
		if pc > 0 {
			cur.Groups[i].Ratio = float64(cur.Groups[i].Count) / float64(pc)
		} else {
			cur.Groups[i].Ratio = 0
		}
		cur.Groups[i].New = pc == 0 && base.Sampled > 0
	}
}

// patternsPageWantsCursor — v0.10.1303 SAF: bu sayfanın ardından bir sayfa
// daha okunacak mı? remaining = tavana kalan satır, pageLimit = bu sayfanın
// boyu. Kalan bu sayfaya sığıyorsa imleç kullanılmayacak demektir —
// Filter.WantCursor sözleşmesi: kullanmayacağın imleci ilan etme (ES PIT
// açar/tutar). Son sayfada false: ES gelen imleçteki PIT'i hemen kapatır.
func patternsPageWantsCursor(remaining, pageLimit int) bool {
	return remaining > pageLimit
}

// PatternsResult — gruplar + örnekleme dürüstlüğü.
type PatternsResult struct {
	Groups []SignatureGroup `json:"groups"`
	// Baseline — v0.10.508 (C6): yalnız ?baseline=1 ile dolar.
	Baseline  *PatternsBaseline `json:"baseline,omitempty"`
	Sampled   int               `json:"sampled"`   // okunan satır
	Total     int               `json:"total"`     // backend'in pencere toplamı (ES ≤10k doygun)
	Cap       int               `json:"cap"`       // PatternsSampleCap
	Truncated bool              `json:"truncated"` // Sampled == Cap && Total > Cap
	Distinct  int               `json:"distinct"`  // limit'ten önceki grup sayısı
	// CoveredFromNs/CoveredToNs (v0.10.441, log arama denetimi C4) —
	// örneklemenin GERÇEKTEN kapsadığı alt pencere (en yeni uç; okunan
	// satırların min/max zaman damgası). Tavan dolduğunda istenen
	// pencereden dardır: yoğun serviste "son 1 saat" sanılan sayımlar
	// aslında son birkaç dakikayı anlatıyordu ve panel bunu söylemiyordu.
	// Kapıyı Sampled >= Cap ile kur, Truncated ile DEĞİL (Total güvenilmez
	// olabilir: ES track_total_hits kapalı; aşağıdaki clamp truncated=false
	// bırakır).
	CoveredFromNs int64 `json:"coveredFromNs,omitempty"`
	CoveredToNs   int64 `json:"coveredToNs,omitempty"`
	// SplitKeys — v0.10.1504: JSON gövdede değeri imzada TUTULAN kategorik
	// anahtarlar (örneklemde <10 farklı değer, ≥2 satır; sıralı). Boş = tüm
	// mesaj dışı değerler `<x>` (v0.10.1496 imzası).
	SplitKeys []string `json:"splitKeys,omitempty"`
	// fine — ince gruplar (tüm kategorik adaylar tutulmuş); taban hizalaması
	// (AlignPatternBaseline) farklı SplitKeys'le yeniden birleştirir.
	fine     []*sigAcc
	maxGroup int
	// v0.10.452 (C1) — Page'in dürüstlük zarfı (v0.9.288) buraya taşınır:
	// ES'te Total ≥10k alt sınırdır, kısmi/shard hatası sayıyı eksiltir.
	// "N hata logu" diye başlık atan yüzey bunları okumak zorunda.
	Partial           bool `json:"partial,omitempty"`
	ShardsFailed      int  `json:"shardsFailed,omitempty"`
	TotalIsLowerBound bool `json:"totalIsLowerBound,omitempty"`
}

// GroupBySignature — st.Search üstünden örnekleyerek gruplar. limit ≤ 0 →
// 50; tavan patternsMaxGroups. Filter'ın Limit/Offset/Cursor/WantCursor
// alanları burada YÖNETİLİR (çağıranınki ezilir).
func GroupBySignature(ctx context.Context, st Store, f Filter, limit int) (*PatternsResult, error) {
	return GroupBySignatureN(ctx, st, f, limit, PatternsSampleCap)
}

// GroupBySignatureN — v0.10.452 (C1, ES maliyeti): örnek tavanı çağırandan.
// Problem detayındaki log kanıtı 500 satırla yetinir (1 ES sayfası; 2000 =
// 4 sayfa). 1..PatternsSampleCap'e kıstırılır.
//
// v0.10.1303 — imleç (ES'te point-in-time) YALNIZ arkasından bir sayfa daha
// okunacaksa istenir (patternsPageWantsCursor). Önceden her sayfa
// WantCursor=true gidiyordu: tek sayfalık çağıranlar (desen açıklaması,
// Trend tabanı, explain_log_spike — sampleCap ≤ patternsPageSize) her
// çağrıda PIT açtırıyor (+1 istek) ve dolu sayfa döndüğü için PIT'i 2 dk
// tutturuyordu; çok sayfalı okumanın SON sayfası da aynı sızıntıyı
// yapıyordu. Karar burada, çağıran bayrak geçmez: kaç sayfa okunacağını
// yalnız bu döngü bilir.
func GroupBySignatureN(ctx context.Context, st Store, f Filter, limit, sampleCap int) (*PatternsResult, error) {
	if st == nil {
		return nil, fmt.Errorf("log store not configured")
	}
	if sampleCap <= 0 || sampleCap > PatternsSampleCap {
		sampleCap = PatternsSampleCap
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > patternsMaxGroups {
		limit = patternsMaxGroups
	}
	f.LeanSource = true // v0.10.500 (A4) — örnekleme yalnız body/zaman/severity okur
	f.Offset, f.Cursor, f.Ascending = 0, "", false
	groups := map[string]*sigAcc{} // ince imza → grup
	var fine []*sigAcc             // ilk görülme sırası (deterministik birleşim)
	stats := map[string]*sigEnumStat{}
	res := &PatternsResult{Cap: sampleCap, maxGroup: limit}
	for res.Sampled < sampleCap {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := sampleCap - res.Sampled
		f.Limit = patternsPageSize
		if remaining < f.Limit {
			f.Limit = remaining
		}
		f.WantCursor = patternsPageWantsCursor(remaining, f.Limit)
		page, err := st.Search(ctx, f)
		if err != nil {
			return nil, err
		}
		if page == nil {
			break
		}
		if page.Total > res.Total {
			res.Total = page.Total
		}
		// v0.10.452 — dürüstlük zarfı sayfalar boyunca OR/max.
		res.Partial = res.Partial || page.Partial
		res.TotalIsLowerBound = res.TotalIsLowerBound || page.TotalIsLowerBound
		if page.ShardsFailed > res.ShardsFailed {
			res.ShardsFailed = page.ShardsFailed
		}
		for _, rec := range page.Logs {
			if rec == nil {
				continue
			}
			res.Sampled++
			// v0.10.441 (C4) — kapsanan uç: Sampled'a eşlik eder (imzasız
			// satır da okunmuştur); ts=0 (bozuk doküman) 1970'e çekmesin.
			if ts := rec.Timestamp; ts > 0 {
				if res.CoveredFromNs == 0 || ts < res.CoveredFromNs {
					res.CoveredFromNs = ts
				}
				if ts > res.CoveredToNs {
					res.CoveredToNs = ts
				}
			}
			info := signatureOf(rec.Body)
			sig := info.sig
			if sig == "" {
				continue
			}
			sev, sevText, svcName := patternRowIdentity(rec, info.level, f.Service)
			fineSig := sig
			if info.js != nil {
				fineSig = info.js.render(sigKeepAll)
				countSigEnums(stats, info.js.cats)
			}
			a, ok := groups[fineSig]
			if !ok {
				a = &sigAcc{g: SignatureGroup{
					Template: fineSig, Sample: rec.Body, Severity: sev, SeverityText: sevText,
					FirstSeen: rec.Timestamp, LastSeen: rec.Timestamp,
				}, svcs: map[string]int64{}, query: info.query, js: info.js}
				groups[fineSig] = a
				fine = append(fine, a)
			}
			a.add(rec.Timestamp, sev, sevText, svcName, 1)
		}
		if len(page.Logs) == 0 || page.NextCursor == "" || len(page.Logs) < f.Limit {
			break
		}
		f.Cursor = page.NextCursor
	}
	res.Truncated = res.Sampled >= sampleCap && res.Total > res.Sampled
	if res.Total < res.Sampled {
		res.Total = res.Sampled
	}
	res.SplitKeys = sigEnumKeys(stats)
	res.fine = fine
	res.Groups, res.Distinct = buildPatternGroups(fine, res.SplitKeys, limit)
	return res, nil
}

// v0.10.1504 — kategorik JSON alanları (operatör: "status OK ve FAILED ayrı
// desen olsun"). NormalizeSignature satır başınadır; bir alanın kategorik mi
// yoksa yüksek kardinaliteli mi olduğu ancak ÖRNEKLEMDE görülür. İki geçiş:
// (1) satırlar İNCE imzayla toplanır (her kategorik aday değeriyle), anahtar
// başına farklı değer + satır sayılır; (2) sigEnumKeys'in seçtiği anahtarlar
// dışındaki değerler `<x>`'e çekilip ince gruplar birleştirilir. Maliyet
// O(satır + ince grup), regex yok; 2000 satırda ms düzeyi.
const (
	patternEnumMaxDistinct = 10 // anahtar bu sayıdan AZ farklı değer taşımalı
	patternEnumMinLines    = 2  // ve en az bu kadar satırda görünmeli
)

// sigAcc — bir (ince ya da birleşik) desen grubunun birikimi.
type sigAcc struct {
	g     SignatureGroup
	svcs  map[string]int64
	query string   // v0.10.1496 — JSON gövdede mesaj metni (anahtar kümesi değil)
	js    *sigJSON // nil = düz metin gövde
}

func (a *sigAcc) add(ts int64, sev uint8, sevText, svc string, n int64) {
	a.g.Count += n
	if ts < a.g.FirstSeen {
		a.g.FirstSeen = ts
	}
	if ts > a.g.LastSeen {
		a.g.LastSeen = ts
	}
	if sev > a.g.Severity {
		a.g.Severity, a.g.SeverityText = sev, sevText
	}
	if svc != "" {
		a.svcs[svc] += n
	}
}

// sigEnumStat — anahtar başına örneklem sayacı. vals en çok
// patternEnumMaxDistinct değer tutar (aşınca anahtar elenir; bellek sınırlı).
type sigEnumStat struct {
	lines int
	vals  map[string]struct{}
}

func sigKeepAll(string) bool { return true }

func countSigEnums(stats map[string]*sigEnumStat, cats map[string]string) {
	for k, lit := range cats {
		st := stats[k]
		if st == nil {
			st = &sigEnumStat{vals: map[string]struct{}{}}
			stats[k] = st
		}
		st.lines++
		if len(st.vals) < patternEnumMaxDistinct {
			st.vals[lit] = struct{}{}
		}
	}
}

// sigEnumKeys — SAF: değeri imzada tutulacak anahtarlar (sıralı).
func sigEnumKeys(stats map[string]*sigEnumStat) []string {
	var keys []string
	for k, st := range stats {
		if st.lines >= patternEnumMinLines && len(st.vals) < patternEnumMaxDistinct {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// buildPatternGroups — ince grupları splitKeys'e göre birleştirir, sıralar,
// limit'e kırpar. Dönen ikinci değer kırpmadan önceki grup sayısı. Birleşim
// ince grupların ilk görülme sırasıyla yürür: Sample / Query / eşit
// seviyede SeverityText tek geçişli toplamla aynı satırdan gelir.
func buildPatternGroups(fine []*sigAcc, splitKeys []string, limit int) ([]SignatureGroup, int) {
	keep := make(map[string]bool, len(splitKeys))
	for _, k := range splitKeys {
		keep[k] = true
	}
	keepFn := func(k string) bool { return keep[k] }
	merged := map[string]*sigAcc{}
	order := make([]*sigAcc, 0, len(fine))
	for _, fa := range fine {
		sig := fa.g.Template
		if fa.js != nil {
			sig = fa.js.render(keepFn)
		}
		m, ok := merged[sig]
		if !ok {
			g := fa.g
			g.Template, g.Hash, g.Count = sig, fmt.Sprintf("%016x", SignatureHash(sig)), 0
			g.Services, g.PrevCount, g.Ratio, g.New = nil, 0, 0, false
			m = &sigAcc{g: g, svcs: map[string]int64{}, query: fa.query, js: fa.js}
			merged[sig] = m
			order = append(order, m)
		}
		m.g.Count += fa.g.Count
		if fa.g.FirstSeen < m.g.FirstSeen {
			m.g.FirstSeen = fa.g.FirstSeen
		}
		if fa.g.LastSeen > m.g.LastSeen {
			m.g.LastSeen = fa.g.LastSeen
		}
		if fa.g.Severity > m.g.Severity {
			m.g.Severity, m.g.SeverityText = fa.g.Severity, fa.g.SeverityText
		}
		for n, c := range fa.svcs {
			m.svcs[n] += c
		}
	}
	out := make([]SignatureGroup, 0, len(order))
	for _, a := range order {
		a.g.ServiceCount = len(a.svcs)
		a.g.Query = PatternSearchQuery(a.query)
		type sc struct {
			n string
			c int64
		}
		svc := make([]sc, 0, len(a.svcs))
		for n, c := range a.svcs {
			svc = append(svc, sc{n, c})
		}
		sort.Slice(svc, func(i, j int) bool {
			if svc[i].c != svc[j].c {
				return svc[i].c > svc[j].c
			}
			return svc[i].n < svc[j].n
		})
		for i := 0; i < len(svc) && i < patternsTopSvc; i++ {
			a.g.Services = append(a.g.Services, svc[i].n)
		}
		if a.g.Services == nil {
			a.g.Services = []string{}
		}
		out = append(out, a.g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].Template < out[j].Template
	})
	distinct := len(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, distinct
}

// AlignPatternBaseline — v0.10.1504: taban örneklemesinin SplitKeys'i
// mevcut pencereninkinden farklıysa (kardinalite kararı örnekleme bağlı)
// taban gruplarını mevcut pencerenin anahtarlarıyla ince gruplardan yeniden
// kurar; böylece hash/şablon birleşimi aynı imza uzayında yapılır ve
// "status OK" tabanda başka anahtar kümesiyle yazıldı diye "yeni"
// görünmez. Aynıysa ya da ince grup yoksa base aynen döner (base
// değiştirilmez; dönen yeni bir sonuçtur).
func AlignPatternBaseline(cur, base *PatternsResult) *PatternsResult {
	if cur == nil || base == nil || base.fine == nil || slices.Equal(cur.SplitKeys, base.SplitKeys) {
		return base
	}
	limit := base.maxGroup
	if limit <= 0 {
		limit = patternsMaxGroups
	}
	out := *base
	out.SplitKeys = append([]string(nil), cur.SplitKeys...)
	out.Groups, out.Distinct = buildPatternGroups(base.fine, out.SplitKeys, limit)
	return &out
}

// patternRowIdentity — v0.10.1496 (d), SAF: desen satırının seviye +
// servis kimliği. Operatör (prod ES): Servisler "–", Seviye 0 — hacim
// çubuğu DEBUG 260 derken. (1) severity_number alanı olmayan kurulumda
// sayı metinden türetilir (severityFromText); (2) kayıtta metin yoksa
// JSON gövdenin seviye alanı; (3) servis boşsa ve süzgeç TEK servise
// kilitliyse (ES servis süzgeci tek değer eşler) satır o servise aittir.
func patternRowIdentity(rec *LogRecord, bodyLevel, filterService string) (uint8, string, string) {
	sev, txt, svc := rec.Severity, rec.SeverityText, rec.ServiceName
	if txt == "" && sev == 0 {
		txt = bodyLevel
	}
	if sev == 0 {
		sev = severityFromText(txt)
	}
	if svc == "" && filterService != "" && !strings.Contains(filterService, ",") {
		svc = filterService
	}
	return sev, txt, svc
}

// PatternSearchQuery — bir şablondan, iki backend'in de anladığı arama
// dizesi: yer tutucular atılır, ≥3 karakterlik literal parçalar tırnaklanıp
// AND'lenir (logql / query_string). Yaklaşıktır ve öyle etiketlenir —
// "connection refused to <x>:<x>" → "connection refused to". Boş → "".
func PatternSearchQuery(template string) string {
	// v0.10.310 — Drain şablonları (log_templates) yer tutucuyu "<*>" yazar;
	// tek türetici kalsın diye burada normalize edilir.
	template = strings.ReplaceAll(template, "<*>", "<x>")
	parts := strings.Split(template, "<x>")
	var terms []string
	for i, p := range parts {
		if i > 0 {
			p = dropGluedUnit(p)
		}
		p = strings.TrimSpace(p)
		p = strings.Trim(p, ":;,.-=()[]{}'\"")
		p = strings.TrimSpace(p)
		if len(p) < 3 {
			continue
		}
		terms = append(terms, `"`+strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p)+`"`)
		if len(terms) >= 6 {
			break
		}
	}
	return strings.Join(terms, " AND ")
}

// dropGluedUnit — v0.10.1829: yer tutucuya YAPIŞIK sayı birimi (`<x>MB used`
// → parça "MB used") aramadan atılır: ES standart çözümleyicisi "18MB"ı tek
// belirteç yapar, "MB used" ifadesi hiçbir satırla eşleşmezdi. Yalnız
// MaskVariableTokens'ın koruduğu birimler (numberUnits); başka yapışık
// parçalar (`GrpHdr<*>`) bugünkü gibi kalır.
func dropGluedUnit(p string) string {
	end := strings.IndexFunc(p, unicode.IsSpace)
	word := p
	if end >= 0 {
		word = p[:end]
	}
	if word == "" || !numberUnits[strings.ToLower(word)] {
		return p
	}
	return p[len(word):]
}

// PatternsMaxGroups — v0.10.508 (C6): taban örneklemesinde grup tavanı
// (limit'in kırpması tabanı "yok" göstermesin).
const PatternsMaxGroups = patternsMaxGroups
