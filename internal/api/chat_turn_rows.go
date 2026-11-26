package api

// chat_turn_rows.go — v0.10.2472: CoSRE konuşma belleği. Kartta gösterilen
// satırlar (endpoint seçici, trace listeleri, top-N servis listeleri, servis
// kümesi) yalnız cevabın veri parçasında yaşıyordu; geçmiş modele düz metin
// gidiyor ve defter 5 sayısal olgu tutuyordu → "ikincisini aç", "sonuncusu",
// "onu aç" çözülecek listeyi bulamıyordu.
//
//   - Yakalama: chatStateTurn.wrapEmit her kademenin olayını görür (pick,
//     trace_list, ev_block/block trace_list, step-result); guided rotası servis
//     listesini (route.TeamServices) noteServiceRows ile bırakır. İlk liste
//     kazanır; tavan chatRowsMax. Kimlik varlık modelinden: servis
//     entity.ServiceID, endpoint "svc:<ad>|<rota>", trace 32-hex — ad ile
//     birleştirme yok.
//   - Çözücü (resolveRowOrdinal, SAF): TR + EN sıra sözü ("ilk", "ikincisi",
//     "2.", "sonuncu", "second", "last"); "onu/bunu/şunu aç" yalnız liste tek
//     satırsa ya da bir satır az önce seçildiyse ve sözden başka içerik yoksa.
//     Çözülürse tur o satır adlanmış gibi sürer (endpoint → kartın İncele
//     turu, trace → trace açma, servis → servis kapsamı); çözülmezse bugünkü yol.
//   - Döngü önsözü: chatStateLineTR satırları + adım özetlerini sınırlı
//     (chatRowsPromptMax) bir VERİ bloğu olarak ekler — sistem istemi değişmez.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	chatRowsMax       = 20   // saklanan satır tavanı
	chatStepsMax      = 6    // saklanan adım özeti tavanı
	chatStepRunes     = 120  // adım özeti satırı
	chatRowNameRunes  = 120  // satır adı
	chatRowsPromptMax = 1500 // önsöz bloğunun rün tavanı (küçük yerel model)
	rowPickMaxRunes   = 120  // sıra sözü bundan uzun soruda aranmaz
)

// Satır türleri.
const (
	rowKindEndpoint = "endpoint"
	rowKindService  = "service"
	rowKindTrace    = "trace"
	// v0.10.2473 — top-N operasyon listesi ve pod listesi.
	rowKindOperation = "operation"
	rowKindPod       = "pod"
)

// chatRow — son turun kartta gösterilen bir satırı.
type chatRow struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	ID      string `json:"id"`
	Service string `json:"service,omitempty"`
	RangeS  int64  `json:"rangeS,omitempty"` // endpoint kartının penceresi (İncele turu aynısını taşır)
}

// endpointRowID — SAF: endpoint satırının kararlı kimliği (servis varlığı + rota).
func endpointRowID(service, route string) string {
	return entity.ServiceID(service) + "|" + route
}

// endpointRowParts — SAF: kimlikten servis + rota ("" = endpoint kimliği değil).
func endpointRowParts(id string) (service, route string) {
	head, route, ok := strings.Cut(id, "|")
	if !ok {
		return "", ""
	}
	if service = svcIDName(head); service == "" || strings.TrimSpace(route) == "" {
		return "", ""
	}
	return service, route
}

// ── yakalama ──────────────────────────────────────────────────────────────

// rowsFromEvent — SAF: kademe olayından satırlar (liste taşımıyorsa nil).
func rowsFromEvent(kind string, payload any) []chatRow {
	switch kind {
	case chatPickEventKind:
		return endpointPickRowsJSON(jsonOf(payload))
	case "trace_list":
		return traceListRowsJSON(jsonOf(payload))
	case "ev_block":
		if b, ok := payload.(mcptools.EvidenceBlock); ok && b.Type == blocks.TypeTraceList {
			return traceListRowsJSON(jsonOf(b.Payload))
		}
	case "block":
		if m, ok := payload.(map[string]any); ok && m["type"] == blocks.TypeTraceList {
			return traceListRowsJSON(jsonOf(m["payload"]))
		}
	}
	return nil
}

func jsonOf(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// endpointPickRowsJSON — SAF: data-pick yükü (endpointPickPayload) → satırlar.
func endpointPickRowsJSON(b []byte) []chatRow {
	var p struct {
		Kind    string `json:"kind"`
		Service string `json:"service"`
		RangeS  int64  `json:"rangeS"`
		Items   []struct {
			Route string `json:"route"`
		} `json:"items"`
	}
	if json.Unmarshal(b, &p) != nil || p.Kind != "endpoint" || strings.TrimSpace(p.Service) == "" {
		return nil
	}
	var out []chatRow
	for _, it := range p.Items {
		r := strings.TrimSpace(it.Route)
		if r == "" || len(out) >= chatRowsMax {
			continue
		}
		out = append(out, chatRow{Kind: rowKindEndpoint, Name: clipRunes(r, chatRowNameRunes), ID: endpointRowID(p.Service, r), Service: p.Service, RangeS: p.RangeS})
	}
	return out
}

// traceListRowsJSON — SAF: trace_list yükü → satırlar (yalnız geçerli 32-hex).
func traceListRowsJSON(b []byte) []chatRow {
	var p struct {
		Traces []struct {
			TraceID     string `json:"traceId"`
			ServiceName string `json:"serviceName"`
			RootName    string `json:"rootName"`
		} `json:"traces"`
	}
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	var out []chatRow
	for _, t := range p.Traces {
		if !traceIDRe.MatchString(t.TraceID) || len(out) >= chatRowsMax {
			continue
		}
		name := strings.TrimSpace(t.RootName)
		if t.ServiceName != "" {
			name = strings.TrimSpace(t.ServiceName + " " + name)
		}
		out = append(out, chatRow{Kind: rowKindTrace, Name: clipRunes(name, chatRowNameRunes), ID: strings.ToLower(t.TraceID), Service: t.ServiceName})
	}
	return out
}

// serviceRows — SAF: sıralı servis adları → satırlar (kimlik entity.ServiceID).
func serviceRows(names []string) []chatRow {
	var out []chatRow
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || len(out) >= chatRowsMax {
			continue
		}
		out = append(out, chatRow{Kind: rowKindService, Name: n, ID: entity.ServiceID(n)})
	}
	return out
}

// operationRows — SAF (v0.10.2473): servisin sıralı operasyonları → satırlar.
// Kimlik endpoint ile aynı biçim: servis varlığı + operasyon adı.
func operationRows(service string, ops []string) []chatRow {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil
	}
	var out []chatRow
	for _, op := range ops {
		op = strings.TrimSpace(op)
		if op == "" || len(out) >= chatRowsMax {
			continue
		}
		out = append(out, chatRow{Kind: rowKindOperation, Name: clipRunes(op, chatRowNameRunes), ID: endpointRowID(service, op), Service: service})
	}
	return out
}

// podRowIn — pod listesinin bir satırı. ClusterID + Namespace biliniyorsa
// (Thanos / kube-state) kimlik entity.PodID; yalnız OTel örnek kimliği
// (host_name) varsa servis varlığı + örnek adı.
type podRowIn struct {
	Service, Pod, ClusterID, Namespace string
}

// podRows — SAF (v0.10.2473): gösterilen sırayla pod satırları.
func podRows(in []podRowIn) []chatRow {
	var out []chatRow
	for _, p := range in {
		pod, svc := strings.TrimSpace(p.Pod), strings.TrimSpace(p.Service)
		if pod == "" || len(out) >= chatRowsMax {
			continue
		}
		var id string
		switch {
		case p.ClusterID != "" && p.Namespace != "":
			id = entity.PodID(p.ClusterID, p.Namespace, pod)
		case svc != "":
			id = endpointRowID(svc, pod)
		default:
			continue // kimliksiz satır saklanmaz
		}
		out = append(out, chatRow{Kind: rowKindPod, Name: clipRunes(pod, chatRowNameRunes), ID: id, Service: svc})
	}
	return out
}

// stepSummary — SAF: step-result olayının tek satırlık özeti ("" = adım değil).
func stepSummary(payload any) string {
	var m struct {
		Tool    string `json:"tool"`
		OK      *bool  `json:"ok"`
		Preview string `json:"preview"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(jsonOf(payload), &m) != nil || strings.TrimSpace(m.Tool) == "" {
		return ""
	}
	status, detail := "ok", m.Preview
	if (m.OK != nil && !*m.OK) || m.Error != "" {
		status, detail = "hata", m.Error
	}
	line := strings.TrimSpace(detail)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	s := m.Tool + " · " + status
	if line != "" {
		s += " · " + line
	}
	return clipRunes(s, chatStepRunes)
}

// noteEvent — wrapEmit'in kancası: liste ve adım yakalama (nil-güvenli).
func (t *chatStateTurn) noteEvent(kind string, payload any) {
	if t == nil {
		return
	}
	if kind == "step-result" {
		if s := stepSummary(payload); s != "" && len(t.steps) < chatStepsMax {
			t.steps = append(t.steps, s)
		}
		return
	}
	if len(t.rows) > 0 {
		return // ilk liste kazanır
	}
	if rows := rowsFromEvent(kind, payload); len(rows) > 0 {
		t.rows = rows
	}
}

// noteRows — bundle'ın yapısal listesi (top-N operasyon, pod); ilk liste kazanır.
func (t *chatStateTurn) noteRows(rows []chatRow) {
	if t == nil || len(t.rows) > 0 || len(rows) == 0 {
		return
	}
	t.rows = rows
}

// serviceListIntents — cevabı GÖSTERİLEN sırayla bir servis listesi olan
// guided rotaları; route.TeamServices bu rotalarda cevabın sırasıdır (öteki
// rotalarda yalnız kapsamdır, satır sayılmaz).
var serviceListIntents = map[guidedIntent]bool{
	guidedTopErrorServices: true, guidedMyTopErrors: true, guidedTeamTopErrors: true,
	guidedTeamServices: true, guidedServiceSet: true,
}

// noteRouteRows — guided rotasının sıralı servis listesi (top-N, takım,
// servis kümesi); bu turda olaydan liste gelmediyse.
func (t *chatStateTurn) noteRouteRows(r guidedRoute) {
	if t == nil || len(t.rows) > 0 || !serviceListIntents[r.Intent] {
		return
	}
	t.rows = serviceRows(r.TeamServices)
}

// ── sıra sözü çözücüsü ────────────────────────────────────────────────────

// rowOrdinalStems — TR sıra sözü kökleri (jeton öneki) → 1 tabanlı sıra.
var rowOrdinalStems = []struct {
	stem string
	n    int
}{
	{"birinci", 1}, {"ikinci", 2}, {"üçüncü", 3}, {"ucuncu", 3}, {"dördüncü", 4}, {"dorduncu", 4},
	{"beşinci", 5}, {"besinci", 5}, {"altıncı", 6}, {"altinci", 6}, {"yedinci", 7}, {"sekizinci", 8},
	{"dokuzuncu", 9}, {"onuncu", 10}, {"sonuncu", -1}, {"sondaki", -1},
}

// rowOrdinalEN — EN sıra sözleri (tam jeton).
var rowOrdinalEN = map[string]int{
	"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5, "sixth": 6, "seventh": 7, "eighth": 8, "ninth": 9, "tenth": 10,
}

var (
	rowOrdinalNumRe = regexp.MustCompile(`^(?:#(\d{1,2})|(\d{1,2})(?:\.|st|nd|rd|th))$`)
	rowCountRe      = regexp.MustCompile(`^\d+$`)
)

// rowFillers — sıra sözünün yanındaki dolgu ("2. satır", "son olan", "the last one").
var rowFillers = map[string]bool{
	"satır": true, "satir": true, "satırı": true, "satiri": true, "satırdaki": true, "sıradaki": true, "siradaki": true,
	"olan": true, "olanı": true, "olani": true, "one": true, "row": true, "item": true, "the": true, "en": true,
}

// rowBareWords — tek başına içerik taşımayan eylem / nezaket sözcükleri.
var rowBareWords = map[string]bool{
	"aç": true, "ac": true, "açar": true, "açın": true, "açalım": true, "göster": true, "goster": true, "incele": true,
	"bak": true, "bakalım": true, "bakar": true, "bakın": true, "seç": true, "sec": true, "getir": true, "detay": true,
	"detayı": true, "detayını": true, "detaylarını": true, "mısın": true, "misin": true, "mısınız": true, "misiniz": true,
	"lütfen": true, "peki": true, "şimdi": true, "simdi": true, "o": true, "zaman": true,
	"open": true, "show": true, "pick": true, "select": true, "inspect": true, "check": true, "look": true, "at": true,
	"please": true, "now": true, "ok": true, "then": true,
}

// rowDeixis — "onu aç" zamirleri.
var rowDeixis = map[string]bool{
	"onu": true, "bunu": true, "şunu": true, "sunu": true, "ona": true, "buna": true, "şuna": true, "suna": true,
	"it": true, "that": true, "this": true,
}

// rowTimeWords — "son 2 saat", "last hour" sıra sözü değildir.
var rowTimeWords = map[string]bool{
	"dakika": true, "saat": true, "gün": true, "gun": true, "hafta": true, "ay": true,
	"minute": true, "minutes": true, "hour": true, "hours": true, "day": true, "days": true, "week": true, "weeks": true, "month": true,
}

// rowPickTokens — SAF: soru jetonları (küçük harf; kesme işareti eki birleşir: "ikinci'yi" → "ikinciyi").
func rowPickTokens(q string) []string {
	n := normalizeGuidedMsg(strings.TrimSpace(q))
	n = strings.NewReplacer("'", "", "’", "").Replace(n)
	toks := strings.FieldsFunc(n, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".#-_/%", r))
	})
	for i, t := range toks {
		if !rowOrdinalNumRe.MatchString(t) {
			toks[i] = strings.TrimRight(t, ".")
		}
	}
	return toks
}

// rowOrdinalAt — SAF: i'inci jetonun sıra değeri (0 = sıra sözü değil; -1 = son).
func rowOrdinalAt(toks []string, i int) int {
	tok := strings.TrimRight(toks[i], ".,")
	next := ""
	if i+1 < len(toks) {
		next = strings.TrimRight(toks[i+1], ".,")
	}
	numericNext := rowCountRe.MatchString(next) || rowTimeWords[next]
	if m := rowOrdinalNumRe.FindStringSubmatch(toks[i]); m != nil {
		v, _ := strconv.Atoi(m[1] + m[2])
		if v >= 1 && v <= chatRowsMax && !rowTimeWords[next] {
			return v
		}
		return 0
	}
	if next == "olarak" || next == "önce" || next == "once" { // "ikinci olarak", "ilk önce" — söylem işareti
		return 0
	}
	for _, s := range rowOrdinalStems {
		if strings.HasPrefix(tok, s.stem) {
			return s.n
		}
	}
	switch {
	case tok == "ilk" && !numericNext:
		return 1
	case strings.HasPrefix(tok, "ilki") || strings.HasPrefix(tok, "ilkin"):
		return 1
	case tok == "son" && !numericNext && (next == "" || rowFillers[next] || rowBareWords[next]):
		return -1
	case tok == "last" && !numericNext:
		return -1
	}
	if v := rowOrdinalEN[tok]; v > 0 && !numericNext {
		return v
	}
	return 0
}

// rowPick — çözücünün çıktısı.
type rowPick struct {
	Index int    // 0 tabanlı
	Rest  string // sıra sözü ve dolgu atılmış içerik ("" = çıplak seçim)
}

// resolveRowOrdinal — SAF: soru + önceki turun satırları → seçilen satır.
// picked: önceki turda sıra sözüyle seçilen satır (1 tabanlı; 0 = yok).
// Belirsiz (iki farklı sıra), aralık dışı, uzun soru ya da liste yok → ok=false.
func resolveRowOrdinal(q string, rows []chatRow, picked int) (rowPick, bool) {
	if len(rows) == 0 || utf8.RuneCountInString(strings.TrimSpace(q)) > rowPickMaxRunes {
		return rowPick{}, false
	}
	toks := rowPickTokens(q)
	ord, at := 0, -1
	for i := range toks {
		v := rowOrdinalAt(toks, i)
		if v == 0 {
			continue
		}
		if ord != 0 && v != ord {
			return rowPick{}, false // "birinci ile ikinciyi karşılaştır"
		}
		ord, at = v, i
	}
	deixis := false
	if ord == 0 {
		for i, t := range toks {
			if rowDeixis[t] {
				deixis, at = true, i
				break
			}
		}
		if !deixis {
			return rowPick{}, false
		}
	}
	var rest []string
	for i, t := range toks {
		if i == at || ((i == at-1 || i == at+1) && rowFillers[t]) {
			continue
		}
		rest = append(rest, t)
	}
	bare := true
	for _, t := range rest {
		if !rowBareWords[t] {
			bare = false
			break
		}
	}
	restText := ""
	if !bare {
		restText = strings.Join(rest, " ")
	}
	switch {
	case deixis:
		// "onu aç": yalnız çıplak seçimde ve tek aday varken ("bunu pod bazında
		// göster" kapsam devamıdır, satır seçimi değil).
		if !bare {
			return rowPick{}, false
		}
		switch {
		case picked >= 1 && picked <= len(rows):
			return rowPick{Index: picked - 1}, true
		case len(rows) == 1:
			return rowPick{Index: 0}, true
		}
		return rowPick{}, false
	case ord == -1:
		return rowPick{Index: len(rows) - 1, Rest: restText}, true
	case ord >= 1 && ord <= len(rows):
		return rowPick{Index: ord - 1, Rest: restText}, true
	}
	return rowPick{}, false
}

// rowPickTurn — çözülen satırın turu: yeniden yazılan soru + kapsam.
type rowPickTurn struct {
	Text    string
	Service string // servis kapsamı ("" = değişmez)
	Route   string // endpoint rotası (kartın İncele turu)
	Trace   string // açılacak trace
	RangeS  int64  // endpoint kartının penceresi
	// v0.10.2473
	Operation string // operasyon kapsamı
	Pod       string // pod kapsamı
	Namespace string // pod'un namespace'i (kimlik entity.PodID ise)
}

// rowPickRequest — SAF: satır + kalan içerik → turun isteği. Kimlik varlık
// modelinden okunur (ad ile birleştirme yok); bozuk kimlik → ok=false.
func rowPickRequest(r chatRow, rest string) (rowPickTurn, bool) {
	with := func(head, bare string) string {
		if rest != "" {
			return head + " " + rest
		}
		return head + bare
	}
	switch r.Kind {
	case rowKindEndpoint:
		svc, route := endpointRowParts(r.ID)
		if svc == "" {
			return rowPickTurn{}, false
		}
		return rowPickTurn{Text: with(svc+" "+route, ""), Service: svc, Route: route, RangeS: r.RangeS}, true
	case rowKindTrace:
		if !traceIDRe.MatchString(r.ID) {
			return rowPickTurn{}, false
		}
		return rowPickTurn{Text: with(r.ID, " trace'ini aç"), Trace: r.ID}, true
	case rowKindService:
		ref, ok := entity.ParseID(r.ID)
		if !ok || ref.Type != entity.TypeService || ref.Name == "" {
			return rowPickTurn{}, false
		}
		return rowPickTurn{Text: with(ref.Name, " nasıl?"), Service: ref.Name}, true
	case rowKindOperation:
		svc, op := endpointRowParts(r.ID)
		if svc == "" {
			return rowPickTurn{}, false
		}
		return rowPickTurn{Text: with(svc+" "+op+" operasyonu", " nasıl?"), Service: svc, Operation: op}, true
	case rowKindPod:
		if ref, ok := entity.ParseID(r.ID); ok && ref.Type == entity.TypePod && ref.Name != "" {
			head := ref.Name + " pod'u"
			if r.Service != "" {
				head = r.Service + " " + head
			}
			return rowPickTurn{Text: with(head, " nasıl?"), Service: r.Service, Pod: ref.Name, Namespace: ref.Namespace}, true
		}
		svc, pod := endpointRowParts(r.ID)
		if svc == "" {
			return rowPickTurn{}, false
		}
		return rowPickTurn{Text: with(svc+" "+pod+" pod'u", " nasıl?"), Service: svc, Pod: pod}, true
	}
	return rowPickTurn{}, false
}

// rowLabelTR — SAF: seçim çipinin etiketi.
func rowLabelTR(r chatRow) string {
	switch r.Kind {
	case rowKindEndpoint, rowKindOperation:
		return r.Service + " " + r.Name
	case rowKindPod:
		return "pod " + r.Name
	case rowKindTrace:
		return "trace " + r.ID[:min(8, len(r.ID))] + " (" + r.Name + ")"
	}
	return r.Name
}

// ── döngü önsözü bloğu ────────────────────────────────────────────────────

// chatRowsLineTR — SAF: önceki turun satırları + adım özetleri; toplam
// chatRowsPromptMax rünü aşmaz (taşan satır yazılmaz). Boşsa "".
func chatRowsLineTR(rows []chatRow, steps []string) string {
	var b strings.Builder
	budget := chatRowsPromptMax
	add := func(s string) bool {
		n := utf8.RuneCountInString(s)
		if n > budget {
			return false
		}
		budget -= n
		b.WriteString(s)
		return true
	}
	if len(rows) > 0 && add("\n\nÖNCEKİ TURUN SATIRLARI (kullanıcı «ilki», «ikincisi», «sonuncusu» derse bu sıra):\n") {
		for i, r := range rows {
			line := fmt.Sprintf("- %d. %s %s", i+1, r.Kind, r.Name)
			if r.Kind == rowKindTrace {
				line += " · " + r.ID
			} else if (r.Kind == rowKindEndpoint || r.Kind == rowKindOperation || r.Kind == rowKindPod) && r.Service != "" {
				line = fmt.Sprintf("- %d. %s %s %s", i+1, r.Kind, r.Service, r.Name)
			}
			if !add(line + "\n") {
				break
			}
		}
	}
	if len(steps) > 0 && add("\n\nÖNCEKİ TURUN ADIMLARI:\n") {
		for _, s := range steps {
			if !add("- " + s + "\n") {
				break
			}
		}
	}
	return b.String()
}

// finalRows — SAF: turun sonunda saklanacak satırlar. Yeni liste eskisinin
// yerine geçer; yeni liste yoksa önceki liste yalnız bu tur ondan bir satır
// seçtiyse kalır ("ikincisini aç" → "şimdi üçüncüsü"); aksi hâlde düşer
// (bayat liste beş tur sonra "ilkine bak"ı yanlış satıra çözmesin).
func finalRows(prev, cur []chatRow, picked int) []chatRow {
	switch {
	case len(cur) > 0:
		return cur
	case picked > 0 && len(prev) > 0:
		return prev
	}
	return nil
}

// applyRowPick — sıra sözü önceki turun bir satırına çözülürse tur o satır
// adlanmış gibi sürer: soru yeniden yazılır, kapsam (servis / rota / trace)
// isteğe ve duruma iner. Açık yeni servis, eş ad, düzeltme, öneri kabulü ya
// da soruda rota / trace varken aranmaz. Dönen: adım çipi etiketi ("" = yok).
func (t *chatStateTurn) applyRowPick(res *stateResolveOut, prev ChatContext, req *chatRequest, hasPrior bool) string {
	if t == nil || !hasPrior || len(prev.Rows) == 0 || res.Offer != nil || res.ServiceSet || res.Alias != nil || t.corr != nil {
		return ""
	}
	last := len(req.Messages) - 1
	q := req.Messages[last].Text
	if questionNamesRoute(q) {
		return ""
	}
	pk, ok := resolveRowOrdinal(q, prev.Rows, prev.Picked)
	if !ok {
		return ""
	}
	row := prev.Rows[pk.Index]
	rq, ok := rowPickRequest(row, pk.Rest)
	if !ok {
		return ""
	}
	req.Messages[last].Text = rq.Text
	if rq.Service != "" {
		if !strings.EqualFold(res.Next.Service, rq.Service) {
			res.Next.Operation, res.Next.Trace, res.Next.Filters = "", "", nil
		}
		res.Next.Service, res.Next.Services = rq.Service, nil
		res.ServiceSet, res.InheritService, res.InheritOperation, res.InheritFilters = true, false, false, false
		req.Context.Service = rq.Service
	}
	if rq.Route != "" { // kartın İncele turu: kart yeniden çıkmaz, pencere kartınki
		req.Context.SkipEndpointPick = true
		res.Next.Operation = clipRunes(rq.Route, chatContextOperationMax)
		if rq.RangeS > 0 && !res.WindowSet && !res.InheritWindow {
			req.Context.RangeS = rq.RangeS
		}
	}
	if rq.Operation != "" { // v0.10.2473 — operasyon satırı: operasyon kapsamı
		res.Next.Operation = clipRunes(rq.Operation, chatContextOperationMax)
		req.Context.Operation = rq.Operation
	}
	if rq.Pod != "" { // v0.10.2473 — pod satırı: pod kapsamı
		res.Next.Pod = rq.Pod
		if rq.Namespace != "" {
			res.Next.Namespace = rq.Namespace
		}
	}
	if rq.Trace != "" {
		res.Next = withContextTrace(res.Next, rq.Trace)
		req.Context.Trace = rq.Trace
	}
	t.picked = pk.Index + 1
	return fmt.Sprintf("seçim: önceki listenin %d. satırı → %s", pk.Index+1, rowLabelTR(row))
}
