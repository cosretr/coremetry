package argocd

// metrics_worker.go — v0.10.983 — ROLLOUTS v2 P3.1: "argocd-metrics" işçisi,
// YALNIZ METRİK (docs/rollouts/v2-audit.md §5, §10.3.3, §10.4, §10.7; karar
// 2026-09-27 "Argo CD: şimdilik yalnız metrik" — Argo CD API'sine bağlanılmaz).
//
// Paketin geri kalanı saftır; bu dosya I/O'yu YALNIZ enjekte edilen
// arayüzlerle yapar (MetricsStore, MetricsQuerier, RegistrySource — üretimde
// *chstore.Store ve metrics_thanos.go adaptörleri), testte sahteler.
//
// ── BAYRAK ───────────────────────────────────────────────────────────────
//
// system_settings["argocd"] enabled=true VE metricsWorker.enabled=true VE en
// az bir hub (v0.10.983 inceleme: ayrı bayrak — P1'de açılmış `enabled`
// deploy'da işçiyi başlatmaz). Varsayılan kapalı:
// main kilidi ve döngüyü WaitActive döndükten SONRA başlatır — sorgu, CH
// okuması, koşu satırı, Redis anahtarı yok. Açıldıktan sonra kapanan bayrakta
// Tick iş yapmaz, bellek düşer (yeniden açılış CH'den kurar).
//
// ── TİK (intervals.metricsS, varsayılan 60 s; yalnız LİDERDE) ────────────
//
// Parça = hub üzerindeki etkin instance (instance namespace'i + job; §5.4
// "shard by instance namespace"). Hub başına injectClusterLabel uygulanır;
// hub Remote Cluster'ı devre dışı/URL'siz ya da tokenRef'i çözülemiyorsa o
// hub'ın parçaları atlanır (kimliksiz istek YOK). Okuma kapsamları örtüşen
// parçaların (aynı Thanos + namespace; küme etiketi ya da job ikisinde de
// dolu ve farklı değilse) HEPSİ atlanır — seriler hangi instance'ındır ayırt
// edilemez (v0.10.983 inceleme; önceden ilki okunur, diğer hub'ın
// uygulamaları onun altında yazılırdı). Parçanın okuma kapsamı (hub, URL, etkin etiket,
// seçici) değişirse bellek yeniden taban ister. En çok 4 parça paralel;
// parçanın bütün sorguları TEK değerlendirme zamanında (tik − 15 s).
//
//   1. Önceki durum: bellekte yoksa argocd_app_status'tan uygulama başına
//      son satır (lider edinimi / yazım hatası / bayrak kapanışı belleği
//      düşürür; §10.3.3 "failover'da ~40k sahte değişiklik basılmasın").
//      lockDegraded sürerken her tik yeniden okunur (başka pod'un yazdıkları).
//   2. Uygulama okuması: tam envanter (dolu taban alınana dek her tik, sonra
//      her intervals.inventoryMin; başarısız/kesik envanter 2ⁿ × aralık geri
//      çekilir) ya da ucuz sabit-olmayan okuma (OutOfSync / Healthy değil /
//      işlem sürüyor). 0 uygulama serisi dönen tam envanter VERİ YOK sayılır
//      (taban da yokluk da işlenmez; inventory_empty).
//   3. Senkron penceresi: değişen + yeni doğan argocd_app_sync_total
//      serileri; artış bellekteki sayaca göre (SyncIncreases).
//   4. Sabit-olmayan tikte hedefli okuma: bellekte sabit olmayan ama bu
//      okumada yok (sabite döndü ya da silindi) + senkronu görülen ama
//      gözlenmeyen uygulamalar, ada göre (≤ 10 sorgu × 50 ad).
//   5. Envanter tikinde tam sayaç okuması: sayaç tabanı. Pencerenin
//      yakalamadığı artış (atlanmış tik) SAYILIR ama YAZILMAZ (sync_missed):
//      zamanı bilinmeyen senkron satırı sınıflandırmayı yanıltırdı. Ardından
//      scrape hedef sağlığı (`up`, aynı seçici, yalnız argocd_app_info üreten
//      job'lar): hedef yok / düşmüş / sayısı azalmış → o envanterde yokluk
//      (deleted) işlenmez; metricsHeldWarn ardışık envanter sürerse partial.
//   Herhangi bir okuma hata / kesik (reader.maxSeries) / uyarılı (kısmi) →
//   o parçanın diff'i ATLANIR, bellek aynen (§5.4 "skip the shard's diff on
//   any warning"); koşu partial.
//   6. DiffStatus → satırlar; liderlik yazımdan hemen önce yeniden doğrulanır
//      (tik sırasında yeniden edinim de yazımı keser) → tek batch.
//   Tik başına TEK rollout_worker_runs satırı (worker="argocd-metrics";
//   ORDER BY (worker, started_at, host) parça başına satırı çökertirdi):
//   scopes = parça, series_read, truncated, partial_response, rows_written,
//   unmapped (cluster_id'si boş yazılan satır), api_calls = sorgu,
//   error = notlar + teşhis sayaçları.
//
//   7. v0.10.985 — EŞLEYİCİ (P3.2; mapper.go mapperStep): her
//      intervals.mapperMin (varsayılan 10 dk), ayar ya da Remote Cluster
//      kaydı değişince hemen; yalnız HAZIR instance'lar (parça atlanmadı,
//      bellek kurulu, son 3 × inventoryMin içinde dolu tam envanter)
//      uzlaştırılır — hazır olmayanın tablodaki kenarlarına dokunulmaz.
//      argocd_app_mapping'e değişen kenarlar yazılır; iş yükü kenarı almayan
//      uygulamalar koşu satırının `unmapped`ına eklenir. Başarısız tur
//      (okuma hatası, tavan, yazım hatası) başarılı tura dek her tik koşuyu
//      partial yapar (mapper_last_round_failed) — sekme kapısı kapalı kalır.
//
// API işçisi (P3.3) ve sınıflandırıcı (P3.4, metrics-only tahmin §7.5) bu
// işçide DEĞİL.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
)

// MetricsStore — işçinin CH kapısı (*chstore.Store karşılar).
type MetricsStore interface {
	// ArgoCDLatestStatuses — instance'ın uygulama başına son satırı (FINAL,
	// keyset, LIMIT 1 BY); son satırı 'deleted' olan HARİÇ; tavan aşımı HATA.
	ArgoCDLatestStatuses(ctx context.Context, instanceID string) ([]StatusRow, error)
	ArgoCDWriteStatuses(ctx context.Context, rows []StatusRow) error
	RecordRolloutWorkerRun(ctx context.Context, run rollout.WorkerRun) error
}

// MetricsQueryResult — bir anlık hub sorgusunun sonucu.
type MetricsQueryResult struct {
	ResultType string
	Result     json.RawMessage
	Series     int
	Truncated  bool
	Partial    bool // en az bir Thanos uyarısı
}

// MetricsQuerier — hub okuyucusu (thanos.WorkerQuery adaptörü). noClusterLabel:
// hub'ın injectClusterLabel=false kararı; lim: argocd reader{} (Normalized).
type MetricsQuerier interface {
	Query(ctx context.Context, hubClusterID, expr string, at time.Time, noClusterLabel bool, lim Reader) (MetricsQueryResult, error)
}

// HubInfo — etkin, URL'li bir Remote Cluster'ın işçinin gördüğü yüzü.
type HubInfo struct {
	ID, Name              string
	URL                   string // parça çakışması denetimi (aynı Thanos)
	LabelName, LabelValue string // etkin küme etiketi (enjeksiyon açıkken)
	TokenBad              bool   // tokenRef tanımlı ama çözülmedi → hub atlanır
}

// Registry — hub'lar + dest_server çözümü.
type Registry struct {
	Hubs map[string]HubInfo // yalnız etkin + URL'li kayıtlar
	// Passive — v0.10.1009: Remote Cluster kaydı DEVRE DIŞI olan kayıtlar
	// (id → ad). Hub listesindeki böyle bir kayıt PASİF hub'dır: instance'ları
	// planlanmaz (hata değil, bilinçli durum — aktif/pasif çift).
	Passive   map[string]string
	ByServer  map[string]string // normalleşmiş apiServerUrl → EffectiveID
	Normalize func(string) string
	// v0.10.985 (P3.2 eşleyici): span cluster değeri → EffectiveID (MV iş
	// yüklerinin kimliği; service_gitops.go bySpan ile aynı kural) ve
	// EffectiveID → argoSuffix (§6 ad ayrıştırma + tutarlılık denetimi).
	BySpan map[string]string
	Suffix map[string]string
}

// RegistrySource — her tik taze Remote Cluster görünümü.
type RegistrySource interface {
	ArgoRegistry() Registry
}

const (
	metricsParallel     = 4
	metricsEvalLag      = 15 * time.Second // parça sorgularının sabit değerlendirme zamanı (rollout v2EvalLag gerekçesi)
	metricsMaxTargeted  = 10               // tik başına hedefli sorgu (× ServiceSelectorCap ad)
	metricsMassHold     = 30 * time.Minute // toplu yokluk dondurmasının üst sınırı
	metricsSyncWindowUp = 5 * time.Minute  // senkron penceresi tavanı (geç yazılan senkron en çok bu kadar geç)
	metricsRunErrorMax  = 2000
	metricsRunNotesMax  = 5
	// metricsHeldWarn — v0.10.983 ikinci inceleme: yokluk bu kadar ardışık
	// envanter hedef sağlığı yüzünden bekletilirse parça partial + not (kalıcı
	// düşmüş hedef silmeyi sessizce sonsuza dek durdurmasın).
	metricsHeldWarn = 3
)

// MetricsActive — SAF bayrak kapısı: enabled VE metricsWorker.enabled VE en
// az bir hub. v0.10.983 inceleme: yalnız `enabled` yetmez — P1/P2'de "yalnız
// bayrağı kaydeder" denerek açılmış bloblar deploy'da işçiyi başlatırdı.
func MetricsActive(s Settings) bool { return s.Enabled && s.MetricsWorker.Enabled && len(s.Hubs) > 0 }

// shardPlan — bir tikte bir instance parçası.
type shardPlan struct {
	Inst     Instance
	Hub      HubInfo
	NoLabel  bool
	Selector string
	// Scope — v0.10.983 inceleme: parçanın OKUDUĞU seri kümesini belirleyen her
	// şey (hub id, normalleşmiş Thanos URL'si, etkin enjekte küme etiketi ya
	// da etiketsiz, seçici). Değişirse bellek yeniden taban ister (processShard).
	Scope string
	Skip  string // doluysa parça bu tikte atlanır (neden)
}

// readScope — bir parçanın okuduğu seri kümesinin çakışma denetimi için
// parçaları: normalleşmiş Thanos URL'si, instance namespace'i, job (boş =
// namespace'in bütün job'ları), etkin enjekte küme etiketi (boş = etiketsiz).
type readScope struct {
	url, ns, job  string
	lname, lvalue string
}

// overlaps — SAF: iki okuma kapsamı ortak seri okuyabilir mi. Ayrık ancak:
// farklı Thanos, farklı namespace, İKİSİ DE etiketli ve aynı etiket adında
// farklı değer, ya da İKİSİ DE job'lu ve farklı job. Boş job / boş etiket
// dolu olanın ÜST kümesidir (v0.10.983 ikinci inceleme — önceden yalnız
// özdeş kapsam yakalanıyordu).
func (a readScope) overlaps(b readScope) bool {
	if a.url != b.url || a.ns != b.ns {
		return false
	}
	if a.lname != "" && b.lname != "" && a.lname == b.lname && a.lvalue != b.lvalue {
		return false
	}
	if a.job != "" && b.job != "" && a.job != b.job {
		return false
	}
	return true
}

// planShards — SAF: ayar + kayıt → parçalar (hub sırası, hub içinde ayar
// sırası). Etkin olmayan instance parça değildir.
//
// v0.10.983 inceleme: okuma kapsamı ÇAKIŞAN parçaların HEPSİ atlanır — birini
// tutmak diğer hub'ın serilerini (aynı Thanos'u etiketsiz okuyan iki hub, aynı
// namespace) o instance_id altında YANLIŞ hub'ın cluster_id'siyle yazardı.
// İkinci inceleme: çakışma = özdeşlik değil ÖRTÜŞME (readScope.overlaps) —
// job'suz seçici job'lunun, etiketsiz okuma etiketlinin üst kümesidir; yalnız
// birine metricsJob ya da etiket vermek ayırmaz.
func planShards(s Settings, reg Registry) []shardPlan {
	var out []shardPlan
	scopes := map[int]readScope{} // out indeksi → okuma kapsamı (atlanmayanlar)
	for _, h := range s.Hubs {
		if _, passive := reg.Passive[h.ClusterID]; passive {
			// v0.10.1009 — pasif hub taranmaz: parça ÜRETİLMEZ (eskiden her
			// instance "hub devre dışı" diye sert atlanıyor, koşu her tik
			// kısmi/başarısız görünüyordu). Özet notu passiveHubNotes yazar.
			continue
		}
		info, ok := reg.Hubs[h.ClusterID]
		for _, inst := range s.Instances {
			if !inst.Enabled || inst.HubClusterID != h.ClusterID {
				continue
			}
			p := shardPlan{Inst: inst, Hub: info, NoLabel: !h.Inject(), Selector: ShardSelector(inst)}
			switch {
			case !ok:
				p.Hub = HubInfo{ID: h.ClusterID, Name: h.ClusterID}
				p.Skip = "hub Remote Cluster'ı devre dışı ya da URL'siz"
			case info.TokenBad:
				p.Skip = "hub tokenRef'i çözülemedi — kimliksiz istek gönderilmez"
			default:
				label := "" // etkin enjeksiyon yok (kapalı ya da kaydın etiketi yok)
				if !p.NoLabel && info.LabelName != "" {
					label = info.LabelName + "=" + info.LabelValue
				}
				url := strings.TrimRight(strings.ToLower(strings.TrimSpace(info.URL)), "/")
				p.Scope = h.ClusterID + "\x00" + url + "\x00" + label + "\x00" + p.Selector
				rs := readScope{url: url, ns: inst.HubNamespace, job: inst.MetricsJob}
				if label != "" {
					rs.lname, rs.lvalue = info.LabelName, info.LabelValue
				}
				scopes[len(out)] = rs
			}
			out = append(out, p)
		}
	}
	for i := range out {
		a, ok := scopes[i]
		if !ok {
			continue
		}
		var ids []string
		for j := range out {
			if b, ok := scopes[j]; ok && a.overlaps(b) {
				ids = append(ids, out[j].Inst.ID)
			}
		}
		if len(ids) > 1 {
			out[i].Skip = fmt.Sprintf("okuma kapsamı %d instance'ta çakışıyor (%s: aynı Thanos + namespace, küme etiketi ya da job ayırmıyor) — hangi serinin hangi instance'a ait olduğu ayırt edilemez, hiçbiri okunmaz; çakışan instance'ların HEPSİNE farklı metricsJob ya da farklı değerli injectClusterLabel verin",
				len(ids), strings.Join(ids, ", "))
		}
	}
	return out
}

// syncWindow — SAF: senkron penceresi. Son başarılı okumadan beri geçen süre
// + bir aralık (en az 2 aralık); tavan max(5 dk, 2 aralık) — daha uzun
// boşluktaki senkron envanterde sync_missed sayılır, geç zamanla yazılmaz.
func syncWindow(last, now time.Time, interval time.Duration) time.Duration {
	w := 2 * interval
	if !last.IsZero() {
		if g := now.Sub(last) + interval; g > w {
			w = g
		}
	}
	ceil := metricsSyncWindowUp
	if 2*interval > ceil {
		ceil = 2 * interval
	}
	if w > ceil {
		w = ceil
	}
	return w
}

// shardMem — instance başına lider belleği.
type shardMem struct {
	loaded        bool
	scope         string // shardPlan.Scope (v0.10.983 inceleme: yalnız seçici değil)
	apps          map[AppKey]AppMem
	invDone       bool // bellek kurulduktan sonra DOLU bir tam envanter alındı
	rebaseline    bool // okuma kapsamı değişti, dolu taban envanteri bekleniyor
	lastInventory time.Time
	invFails      int       // ardışık başarısız (hata/kesik/uyarılı) envanter denemesi
	invFailedAt   time.Time // son başarısız envanter denemesi
	counters      map[SyncKey]float64
	countersOK    bool // tam sayaç tabanı alındı
	lastSyncRead  time.Time
	massSince     time.Time
	liveUp        int             // son envanterdeki sağlam scrape hedefi (up==1) sayısı
	pendingBase   map[AppKey]bool // taban envanterinde belirsiz görülen anahtarlar
	heldStreak    int             // yoklüğu hedef sağlığıyla bekletilen ardışık envanter
}

// inventoryBackoff — SAF (v0.10.983 inceleme): n. ardışık başarısız envanterden
// sonra yeniden deneme beklemesi: 2ⁿ × aralık, tavan inventoryMin. Kesik ya da
// sürekli hata veren parça her tik ~20–30 MB'lık tam envanteri yeniden
// çekmez; arada ucuz sabit-olmayan okuma koşar.
func inventoryBackoff(n int, interval, ceil time.Duration) time.Duration {
	if n <= 0 {
		return 0
	}
	d := interval
	for i := 0; i < n && d < ceil; i++ {
		d *= 2
	}
	if d > ceil {
		d = ceil
	}
	return d
}

// MetricsWorker — argocd_app_status'un TEK yazıcısı.
type MetricsWorker struct {
	store    MetricsStore
	querier  MetricsQuerier
	registry RegistrySource
	settings func() Settings
	leader   func() bool
	degraded func() bool
	host     string
	now      func() time.Time
	parallel int

	inFlight     atomic.Bool
	resetPending atomic.Bool
	mem          map[string]*shardMem // yalnız inFlight altında
	lastRun      atomic.Pointer[rollout.WorkerRun]
	lastStatus   atomic.Pointer[string]

	verMu   sync.Mutex
	lastVer uint64

	// v0.10.985 — eşleyici (mapper.go). mapper nil → adım yok. mapperLast /
	// mapperFP / mapperErr yalnız inFlight altında; edinim ve bayrak
	// kapanışı sıfırlar. mapperErr: son turun hata notu — başarılı tur
	// temizler, o zamana dek her tik koşuyu partial yapar.
	mapper      MapperStore
	mapperLast  time.Time
	mapperFP    string
	mapperErr   string
	mapperErrAt time.Time
}

// NewMetricsWorker — v0.10.983. settings: canlı argocd blobu (SettingsService.Current).
func NewMetricsWorker(store MetricsStore, q MetricsQuerier, reg RegistrySource, settings func() Settings) *MetricsWorker {
	host, _ := os.Hostname()
	if strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	return &MetricsWorker{store: store, querier: q, registry: reg, settings: settings,
		leader: func() bool { return true }, host: host, now: time.Now, parallel: metricsParallel}
}

func (w *MetricsWorker) SetLeaderCheck(f func() bool) { w.leader = f }

// SetDegradedCheck — lockDegraded canlı değeri: doğruyken durum her tik CH'den tazelenir.
func (w *MetricsWorker) SetDegradedCheck(f func() bool) { w.degraded = f }

// OnAcquire — liderlik edinimi: bellek bir sonraki tikte düşer, durum CH'den kurulur.
func (w *MetricsWorker) OnAcquire() { w.resetPending.Store(true) }

// LastRun — son tikin KOPYASI.
func (w *MetricsWorker) LastRun() (rollout.WorkerRun, bool) {
	if p := w.lastRun.Load(); p != nil {
		return *p, true
	}
	return rollout.WorkerRun{}, false
}

// nextVersion — tekdüze açık istemci version'ı (ns; eşitlikte +1).
func (w *MetricsWorker) nextVersion() uint64 {
	w.verMu.Lock()
	defer w.verMu.Unlock()
	v := uint64(w.now().UnixNano())
	if v <= w.lastVer {
		v = w.lastVer + 1
	}
	w.lastVer = v
	return v
}

// WaitActive — bayrak ilk kez açılana dek bekler; true = açıldı, false =
// bağlam kapandı (rollout V2Detector.WaitActive emsali).
func (w *MetricsWorker) WaitActive(ctx context.Context, poll time.Duration) bool {
	if poll <= 0 {
		poll = 30 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return false
		}
		if MetricsActive(w.settings()) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
	}
}

func (w *MetricsWorker) interval() time.Duration {
	return time.Duration(w.settings().Normalized().Intervals.MetricsS) * time.Second
}

// Run — periyodik döngü; ilk tik SetOnAcquire'dan. Uyku ≤ 30 s adımlarla.
func (w *MetricsWorker) Run(ctx context.Context) {
	for {
		start := w.now()
		for {
			if ctx.Err() != nil {
				return
			}
			rem := start.Add(w.interval()).Sub(w.now())
			if rem <= 0 {
				break
			}
			if rem > 30*time.Second {
				rem = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(rem):
			}
		}
		w.Tick(ctx)
	}
}

// Tick — bir tur; örtüşen çağrı atlanır. Döner: koştu mu.
func (w *MetricsWorker) Tick(ctx context.Context) bool {
	if !w.inFlight.CompareAndSwap(false, true) {
		return false
	}
	defer w.inFlight.Store(false)
	s := w.settings()
	if !MetricsActive(s) || !w.leader() {
		w.mem = nil
		w.mapperLast, w.mapperFP, w.mapperErr = time.Time{}, "", ""
		w.resetPending.Store(false)
		return false
	}
	if w.resetPending.Swap(false) {
		w.mem = nil
		w.mapperLast, w.mapperFP, w.mapperErr = time.Time{}, "", ""
	}
	if w.mem == nil {
		w.mem = map[string]*shardMem{}
	}
	set := s.Normalized()
	iv := time.Duration(set.Intervals.MetricsS) * time.Second
	tctx, cancel := context.WithTimeout(ctx, metricsTickDeadline(iv))
	defer cancel()
	w.tick(ctx, tctx, set)
	return true
}

// metricsTickDeadline — SAF: bir tikin zaman bütçesi (5 aralık, en az 2 dk).
func metricsTickDeadline(iv time.Duration) time.Duration {
	if dl := 5 * iv; dl >= 2*time.Minute {
		return dl
	}
	return 2 * time.Minute
}

// passiveHubNotes — v0.10.1009 — SAF: pasif hub başına TEK not ("pasif hub
// hub-2: 190 instance taranmadı") + taranmayan etkin instance toplamı. Not
// koşu durumunu DÜŞÜRMEZ: pasif hub hata değil, operatörün kurduğu durumdur.
func passiveHubNotes(s Settings, reg Registry) ([]string, int) {
	var notes []string
	total := 0
	for _, h := range s.Hubs {
		name, passive := reg.Passive[h.ClusterID]
		if !passive {
			continue
		}
		n := 0
		for _, inst := range s.Instances {
			if inst.Enabled && inst.HubClusterID == h.ClusterID {
				n++
			}
		}
		if name == "" {
			name = h.ClusterID
		}
		notes = append(notes, fmt.Sprintf("pasif hub %s: %d instance taranmadı (Remote Cluster kaydı devre dışı)", name, n))
		total += n
	}
	return notes, total
}

// MetricsRunFreshness — v0.10.985 inceleme — SAF: son argocd-metrics koşusunun
// (started_at) "işçi canlı" sayıldığı en büyük yaş: iki aralık + bir tik
// bütçesi (koşu satırı tik bitince yazılır, started_at taşır). GitOps sekmesi
// eşleyici tablosunu yalnız bundan yeni ve 'ok' bir koşu varken kullanır
// (service_gitops_mapper.go); varsayılan 60 s aralıkta 7 dk.
func MetricsRunFreshness(set Settings) time.Duration {
	iv := time.Duration(set.Normalized().Intervals.MetricsS) * time.Second
	return 2*iv + metricsTickDeadline(iv)
}

type shardResult struct {
	ok         bool
	hard       bool
	calls      int
	series     int
	truncated  bool
	partial    bool
	rows       int
	unmapped   int
	note       string
	diag       map[string]int
	lostLeader bool
}

func (w *MetricsWorker) tick(parent, ctx context.Context, set Settings) {
	run := rollout.WorkerRun{Worker: rollout.WorkerArgoCDMetrics, StartedAt: w.now(), Host: w.host, Status: rollout.RunOK}
	diag := map[string]int{}
	var notes []string
	var canceled bool
	defer func() { w.finish(parent, &run, notes, diag, canceled) }()

	reg := w.registry.ArgoRegistry()
	plans := planShards(set, reg)
	if pn, pc := passiveHubNotes(set, reg); len(pn) > 0 { // v0.10.1009
		notes = append(notes, pn...)
		diag["passive_hub_instances"] = pc
	}
	live := map[string]bool{}
	for _, p := range plans {
		live[p.Inst.ID] = true
	}
	for id := range w.mem {
		if !live[id] {
			delete(w.mem, id) // kaldırılan / devre dışı instance
		}
	}
	run.ScopesTotal = len(plans)
	now := w.now()
	iv := time.Duration(set.Intervals.MetricsS) * time.Second
	changedAt := now.Truncate(iv)
	results := make([]shardResult, len(plans))
	par := w.parallel
	if par <= 0 {
		par = 1
	}
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for i, p := range plans {
		m := w.mem[p.Inst.ID]
		if m == nil {
			m = &shardMem{}
			w.mem[p.Inst.ID] = m
		}
		wg.Add(1)
		go func(i int, p shardPlan, m *shardMem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = w.processShard(ctx, set, p, reg, now, changedAt, m)
		}(i, p, m)
	}
	wg.Wait()

	okN, hardN, softN := 0, 0, 0
	for i, r := range results {
		run.APICalls += r.calls
		run.SeriesRead += r.series
		run.Truncated = run.Truncated || r.truncated
		run.PartialResponse = run.PartialResponse || r.partial
		run.RowsWritten += r.rows
		run.Unmapped += r.unmapped
		for k, v := range r.diag {
			diag[k] += v
		}
		switch {
		case r.ok:
			okN++
		case r.hard:
			hardN++
		default:
			softN++
		}
		if r.note != "" {
			hub := plans[i].Hub.Name
			if hub == "" {
				hub = plans[i].Hub.ID
			}
			notes = append(notes, hub+"/"+plans[i].Inst.ID+": "+r.note)
		}
	}
	run.ScopesOK = okN
	run.Status = metricsRunStatus(len(plans), okN, hardN, softN)
	if note := w.mapperStep(ctx, set, reg, plans, results, now, &run, diag); note != "" {
		notes = append(notes, "eşleyici: "+note)
		if run.Status == rollout.RunOK {
			run.Status = rollout.RunPartial
		}
	}
	canceled = errors.Is(ctx.Err(), context.Canceled) && parent.Err() != nil
}

// metricsRunStatus — SAF: parça sonuçları → koşu durumu (rollout v2RunStatus
// kuralı): hepsi tamam → ok; hiçbiri tamam değil ve hepsi sert → failed;
// aksi → partial. Parça yoksa ok (koşu notu "etkin instance yok" der).
func metricsRunStatus(total, okN, hardN, softN int) string {
	switch {
	case total == 0 || okN == total:
		return rollout.RunOK
	case okN == 0 && softN == 0 && hardN > 0:
		return rollout.RunFailed
	default:
		return rollout.RunPartial
	}
}

func (w *MetricsWorker) finish(parent context.Context, run *rollout.WorkerRun, notes []string, diag map[string]int, canceled bool) {
	run.FinishedAt = w.now()
	if run.FinishedAt.Before(run.StartedAt) {
		run.FinishedAt = run.StartedAt
	}
	run.DurationMs = int(run.FinishedAt.Sub(run.StartedAt) / time.Millisecond)
	if canceled && run.Status != rollout.RunOK {
		run.Status = rollout.RunSkipped
		notes = append([]string{"kapanış — tik yarıda kesildi"}, notes...)
	}
	if run.ScopesTotal == 0 {
		notes = append(notes, "etkin instance yok (hubs[] üzerindeki instances[].enabled)")
	}
	run.Error = metricsRunErrorText(notes, diag)
	cp := *run
	w.lastRun.Store(&cp)
	prev := ""
	if p := w.lastStatus.Load(); p != nil {
		prev = *p
	}
	if run.Status != prev && run.Status != rollout.RunOK {
		log.Printf("[argocd-metrics] tik %s: %s", run.Status, run.Error)
	}
	st := run.Status
	w.lastStatus.Store(&st)
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer rcancel()
	if err := w.store.RecordRolloutWorkerRun(rctx, *run); err != nil {
		log.Printf("[argocd-metrics] koşu kaydı: %v", err)
	}
}

// metricsRunErrorText — SAF: notlar (en çok 5) + teşhis sayaçları → error kolonu.
func metricsRunErrorText(notes []string, diag map[string]int) string {
	var parts []string
	if len(notes) > 0 {
		shown := notes
		if len(shown) > metricsRunNotesMax {
			shown = append(append([]string(nil), notes[:metricsRunNotesMax]...), fmt.Sprintf("… (+%d)", len(notes)-metricsRunNotesMax))
		}
		parts = append(parts, strings.Join(shown, " | "))
	}
	var codes []string
	for k, v := range diag {
		if v != 0 {
			codes = append(codes, k)
		}
	}
	sort.Strings(codes)
	if len(codes) > 0 {
		kv := make([]string, len(codes))
		for i, k := range codes {
			kv[i] = fmt.Sprintf("%s=%d", k, diag[k])
		}
		parts = append(parts, "teşhis: "+strings.Join(kv, " "))
	}
	out := strings.Join(parts, " || ")
	if r := []rune(out); len(r) > metricsRunErrorMax {
		out = string(r[:metricsRunErrorMax-1]) + "…"
	}
	return out
}

// errNotUsable — kesik ya da kısmi okuma (parça atlanır, sert hata değil).
var errNotUsable = errors.New("kesik/kısmi okuma")

func (w *MetricsWorker) processShard(ctx context.Context, set Settings, p shardPlan, reg Registry, now, changedAt time.Time, mem *shardMem) shardResult {
	res := shardResult{diag: map[string]int{}}
	if p.Skip != "" {
		res.hard, res.note = true, p.Skip
		res.diag["shard_skipped"]++
		return res
	}
	inst := p.Inst
	if mem.loaded && mem.scope != p.Scope {
		// v0.10.983 inceleme: hub, Thanos URL'si, etkin küme etiketi
		// (injectClusterLabel / kayıt etiketi) ya da namespace/job değişti:
		// aynı instance_id, BAŞKA seri kümesi. Envanter, sayaç tabanı ve hedef
		// sayısı yeniden alınır; uygulama belleği korunur ama ilk dolu envanter
		// taban sayılır (bilinmeyen → baseline, farklı tuple → baseline; sahte
		// 'appeared'/'state' yok).
		mem.invDone, mem.countersOK, mem.counters, mem.rebaseline = false, false, nil, true
		mem.lastSyncRead, mem.massSince, mem.liveUp, mem.pendingBase = time.Time{}, time.Time{}, 0, nil
		mem.invFails, mem.invFailedAt = 0, time.Time{}
		res.diag["scope_changed"]++
	}
	mem.scope = p.Scope
	refresh := mem.loaded && w.degraded != nil && w.degraded()
	if !mem.loaded || refresh {
		rows, err := w.store.ArgoCDLatestStatuses(ctx, inst.ID)
		if err != nil {
			res.hard, res.note = true, "önceki durum okunamadı: "+err.Error()
			return res
		}
		apps := make(map[AppKey]AppMem, len(rows))
		for _, r := range rows {
			m := AppMem{Row: r, Deleted: r.ChangeKind == ChangeDeleted}
			if old, ok := mem.apps[r.Key()]; refresh && ok && old.Row.Version == r.Version {
				m.Absent = old.Absent // yokluk sayacı yerel bilgi; aynı satırsa korunur
			}
			apps[r.Key()] = m
		}
		mem.apps = apps
		if refresh {
			res.diag["state_refreshed_degraded"]++
		} else {
			if len(rows) == 0 {
				res.diag["first_run"]++
			}
			mem.loaded, mem.invDone, mem.countersOK = true, false, false
			mem.counters, mem.lastSyncRead, mem.massSince = map[SyncKey]float64{}, time.Time{}, time.Time{}
			mem.liveUp, mem.pendingBase, mem.invFails, mem.invFailedAt = 0, nil, 0, time.Time{}
			res.diag["state_rebuilt"]++
		}
	}

	at := now.Add(-metricsEvalLag)
	iv := time.Duration(set.Intervals.MetricsS) * time.Second
	invMin := time.Duration(set.Intervals.InventoryMin) * time.Minute
	invDue := !mem.invDone || now.Sub(mem.lastInventory) >= invMin
	inventory := invDue && (mem.invFails == 0 || now.Sub(mem.invFailedAt) >= inventoryBackoff(mem.invFails, iv, invMin))
	if invDue && !inventory {
		res.diag["inventory_backoff"]++
	}
	query := func(expr string) (MetricsQueryResult, error) {
		r, err := w.querier.Query(ctx, p.Hub.ID, expr, at, p.NoLabel, set.Reader)
		res.calls++
		if err != nil {
			return r, err
		}
		res.series += r.Series
		res.truncated = res.truncated || r.Truncated
		res.partial = res.partial || r.Partial
		if r.Truncated || r.Partial {
			return r, errNotUsable
		}
		return r, nil
	}
	fail := func(what string, err error) shardResult {
		if inventory {
			// Başarısız envanter denemesi: yeniden deneme geri çekilir
			// (inventoryBackoff); arada ucuz okuma.
			mem.invFails++
			mem.invFailedAt = now
		}
		if errors.Is(err, errNotUsable) {
			// §5.4: kesik/uyarılı okumadan fark alınmaz; bellek aynen.
			if res.truncated {
				res.diag["shard_truncated"]++
				res.note = what + ": sonuç reader.maxSeries'te kesildi — bu tikte fark alınmadı (parça dest_server'a bölünmeli, §5.4 / H1.6)"
			} else {
				res.diag["shard_partial"]++
				res.note = what + ": Thanos kısmi yanıt (uyarı) — bu tikte fark alınmadı"
			}
			return res
		}
		res.hard, res.note = true, what+": "+err.Error()
		res.diag["fetch_error"]++
		return res
	}

	appQ, what := NonSteadyQuery(p.Selector), "sabit olmayan okuma"
	if inventory {
		appQ, what = InventoryQuery(p.Selector), "envanter"
	}
	ar, err := query(appQ)
	if err != nil {
		return fail(what, err)
	}
	obs, err := ParseShardApps(inst.ID, inst.HubNamespace, ar.ResultType, ar.Result)
	if err != nil {
		return fail(what, err)
	}
	if inventory && len(obs.Apps) == 0 && len(obs.Ambiguous) == 0 {
		// v0.10.983 inceleme: 0 uygulama serisi dönen tam envanter VERİ YOK
		// sayılır, "hepsi silindi" ya da "taban alındı" değil: scrape kopması
		// (controller çöktü, ServiceMonitor bozuk), yanlış injectClusterLabel
		// (§11 H0.3/H0.5) ya da metrikler henüz yok. Bellek, sayaç tabanı ve
		// taban durumu AYNEN — sonraki dolu envanter hâlâ 'baseline' yazar,
		// bilinen uygulamaların yokluğu sayılmaz. Boş sonuç ucuzdur: geri
		// çekilme yok, sonraki tik yeniden dener.
		res.diag["inventory_empty"]++
		res.note = "tam envanter boş (0 uygulama serisi) — veri yok sayıldı, taban/yokluk işlenmedi (scrape, injectClusterLabel ya da seçiciyi doğrulayın; §11 H0.3/H0.5)"
		return res
	}
	sr, err := query(SyncWindowQuery(p.Selector, syncWindow(mem.lastSyncRead, now, iv)))
	if err != nil {
		return fail("senkron penceresi", err)
	}
	curW, err := ParseSyncCounters(inst.HubNamespace, sr.ResultType, sr.Result)
	if err != nil {
		return fail("senkron penceresi", err)
	}
	inc, sdiag := SyncIncreases(mem.counters, curW, mem.countersOK)
	for k, v := range sdiag {
		res.diag[k] += v
	}
	phases := SyncPhases(inst.ID, inc)

	if !inventory {
		var names []string
		for k, m := range mem.apps {
			if _, seen := obs.Apps[k]; !seen && !m.Deleted && !obs.Ambiguous[k] && m.Row.Tuple().NonSteady() {
				names = append(names, k.Name)
			}
		}
		for k := range phases {
			if _, seen := obs.Apps[k]; !seen && !obs.Ambiguous[k] {
				names = append(names, k.Name)
			}
		}
		qs, rest := TargetedQueries(p.Selector, names, metricsMaxTargeted)
		if len(rest) > 0 {
			res.diag["targeted_capped"] += len(rest)
		}
		for _, q := range qs {
			tr, err := query(q)
			if err != nil {
				return fail("hedefli okuma", err)
			}
			part, err := ParseShardApps(inst.ID, inst.HubNamespace, tr.ResultType, tr.Result)
			if err != nil {
				return fail("hedefli okuma", err)
			}
			obs.Merge(part)
		}
		if len(qs) > 0 {
			res.diag["targeted_reads"] += len(qs)
		}
	}
	// Tuple'ı bilinmeyen senkron satırı yazılmaz: gözlenmeyen uygulamanın
	// artışı ERTELENİR — sayacı ilerletilmez, sonraki tik pencere içindeyse
	// yeniden görür (sonra envanterde sync_missed).
	deferred := map[[2]string]bool{}
	for k := range phases {
		if _, seen := obs.Apps[k]; !seen {
			deferred[[2]string{k.AppNamespace, k.Name}] = true
			delete(phases, k)
			res.diag["sync_deferred"]++
		}
	}
	counters := make(map[SyncKey]float64, len(mem.counters)+len(curW))
	for k, v := range mem.counters {
		counters[k] = v
	}
	for k, v := range curW {
		if !deferred[[2]string{k.AppNamespace, k.Name}] {
			counters[k] = v
		}
	}
	holdAbsence, liveUp := false, mem.liveUp
	if inventory {
		fr, err := query(SyncFullQuery(p.Selector))
		if err != nil {
			return fail("sayaç tabanı", err)
		}
		curF, err := ParseSyncCounters(inst.HubNamespace, fr.ResultType, fr.Result)
		if err != nil {
			return fail("sayaç tabanı", err)
		}
		if mem.countersOK {
			miss, _ := SyncIncreases(counters, curF, true)
			for k := range miss {
				if !deferred[[2]string{k.AppNamespace, k.Name}] {
					res.diag["sync_missed"]++
				}
			}
		}
		base := make(map[SyncKey]float64, len(curF))
		for k, v := range curF {
			if old, ok := counters[k]; ok && deferred[[2]string{k.AppNamespace, k.Name}] {
				v = old // ertelenen artış tabana yutulmasın
			}
			base[k] = v
		}
		counters = base

		// v0.10.983 inceleme: yokluk (deleted) yalnız parçanın scrape hedefleri
		// sağlamken işlenir. Controller shard'ı çöktüğünde ya da scrape
		// koptuğunda seriler bayatlar ve Thanos uyarısız KÜÇÜK bir vektör döner.
		// Hedef yok / düşmüş hedef var / sağlam hedef sayısı azaldı → yokluk bu
		// envanterde sayılmaz (satır yok, Absent ilerlemez). §11 doğrulanmadı:
		// `up` serisi aynı seçiciyle bulunamazsa silme HİÇ yazılmaz (fail-safe).
		// İkinci inceleme: yalnız argocd_app_info üreten job'ların hedefleri
		// (LivenessQuery) — namespace'teki ilgisiz düşmüş hedef bekletmez.
		lr, err := query(LivenessQuery(p.Selector))
		if err != nil {
			return fail("hedef sağlığı", err)
		}
		up, down, err := ParseLiveness(lr.ResultType, lr.Result)
		if err != nil {
			return fail("hedef sağlığı", err)
		}
		switch {
		case up == 0:
			holdAbsence = true
			res.diag["absence_held_no_targets"]++
		case down > 0:
			holdAbsence = true
			res.diag["absence_held_targets_down"]++
		case up < mem.liveUp:
			holdAbsence = true
			res.diag["absence_held_targets_lost"]++
		}
		liveUp = up
	}

	baseline := !mem.invDone
	massAccept := !mem.massSince.IsZero() && now.Sub(mem.massSince) >= metricsMassHold
	out := DiffStatus(DiffInput{
		InstanceID: inst.ID, HubClusterID: p.Hub.ID, ChangedAt: changedAt, Inventory: inventory, Obs: obs,
		Syncs: phases, Prev: mem.apps, Baseline: baseline, BaselineKeys: mem.pendingBase, Rebaseline: mem.rebaseline,
		HoldAbsence: holdAbsence, RefreshBefore: now.Add(-StatusRefreshAge), MassAccept: massAccept,
		ClusterOf:   func(ds string) string { return DestClusterID(ds, p.Hub.ID, reg.ByServer, reg.Normalize) },
		NextVersion: w.nextVersion,
	})
	for k, v := range out.Diag {
		res.diag[k] += v
	}
	if len(out.Rows) > 0 {
		// Liderlik yazımdan hemen önce yeniden doğrulanır; tik sırasında
		// kaybedilip yeniden edinildiyse (resetPending) bellek bayat.
		if !w.leader() || w.resetPending.Load() {
			mem.loaded = false
			res.lostLeader, res.note = true, "liderlik tik sırasında kaybedildi/yeniden edinildi — yazım atlandı"
			return res
		}
		if err := w.store.ArgoCDWriteStatuses(ctx, out.Rows); err != nil {
			mem.loaded = false
			res.hard, res.note = true, "argocd_app_status yazılamadı: "+err.Error()
			return res
		}
		res.rows = len(out.Rows)
		for _, r := range out.Rows {
			if r.ClusterID == "" {
				res.unmapped++
			}
		}
	}
	mem.apps, mem.counters, mem.lastSyncRead = out.Next, counters, now
	// Taban envanterinde belirsiz kalan anahtarlar ilk gözlemde 'baseline'
	// yazılır (v0.10.983 inceleme); gözlenen (artık bilinen) ya da sonraki
	// envanterde hiç görülmeyen anahtar listeden düşer.
	pend := map[AppKey]bool{}
	switch {
	case inventory && baseline:
		for k := range obs.Ambiguous {
			if _, known := out.Next[k]; !known {
				pend[k] = true
			}
		}
	default:
		for k := range mem.pendingBase {
			if _, known := out.Next[k]; known || (inventory && !obs.Ambiguous[k]) {
				continue
			}
			pend[k] = true
		}
	}
	mem.pendingBase = nil
	if len(pend) > 0 {
		mem.pendingBase = pend
		res.diag["baseline_pending_ambiguous"] += len(pend)
	}
	if inventory {
		mem.invDone, mem.countersOK, mem.lastInventory, mem.rebaseline = true, true, now, false
		mem.invFails, mem.invFailedAt, mem.liveUp = 0, time.Time{}, liveUp
		res.diag["inventory"]++
		if holdAbsence {
			mem.heldStreak++
		} else {
			mem.heldStreak = 0
		}
	}
	res.ok = true
	switch {
	case out.TotalAbsence:
		// Bilinen canlı uygulamaların HEPSİ yok (ama envanter dolu): veri
		// kaynağı değişimi / yanlış okuma sayılır, süreyle de kabul edilmez.
		res.ok = false
		res.note = "bilinen uygulamaların hiçbiri envanterde yok — veri kaynağı sorunu sayıldı, silme işlenmedi (toplu silme otomatik kabul edilmez)"
	case out.Mass && !massAccept:
		if mem.massSince.IsZero() {
			mem.massSince = now
		}
		res.ok = false
		res.note = "toplu yokluk (bilinen uygulamaların yarıdan fazlası envanterde yok) — silme işlemesi en çok 30 dk donduruldu"
	case out.Mass:
		// Süre doldu ve toplu yokluk sürüyor (hedefler sağlam): kabul edilir
		// (gerçek toplu silme); massSince KORUNUR ki ikinci envanter de kabul
		// etsin ve deleted yazılsın.
		res.diag["mass_absence_accepted"]++
	case inventory && !holdAbsence:
		mem.massSince = time.Time{}
	}
	if mem.heldStreak >= metricsHeldWarn && res.note == "" {
		res.ok = false
		res.diag["absence_held_streak"]++
		res.note = fmt.Sprintf("silme %d ardışık envanterdir bekletiliyor (scrape hedefi yok / düşmüş / azalmış — `up` ve metricsJob'u doğrulayın; §11 H)", mem.heldStreak)
	}
	return res
}
