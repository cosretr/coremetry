package chstore

// ml_events.go — ES log ML katmanı dilim 0 (docs/audits/ml-audit.md).
// Tablo store.go `ml_events` (RMT(version), ORDER BY id, FINAL).
// anomaly_events'ten AYRI: hiçbir eski okuyucu (Inbox, fusion, rootcause,
// terfi) bu satırları görmez (K1). Geri bildirim mevcut anomaly_verdicts
// tablosunda, event_id = MLVerdictID(id) ("ml:" öneki) — ayrı tablo
// açılmadı (operatör: mevcut altyapı varsa onu kullan). Karne = bu iki
// tablonun 30 günlük birleşimi, dedektör başına.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MLSignalLog    = "log"
	MLSignalTrace  = "trace"
	MLSignalMetric = "metric"
	MLStageShadow  = "shadow"
	MLStagePromote = "promoted"
	MLStageClosed  = "closed"
	MLStageChronic = "chronic" // B2: tekrarlayan desen tek kayıt (epizot açmaz) // epizot kapandı (B3, v0.10.1768); last_seen = bitiş
	mlVerdictPref  = "ml:"
	// MLListCap — liste ucu tavanı (events ucuyla aynı: 200).
	MLListCap = 200
	// MLUnifiedCap — v0.10.2051: /api/anomalies/unified okuma tavanı (liste
	// sunucuda birleşip sayfalanır; tavana değen cevap truncated söyler).
	MLUnifiedCap = 2000
	// MLDetSvcVolumeSpike — v0.10.2118 (operatör kararı 2026-11-13): servis
	// span hacmi sıçraması. YAZICISI ML motoru DEĞİL, evaluator
	// (selfhealth_volume.go; 24 sa / önceki 24 sa kuralı). Bu yüzden
	// MLDetectorNames'te YOK (ML ayarı / karne anahtarı değil); sinyali
	// MLDetectorSignal'da trace. Grup kapısı bu dedektörü hiçbir kolda
	// saymaz (ml/group_gate.go) — Problem ve bildirim üretmez.
	MLDetSvcVolumeSpike = "svc_volume_spike"
	// MLDetSvcTelemetryVolume — v0.10.2242 (anomali kartı Dilim B): ML
	// motorunun telemetri hacmi dedektörü (ml/volume.go; istek başına span).
	// svc_volume_spike'ın yerine geçer (evaluator kuralı emekli). Ayrı dedektör
	// anahtarı YOK (yeni ayar yok): ML ana anahtarıyla koşar; bu yüzden
	// MLDetectorNames'te yok, sinyali MLDetectorSignal'da trace. Grup/servis
	// kapısı saymaz (ml/group_gate.go) — Problem ve bildirim üretmez.
	MLDetSvcTelemetryVolume = "svc_telemetry_volume"
)

// MLVerdictID — anomaly_verdicts.event_id anahtarı. Önek, eski
// anomali kararlarıyla çakışmayı (aynı sha1 türevi) keser.
func MLVerdictID(eventID string) string { return mlVerdictPref + eventID }

// IsMLVerdictID — SAF; karne SQL'i LIKE ile aynı öneki süzer.
func IsMLVerdictID(id string) bool { return strings.HasPrefix(id, mlVerdictPref) }

// MLEvent — bir dedektör olayı (servis × pencere). Evidence JSON:
// dedektöre özgü (influencer, taban kovaları…); şema sabit değil.
type MLEvent struct {
	ID        string  `json:"id"`
	Detector  string  `json:"detector"`
	Signal    string  `json:"signal"` // log | trace
	Stage     string  `json:"stage"`
	Service   string  `json:"service"`
	Level     string  `json:"level,omitempty"`
	Pod       string  `json:"pod,omitempty"`
	Code      string  `json:"code,omitempty"`
	StartedAt int64   `json:"startedAt"` // unix ns
	LastSeen  int64   `json:"lastSeen"`  // unix ns
	WindowSec uint32  `json:"windowSec"`
	Baseline  float64 `json:"baseline"`
	Observed  float64 `json:"observed"`
	Score     float64 `json:"score"`
	Direction string  `json:"direction"` // up | down
	Evidence  string  `json:"evidence,omitempty"`
	DupOf     string  `json:"dupOf,omitempty"`
	// v0.10.2199 — olay-kaydı sözleşmesi H1 (docs/ml/anomaly-card-audit.md §9):
	// Class / PublishState tipli süzgeç kolonları, OnsetAt sapmanın başı
	// (unix ns, 0 = bilinmiyor), Record sözleşmenin geri kalanı (ml.EventRecord
	// JSON; '' = eski kayıt). H1'de hiçbir dedektör doldurmaz; yazım/okuma
	// hasMLRecordCols'a bağlı ve RMT tam satır taşıması için ListMLEvents
	// bunları okur.
	Class        string `json:"class,omitempty"`
	PublishState string `json:"publishState,omitempty"`
	OnsetAt      int64  `json:"onsetAt,omitempty"`
	Record       string `json:"record,omitempty"`
	// Karar (anomaly_verdicts'ten, listede zenginleştirilir).
	Verdict   string `json:"verdict,omitempty"`
	VerdictBy string `json:"verdictBy,omitempty"`
	VerdictAt int64  `json:"verdictAt,omitempty"`
	Note      string `json:"note,omitempty"`
	// VerdictReason — v0.10.2256: "Sorun değil" nedeni (yalnız karne etiketi).
	VerdictReason string `json:"verdictReason,omitempty"`
}

// MLEventID — SAF: dedektör × servis × bölüm (level/pod/code) parmak izi.
// Aynı sürekli olay her tikte aynı id'ye yazar → RMT tek satır tutar.
func MLEventID(detector, service, part string) string {
	return FingerprintAnomaly("ml:"+detector, part, service)
}

// MLEpisodeID — SAF: taban kimlik (MLEventID) + epizot başlangıcı → epizot
// kimliği. Taban kimlik zamansızdı ve tekrar eden olayları üstüne yazıyordu
// (B3); her epizot kendi satırı, karar/karne/replay epizot başına.
func MLEpisodeID(baseID string, start time.Time) string {
	return FingerprintAnomaly("ml-ep", baseID, strconv.FormatInt(start.Unix(), 10))
}

// UpsertMLEvent — tam satır yazımı (RMT: tüm alanlar taşınır).
func (s *Store) UpsertMLEvent(ctx context.Context, e MLEvent) error {
	if e.Signal == "" {
		e.Signal = MLDetectorSignal(e.Detector)
	}
	if e.Stage == "" {
		e.Stage = MLStageShadow
	}
	if e.Direction == "" {
		e.Direction = "up"
	}
	withRec := s.hasMLRecordCols.Load()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO ml_events (`+strings.Join(mlEventCols(withRec), ", ")+`)`)
	if err != nil {
		return err
	}
	if err := batch.Append(mlEventInsertArgs(e, withRec)...); err != nil {
		return err
	}
	return batch.Send()
}

// mlEventBaseCols — ml_events'in CREATE TABLE kolonları (version hariç).
var mlEventBaseCols = []string{"id", "detector", "signal", "stage", "service", "level", "pod", "code",
	"started_at", "last_seen", "window_sec", "baseline", "observed", "score", "direction", "evidence", "dup_of"}

// mlEventRecordCols — v0.10.2199 H1 kolonları (ALTER; hasMLRecordCols).
var mlEventRecordCols = []string{"class", "publish_state", "onset_at", "record"}

// mlEventCols — SAF: INSERT ve tam satır SELECT'in TEK kolon listesi
// (ikisi aynı listeden kurulur → taşıma okuması yazılanı eksiksiz görür).
func mlEventCols(withRecord bool) []string {
	cols := append([]string(nil), mlEventBaseCols...)
	if withRecord {
		cols = append(cols, mlEventRecordCols...)
	}
	return cols
}

// mlEventInsertArgs — SAF: mlEventCols sırasıyla INSERT değerleri.
func mlEventInsertArgs(e MLEvent, withRecord bool) []any {
	args := []any{e.ID, e.Detector, e.Signal, e.Stage, e.Service, e.Level, e.Pod, e.Code,
		time.Unix(0, e.StartedAt), time.Unix(0, e.LastSeen), e.WindowSec, e.Baseline, e.Observed, e.Score, e.Direction, e.Evidence, e.DupOf}
	if withRecord {
		args = append(args, e.Class, e.PublishState, time.Unix(0, e.OnsetAt), e.Record)
	}
	return args
}

// mlEventScan — mlEventCols sırasıyla Scan hedefleri; done() zaman
// kolonlarını e'ye çevirir (Scan'den SONRA çağrılır).
func mlEventScan(e *MLEvent, withRecord bool) (dest []any, done func()) {
	var st, ls, on time.Time
	dest = []any{&e.ID, &e.Detector, &e.Signal, &e.Stage, &e.Service, &e.Level, &e.Pod, &e.Code, &st, &ls,
		&e.WindowSec, &e.Baseline, &e.Observed, &e.Score, &e.Direction, &e.Evidence, &e.DupOf}
	if withRecord {
		dest = append(dest, &e.Class, &e.PublishState, &on, &e.Record)
	}
	return dest, func() {
		e.StartedAt, e.LastSeen = st.UnixNano(), ls.UnixNano()
		if withRecord && on.Unix() > 0 {
			e.OnsetAt = on.UnixNano()
		}
	}
}

// probeMLRecordCols — ml_events H1 kolonları (class, publish_state,
// onset_at, record) var mı (v0.10.2199). probeAnomalySubjectCols'un şekli
// (system.columns metadata). DÖRT kolon TEK probe: aynı ALTER turunda
// giderler; yarım tur false sayılır (okuma/yazım ya hepsini ya hiçbirini anar).
func (s *Store) probeMLRecordCols(ctx context.Context) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = currentDatabase() AND table = 'ml_events'
		   AND name IN ('class', 'publish_state', 'onset_at', 'record')
		 SETTINGS max_execution_time = 5`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == uint64(len(mlEventRecordCols)), nil
}

// reprobeMLRecordCols — ertelenen DDL sonrası yeniden deneme; bayrak yalnız
// false → true döner, geçiş BİR KEZ loglanır. Dönüş: bayrağın son hâli.
func (s *Store) reprobeMLRecordCols(ctx context.Context) bool {
	if s.hasMLRecordCols.Load() {
		return true
	}
	if ok, _ := s.probeMLRecordCols(ctx); ok && s.hasMLRecordCols.CompareAndSwap(false, true) {
		log.Printf("[chstore] ml_events class/publish_state/onset_at/record ertelenen DDL sonrası görüldü — olay-kaydı kolonları yazım ve okumada (restart gerekmedi)")
	}
	return s.hasMLRecordCols.Load()
}

// MLEventsFilter — liste süzgeci; boş alan = süzme yok.
type MLEventsFilter struct {
	Since time.Time
	// ID — tek olay (detay kartı, v0.10.1843); ORDER BY id → birincil anahtar araması.
	ID       string
	Detector string
	// Detectors — v0.10.2474: dedektör kümesi (IN; Detector ile birlikte
	// verilirse ikisi de uygulanır). nil = kısıt yok.
	Detectors []string
	Signal    string
	Service   string
	// Services — v0.10.2080 (anomali erişicisi ListAnomalyFeed): servis
	// kümesi, STRICT IN (eski süzgeçle aynı anlam). nil = kısıt yok; boş
	// küme çağıranda elenir (okuma yapılmaz).
	Services []string
	Stage    string
	Limit    int
	// Members — v0.10.2156 (Davis tarzı tek Problem D2): üye dedektörlerinin
	// (svc_slowdown / metric_anomaly / anomaly_promotion) olayları da okunsun.
	// Varsayılan false: bu satırlar bir Problem'in üyelik kaydıdır, anomali
	// yüzeylerinde (liste, akış, sağlık kontrolü) görünmez. Yalnız Problem
	// olay çizelgesi ve üyelik okumaları true geçer.
	Members bool
	// View — v0.10.2243 (anomali kartı Dilim C): "" süzgeç yok (varsayılan;
	// mevcut okuyucular değişmez) | MLViewService servis listesi (platform
	// satırları dışarıda) | MLViewPlatform yalnız platform satırları |
	// MLViewHeld kapı satırında kalan olan kayıtlar (gölge ön süzgeci; kesin
	// karar ml.ShadowHeld).
	View string
	// HideHeld — v0.10.2343 (anomali kartı A2): servis / platform görünümünde
	// publish_state='held' satırı düşer. Yalnız gürültü kapısı kipi on iken
	// (MLConfig.PublishGatesMode) api doldurur; false = SQL süzgeçsiz. ID
	// okuması ve held görünümü etkilenmez (tutulan olay erişilebilir kalır).
	HideHeld bool
	// recCols — ListMLEvents doldurur (hasMLRecordCols); kolon yoksa sınıf
	// süzgeci yalnız dedektör adına düşer.
	recCols bool
}

// Görünüm değerleri (MLEventsFilter.View).
const (
	MLViewService  = "service"
	MLViewPlatform = "platform"
	MLViewHeld     = "held"
)

// mlPlatformCond — SAF: platform satırı = sınıfı telemetry_volume (K8) ya da
// telemetri hacmi dedektörlerinin (eski svc_volume_spike, K11; halefi
// svc_telemetry_volume) satırı — dedektör adıyla, sınıfından bağımsız.
// v0.10.2475 (operatör): hacim olayı anomali DEĞİL; kaydı henüz / hiç
// eklenmemiş (sınıf boş) ya da etki kanıtıyla service_anomaly sınıflı hacim
// olayı da servis listesine (ve onun sayılarına) girmez, "Telemetri hacmi"
// görünümünde kalır. Kolon yoksa yalnız ad.
func mlPlatformCond(recCols bool) string {
	dets := "detector IN ('" + MLDetSvcVolumeSpike + "', '" + MLDetSvcTelemetryVolume + "')"
	if !recCols {
		return dets
	}
	return "(class = 'telemetry_volume' OR " + dets + ")"
}

// mlHeldCond — SAF (v0.10.2343): A2'nin liste süzgeci; kolon yoksa held
// satırı da yoktur (süzgeç eklenmez).
func mlHeldCond(f MLEventsFilter) []string {
	if !f.HideHeld || !f.recCols {
		return nil
	}
	return []string{"publish_state != 'held'"}
}

// mlEventsWhere — SAF: ListMLEvents süzgeci → WHERE gövdesi + argümanlar
// (Limit hariç). Üye dedektörleri Members=false iken dışlanır.
func mlEventsWhere(f MLEventsFilter) (string, []any) {
	where := []string{"last_seen >= ?"}
	args := []any{chDateTime64Arg(f.Since)}
	if f.ID != "" {
		where = append(where, "id = ?")
		args = append(args, f.ID)
	}
	if f.Detector != "" {
		where = append(where, "detector = ?")
		args = append(args, f.Detector)
	}
	if len(f.Detectors) > 0 {
		where = append(where, "detector IN ?")
		args = append(args, f.Detectors)
	}
	if f.Signal != "" {
		where = append(where, "signal = ?")
		args = append(args, f.Signal)
	}
	if f.Service != "" {
		where = append(where, "service = ?")
		args = append(args, f.Service)
	}
	if len(f.Services) > 0 {
		where = append(where, "service IN ?")
		args = append(args, f.Services)
	}
	if f.Stage != "" {
		where = append(where, "stage = ?")
		args = append(args, f.Stage)
	}
	if !f.Members {
		where = append(where, "detector NOT IN ("+mlMemberDetectorsSQL()+")")
	}
	switch f.View {
	case MLViewService:
		where = append(where, "NOT "+mlPlatformCond(f.recCols))
		where = append(where, mlHeldCond(f)...)
	case MLViewPlatform:
		where = append(where, mlPlatformCond(f.recCols))
		where = append(where, mlHeldCond(f)...)
	case MLViewHeld:
		if f.recCols {
			where = append(where, `record != '' AND publish_state != 'invalid' AND position(record, '"passed":false') > 0`)
		} else {
			where = append(where, "0")
		}
	}
	return strings.Join(where, " AND "), args
}

// ListMLEvents — son olaylar (FINAL), last_seen DESC; kararlar eklenir.
func (s *Store) ListMLEvents(ctx context.Context, f MLEventsFilter) ([]MLEvent, error) {
	if f.Limit <= 0 {
		f.Limit = MLListCap
	}
	if f.Limit > MLUnifiedCap {
		f.Limit = MLUnifiedCap
	}
	if f.Since.IsZero() {
		f.Since = time.Now().Add(-24 * time.Hour)
	}
	withRec := s.hasMLRecordCols.Load()
	f.recCols = withRec
	where, args := mlEventsWhere(f)
	args = append(args, f.Limit)
	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM ml_events FINAL
		WHERE %s
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`, strings.Join(mlEventCols(withRec), ", "), where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLEvent
	var ids []string
	for rows.Next() {
		var e MLEvent
		dest, done := mlEventScan(&e, withRec)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		done()
		out = append(out, e)
		ids = append(ids, MLVerdictID(e.ID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if vs, verr := s.ListAnomalyVerdicts(ctx, ids); verr == nil {
			for i := range out {
				if v, ok := vs[MLVerdictID(out[i].ID)]; ok {
					out[i].Verdict, out[i].VerdictBy, out[i].VerdictAt, out[i].Note = v.Verdict, v.CreatedBy, v.CreatedAt, v.Note
					out[i].VerdictReason = v.Reason
				}
			}
		}
	}
	return out, nil
}

// mlGroupRowsSQL — v0.10.2013 (anomali tekilleştirme D2): gruplama girdisi.
// Liste sayfasındaki servislerin TÜM dedektör satırları (liste dedektör /
// sinyal süzgeci taşısa da grup tam kurulsun). Kanıt okunmaz (ağır kolon).
const mlGroupRowsSQL = `
		SELECT id, detector, signal, stage, service, started_at, last_seen, baseline, observed, score, direction
		FROM ml_events FINAL
		WHERE last_seen >= ? AND service IN ? AND detector NOT IN ('svc_slowdown','metric_anomaly','anomaly_promotion')
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// MLGroupRowsCap — gruplama okuması tavanı.
const MLGroupRowsCap = 5000

// ListMLEventsForGroups — gruplama girdisi (hafif kolonlar, karar yok).
// Boş servis listesi = okuma yok.
func (s *Store) ListMLEventsForGroups(ctx context.Context, services []string, since time.Time) ([]MLEvent, error) {
	if len(services) == 0 {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, mlGroupRowsSQL, chDateTime64Arg(since), services, MLGroupRowsCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLEvent
	for rows.Next() {
		var e MLEvent
		var st, ls time.Time
		if err := rows.Scan(&e.ID, &e.Detector, &e.Signal, &e.Stage, &e.Service, &st, &ls, &e.Baseline, &e.Observed, &e.Score, &e.Direction); err != nil {
			return nil, err
		}
		e.StartedAt, e.LastSeen = st.UnixNano(), ls.UnixNano()
		out = append(out, e)
	}
	return out, rows.Err()
}

// MLDetectorScore — dedektör karnesi satırı.
type MLDetectorScore struct {
	Detector string  `json:"detector"`
	Signal   string  `json:"signal"`
	Events   int64   `json:"events"`  // penceredeki olay sayısı
	Samples  int64   `json:"samples"` // karar verilmiş olay sayısı
	Hits     int64   `json:"hits"`    // verdict = anomaly
	HitRate  float64 `json:"hitRate"` // Hits / Samples (Samples=0 → 0)
}

// MLScorecardRate — SAF: karar girilmemiş satırlar paydaya GİRMEZ (S4).
func MLScorecardRate(hits, samples int64) float64 {
	if samples <= 0 {
		return 0
	}
	return float64(hits) / float64(samples)
}

// MLScorecardFromRows — SAF: olay (id→detector) × karar (verdict id→verdict)
// → dedektör başına olay / karar / isabet. Kararsız satır paydaya girmez (S4).
func MLScorecardFromRows(events map[string]string, verdicts map[string]string) []MLDetectorScore {
	acc := map[string]*MLDetectorScore{}
	for id, det := range events {
		r, ok := acc[det]
		if !ok {
			r = &MLDetectorScore{Detector: det, Signal: MLDetectorSignal(det)}
			acc[det] = r
		}
		r.Events++
		if v, ok := verdicts[MLVerdictID(id)]; ok && v != "" {
			r.Samples++
			if v == VerdictAnomaly {
				r.Hits++
			}
		}
	}
	out := make([]MLDetectorScore, 0, len(acc))
	for _, r := range acc {
		r.HitRate = MLScorecardRate(r.Hits, r.Samples)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	return out
}

// MLSignalScores — SAF: dedektör satırlarından sinyal başına karne (log,
// trace, metric; olay yoksa satır yok).
func MLSignalScores(rows []MLDetectorScore) []MLDetectorScore {
	acc := map[string]*MLDetectorScore{}
	for _, r := range rows {
		sig := r.Signal
		if sig == "" {
			sig = MLDetectorSignal(r.Detector)
		}
		a := acc[sig]
		if a == nil {
			a = &MLDetectorScore{Detector: sig, Signal: sig}
			acc[sig] = a
		}
		a.Events += r.Events
		a.Samples += r.Samples
		a.Hits += r.Hits
	}
	out := make([]MLDetectorScore, 0, len(acc))
	for _, sig := range []string{MLSignalLog, MLSignalTrace, MLSignalMetric} {
		if a := acc[sig]; a != nil {
			a.HitRate = MLScorecardRate(a.Hits, a.Samples)
			out = append(out, *a)
		}
	}
	return out
}

// mlScorecardEventsSQL — karne olay okuması. Üye dedektörleri (v0.10.2156)
// karneye girmez: ML dedektörü değiller, kararları da yok.
const mlScorecardEventsSQL = `
		SELECT id, detector FROM ml_events FINAL
		WHERE last_seen >= ? AND detector NOT IN ('svc_slowdown','metric_anomaly','anomaly_promotion')
		LIMIT 5000
		SETTINGS max_execution_time = 5`

// MLScorecard — son `since`'den beri dedektör başına olay / karar /
// isabet. İki ayrı okuma (JOIN/alt sorgu yok: audit 5/5b, dağıtık-güvenli),
// birleştirme Go'da (MLScorecardFromRows). Olay tavanı 5000 (30 g × gün
// tavanı), karar okuması 'ml:' önekiyle sınırlı.
func (s *Store) MLScorecard(ctx context.Context, since time.Time) ([]MLDetectorScore, error) {
	rows, err := s.conn.Query(ctx, mlScorecardEventsSQL, chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	events := map[string]string{}
	for rows.Next() {
		var id, det string
		if err := rows.Scan(&id, &det); err != nil {
			rows.Close()
			return nil, err
		}
		events[id] = det
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	vrows, err := s.conn.Query(ctx, `
		SELECT event_id, verdict FROM anomaly_verdicts FINAL
		WHERE event_id LIKE 'ml:%' AND created_at >= ?
		LIMIT 10000
		SETTINGS max_execution_time = 5`, chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	verdicts := map[string]string{}
	for vrows.Next() {
		var id, v string
		if err := vrows.Scan(&id, &v); err != nil {
			return nil, err
		}
		verdicts[id] = v
	}
	if err := vrows.Err(); err != nil {
		return nil, err
	}
	return MLScorecardFromRows(events, verdicts), nil
}

// MLTableReady — sre pod'u DDL koşmaz (v0.10.1282); tablo api/worker
// boot'unda iner. Lider tik öncesi bunu yoklar, yoksa bekler (çökmez).
func (s *Store) MLTableReady(ctx context.Context) (bool, error) {
	return s.tableExists(ctx, "ml_events")
}

// MLEpisodeCounts — dedektörün servis × bölüm (code) başına epizot sayısı
// (`since`'den beri başlayan satırlar; FINAL). B2 kronik kararı.
func (s *Store) MLEpisodeCounts(ctx context.Context, detector string, since time.Time) (map[string]int, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT service, code, count() FROM ml_events FINAL
		WHERE detector = ? AND started_at >= ? AND stage != 'chronic'
		GROUP BY service, code
		LIMIT 20000
		SETTINGS max_execution_time = 5`, detector, chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var svc, code string
		var n uint64
		if err := rows.Scan(&svc, &code, &n); err != nil {
			return nil, err
		}
		out[MLSeriesKey(svc, code)] = int(n)
	}
	return out, rows.Err()
}
