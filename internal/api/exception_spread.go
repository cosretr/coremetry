package api

// exception_spread.go — v0.10.949 (operatör 2026-09-26: "aynı anda farklı
// servislerden gelmiyorsa 5'ten düşük exception'ı göstermeye gerek yok").
//
// Varsayılan occurrence tabanı 5; aynı exception aynı anda (±StormWindow)
// ≥2 serviste görülüyorsa 5'in altında da görünür kalır. Yayılımın kendisi
// chstore.ExceptionSpread'de (saf çekirdek + 60 sn memo); bu dosya Inbox,
// Inbox rozeti ve /problems Exceptions listesinin ORTAK kablolaması:
// taban hesabı, soft-fail'li okuma ve satır işaretleri.
//
// Açık ?minOcc=N (0 = hepsi dahil) istisna ALMAZ — bugünkü davranış aynen;
// yalnız VARSAYILAN taban istisnalıdır.
//
// v0.10.949 (operatör kararı 2026-09-26) — ikinci istisna: REGRESSED grup
// (resolve edilmiş, sonra yeniden görülmüş → P2 "regressed") 5'in altında
// ve tek serviste de olsa varsayılan görünümde kalır. Çoklu-servis
// istisnasıyla AYNI yollardan geçer: üst (taban) SQL çekimi, taban-altı
// tekilleştirme, Go ayrımı (applyInboxMinOcc), chip sayımları, Inbox rozeti
// ve /problems listesi + gizli sayısı — liste, sayılar ve rozet aynı kümeyi
// görür. Yayılımdan bağımsızdır: yayılım okunamasa da geçerli.

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// spreadWarnAt — soft-fail log'unun hız sınırı (dakikada en çok bir satır).
var spreadWarnAt atomic.Int64

// exceptionSpread — v0.10.949 — filo yayılımı; CH hatasında (ya da hata
// sonrası 30 sn'lik backoff'ta) nil (soft-fail): istisna yok, rozet yok,
// istisnasız taban uygulanır. Liste bir yayılım okuması yüzünden DÜŞMEZ.
// Yanıtlar bunu `spreadAvailable=false` ile söyler — UI "çoklu-servis
// gösterildi / tek serviste gizli" dilini düşürür (gizli sayı o an
// çoklu-servis grupları da içerir).
func (s *Server) exceptionSpread(ctx context.Context) *chstore.ExceptionSpread {
	if s == nil || s.store == nil {
		return nil
	}
	sp, err := s.store.ExceptionSpread(ctx, currentExceptionTriage().StormWindow())
	if err != nil {
		now := time.Now().Unix()
		if last := spreadWarnAt.Load(); now-last >= 60 && spreadWarnAt.CompareAndSwap(last, now) {
			log.Printf("[exception-spread] yayılım okunamadı (istisnasız taban uygulanıyor): %v", err)
		}
		return nil
	}
	return sp
}

// effectiveDefaultFloor — v0.10.949 — varsayılan kipin tabanı:
// min(inboxDefaultMinOcc, P1MinOccurrences). Hacim-P1 kapısı yapışkan
// (v0.10.741) ve ayarlanabilir (1'e kadar): operatör kapıyı 5'in altına
// çekerse o gruplar P1'dir ve varsayılan taban onları GİZLEYEMEZ. SQL'de
// ifade edilebilir olduğu için liste, sayfalama ve rozet aynı kümeyi görür.
// Varsayılan ayarda (500) hiçbir şeyi değiştirmez. BurstMinTotal < 5 ile
// oluşan patlama-P1 bilerek kapsanmıyor: 5'ten az olaylık patlama anlamsız.
func effectiveDefaultFloor(cfg chstore.ExceptionTriageConfig) uint64 {
	floor := uint64(inboxDefaultMinOcc)
	if p := chstore.NormalizeExceptionTriage(cfg).P1MinOccurrences; p > 0 && uint64(p) < floor {
		floor = uint64(p)
	}
	return floor
}

// exceptionIsRegressed — v0.10.949 — "regressed" olgusunun TEK Go kaynağı:
// exception_groups.state = chstore.ExStateRegressed (resolve sonrası yeniden
// görülen grup; chstore.shouldRegress yazar). Öncelik merdiveni
// (exceptionPriorityAt → P2 "regressed") ve varsayılan tabanın istisnası
// (floorExemption; SQL'de ExceptionGroupFilter.FloorExemptRegressed →
// `state = ?`) aynı alana bakar — ikisi ayrışırsa P2 satır tabanda gizlenir.
func exceptionIsRegressed(state string) bool { return state == chstore.ExStateRegressed }

// floorExemption — v0.10.949 — varsayılan tabanın istisna kuralı, Go tarafı.
// SQL karşılığı ExceptionGroupFilter{FloorExemptRegressed, FloorExempt};
// ikisi AYNI kümeyi tanımlar ki liste, chip, rozet ve /problems gizli sayısı
// ayrışmasın. Sıfır değer = istisnasız taban (açık ?minOcc=N, 0 dahil).
type floorExemption struct {
	// regressed — state=regressed satırlar muaf (varsayılan kipte HEP açık,
	// yayılımdan bağımsız).
	regressed bool
	// spread — ExemptBelow'un (SpreadExemptCap'le kırpılmış) kümesi; SQL'e
	// bağlanan listenin aynısı. Spread alanı (UI işareti) üyelik ölçütü DEĞİL.
	spread map[string]struct{}
}

// newFloorExemption — varsayılan kipin istisnası: regressed + verilen
// çoklu-servis listesi (yayılım soft-fail'de nil → yalnız regressed).
func newFloorExemption(spreadFPs []string) floorExemption {
	ex := floorExemption{regressed: true, spread: make(map[string]struct{}, len(spreadFPs))}
	for _, fp := range spreadFPs {
		ex.spread[fp] = struct{}{}
	}
	return ex
}

// Floor exemption reasons (floorExemption.reason). Regressed ÖNCE: regressed
// satır yayılımdan bağımsız görünürdü, yani "çoklu-servis sayesinde
// gösterildi" sayımına (keptBySpread) girmez.
const (
	floorKeptRegressed = "regressed"
	floorKeptSpread    = "spread"
)

// reason — taban-altı exception/httperror satırı neden kalır: "regressed",
// "spread" ya da "" (gizlenir). Tür ayrımı çağıranda (applyInboxMinOcc).
func (e floorExemption) reason(it InboxItem) string {
	if e.regressed && exceptionIsRegressed(it.Status) {
		return floorKeptRegressed
	}
	if it.Exception != nil {
		if _, ok := e.spread[it.Exception.Fingerprint]; ok {
			return floorKeptSpread
		}
	}
	return ""
}

// spreadWindowMin — yanıtta "aynı anda" penceresi (dk); UI metni bunu okur,
// istemci kendi sabitini tutmaz.
func spreadWindowMin() int {
	return chstore.NormalizeExceptionTriage(currentExceptionTriage()).StormWindowMinutes
}

// annotateExceptionSpread — /problems satırlarına yayılım işareti. HER kipte
// (açık taban ve "show all" dahil) çalışır: "başka serviste de oluyor"
// bilgisi tabandan bağımsız bir olgu.
func annotateExceptionSpread(items []chstore.ExceptionGroup, sp *chstore.ExceptionSpread) {
	if sp == nil {
		return
	}
	for i := range items {
		if info, ok := sp.Of(items[i].Fingerprint); ok && info.Services >= 2 {
			items[i].Spread, items[i].SpreadServices = info.Services, info.Partners
		}
	}
}

// annotateInboxSpread — Inbox exception/httperror satırlarına aynı işaret.
// Yalnız UI işareti (v0.10.949): taban istisnası ExemptBelow kümesine bakar,
// bu alana değil. Çağrı tabandan ÖNCE — gizlenecek satır da işaretlenir ama
// zararsız.
func annotateInboxSpread(items []InboxItem, sp *chstore.ExceptionSpread) {
	if sp == nil {
		return
	}
	for i := range items {
		ex := items[i].Exception
		if ex == nil || (items[i].Kind != "exception" && items[i].Kind != "httperror") {
			continue
		}
		if info, ok := sp.Of(ex.Fingerprint); ok && info.Services >= 2 {
			ex.Spread, ex.SpreadServices = info.Services, info.Partners
		}
	}
}
