package anomaly

// subjects.go — v0.10.1791 (Davis paritesi FAZ 1.3, docs/plans/spec-davis-faz1.md):
// anomali tabanının ÖZNE anahtarı ve bütçe planlayıcısı. SAF.
//
// Özne = (tür, sahip, kimlik):
//
//	service  → sahip = servis, kimlik = ""        (bugünkü dedektör; burada yok)
//	endpoint → sahip = servis, kimlik = HTTP rota (VM http_route, karar 2)
//	db       → sahip = kimlik = chstore.DBSubjectID(db_system, instance)
//	queue    → sahip = kimlik = chstore.QueueSubjectID(msg_system, destination)
//
// db/queue'nun sahibi kimliğin kendisi: db Problem'lerinin öznesi deseni
// (problems.service = DBSubjectID). Seçim servis başına (endpoint) / sistem
// başına (db/queue) trafiğe göre üst-N (karar 1). Rota etiketi olmayan servis
// endpoint kümesine hiç girmez ve servis düzeyinde değerlendirilmeye devam
// eder (düşüş = bugünkü davranış).
//
// Bütçe: özne türleri SIRAYLA (endpoint, db, queue) planlanır. Kapalı tür
// sorgu açmaz. Seçilen seri sayısı MaxSeriesPerTick'i aşan tür o tik ATLANIR
// (kesilmez: yarım filo tabanı "hangi servis eksik" sorusunu cevapsız
// bırakır). Endpoint VM sorguları servis yolunun bu tik kullandığından kalan
// payı (vmMaxQueriesPerTick − servis) aşamaz; seçim + en az bir okuma için
// pay yoksa tür atlanır.

import (
	"math"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// subjectKeySep — depo/özne anahtarı ayracı. Unit Separator: rota, servis ve
// veritabanı adlarında görünmez.
const subjectKeySep = "\x1f"

// subjectKey — tabanın özne kimliği; Metric anahtarın üçüncü bileşeni
// (fetch haritaları metrik başına ayrıldığı için burada yok).
type subjectKey struct {
	Kind  string // chstore.AnomalySubject*
	Owner string // servis (endpoint) | özne kimliği (db / queue)
	ID    string // rota | özne kimliği
}

// String — SAF: harita anahtarı.
func (k subjectKey) String() string {
	return k.Kind + subjectKeySep + k.Owner + subjectKeySep + k.ID
}

// parseSubjectKey — String'in tersi.
func parseSubjectKey(s string) (subjectKey, bool) {
	p := strings.Split(s, subjectKeySep)
	if len(p) != 3 || p[0] == "" || p[2] == "" {
		return subjectKey{}, false
	}
	return subjectKey{Kind: p[0], Owner: p[1], ID: p[2]}, true
}

// endpointStoreKey — VM deposundaki endpoint serisinin anahtarı (servis +
// ayraç + rota). Servis düzeyi anahtarlar ayraç taşımaz.
func endpointStoreKey(svc, route string) string { return svc + subjectKeySep + route }

// splitEndpointStoreKey — endpointStoreKey'in tersi; ayraç yoksa ok=false.
func splitEndpointStoreKey(k string) (svc, route string, ok bool) {
	i := strings.Index(k, subjectKeySep)
	if i < 0 {
		return "", "", false
	}
	return k[:i], k[i+len(subjectKeySep):], true
}

// subjectOwner — depo anahtarının servis kısmı (ayraçsız anahtar = kendisi).
// VM görünümünün servis başına kümeleri (durum etiketli servisler) endpoint
// anahtarıyla bunun üzerinden sorgulanır.
func subjectOwner(storeKey string) string {
	if svc, _, ok := splitEndpointStoreKey(storeKey); ok {
		return svc
	}
	return storeKey
}

// subjectVolume — seçim girdisi: grup (endpoint: servis, db/queue: sistem),
// kimlik ve son pencerenin istek hacmi.
type subjectVolume struct {
	Group  string
	ID     string
	Volume float64
}

// pickTopSubjects — SAF: grup başına hacme göre üst-N. Hacmi sıfır/negatif/
// NaN olan aday düşer (trafiksiz özne taban kuramaz). Sıra deterministik:
// grup artan, grup içinde hacim azalan, eşitlikte kimlik artan — iki pod
// aynı girdiden aynı kümeyi seçer.
func pickTopSubjects(vols []subjectVolume, topN int) []subjectVolume {
	if topN < 1 {
		return nil
	}
	cand := make([]subjectVolume, 0, len(vols))
	for _, v := range vols {
		if v.ID == "" || math.IsNaN(v.Volume) || math.IsInf(v.Volume, 0) || v.Volume <= 0 {
			continue
		}
		cand = append(cand, v)
	}
	sort.Slice(cand, func(i, j int) bool {
		if cand[i].Group != cand[j].Group {
			return cand[i].Group < cand[j].Group
		}
		if cand[i].Volume != cand[j].Volume {
			return cand[i].Volume > cand[j].Volume
		}
		return cand[i].ID < cand[j].ID
	})
	out := make([]subjectVolume, 0, len(cand))
	n, group := 0, ""
	for i, v := range cand {
		if i == 0 || v.Group != group {
			group, n = v.Group, 0
		}
		if n < topN {
			out = append(out, v)
			n++
		}
	}
	return out
}

// subjectKindReq — bir özne türünün bu tikteki isteği.
type subjectKindReq struct {
	Kind      string
	Cfg       chstore.AnomalySubjectKindConfig
	Series    int  // seçilen seri sayısı (seçim okumasından); -1 = bilinmiyor
	VM        bool // VM bütçesinden mi yer (endpoint)
	VMNeedMin int  // VM türünün bu tik koşabilmesi için gereken en az sorgu
	// Source — v0.10.1802: aynı türün ikinci kaynağı ("" = türün asıl
	// kaynağı; subjectSourceKafkaLag = queue'nun VM consumer lag'i).
	Source string
}

// subjectKindPlan — planlayıcının kararı.
type subjectKindPlan struct {
	Kind     string `json:"kind"`
	Source   string `json:"source,omitempty"` // v0.10.1802
	Enabled  bool   `json:"enabled"`
	Series   int    `json:"series"`
	VMBudget int    `json:"vmBudget,omitempty"` // endpoint: bu tik harcanabilecek VM sorgusu
	Skipped  bool   `json:"skipped"`
	Reason   string `json:"reason,omitempty"`
}

// Atlanma gerekçeleri (self-obs sayaç anahtarları).
const (
	subjectSkipSeriesCap = "seri tavanı"
	subjectSkipVMBudget  = "VM bütçesi"
	subjectSkipNoSource  = "kaynak yok"
)

// planSubjectBudget — SAF: türleri sırayla değerlendirir. vmLeft = servis
// yolundan kalan VM sorgu payı (vmMaxQueriesPerTick − servis yolunun bu tik
// harcadığı). Kapalı tür Skipped=false, Enabled=false döner (sayaç artmaz).
//
// v0.10.1802 — iki VM türü (endpoint, queue/consumer lag): VM türü kalan payı
// SONRAKİ açık VM türlerinin en az payını (VMNeedMin) ayırarak kullanır —
// endpoint ısınması tüm payı yiyip lag'i her tik "VM bütçesi"yle aç bırakmasın.
func planSubjectBudget(reqs []subjectKindReq, vmLeft int) []subjectKindPlan {
	out := make([]subjectKindPlan, 0, len(reqs))
	for i, r := range reqs {
		p := subjectKindPlan{Kind: r.Kind, Source: r.Source, Enabled: r.Cfg.Enabled, Series: r.Series}
		switch {
		case !r.Cfg.Enabled:
		case r.Series > r.Cfg.MaxSeriesPerTick:
			p.Skipped, p.Reason = true, subjectSkipSeriesCap
		case r.VM:
			avail := vmLeft - subjectVMReserveAfter(reqs, i)
			if avail < r.VMNeedMin {
				p.Skipped, p.Reason = true, subjectSkipVMBudget
				break
			}
			p.VMBudget = avail
			vmLeft -= avail
		}
		out = append(out, p)
	}
	return out
}

// subjectVMReserveAfter — SAF: i'den sonraki açık VM türlerinin en az payı.
func subjectVMReserveAfter(reqs []subjectKindReq, i int) int {
	n := 0
	for _, r := range reqs[i+1:] {
		if r.VM && r.Cfg.Enabled {
			n += r.VMNeedMin
		}
	}
	return n
}

// subjectKindsOrdered — SAF: ayardan sıralı istek iskeleti (endpoint, db,
// queue, queue/consumer lag — v0.10.1802: queue açıkken VM'den Kafka lag'i de).
func subjectKindsOrdered(c chstore.AnomalySubjectsConfig) []subjectKindReq {
	return []subjectKindReq{
		{Kind: chstore.AnomalySubjectEndpoint, Cfg: c.Endpoint, Series: -1, VM: true, VMNeedMin: endpointVMMinQueries},
		{Kind: chstore.AnomalySubjectDB, Cfg: c.DB, Series: -1},
		{Kind: chstore.AnomalySubjectQueue, Cfg: c.Queue, Series: -1},
		{Kind: chstore.AnomalySubjectQueue, Source: subjectSourceKafkaLag, Cfg: c.Queue, Series: -1, VM: true, VMNeedMin: kafkaLagVMMinQueries},
	}
}

// endpointVMMinQueries — endpoint türünün bir tikte anlamlı iş yapması için
// en az VM sorgusu: seçim + bir okuma grubu.
const endpointVMMinQueries = 2
