package oracle

// coverage.go — v0.10.999 — ÖZNE KAPSAMI raporunun SAF yarısı (Oracle odak
// "2": trace'i Coremetry'de olmayan satırlar servissiz kalıyordu — 2026-09-23
// canlı testinde ~%58).
//
// Soru: "kaynağın hata satırlarının ne kadarı gerçek bir servise bağlanıyor,
// bağlanmayanlar NEDEN bağlanmıyor?" Özne çözücü (subject.go) bunu yalnız
// Problem AÇILIRKEN cevaplar ve cevabı Problem'in notuna yazar; kaynağın
// bütününe dair bir sayı hiçbir yerde yoktu, yani boşluğu kapatacak yöntem
// (pod adı? host adı? elle eşleme?) tahminle seçilecekti. Bu rapor yöntemi
// VERİYE bağlar: çözülmeyen satırları nedene göre böler.
//
// Sınıflama çözücünün kalıcı basamaklarını aynalar (bu-tik trace oyu çevrim
// dışı bilinemez, ama trace'i bulunan her operasyon öğrenilmiş haritaya girer
// — harita o basamağın izidir):
//
//	learned   onaylı harita girdisi (≥3 teyit, ≥%70) ve servis canlı
//	pod       en sık instance_id bir pod adı ve canlı bir servise çözülüyor
//	unresolved → Reason:
//	  dead_service     onaylı servis son 24 saatte canlı değil
//	  multi_service    harita girdisi var, ≥3 oy ama çoğunluk < %70
//	  unconfirmed      harita girdisi var, henüz < 3 teyit
//	  no_operation     operasyon kodu boş (çözücü op'suz satırı çözemez)
//	  no_trace_id      satırlarda trace kimliği hiç yok
//	  trace_not_found  trace kimliği var, haritada iz yok (trace Coremetry'de değil)
//
// v0.10.1000 — FONKSİYON KODU basamağı (fncode.go) raporda ayrı ölçülür:
// yukarıdaki basamaklarla çözülmeyen operasyonların satırlarından, kodu
// span'lerde tek bir serviste toplananlar (PickFunctionService). Kaynakta
// eşleme AÇIKSA bu satırlar "bağlanan"a sayılır; KAPALIYSA yalnız
// RowsFunctionCode olarak raporlanır ("açılırsa şu kadar satır bağlanır") —
// operatör açmadan önce etkisini görür.
//
// alive nil = canlı servis listesi okunamadı: pod basamağı DOĞRULANAMAZ
// (uydurma servis yok), learned canlı varsayılır (çözücüyle aynı yön) ve
// rapor AliveKnown=false der.

import (
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Kapsam durumları ve nedenleri.
const (
	CoverageLearned    = "learned"
	CoveragePod        = "pod"
	CoverageUnresolved = "unresolved"

	ReasonDeadService   = "dead_service"
	ReasonMultiService  = "multi_service"
	ReasonUnconfirmed   = "unconfirmed"
	ReasonNoOperation   = "no_operation"
	ReasonNoTraceID     = "no_trace_id"
	ReasonTraceNotFound = "trace_not_found"
)

// CoverageOp — bir operasyonun kapsam satırı.
type CoverageOp struct {
	Operation string `json:"operation"`
	Rows      uint64 `json:"rows"`
	WithTrace uint64 `json:"withTrace"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Service   string `json:"service,omitempty"` // çözülen (ya da ölü / çoğunluksuz adayın) servisi
	// Votes — harita girdisinin oyları "isabet/toplam" (varsa).
	Votes string `json:"votes,omitempty"`
	// Instance — en sık instance_id; PodLike = pod adı biçiminde (aday üretiyor).
	Instance string `json:"instance,omitempty"`
	PodLike  bool   `json:"podLike,omitempty"`
	Host     string `json:"host,omitempty"`
	// FnRows — bu operasyonun fonksiyon kodundan servise bağlanan satırları;
	// FnService o satırların en çok bağlandığı servis.
	FnRows    uint64 `json:"fnRows,omitempty"`
	FnService string `json:"fnService,omitempty"`
}

// CoverageFn — fonksiyon kodu basamağının girdisi (nil = ölçülmedi).
type CoverageFn struct {
	Enabled bool   // kaynak ayarı functionCodeMatch
	Source  string // okuma yolu: rollup | spans | "" (yok)
	Pairs   []chstore.OracleOpCode
	ByCode  map[string][]chstore.FunctionCodeService
}

// fnOpCoverage — SAF: operasyon → fonksiyon kodundan bağlanan satır + servis.
func fnOpCoverage(fn *CoverageFn) (rows map[string]uint64, service map[string]string) {
	rows, service = map[string]uint64{}, map[string]string{}
	if fn == nil || fn.Source == "" {
		return rows, service
	}
	picks := map[string]string{}
	bySvc := map[string]map[string]uint64{}
	for _, p := range fn.Pairs {
		svc, ok := picks[p.Code]
		if !ok {
			svc = PickFunctionService(fn.ByCode[p.Code]).Service
			picks[p.Code] = svc
		}
		if svc == "" {
			continue
		}
		rows[p.Operation] += p.Rows
		if bySvc[p.Operation] == nil {
			bySvc[p.Operation] = map[string]uint64{}
		}
		bySvc[p.Operation][svc] += p.Rows
	}
	for op, m := range bySvc {
		best, bestN := "", uint64(0)
		for svc, n := range m {
			if n > bestN || (n == bestN && svc < best) {
				best, bestN = svc, n
			}
		}
		service[op] = best
	}
	return rows, service
}

// CoverageReport — kaynağın özne kapsamı.
type CoverageReport struct {
	RowsTotal    uint64 `json:"rowsTotal"`    // pencerenin tamamı
	RowsListed   uint64 `json:"rowsListed"`   // sınıflanan operasyonların satırları (≤ limit op)
	RowsResolved uint64 `json:"rowsResolved"` // bunların servise bağlananı
	RowsLearned  uint64 `json:"rowsLearned"`
	RowsPod      uint64 `json:"rowsPod"`
	OpsTotal     uint64 `json:"opsTotal"`
	OpsListed    int    `json:"opsListed"`
	OpsResolved  int    `json:"opsResolved"`
	// ByReason — çözülmeyen satırlar, nedene göre.
	ByReason map[string]uint64 `json:"byReason"`
	// Unresolved — en çok satırlı çözülmeyen operasyonlar (≤ topN).
	Unresolved []CoverageOp `json:"unresolved"`
	AliveKnown bool         `json:"aliveKnown"`
	// Fonksiyon kodu basamağı (v0.10.1000). FnChecked=false: ölçülmedi.
	// FnSource "" = okuma yolu yok. RowsFunctionCode: diğer basamaklarla
	// çözülmeyen operasyonların fonksiyon kodundan bağlanan satırları —
	// FnEnabled ise RowsResolved'a DAHİL, değilse yalnız potansiyel.
	FnChecked        bool   `json:"fnChecked"`
	FnEnabled        bool   `json:"fnEnabled"`
	FnSource         string `json:"fnSource,omitempty"`
	RowsFunctionCode uint64 `json:"rowsFunctionCode"`
	// FnCodes — pencerede görülen tekil fonksiyon kodu sayısı. 0 = satırlarda
	// fonksiyon kodu yok (ne `code` alanı ona eşli ne FUNCTIONCODE kolonu var).
	FnCodes int `json:"fnCodes"`
}

// classifyCoverageOp — SAF: tek operasyonun durumu (dosya başı).
func classifyCoverageOp(o chstore.OracleOpObs, e *LearnedEntry, alive map[string]bool, now time.Time) CoverageOp {
	c := CoverageOp{Operation: o.Operation, Rows: o.Rows, WithTrace: o.WithTrace, Status: CoverageUnresolved,
		Instance: o.TopInstance, Host: o.TopHost, PodLike: len(podServiceCandidates(o.TopInstance)) > 0}
	if e != nil && e.Total > 0 {
		c.Votes = fmt.Sprintf("%d/%d", e.Hits, e.Total)
	}
	if o.Operation == "" {
		c.Reason = ReasonNoOperation
		return c
	}
	if e != nil && e.Confirmed(now) {
		c.Service = e.Service
		if alive == nil || alive[e.Service] {
			c.Status = CoverageLearned
			return c
		}
		// Onaylı servis ölü: pod adı canlı bir servise çözülüyorsa o kazanır.
		if svc := podService(o.TopInstance, alive); svc != "" {
			c.Status, c.Service = CoveragePod, svc
			return c
		}
		c.Reason = ReasonDeadService
		return c
	}
	if svc := podService(o.TopInstance, alive); svc != "" {
		c.Status, c.Service = CoveragePod, svc
		return c
	}
	switch {
	case e != nil && e.Total > 0:
		// Harita girdisi var ama onaylı değil: yeterli oy toplanmış ve pay
		// %70'in altındaysa operasyon çok servislidir; aksi hâlde henüz az teyit.
		c.Reason, c.Service = ReasonUnconfirmed, e.Service
		if e.Total >= learnedMinHits && float64(e.Hits)/float64(e.Total) < learnedMinShare {
			c.Reason = ReasonMultiService
		}
	case o.WithTrace == 0:
		c.Reason = ReasonNoTraceID
	default:
		c.Reason = ReasonTraceNotFound
	}
	return c
}

// BuildCoverage — SAF (tablo testli): operasyon gözlemleri + öğrenilmiş harita
// + canlı servis kümesi → rapor. obs en çok satırlı önce gelir (chstore);
// Unresolved o sırayı korur ve topN'de kesilir.
func BuildCoverage(obs []chstore.OracleOpObs, totals chstore.OracleOpTotals, learned LearnedMap, alive map[string]bool, now time.Time, topN int, fn *CoverageFn) CoverageReport {
	rep := CoverageReport{RowsTotal: totals.Rows, OpsTotal: totals.Ops, OpsListed: len(obs),
		ByReason: map[string]uint64{}, Unresolved: []CoverageOp{}, AliveKnown: alive != nil}
	if fn != nil {
		rep.FnChecked, rep.FnEnabled, rep.FnSource = true, fn.Enabled, fn.Source
		codes := map[string]bool{}
		for _, p := range fn.Pairs {
			codes[p.Code] = true
		}
		rep.FnCodes = len(codes)
	}
	fnRows, fnSvc := fnOpCoverage(fn)
	var unresolved []CoverageOp
	for _, o := range obs {
		c := classifyCoverageOp(o, learned.Entries[o.Operation], alive, now)
		rep.RowsListed += o.Rows
		switch c.Status {
		case CoverageLearned:
			rep.RowsResolved += o.Rows
			rep.RowsLearned += o.Rows
			rep.OpsResolved++
		case CoveragePod:
			rep.RowsResolved += o.Rows
			rep.RowsPod += o.Rows
			rep.OpsResolved++
		default:
			// Fonksiyon kodundan bağlanan pay (çiftler kodu dolu satırları sayar;
			// operasyon toplamını aşamaz).
			if c.FnRows = fnRows[o.Operation]; c.FnRows > o.Rows {
				c.FnRows = o.Rows
			}
			c.FnService = fnSvc[o.Operation]
			rep.RowsFunctionCode += c.FnRows
			left := o.Rows
			if rep.FnEnabled {
				rep.RowsResolved += c.FnRows
				left -= c.FnRows
				if left == 0 {
					rep.OpsResolved++
					continue
				}
			}
			rep.ByReason[c.Reason] += left
			unresolved = append(unresolved, c)
		}
	}
	sort.SliceStable(unresolved, func(i, j int) bool { return unresolved[i].Rows > unresolved[j].Rows })
	if topN > 0 && len(unresolved) > topN {
		unresolved = unresolved[:topN]
	}
	if unresolved != nil {
		rep.Unresolved = unresolved
	}
	return rep
}
