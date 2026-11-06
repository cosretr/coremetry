package devops

// operation_card.go — v0.10.1506 (plan-operation-card §2.3.2, R4: operasyon
// kartının KOD yarısı). FetchOperationCode'un (v0.10.1330) kardeşi: aynı
// sourceChain (branş, ağaç, çalışan sürüm → commit), aynı süre tavanı
// (operationDeadline ≤15 sn), daha derin ama SINIRLI bir okuma:
//
//   1. Giriş metodu: operasyon kodu varsa bugünkü yol (OperationSearchQuery +
//      RankOperationHits + AnnotatedMethodWindow); yoksa HTTP rotası
//      (RouteSearchSegment → tırnaklı metin araması → RouteMethodWindow ile
//      DOĞRULAMA) ya da RPC span adı (`pkg.Svc/Method` → `SvcImplBase`).
//   2. İmza → istek/cevap DTO'ları: ağaçta `Tip.java` (ücretsiz, birebir ad)
//      → yoksa `class:Tip` kod öğesi filtresi → o da boşsa düz metin.
//      İç içe tipler BİR seviye açılır. Yalnız alan ADI + TİPİ.
//   3. Çağrı yerleri: giriş gövdesi + ≤3 çağrılan (Manager/Service/Facade
//      gövdesi; Client/Gateway için @FeignClient adı + mapping yolu).
//      Çağrılanın dosyası ağaçta yoksa `def:metot` + tip adı araması.
//   4. Eşleme: MatchCallSitesToChildren (SAF) — trace yarısının çocukları
//      her istekte YENİDEN eşlenir; önbellek eşlemesiz sonucu tutar.
//
// İSTEK BÜTÇESİ (operatör kuralı "bounded Azure DevOps calls"): kart başına
// en çok opCardSearchCap (4) arama + opCardFileCap (8) dosya; toplam süre
// operationDeadline. Tavana değen adım dürüst bir notla atlanır (CodePartial).
// Sonuç 10 dk önbellekte (opCardCache), anahtar TÜM girdiler
// (operationCardCacheKey, testli).
//
// GÖVDE NEREYE GİDER. Windows (satır numaralı kaynak) YALNIZ modele
// (R5, localOnly profil); api katmanı tarayıcıya Entry/Request/Response/
// CallSites'ı (konum + ad + tip + literal rota/topic) taşır, Windows'u ASLA
// (TestOperationCardCodePayloadCarriesNoCodeBodies).
//
// Taksonomi DEĞİŞMEZ (code_stats.go pinli): yeni Outcome yok, sayaç ARTMAZ
// (kart bir kod incelemesi değil, sayılmaz). Code Search kapalı = SearchOff
// bayrağı + CodeUnconfigured + dürüst Reason.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	opCardSearchCap   = 4
	opCardFileCap     = 8
	opCardEntryTries  = 2
	opCardDTOFileCap  = 4
	opCardCalleeCap   = 3
	opCardCallSiteCap = 25
	opCardBudgetCap   = 9000
	opCardCacheTTL    = 10 * time.Minute
	opCardCacheMax    = 64
)

// OperationCardSearchOffReason — Code Search kapalıyken kartın tek yazımı.
const OperationCardSearchOffReason = "giriş metodu için Code Search gerekli (Ayarlar → Kod entegrasyonu → Code Search kapalı) — kart trace yarısıyla geçerli"

// OperationCardRequest — FetchOperationCard girdisi. Repo/Hint çağıranın
// servis → depo çözümünden (pin fail-closed → ResolveRepo).
type OperationCardRequest struct {
	Repo    string
	Hint    ProjectHint
	Service string // ham servis adı (sürüm→ref deseni)
	Version string // trace yarısının çoğunluk sürümü; boş = dal ucu
	// Operation — span'den gelen operasyon kodu ("" → rota/span adı).
	Operation string
	// Entry — "http" (Route = http_route, Method = HTTP yöntemi) | "rpc"
	// (Route = span adı).
	Entry         string
	Method, Route string
	Children      []CardChildHint
}

// OperationCardEntry — giriş metodu: konum + imza (gövde YOK).
type OperationCardEntry struct {
	Path      string `json:"path"`
	FromLine  int    `json:"fromLine"`
	ToLine    int    `json:"toLine"`
	Signature string `json:"signature"`
	URL       string `json:"url,omitempty"`
	// Found — giriş nasıl bulundu: operation (op kodu anotasyonu) | route
	// (HTTP mapping) | rpc (gRPC ImplBase metodu).
	Found string `json:"found"`
}

// OperationCardCode — kartın kod yarısı. Windows YALNIZ modele.
type OperationCardCode struct {
	Outcome   CodeOutcome
	Reason    string
	Trimmed   string
	SearchOff bool
	Repo      string
	Branch    string
	BrowseURL string
	Revision  *Revision
	Stats     FetchStats
	Searches  int
	Entry     *OperationCardEntry
	Request   []DTOField
	Response  []DTOField
	CallSites []CallSite
	Windows   []CodeWindow
	Budget    int
}

// clone — derin kopya (önbellek paylaşımı yarışsız).
func (c OperationCardCode) clone() OperationCardCode {
	out := c
	if c.Entry != nil {
		e := *c.Entry
		out.Entry = &e
	}
	if c.Revision != nil {
		r := *c.Revision
		out.Revision = &r
	}
	out.Request = append([]DTOField(nil), c.Request...)
	out.Response = append([]DTOField(nil), c.Response...)
	out.CallSites = append([]CallSite(nil), c.CallSites...)
	out.Windows = append([]CodeWindow(nil), c.Windows...)
	return out
}

// ── önbellek ────────────────────────────────────────────────────────────

type opCardCacheEntry struct {
	v  OperationCardCode
	at time.Time
}

type opCardCacheT struct {
	mu sync.Mutex
	m  map[string]opCardCacheEntry
}

var opCardCaches sync.Map // *Service → *opCardCacheT

func (s *Service) cardCache() *opCardCacheT {
	v, _ := opCardCaches.LoadOrStore(s, &opCardCacheT{})
	return v.(*opCardCacheT)
}

func (c *opCardCacheT) get(key string) (OperationCardCode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Since(e.at) > opCardCacheTTL {
		return OperationCardCode{}, false
	}
	return e.v.clone(), true
}

func (c *opCardCacheT) put(key string, v OperationCardCode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]opCardCacheEntry{}
	}
	if len(c.m) >= opCardCacheMax {
		oldestKey, oldestAt := "", time.Now()
		for k, e := range c.m {
			if e.at.Before(oldestAt) {
				oldestKey, oldestAt = k, e.at
			}
		}
		delete(c.m, oldestKey)
	}
	c.m[key] = opCardCacheEntry{v: v.clone(), at: time.Now()}
}

// operationCardCacheKey — SAF: sonucu değiştiren HER girdi (v0.5.187 dersi):
// sunucu/koleksiyon/proje, depo, OKUNAN ref (sürüm → commit), giriş türü,
// op kodu, yöntem, rota, bütçe. Children anahtarda DEĞİL: eşleme önbellek
// dışında her istekte yeniden yapılır. Serbest metin alanları %q ile
// tırnaklanır ki ayraç taklit edilemesin.
func operationCardCacheKey(cfg Settings, repo string, src RefSpec, req OperationCardRequest, budget int) string {
	return fmt.Sprintf("opcard:v1|%q|%q|%q|%q@%s|e=%q|op=%q|m=%q|r=%q|b=%d",
		cfg.BaseURL, cfg.Collection, cfg.Project, repo, refCacheName(src),
		req.Entry, strings.TrimSpace(req.Operation), strings.ToUpper(strings.TrimSpace(req.Method)), strings.TrimSpace(req.Route), budget)
}

// ── bütçeli okuyucu ─────────────────────────────────────────────────────

var errOpCardCap = errors.New("kart istek tavanı doldu")

type opCardReader struct {
	s   *Service
	ctx context.Context
	// project — v0.10.1885: deponun DevOps projesi (arama süzgeci onsuz 400 döner).
	project  string
	repo     string
	sc       sourceChainResult
	searches int
	fetched  int
	files    map[string]string
}

func (r *opCardReader) search(text string) ([]ServiceCodeHit, error) {
	if r.searches >= opCardSearchCap {
		return nil, errOpCardCap
	}
	r.searches++
	return r.s.SearchServiceCode(r.ctx, r.project, r.repo, text, operationSearchTop)
}

func (r *opCardReader) fetch(path string) (string, error) {
	if b, ok := r.files[path]; ok {
		return b, nil
	}
	if r.fetched >= opCardFileCap {
		return "", errOpCardCap
	}
	r.fetched++
	body, err := r.sc.fetch(r.ctx, path)
	if err != nil {
		return "", err
	}
	body, _ = trimTruncatedBody(body, fileBodyCap)
	r.files[path] = body
	return body, nil
}

// treePath — arama isabeti → okunan ağaçtaki yol ("" = yok).
func (r *opCardReader) treePath(hit string) string {
	if p := treePathFor(r.sc.paths, hit); p != "" {
		return p
	}
	if r.sc.capped {
		return "/" + strings.TrimPrefix(hit, "/")
	}
	return ""
}

// typePathsInTree — SAF: `Tip.java` adlı ağaç yolları (test yolları hariç,
// giriş dosyasına dizince yakın önce).
func typePathsInTree(paths []string, typ, entryPath string) []string {
	var out []string
	for _, p := range CalleePathCandidates(paths, typ, entryPath) {
		if strings.HasSuffix(strings.ToLower(p), "/"+strings.ToLower(typ)+".java") {
			out = append(out, p)
		}
	}
	return out
}

// hitsByBasename — SAF: isabetler içinde dosya adı `name` olanlar önce.
func hitsByBasename(hits []ServiceCodeHit, names ...string) []ServiceCodeHit {
	var first, rest []ServiceCodeHit
	for _, h := range hits {
		if operationHitDropped(h.Path) {
			continue
		}
		base := strings.ToLower(h.Path[strings.LastIndexByte(h.Path, '/')+1:])
		hit := false
		for _, n := range names {
			if base == strings.ToLower(n) {
				hit = true
			}
		}
		if hit {
			first = append(first, h)
		} else {
			rest = append(rest, h)
		}
	}
	return append(first, rest...)
}

// RPCEntryParts — SAF: gRPC span adı `pkg.OrderService/Approve` → (OrderService,
// Approve); biçim değilse ok=false.
func RPCEntryParts(span string) (svc, method string, ok bool) {
	span = strings.TrimSpace(span)
	i := strings.LastIndexByte(span, '/')
	if i <= 0 || i == len(span)-1 {
		return "", "", false
	}
	svc, method = span[:i], span[i+1:]
	if j := strings.LastIndexByte(svc, '.'); j >= 0 {
		svc = svc[j+1:]
	}
	if !ValidCodeIdent(svc) || !ValidCodeIdent(method) {
		return "", "", false
	}
	return svc, method, true
}

func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// FetchOperationCard — v0.10.1506: kartın kod yarısı (bkz. dosya başı).
// FAIL-OPEN: hata dönmez; boş sonuç + Outcome + Reason. Children her
// çağrıda eşlenir (önbellek eşlemesiz tutar).
func (s *Service) FetchOperationCard(ctx context.Context, req OperationCardRequest) OperationCardCode {
	out := s.fetchOperationCard(ctx, req)
	out.CallSites = MatchCallSitesToChildren(out.CallSites, req.Children)
	if out.CallSites == nil {
		out.CallSites = []CallSite{}
	}
	return out
}

func (s *Service) fetchOperationCard(ctx context.Context, req OperationCardRequest) (out OperationCardCode) {
	out = OperationCardCode{Repo: strings.TrimSpace(req.Repo)}
	if s == nil || !s.Configured() {
		out.Outcome, out.Reason = CodeUnconfigured, "kod entegrasyonu yapılandırılmamış (Ayarlar → Kod entegrasyonu)"
		return out
	}
	if !s.CodeSearchEnabled() {
		out.Outcome, out.Reason, out.SearchOff = CodeUnconfigured, OperationCardSearchOffReason, true
		return out
	}
	if out.Repo == "" {
		out.Outcome, out.Reason = CodeRepoUnresolved, "servis için depo çözülemedi"
		return out
	}
	cfg := s.CurrentSettings()
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, s.operationDeadline())
	defer cancel()
	project, _, dead := pickProject(cfg, req.Hint)
	if dead != "" {
		out.Outcome, out.Reason = CodeProjectDeadEnd, dead
		return out
	}
	cfg.Project = project
	version := strings.TrimSpace(req.Version)
	if version != "" && !SourceVersionUsable(version) {
		version = ""
	}
	sc := s.sourceChain(ctx, parent, cfg, out.Repo, req.Service, version)
	ch := sc.chain
	out.Repo, out.Branch = ch.repo, ch.branch
	if ch.class != "" {
		out.Outcome, out.Reason = ch.class, ch.reason
		if deadlineHit(parent, ctx) {
			out.Outcome, out.Reason = CodeDeadline, deadlineReason(s.operationDeadline())
		}
		return out
	}
	out.BrowseURL = BrowseURL(cfg, ch.repo, ch.branch)
	note := ch.note
	if rev := sc.rev; rev != nil {
		out.Revision = rev
		if !rev.Verified {
			note = withNote(note, revisionFallbackNote(rev, ch.branch))
		}
	}
	budget := cfg.codeBudget()
	if budget > opCardBudgetCap {
		budget = opCardBudgetCap
	}
	out.Budget = budget
	key := operationCardCacheKey(cfg, ch.repo, sc.src, req, budget)
	if c, ok := s.cardCache().get(key); ok {
		c.Reason = withNote(note, c.Reason)
		c.Stats.Fetched = 0
		c.Searches = 0
		return c
	}
	rd := &opCardReader{s: s, ctx: ctx, project: cfg.Project, repo: ch.repo, sc: sc, files: map[string]string{}}
	var notes []string
	// transient — bir arama/dosya isteği ağ/sunucu hatasıyla düştü: sonuç
	// önbelleğe YAZILMAZ (geçici bir DevOps arızası 10 dk "bulunamadı"
	// diye saklanmasın; v0.10.1509).
	transient := false
	addNote := func(n string) { notes = append(notes, n) }
	errNote := func(what string, err error) {
		if !errors.Is(err, errOpCardCap) {
			transient = true
		}
		if errors.Is(err, errOpCardCap) {
			addNote(what + ": kart istek tavanı doldu (≤" + fmt.Sprint(opCardSearchCap) + " arama, ≤" + fmt.Sprint(opCardFileCap) + " dosya)")
			return
		}
		addNote(what + ": " + sanitize(firstLine(err.Error()), cfg))
	}
	finish := func(class CodeOutcome) OperationCardCode {
		out.Searches, out.Stats.Fetched = rd.searches, rd.fetched
		out.Outcome = class
		out.Reason = strings.Join(notes, "; ")
		if deadlineHit(parent, ctx) {
			out.Outcome = CodeDeadline
			out.Reason = withNote(out.Reason, deadlineReason(s.operationDeadline()))
		} else if parent.Err() != nil {
			out.Outcome, out.Reason = CodeCancelled, "istek iptal edildi — kod yarısı toplanamadı"
			return out
		}
		if class == CodeOK || class == CodePartial || class == CodeTreeMiss || class == CodeWindowFailed {
			if out.Outcome != CodeDeadline && !transient {
				s.cardCache().put(key, out)
			}
		}
		out.Reason = withNote(note, out.Reason)
		return out
	}

	// ── 1) giriş metodu ──
	var (
		entry     MethodWindow
		entryPath string
		entrySrc  string
		found     string
	)
	tryHits := func(hits []ServiceCodeHit, cut func(src string) (MethodWindow, bool)) bool {
		tries := 0
		for _, h := range hits {
			if tries >= opCardEntryTries {
				break
			}
			p := rd.treePath(h.Path)
			if p == "" {
				continue
			}
			tries++
			body, err := rd.fetch(p)
			if err != nil {
				errNote("giriş dosyası okunamadı", err)
				continue
			}
			if mw, ok := cut(body); ok {
				entry, entryPath, entrySrc = mw, p, body
				return true
			}
		}
		return false
	}
	op := strings.TrimSpace(req.Operation)
	if ValidOperationCode(op) {
		hits, err := rd.search(OperationSearchQuery(op))
		if err != nil {
			errNote("operasyon kodu araması başarısız", err)
		} else if tryHits(RankOperationHits(hits), func(src string) (MethodWindow, bool) {
			return AnnotatedMethodWindow(src, op, operationEntryMaxLines)
		}) {
			found = "operation"
		}
	}
	route := strings.TrimSpace(req.Route)
	if found == "" && req.Entry != "rpc" && route != "" {
		seg := RouteSearchSegment(route)
		q, qerr := BuildCodeQuery([]CodeTerm{{Kind: CodeQueryText, Value: seg}}, "java")
		switch {
		case seg == "" || qerr != nil:
			addNote("rotada aranabilir sabit parça yok (" + NormRoute(route) + ")")
		default:
			hits, err := rd.search(q)
			if err != nil {
				errNote("rota araması başarısız", err)
			} else if tryHits(RankOperationHits(hits), func(src string) (MethodWindow, bool) {
				return RouteMethodWindow(src, req.Method, route, operationEntryMaxLines)
			}) {
				found = "route"
			}
		}
	}
	if found == "" && req.Entry == "rpc" {
		if svc, meth, ok := RPCEntryParts(route); ok {
			q, _ := BuildCodeQuery([]CodeTerm{{Kind: CodeQueryText, Value: svc + "ImplBase"}}, "java")
			hits, err := rd.search(q)
			if err != nil {
				errNote("RPC araması başarısız", err)
			} else if tryHits(RankOperationHits(hits), func(src string) (MethodWindow, bool) {
				return CalleeMethodWindow(src, lowerFirst(meth), operationEntryMaxLines)
			}) {
				found = "rpc"
			}
		} else {
			addNote("RPC girişi Servis/Metot biçiminde değil — giriş metodu aranmadı")
		}
	}
	if found == "" {
		if len(notes) == 0 {
			addNote("giriş metodu bulunamadı: Code Search bu operasyonun handler'ını döndürmedi")
		} else {
			addNote("giriş metodu bulunamadı")
		}
		return finish(CodeTreeMiss)
	}
	sig := MethodSignature(entry.Header)
	out.Entry = &OperationCardEntry{
		Path: entryPath, FromLine: entry.Window.FromLine, ToLine: entry.Window.ToLine,
		Signature: sig, Found: found,
		URL: FileURLAt(cfg, cfg.Project, ch.repo, operationLinkRef(sc.src, ch.branch), entryPath, entry.Window.FromLine),
	}
	ew := entry.Window
	ew.Path, ew.Operation, ew.Frame = entryPath, true, op
	windows := []CodeWindow{ew}
	partial := false
	if entry.Truncated() {
		addNote(fmt.Sprintf("giriş metodu %d satır — ilk %d satır modele verilir", entry.Lines, operationEntryMaxLines))
	}

	// ── 2) DTO'lar ──
	params, ret, _, _ := MethodSignatureTypes(sig)
	reqTypes, respTypes := RequestResponseTypes(params, ret)
	dtoFiles := 0
	visited := map[string]bool{}
	resolveType := func(typ string) ([]DTOField, bool) {
		if f, ok := DTOFields(entrySrc, typ); ok {
			return f, true
		}
		for _, src := range rd.files {
			if f, ok := DTOFields(src, typ); ok {
				return f, true
			}
		}
		if dtoFiles >= opCardDTOFileCap {
			addNote(typ + " okunmadı (DTO dosya tavanı " + fmt.Sprint(opCardDTOFileCap) + ")")
			return nil, false
		}
		var cands []string
		cands = append(cands, typePathsInTree(rd.sc.paths, typ, entryPath)...)
		if len(cands) == 0 {
			for _, terms := range [][]CodeTerm{{{Kind: CodeQueryClass, Value: typ}}, {{Kind: CodeQueryText, Value: typ}}} {
				q, qerr := BuildCodeQuery(terms, "java")
				if qerr != nil {
					break
				}
				hits, err := rd.search(q)
				if err != nil {
					errNote(typ+" araması başarısız", err)
					break
				}
				for _, h := range hitsByBasename(hits, typ+".java") {
					if p := rd.treePath(h.Path); p != "" {
						cands = append(cands, p)
					}
				}
				if len(cands) > 0 {
					break
				}
			}
		}
		if len(cands) > 0 {
			// Yalnız en iyi aday (dizince en yakın): DTO dosya tavanı küçük.
			body, err := rd.fetch(cands[0])
			dtoFiles++
			if err != nil {
				errNote(typ+" dosyası okunamadı", err)
				return nil, false
			}
			if f, ok := DTOFields(body, typ); ok {
				return f, true
			}
		}
		addNote(typ + " tipi depoda bulunamadı")
		return nil, false
	}
	expand := func(types []string) []DTOField {
		fields := []DTOField{}
		var nested []string
		for _, t := range types {
			if visited[t] {
				continue
			}
			visited[t] = true
			f, ok := resolveType(t)
			if !ok {
				partial = true
				continue
			}
			fields = append(fields, f...)
			for _, x := range f {
				if x.Nested {
					nested = append(nested, AppTypes(x.Type)...)
				}
			}
		}
		for _, t := range nested {
			if visited[t] {
				continue
			}
			visited[t] = true
			if f, ok := resolveType(t); ok {
				for i := range f {
					f[i].Nested = false // bir seviye: iç içe tip açılmaz
				}
				fields = append(fields, f...)
			}
		}
		return fields
	}
	out.Request = expand(reqTypes)
	out.Response = expand(respTypes)

	// ── 3) çağrı yerleri ──
	sites := ClassifyCallSites(entry, entryPath)
	// Kuyruk: giriş gövdesinin çağrılanları (önce gövdeli Manager/Service/
	// Facade, sonra Feign adayı Client/Gateway); bir Service gövdesinde
	// görülen Client/Gateway de kuyruğa girer (Feign istemcisi çoğunlukla
	// servis katmanından çağrılır). Toplam okuma ≤ opCardCalleeCap.
	type qItem struct {
		c        Callee
		ownerSrc string // alan tipinin arandığı kaynak
		depth    int
	}
	isClientRecv := func(r string) bool { return strings.HasSuffix(r, "Client") || strings.HasSuffix(r, "Gateway") }
	isBodyRecv := func(r string) bool {
		return strings.HasSuffix(r, "Manager") || strings.HasSuffix(r, "Service") || strings.HasSuffix(r, "Facade")
	}
	var queue []qItem
	enqueue := func(body, ownerSrc string, depth int) {
		cs := ManagerCallees(body, 0)
		for _, c := range cs {
			if isBodyRecv(c.Receiver) && depth == 1 {
				queue = append(queue, qItem{c, ownerSrc, depth})
			}
		}
		for _, c := range cs {
			if isClientRecv(c.Receiver) {
				queue = append(queue, qItem{c, ownerSrc, depth})
			}
		}
	}
	enqueue(entry.Body, entrySrc, 1)
	read, skipped := 0, 0
	seenCallee := map[string]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		c := it.c
		typ, _ := FieldType(it.ownerSrc, c.Receiver)
		if k := typ + "." + c.Method; seenCallee[k] {
			continue
		} else {
			seenCallee[k] = true
		}
		if read >= opCardCalleeCap {
			skipped++
			continue
		}
		read++
		isClient := isClientRecv(c.Receiver)
		var cands []string
		if isClient {
			cands = typePathsInTree(rd.sc.paths, typ, entryPath)
		} else {
			cands = CalleePathCandidates(rd.sc.paths, typ, entryPath)
		}
		if len(cands) == 0 && ValidCodeIdent(c.Method) && ValidCodeIdent(typ) {
			q, qerr := BuildCodeQuery([]CodeTerm{{Kind: CodeQueryDef, Value: c.Method}, {Kind: CodeQueryText, Value: typ}}, "java")
			if qerr == nil {
				hits, err := rd.search(q)
				if err != nil {
					errNote(typ+"."+c.Method+" araması başarısız", err)
				}
				for _, h := range hitsByBasename(hits, typ+"Impl.java", typ+".java") {
					if p := rd.treePath(h.Path); p != "" {
						cands = append(cands, p)
						break
					}
				}
			}
		}
		if len(cands) == 0 {
			partial = true
			addNote("çağrılan " + typ + " dosyası okunan ağaçta yok")
			continue
		}
		cp := cands[0]
		body, err := rd.fetch(cp)
		if err != nil {
			partial = true
			errNote("çağrılan "+typ+" okunamadı", err)
			continue
		}
		if name, path, ok := FeignTarget(body, c.Method); ok {
			for i := range sites {
				if sites[i].Callee == c.Receiver+"."+c.Method {
					sites[i].Service = name
					if sites[i].Target == "" {
						sites[i].Target = path
					}
				}
			}
			continue
		}
		mw, ok := CalleeMethodWindow(body, c.Method, operationCalleeMaxLines)
		if !ok {
			continue
		}
		cw := mw.Window
		segs := strings.Split(cp, "/")
		cw.Path, cw.Operation, cw.Callee, cw.Frame = cp, true, true, strings.TrimSuffix(segs[len(segs)-1], ".java")+"."+c.Method
		windows = append(windows, cw)
		sites = append(sites, ClassifyCallSites(mw, cp)...)
		if it.depth == 1 {
			enqueue(mw.Body, body, 2)
		}
	}
	if skipped > 0 {
		addNote(fmt.Sprintf("%d çağrılan daha okunmadı (tavan %d)", skipped, opCardCalleeCap))
	}
	if len(sites) > opCardCallSiteCap {
		addNote(fmt.Sprintf("%d çağrı yerinden ilk %d'i gösteriliyor", len(sites), opCardCallSiteCap))
		sites = sites[:opCardCallSiteCap]
	}
	linkRef := operationLinkRef(sc.src, ch.branch)
	for i := range sites {
		sites[i].WebURL = FileURLAt(cfg, cfg.Project, ch.repo, linkRef, sites[i].Path, sites[i].Line)
	}
	out.CallSites = sites

	// ── 4) model pencereleri (tarayıcıya GİTMEZ) ──
	clamped, cut := ClampCodeWindows(windows, budget)
	if cut {
		out.Trimmed = fmt.Sprintf("kod bütçesi (%d karakter) doldu — pencereler kısaltıldı", budget)
	}
	stampWindowLinksAt(cfg, ch.repo, ch.branch, sc.src, clamped)
	out.Windows = clamped
	out.Stats.Resolved = len(windows)
	if partial {
		return finish(CodePartial)
	}
	return finish(CodeOK)
}
