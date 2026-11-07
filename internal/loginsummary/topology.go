package loginsummary

// topology.go — siluet seçimi, katman sınıflaması, tuzlu düğüm kimliği
// (audit §5.4). Hepsi SAF; ad yalnız sunucuda kullanılır, cevaba yalnız
// tuzlu özet (ve "isimli" kipte ad) çıkar.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// topoMaxNodes — siluetin düğüm tavanı: 300 servis + 60 db/kuyruk.
	// v0.10.1934 (operatör, mockup karşılaştırması: "360"): 180 → 360; prod 1300
	// servisin ~%25'i görünür (önceden ~%11).
	topoMaxNodes = 360
	// topoWindowS — okuma penceresi (15 dk; son kova geç yazılırsa siluet boşalmasın).
	topoWindowS = 900
)

var (
	gwNameRe  = regexp.MustCompile(`(^|[-_.])(gw|gateway|ingress|edge|apigw)([-_.]|$)`)
	bffNameRe = regexp.MustCompile(`(^|[-_.])bff([-_.]|$)`)
	// sysNameRe — db/kuyrukta cevaba çıkabilecek sistem adı (redis, kafka, oracle…).
	sysNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,23}$`)
	// dbLabelRe — v0.10.1931: veritabanı ADI (db.name'in ilk parçası, küçük
	// harf; ör. ordersdb1). Nokta YOK: host/alan adı bu kalıptan geçemez.
	dbLabelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_$-]{0,31}$`)
	// topicRe — v0.10.1931: kuyruk konusu (orders.events gibi). Boşluk, @, :
	// ve / yok — host:port ya da URL konu kılığında geçemez.
	topicRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,47}$`)
	digitsRe = regexp.MustCompile(`^[0-9]+$`)
)

// SilNode — siluet düğümü. Key: kimlik girdisi (servis adı ya da tam çocuk
// kimliği, ör. db:oracle@host — YALNIZ özetlenir, asla çıkmaz). Name: gösterim
// adı (servis adı ya da yalnız sistem adı). Calls: skor (giren+çıkan çağrı).
type SilNode struct {
	Key           string
	Name          string
	Backend       bool // db: / queue:
	Queue         bool // queue: (v0.10.1931 — ayrı katman)
	Calls         uint64
	InDeg, OutDeg int
}

// SilEdge — siluet kenarı (uçlar SilNode.Key).
type SilEdge struct {
	From, To string
	Calls    uint64
}

// Silhouette — seçilmiş alt grafik.
type Silhouette struct {
	Nodes []SilNode
	Edges []SilEdge
}

// backendName — db:/queue: çocuk kimliğinden cevaba çıkabilecek ad. Host /
// instance ASLA çıkmaz. db: sistem adı (veritabanı adı sonradan
// NameDatabases ile gelir). queue: v0.10.1931 (operatör: "isimleri gözüksün")
// — `queue:<sistem>:<konu>` → konu adı; `queue:<sistem>@<host>` ve
// `queue:<sistem>` → sistem adı. Güvenli değilse genel etiket ("db"/"queue").
func backendName(child string) (name string, queue, ok bool) {
	kind, rest := chstore.TopologyNodeIdentity(child)
	switch kind {
	case chstore.NodeKindDB:
		if at := strings.Index(rest, "@"); at > 0 {
			if sys := strings.ToLower(rest[:at]); sysNameRe.MatchString(sys) {
				return sys, false, true
			}
		}
		return "db", false, true
	case chstore.NodeKindQueue:
		at, colon := strings.Index(rest, "@"), strings.Index(rest, ":")
		if colon > 0 && (at < 0 || colon < at) {
			if topic := rest[colon+1:]; topicRe.MatchString(topic) {
				return topic, true, true
			}
			rest = rest[:colon]
		} else if at > 0 {
			rest = rest[:at]
		}
		if sys := strings.ToLower(rest); sysNameRe.MatchString(sys) {
			return sys, true, true
		}
		return "queue", true, true
	}
	return "", false, false
}

// dbLabel — SAF. db.name değerinden gösterilecek veritabanı adı: ilk nokta
// parçası, küçük harf (ORDERSDB1.ACME.LOCAL → ordersdb1). Kalıba uymayan ya da
// yalnız rakam (IP parçası) → "".
func dbLabel(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "."); i >= 0 {
		s = s[:i]
	}
	s = strings.ToLower(s)
	if !dbLabelRe.MatchString(s) || digitsRe.MatchString(s) {
		return ""
	}
	return s
}

// DatabaseKeys — siluetteki veritabanı düğümlerinin `<db_system>@<instance>`
// anahtarları (ad sorgusu yalnız bunları sorar; boşsa sorgu atlanır).
func (s Silhouette) DatabaseKeys() []string {
	var out []string
	for _, n := range s.Nodes {
		if n.Backend && !n.Queue {
			if _, rest := chstore.TopologyNodeIdentity(n.Key); rest != "" {
				out = append(out, rest)
			}
		}
	}
	return out
}

// NameDatabases — SAF. v0.10.1931 (operatör: "database'lerin isimleri
// gözüksün, host adresi değil"): db düğümünün adı sistem adı yerine
// veritabanı adı olur. names anahtarı `<db_system>@<instance>` — topoloji
// kimliğinin önekten sonraki kısmıyla birebir (chstore.LoginDBNames).
// Adı olmayan ya da kalıba uymayan düğüm sistem adında kalır.
func NameDatabases(sil *Silhouette, names map[string]string) {
	for i := range sil.Nodes {
		n := &sil.Nodes[i]
		if !n.Backend || n.Queue {
			continue
		}
		if _, rest := chstore.TopologyNodeIdentity(n.Key); rest != "" {
			if l := dbLabel(names[rest]); l != "" {
				n.Name = l
			}
		}
	}
}

// PickSilhouette — SAF. 1) `ext:` çocuklu kenarlar atılır; 2) servis skoru =
// servis↔servis kenarlarında giren+çıkan çağrı, ilk (maxNodes-maxNodes/6)
// servis; 3) bu servislere bağlı db:/queue: düğümlerinden çağrıya göre ilk
// maxNodes/6; 4) iki ucu da seçili kenarlardan ağırlığa göre ilk 1,5×maxNodes
// (v0.10.1934: 2× idi; 360 düğümde 540 kenar — en zayıf bağlar zaten görünmüyordu,
// kare başına çizgi maliyeti sınırlı kalır).
func PickSilhouette(edges []chstore.ServiceTopologyEdge, maxNodes int) Silhouette {
	if maxNodes <= 0 {
		maxNodes = topoMaxNodes
	}
	dbCap := maxNodes / 6
	svcCap := maxNodes - dbCap

	type agg struct {
		calls   uint64
		in, out int
	}
	svc := map[string]*agg{}
	bk := map[string]*agg{} // anahtar: tam çocuk kimliği
	touch := func(m map[string]*agg, k string) *agg {
		a := m[k]
		if a == nil {
			a = &agg{}
			m[k] = a
		}
		return a
	}
	type rawEdge struct {
		from, to string
		backend  bool
		calls    uint64
	}
	var kept []rawEdge
	for _, e := range edges {
		p, c := e.ParentService, e.ChildNode
		if p == "" || c == "" || p == c || e.Calls == 0 {
			continue
		}
		if isDroppedNode(p) || isDroppedNode(c) || e.NodeKind == chstore.NodeKindExternal {
			continue
		}
		if _, _, isBackend := backendName(c); isBackend {
			kept = append(kept, rawEdge{p, c, true, e.Calls})
			a := touch(svc, p)
			a.out++
			touch(bk, c).calls += e.Calls
			continue
		}
		if kind, _ := chstore.TopologyNodeIdentity(c); kind != "" {
			continue // tanınan ama kullanmadığımız önek (node: …)
		}
		kept = append(kept, rawEdge{p, c, false, e.Calls})
		ap, ac := touch(svc, p), touch(svc, c)
		ap.calls += e.Calls
		ac.calls += e.Calls
		ap.out++
		ac.in++
	}

	// servisler: skora göre ilk svcCap (eşitlikte ada göre — deterministik)
	svcNames := make([]string, 0, len(svc))
	for k := range svc {
		svcNames = append(svcNames, k)
	}
	sort.Slice(svcNames, func(i, j int) bool {
		a, b := svc[svcNames[i]], svc[svcNames[j]]
		if a.calls != b.calls {
			return a.calls > b.calls
		}
		return svcNames[i] < svcNames[j]
	})
	if len(svcNames) > svcCap {
		svcNames = svcNames[:svcCap]
	}
	selSvc := map[string]bool{}
	for _, n := range svcNames {
		selSvc[n] = true
	}

	// db/kuyruk: yalnız seçili bir servise bağlı olanlar, çağrıya göre ilk dbCap
	attached := map[string]uint64{}
	for _, e := range kept {
		if e.backend && selSvc[e.from] {
			attached[e.to] += e.calls
		}
	}
	bkKeys := make([]string, 0, len(attached))
	for k := range attached {
		bkKeys = append(bkKeys, k)
	}
	sort.Slice(bkKeys, func(i, j int) bool {
		if attached[bkKeys[i]] != attached[bkKeys[j]] {
			return attached[bkKeys[i]] > attached[bkKeys[j]]
		}
		return bkKeys[i] < bkKeys[j]
	})
	if len(bkKeys) > dbCap {
		bkKeys = bkKeys[:dbCap]
	}
	selBk := map[string]bool{}
	for _, k := range bkKeys {
		selBk[k] = true
	}

	var out Silhouette
	for _, n := range svcNames {
		a := svc[n]
		out.Nodes = append(out.Nodes, SilNode{Key: n, Name: n, Calls: a.calls, InDeg: a.in, OutDeg: a.out})
	}
	for _, k := range bkKeys {
		name, queue, _ := backendName(k)
		out.Nodes = append(out.Nodes, SilNode{Key: k, Name: name, Backend: true, Queue: queue, Calls: attached[k]})
	}

	// kenarlar: iki ucu da seçili, ağırlığa göre ilk 1,5×maxNodes (aynı çift birleşir)
	pair := map[[2]string]uint64{}
	for _, e := range kept {
		okFrom := selSvc[e.from]
		okTo := (!e.backend && selSvc[e.to]) || (e.backend && selBk[e.to])
		if okFrom && okTo {
			pair[[2]string{e.from, e.to}] += e.calls
		}
	}
	for k, c := range pair {
		out.Edges = append(out.Edges, SilEdge{From: k[0], To: k[1], Calls: c})
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		a, b := out.Edges[i], out.Edges[j]
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	if edgeCap := maxNodes * 3 / 2; len(out.Edges) > edgeCap {
		out.Edges = out.Edges[:edgeCap]
	}
	return out
}

// isDroppedNode — cevaba HİÇBİR kipte girmeyen düğüm önekleri.
func isDroppedNode(id string) bool {
	return strings.HasPrefix(id, "ext:") || strings.HasPrefix(id, "node:")
}

// TierOf — SAF; yalnız katman çıkar, kural adı sunucuda kullanır. Sırayla:
// db/kuyruk → db; ad gw kalıbı → gw; ad bff kalıbı → bff; servis çağıranı
// yok ve çıkış ≥ 3 → gw; diğer → svc.
func TierOf(name string, backend bool, inDeg, outDeg int) string {
	switch {
	case backend:
		return TierDB
	case gwNameRe.MatchString(name):
		return TierGateway
	case bffNameRe.MatchString(name):
		return TierBFF
	case inDeg == 0 && outDeg >= 3:
		return TierGateway
	default:
		return TierService
	}
}

// NewSalt — 16 rastgele bayt (hex). Yalnız ilk kurulumda üretilir; sonra
// anlık görüntüyle taşınır (id'ler tur ve lider değişiminde sabit kalır).
func NewSalt() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand başarısızlığı pratikte yok; sabit olmayan yedek (zaman) kullanma.
		panic("loginsummary: crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// NodeID — hex(sha256(tuz‖anahtar))[:10]. Tuz gizli olduğundan servis adı
// sözlüğüyle çözülemez.
func NodeID(salt, key string) string {
	h := sha256.Sum256([]byte(salt + "\x00" + key))
	return hex.EncodeToString(h[:])[:10]
}

// round2 — iki hane, [0,1] aralığına sıkıştırılmış.
func round2(x float64) float64 {
	if math.IsNaN(x) || x < 0 {
		return 0
	}
	if x > 1 {
		x = 1
	}
	return math.Round(x*100) / 100
}

// BuildTopology — siluet → anlık görüntü topolojisi: tuzlu id, katman,
// log1p-normalize ağırlık (mutlak çağrı sayısı YOK).
func BuildTopology(sil Silhouette, salt string) *SnapTopology {
	if len(sil.Nodes) == 0 {
		return nil
	}
	var maxNode, maxEdge uint64
	for _, n := range sil.Nodes {
		if n.Calls > maxNode {
			maxNode = n.Calls
		}
	}
	for _, e := range sil.Edges {
		if e.Calls > maxEdge {
			maxEdge = e.Calls
		}
	}
	norm := func(c, max uint64) float64 {
		if max == 0 {
			return 0
		}
		return round2(math.Log1p(float64(c)) / math.Log1p(float64(max)))
	}
	t := &SnapTopology{WindowS: topoWindowS}
	for _, n := range sil.Nodes {
		tier := TierOf(n.Name, n.Backend, n.InDeg, n.OutDeg)
		if n.Backend && n.Queue {
			tier = TierQueue
		}
		t.Nodes = append(t.Nodes, SnapNode{
			ID: NodeID(salt, n.Key), Name: n.Name,
			Tier: tier, Weight: norm(n.Calls, maxNode),
		})
	}
	for _, e := range sil.Edges {
		t.Edges = append(t.Edges, Edge{From: NodeID(salt, e.From), To: NodeID(salt, e.To), Weight: norm(e.Calls, maxEdge)})
	}
	return t
}
