package chstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// skipidx_materialize.go — v0.10.2424: GENEL arka plan işi — bir skip
// indeksini ya da projection'ı eski partition'lara yazar
// (`ALTER TABLE … MATERIALIZE INDEX|PROJECTION … IN PARTITION ID '…'`).
//
// Neden ayrı iş: tablo boyu tek MATERIALIZE, 70 milyar span'lik bir tabloda
// saatlerce disk + merge yükü ve tek bir mutasyon olarak ya hep ya hiç.
// Bu iş:
//   - partition partition, EN YENİDEN eskiye (en çok sorgulanan veri önce);
//   - tabloda aynı anda EN ÇOK BİR materialize mutasyonu: bir partition'ın
//     mutasyonu her host'ta bitmeden sonrakine geçmez; işler sırayla (aynı
//     anda tek iş);
//   - kısar: tabloda BAŞKA bitmemiş mutasyon varsa, merge birikimi
//     (asynchronous_metrics MaxPartCountForPartition) eşiği aştıysa ya da en
//     dolu disk %10'un altına indiyse yeni mutasyon göndermez ("paused");
//   - durumu system_settings'te tek JSON (yeni şema yok) — partition başına
//     pending/running/done/failed/dropped, deneme sayısı, geri çekilme;
//     tik tabanlı durum makinesi olduğu için restart'ta kaldığı yerden
//     devam eder (running partition yeniden GÖNDERİLMEZ, mutasyonu izlenir);
//   - retention'ın düşürdüğü partition "dropped" olur, iş takılmaz;
//   - Redis lider kilidiyle tek pod'da koşar; küme kipinde <tablo>_local
//     ON CLUSTER, durum clusterAllReplicas ile okunur.
//
// Tüketiciler: 0017 sihirbazının opsiyonel MATERIALIZE adımı (kendi
// döngüsü yerine iş sıraya alır) ve n-gram indeksi kartı
// (spans_ngram_admin.go).

const (
	skipMatSettingKey = "skipidx_materialize_jobs"
	skipMatLockKey    = "coremetry:lock:skipidx-materialize"

	SkipMatKindIndex      = "index"
	SkipMatKindProjection = "projection"

	// skipMatMaxAttempts — partition başına deneme; sonra "failed" (operatör
	// "yeniden dene" ile sıfırlar) ve self-health notu açılır.
	skipMatMaxAttempts = 5
	// skipMatMaxPartsBacklog — en kalabalık partition'daki aktif part sayısı
	// bunu aşarsa merge'ler geride: yeni mutasyon gönderilmez.
	skipMatMaxPartsBacklog = 300
	// skipMatMutationTimeout — gönderilen mutasyon bu sürede bitmezse deneme
	// başarısız sayılır (mutasyon ÖLDÜRÜLMEZ; sonraki deneme o bitmeden
	// gönderilmez çünkü "başka mutasyon var" kısması devrede).
	skipMatMutationTimeout = 6 * time.Hour
	// skipMatVisibilityGrace — ON CLUSTER kuyruğu mutasyonu host'lara
	// dağıtana kadar system.mutations'ta hiç satır görünmeyebilir.
	skipMatVisibilityGrace = 2 * time.Minute
)

// Partition / iş durumları.
const (
	skipMatPending   = "pending"
	skipMatRunning   = "running"
	skipMatPaused    = "paused"
	skipMatDone      = "done"
	skipMatFailed    = "failed"
	skipMatDropped   = "dropped"
	skipMatCancelled = "cancelled"
	skipMatDeclined  = "declined"
)

var (
	skipMatIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	skipMatPartRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// SkipMatPartition — işin bir partition'ı.
type SkipMatPartition struct {
	ID       string `json:"id"`
	State    string `json:"state"` // pending | running | done | failed | dropped
	Attempts int    `json:"attempts,omitempty"`
	NextAt   int64  `json:"nextAt,omitempty"`   // unix ms — geri çekilme bitişi
	IssuedAt int64  `json:"issuedAt,omitempty"` // unix ms — mutasyonun gönderildiği an
	DoneAt   int64  `json:"doneAt,omitempty"`
	Err      string `json:"err,omitempty"`
}

// SkipMatJob — (tablo, indeks|projection) başına tek iş.
type SkipMatJob struct {
	ID         string             `json:"id"`      // <table>:<kind>:<name>
	Table      string             `json:"table"`   // mantıksal ad (spans)
	Local      string             `json:"local"`   // ALTER'ın hedefi (spans_local | spans)
	Cluster    string             `json:"cluster"` // "" = tek düğüm
	Kind       string             `json:"kind"`    // index | projection
	Name       string             `json:"name"`
	State      string             `json:"state"` // pending | running | paused | done | failed | cancelled | declined
	CreatedAt  int64              `json:"createdAt"`
	CreatedBy  string             `json:"createdBy,omitempty"`
	UpdatedAt  int64              `json:"updatedAt"`
	Note       string             `json:"note,omitempty"` // son kısma / hata nedeni
	Partitions []SkipMatPartition `json:"partitions"`
}

type skipMatState struct {
	Jobs []SkipMatJob `json:"jobs"`
}

// SkipMatJobID — SAF.
func SkipMatJobID(table, kind, name string) string { return table + ":" + kind + ":" + name }

// skipMatTerminal — iş bir daha tik almaz.
func skipMatTerminal(state string) bool {
	switch state {
	case skipMatDone, skipMatFailed, skipMatCancelled, skipMatDeclined:
		return true
	}
	return false
}

// skipMatSQL — SAF: tek partition'ın MATERIALIZE ifadesi.
func skipMatSQL(j SkipMatJob, partitionID string) string {
	oc := ""
	if j.Cluster != "" {
		oc = " ON CLUSTER " + j.Cluster
	}
	return fmt.Sprintf("ALTER TABLE %s%s MATERIALIZE %s %s IN PARTITION ID '%s'",
		j.Local, oc, strings.ToUpper(j.Kind), j.Name, partitionID)
}

// skipMatCommandLike — SAF: system.mutations.command için LIKE deseni. Ad
// arkasındaki " IN PARTITION" sayesinde idx_name, idx_name_x'i yakalamaz.
func skipMatCommandLike(j SkipMatJob, partitionID string) string {
	return "%MATERIALIZE " + strings.ToUpper(j.Kind) + " " + j.Name + " IN PARTITION ID '" + partitionID + "'%"
}

// skipMatBackoff — SAF: n. başarısız denemeden sonra bekleme (1, 2, 4 … dk; ≤ 1 saat).
func skipMatBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Minute << uint(attempts-1)
	if d > time.Hour || d <= 0 {
		d = time.Hour
	}
	return d
}

// skipMatFail — SAF: partition denemesi başarısız; deneme hakkı bittiyse failed.
func skipMatFail(p *SkipMatPartition, msg string, now time.Time) {
	p.Attempts++
	p.Err = msg
	p.IssuedAt = 0
	if p.Attempts >= skipMatMaxAttempts {
		p.State = skipMatFailed
		p.NextAt = 0
		return
	}
	p.State = skipMatPending
	p.NextAt = now.Add(skipMatBackoff(p.Attempts)).UnixMilli()
}

// skipMatRunningIdx — SAF: gönderilmiş (izlenen) partition; -1 yok.
func skipMatRunningIdx(j SkipMatJob) int {
	for i, p := range j.Partitions {
		if p.State == skipMatRunning {
			return i
		}
	}
	return -1
}

// skipMatNextIdx — SAF: sıradaki partition (liste en yeniden eskiye
// sıralı); geri çekilmesi sürenler atlanır. -1 = şu an gönderilecek yok.
func skipMatNextIdx(j SkipMatJob, now time.Time) int {
	for i, p := range j.Partitions {
		if p.State == skipMatPending && p.NextAt <= now.UnixMilli() {
			return i
		}
	}
	return -1
}

// skipMatSettle — SAF: partition'lar bittiyse işin son hâli ("" = sürüyor).
func skipMatSettle(j SkipMatJob) string {
	failed := false
	for _, p := range j.Partitions {
		switch p.State {
		case skipMatPending, skipMatRunning:
			return ""
		case skipMatFailed:
			failed = true
		}
	}
	if failed {
		return skipMatFailed
	}
	return skipMatDone
}

// SkipMatCounts — partition sayımları (UI + self-health).
type SkipMatCounts struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Pending int `json:"pending"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
	Dropped int `json:"dropped"`
}

// skipMatCount — SAF.
func skipMatCount(j SkipMatJob) SkipMatCounts {
	c := SkipMatCounts{Total: len(j.Partitions)}
	for _, p := range j.Partitions {
		switch p.State {
		case skipMatDone:
			c.Done++
		case skipMatPending:
			c.Pending++
		case skipMatRunning:
			c.Running++
		case skipMatFailed:
			c.Failed++
		case skipMatDropped:
			c.Dropped++
		}
	}
	return c
}

// skipMatThrottleReason — SAF: yeni mutasyon göndermeyi engelleyen ilk neden.
func skipMatThrottleReason(otherMutations int, maxParts uint64, minFreeRatio float64) string {
	switch {
	case otherMutations > 0:
		return fmt.Sprintf("tabloda %d bitmemiş mutasyon var — bitmesi bekleniyor", otherMutations)
	case maxParts > skipMatMaxPartsBacklog:
		return fmt.Sprintf("merge birikimi: en kalabalık partition'da %d aktif part (eşik %d) — merge'ler yetişince devam", maxParts, skipMatMaxPartsBacklog)
	case minFreeRatio < attrKVHDiskMinFreeRatio:
		return fmt.Sprintf("disk: en dolu host %%%.0f boş (eşik %%%.0f) — yer açılınca devam", minFreeRatio*100, attrKVHDiskMinFreeRatio*100)
	}
	return ""
}

// skipMatHealthNote — SAF: self-health için "iş sürekli başarısız" hükmü.
// Bir iş failed'e düştüyse ya da sürmekte olan bir işte bir partition en az
// 3 kez üst üste başarısız olduysa açık.
func skipMatHealthNote(st skipMatState) (bool, string) {
	var bad []string
	for _, j := range st.Jobs {
		if j.State == skipMatCancelled || j.State == skipMatDeclined || j.State == skipMatDone {
			continue
		}
		c := skipMatCount(j)
		worst := 0
		last := ""
		for _, p := range j.Partitions {
			if p.Attempts > worst && p.State != skipMatDone && p.State != skipMatDropped {
				worst, last = p.Attempts, p.Err
			}
		}
		if j.State == skipMatFailed || worst >= 3 {
			bad = append(bad, fmt.Sprintf("%s (%d/%d partition başarısız, en çok %d deneme; son hata: %s)", j.ID, c.Failed, c.Total, worst, last))
		}
	}
	if len(bad) == 0 {
		return false, ""
	}
	return true, strings.Join(bad, "; ")
}

// ── kalıcı durum ─────────────────────────────────────────────────────────

var skipMatMu sync.Mutex

func (s *Store) loadSkipMat(ctx context.Context) (skipMatState, error) {
	var st skipMatState
	raw, err := s.GetSetting(WithoutSettingsSnapshot(ctx), skipMatSettingKey)
	if err != nil {
		return st, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &st); err != nil {
			return st, fmt.Errorf("%s çözülemedi: %w", skipMatSettingKey, err)
		}
	}
	return st, nil
}

// mutateSkipMat — oku → değiştir → yaz (pod içi kilitli). Pod'lar arası
// yarış: API (sıraya al / iptal) ile lider işçi (ilerleme) aynı anahtara
// yazar; her yazım hemen öncesinde yeniden okur ve yalnız kendi işine
// dokunur, işçi iptal edilmiş bir işin üstüne yazmaz.
func (s *Store) mutateSkipMat(ctx context.Context, f func(st *skipMatState) error) error {
	skipMatMu.Lock()
	defer skipMatMu.Unlock()
	st, err := s.loadSkipMat(ctx)
	if err != nil {
		return err
	}
	if err := f(&st); err != nil {
		return err
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, skipMatSettingKey, raw)
}

func (st *skipMatState) job(id string) *SkipMatJob {
	for i := range st.Jobs {
		if st.Jobs[i].ID == id {
			return &st.Jobs[i]
		}
	}
	return nil
}

// ── hedef çözümü ─────────────────────────────────────────────────────────

// skipMatTarget — mantıksal tablo → (ALTER hedefi, küme). Küme kipinde
// (uygulama yönetimli ya da dış Distributed) <tablo>_local ON CLUSTER;
// tek düğümde tablonun kendisi.
func (s *Store) skipMatTarget(ctx context.Context, table string) (local, cluster string) {
	cluster = s.entityLayerCluster(ctx)
	if cluster != "" {
		if ok, err := s.tableExists(ctx, table+"_local"); err == nil && ok {
			return table + "_local", cluster
		}
	}
	return table, ""
}

// skipMatSrc — system.<name>; küme kipinde her replikadan.
func skipMatSrc(cluster, name string) string {
	if cluster == "" {
		return "system." + name
	}
	return fmt.Sprintf("clusterAllReplicas('%s', system.%s)", cluster, name)
}

const skipMatQS = " SETTINGS max_execution_time = 10, skip_unavailable_shards = 1"

// skipMatObjectExists — indeks / projection hedef tabloda (bu host) var mı.
func (s *Store) skipMatObjectExists(ctx context.Context, local, kind, name string) (bool, error) {
	q := `SELECT count() FROM system.data_skipping_indices WHERE database = currentDatabase() AND table = ? AND name = ?`
	if kind == SkipMatKindProjection {
		q = `SELECT count() FROM system.projections WHERE database = currentDatabase() AND table = ? AND name = ?`
	}
	var n uint64
	if err := s.conn.QueryRow(ctx, q, local, name).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// skipMatPartitions — aktif partition'lar, en yeniden eskiye.
func (s *Store) skipMatPartitions(ctx context.Context, local, cluster string) ([]string, error) {
	rows, err := s.conn.Query(ctx, `SELECT DISTINCT partition_id FROM `+skipMatSrc(cluster, "parts")+`
		WHERE database = currentDatabase() AND table = ? AND active
		ORDER BY partition_id DESC LIMIT 2000`+skipMatQS, local)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if skipMatPartRe.MatchString(id) {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// ── API yüzü ─────────────────────────────────────────────────────────────

// SkipMatEnqueue — (tablo, tür, ad) için işi sıraya alır: o anki aktif
// partition'ların listesi en yeniden eskiye dondurulur (sonradan gelen
// partition'lar indeksle yazılır, işe girmez). Aynı hedefte sürmekte olan
// iş varsa reddedilir; bitmiş/iptal iş yenisiyle değişir.
func (s *Store) SkipMatEnqueue(ctx context.Context, table, kind, name, by string) (SkipMatJob, error) {
	if kind != SkipMatKindIndex && kind != SkipMatKindProjection {
		return SkipMatJob{}, fmt.Errorf("tür index ya da projection olmalı")
	}
	if !skipMatIdentRe.MatchString(table) || !skipMatIdentRe.MatchString(name) {
		return SkipMatJob{}, fmt.Errorf("geçersiz tablo / nesne adı")
	}
	local, cluster := s.skipMatTarget(ctx, table)
	ok, err := s.skipMatObjectExists(ctx, local, kind, name)
	if err != nil {
		return SkipMatJob{}, err
	}
	if !ok {
		return SkipMatJob{}, fmt.Errorf("%s %s, %s üzerinde yok — önce eklenmeli", kind, name, local)
	}
	ids, err := s.skipMatPartitions(ctx, local, cluster)
	if err != nil {
		return SkipMatJob{}, err
	}
	now := time.Now().UnixMilli()
	j := SkipMatJob{ID: SkipMatJobID(table, kind, name), Table: table, Local: local, Cluster: cluster,
		Kind: kind, Name: name, State: skipMatPending, CreatedAt: now, UpdatedAt: now, CreatedBy: by,
		Partitions: make([]SkipMatPartition, 0, len(ids))}
	for _, id := range ids {
		j.Partitions = append(j.Partitions, SkipMatPartition{ID: id, State: skipMatPending})
	}
	if len(ids) == 0 {
		j.State = skipMatDone
	}
	err = s.mutateSkipMat(ctx, func(st *skipMatState) error {
		if old := st.job(j.ID); old != nil {
			if !skipMatTerminal(old.State) {
				return fmt.Errorf("%s zaten sırada (%s)", j.ID, old.State)
			}
			*old = j
			return nil
		}
		st.Jobs = append(st.Jobs, j)
		return nil
	})
	return j, err
}

// SkipMatDecline — operatör eski partition'ları materialize ETMEMEYİ seçti
// (kart bir daha "karar bekliyor" demesin); kayıt "declined".
func (s *Store) SkipMatDecline(ctx context.Context, table, kind, name, by string) error {
	now := time.Now().UnixMilli()
	j := SkipMatJob{ID: SkipMatJobID(table, kind, name), Table: table, Kind: kind, Name: name,
		State: skipMatDeclined, CreatedAt: now, UpdatedAt: now, CreatedBy: by, Partitions: []SkipMatPartition{}}
	return s.mutateSkipMat(ctx, func(st *skipMatState) error {
		if old := st.job(j.ID); old != nil {
			if !skipMatTerminal(old.State) {
				return fmt.Errorf("%s sürüyor (%s) — önce iptal", j.ID, old.State)
			}
			*old = j
			return nil
		}
		st.Jobs = append(st.Jobs, j)
		return nil
	})
}

// SkipMatRetry — başarısız partition'ları sıfırlar; iş yeniden sıraya girer.
func (s *Store) SkipMatRetry(ctx context.Context, id string) (int, error) {
	n := 0
	err := s.mutateSkipMat(ctx, func(st *skipMatState) error {
		j := st.job(id)
		if j == nil {
			return fmt.Errorf("iş yok: %s", id)
		}
		for i := range j.Partitions {
			if j.Partitions[i].State == skipMatFailed {
				j.Partitions[i] = SkipMatPartition{ID: j.Partitions[i].ID, State: skipMatPending}
				n++
			}
		}
		if n == 0 {
			return fmt.Errorf("%s: yeniden denenecek başarısız partition yok", id)
		}
		j.State, j.Note, j.UpdatedAt = skipMatPending, "", time.Now().UnixMilli()
		return nil
	})
	return n, err
}

// SkipMatCancel — iş durur; gönderilmiş mutasyon ÖLDÜRÜLMEZ (CH'de biter).
func (s *Store) SkipMatCancel(ctx context.Context, id string) error {
	return s.mutateSkipMat(ctx, func(st *skipMatState) error {
		j := st.job(id)
		if j == nil {
			return fmt.Errorf("iş yok: %s", id)
		}
		if skipMatTerminal(j.State) {
			return fmt.Errorf("%s zaten bitmiş (%s)", id, j.State)
		}
		j.State, j.Note, j.UpdatedAt = skipMatCancelled, "operatör iptal etti", time.Now().UnixMilli()
		return nil
	})
}

// SkipMatJobView — UI satırı.
type SkipMatJobView struct {
	SkipMatJob
	Counts SkipMatCounts `json:"counts"`
	// SQL — sıradaki partition'ın ifadesi (bilgi).
	SQL string `json:"sql,omitempty"`
}

// SkipMatStatusResult — tüm işler + self-health hükmü.
type SkipMatStatusResult struct {
	Jobs      []SkipMatJobView `json:"jobs"`
	Active    bool             `json:"active"` // sürmekte olan iş var
	Failing   bool             `json:"failing"`
	Health    string           `json:"health,omitempty"`
	Generated int64            `json:"generated"`
}

// SkipMatStatus — kalıcı durumdan görünüm (CH'ye ek sorgu yok).
func (s *Store) SkipMatStatus(ctx context.Context) (SkipMatStatusResult, error) {
	out := SkipMatStatusResult{Jobs: []SkipMatJobView{}, Generated: time.Now().Unix()}
	st, err := s.loadSkipMat(ctx)
	if err != nil {
		return out, err
	}
	for _, j := range st.Jobs {
		v := SkipMatJobView{SkipMatJob: j, Counts: skipMatCount(j)}
		if v.Partitions == nil {
			v.Partitions = []SkipMatPartition{}
		}
		if i := skipMatNextIdx(j, time.Now()); i >= 0 && !skipMatTerminal(j.State) {
			v.SQL = skipMatSQL(j, j.Partitions[i].ID)
		}
		if !skipMatTerminal(j.State) {
			out.Active = true
		}
		out.Jobs = append(out.Jobs, v)
	}
	out.Failing, out.Health = skipMatHealthNote(st)
	return out, nil
}

// SkipMatHealth — self-health kuralı için (evaluator).
func (s *Store) SkipMatHealth(ctx context.Context) (bool, string, error) {
	st, err := s.loadSkipMat(ctx)
	if err != nil {
		return false, "", err
	}
	bad, note := skipMatHealthNote(st)
	return bad, note, nil
}

// ── işçi ─────────────────────────────────────────────────────────────────

// StartSkipMatWorker — worker kipinde, lider kilidiyle; her tik TEK adım
// (izle | kıs | gönder). interval ≤ 0 → 30 s.
func (s *Store) StartSkipMatWorker(ctx context.Context, interval time.Duration, lock cache.Lock) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	leader := cache.NewLeaderHolder(lock, skipMatLockKey, cache.LeaderTTL(interval))
	leader.Start(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !leader.IsLeader() {
				continue
			}
			tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			if err := s.skipMatTick(tctx, time.Now()); err != nil {
				log.Printf("[skipidx-materialize] %v", err)
			}
			cancel()
		}
	}
}

// skipMatSave — işçinin yazımı: iptal edilmiş/değişmiş işin üstüne yazmaz.
func (s *Store) skipMatSave(ctx context.Context, j SkipMatJob) error {
	j.UpdatedAt = time.Now().UnixMilli()
	return s.mutateSkipMat(ctx, func(st *skipMatState) error {
		cur := st.job(j.ID)
		if cur == nil || cur.CreatedAt != j.CreatedAt || skipMatTerminal(cur.State) {
			return nil // silinmiş / yenisiyle değişmiş / iptal: dokunma
		}
		*cur = j
		return nil
	})
}

// skipMatTick — sıradaki (en eski) etkin işte tek adım.
func (s *Store) skipMatTick(ctx context.Context, now time.Time) error {
	st, err := s.loadSkipMat(ctx)
	if err != nil {
		return err
	}
	var jobs []SkipMatJob
	for _, j := range st.Jobs {
		if !skipMatTerminal(j.State) {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	sort.SliceStable(jobs, func(a, b int) bool { return jobs[a].CreatedAt < jobs[b].CreatedAt })
	j := jobs[0]
	changed := s.skipMatStep(ctx, &j, now)
	if changed {
		return s.skipMatSave(ctx, j)
	}
	return nil
}

// skipMatStep — işin durum makinesinde bir adım; true = kaydedilecek.
func (s *Store) skipMatStep(ctx context.Context, j *SkipMatJob, now time.Time) bool {
	// 1) Gönderilmiş partition varsa yalnız onu izle (restart sonrası da).
	if i := skipMatRunningIdx(*j); i >= 0 {
		p := &j.Partitions[i]
		pend, done, fail, err := s.skipMatMutationProbe(ctx, *j, p.ID)
		issued := time.UnixMilli(p.IssuedAt)
		switch {
		case err != nil:
			return s.skipMatNote(j, "mutasyon durumu okunamadı: "+err.Error())
		case fail != "":
			skipMatFail(p, "mutasyon hatası: "+fail, now)
		case pend > 0:
			if now.Sub(issued) < skipMatMutationTimeout {
				return s.skipMatNote(j, fmt.Sprintf("partition %s yazılıyor", p.ID))
			}
			skipMatFail(p, "mutasyon 6 saatte bitmedi (öldürülmedi; system.mutations izleyin)", now)
		case done > 0:
			p.State, p.DoneAt, p.Err, p.IssuedAt = skipMatDone, now.UnixMilli(), "", 0
		default:
			// Hiç satır yok: ON CLUSTER kuyruğu henüz dağıtmadı ya da
			// partition retention'la düştü.
			if now.Sub(issued) < skipMatVisibilityGrace {
				return false
			}
			if ok, perr := s.skipMatPartitionExists(ctx, *j, p.ID); perr == nil && !ok {
				p.State, p.IssuedAt, p.Err = skipMatDropped, 0, ""
			} else {
				skipMatFail(p, "mutasyon system.mutations'ta görünmedi (DDL kuyruğu?)", now)
			}
		}
		j.State, j.Note = skipMatRunning, ""
		if fin := skipMatSettle(*j); fin != "" {
			j.State = fin
		}
		return true
	}
	// 2) Sıradaki partition.
	i := skipMatNextIdx(*j, now)
	if i < 0 {
		if fin := skipMatSettle(*j); fin != "" {
			j.State, j.Note = fin, ""
			return true
		}
		return s.skipMatNote(j, "geri çekilme süresi bekleniyor")
	}
	if reason := s.skipMatThrottle(ctx, *j); reason != "" {
		changed := j.State != skipMatPaused || j.Note != reason
		j.State, j.Note = skipMatPaused, reason
		return changed
	}
	p := &j.Partitions[i]
	if ok, err := s.skipMatPartitionExists(ctx, *j, p.ID); err == nil && !ok {
		p.State, p.Err = skipMatDropped, ""
		if fin := skipMatSettle(*j); fin != "" {
			j.State = fin
		}
		return true
	}
	if !skipMatPartRe.MatchString(p.ID) || !skipMatIdentRe.MatchString(j.Name) || !skipMatIdentRe.MatchString(j.Local) {
		skipMatFail(p, "beklenmeyen ad / partition_id", now)
		return true
	}
	qctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err := s.conn.Exec(qctx, skipMatSQL(*j, p.ID))
	cancel()
	if err != nil {
		skipMatFail(p, err.Error(), now)
	} else {
		p.State, p.IssuedAt, p.Err = skipMatRunning, now.UnixMilli(), ""
		log.Printf("[skipidx-materialize] %s: partition %s gönderildi", j.ID, p.ID)
	}
	j.State, j.Note = skipMatRunning, ""
	if fin := skipMatSettle(*j); fin != "" {
		j.State = fin
	}
	return true
}

func (s *Store) skipMatNote(j *SkipMatJob, note string) bool {
	if j.Note == note {
		return false
	}
	j.Note = note
	return true
}

// skipMatMutationProbe — bu partition'ın MATERIALIZE mutasyonu: bitmemiş
// satır, bitmiş satır, ilk hata nedeni (bitmemişlerden).
func (s *Store) skipMatMutationProbe(ctx context.Context, j SkipMatJob, partitionID string) (pend, done uint64, fail string, err error) {
	err = s.conn.QueryRow(ctx, `SELECT countIf(NOT is_done), countIf(is_done),
			anyIf(latest_fail_reason, latest_fail_reason != '' AND NOT is_done)
		FROM `+skipMatSrc(j.Cluster, "mutations")+`
		WHERE database = currentDatabase() AND table = ? AND command LIKE ?`+skipMatQS,
		j.Local, skipMatCommandLike(j, partitionID)).Scan(&pend, &done, &fail)
	return
}

func (s *Store) skipMatPartitionExists(ctx context.Context, j SkipMatJob, partitionID string) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx, `SELECT count() FROM `+skipMatSrc(j.Cluster, "parts")+`
		WHERE database = currentDatabase() AND table = ? AND partition_id = ? AND active`+skipMatQS,
		j.Local, partitionID).Scan(&n)
	return n > 0, err
}

// skipMatThrottle — üç ucuz sinyal: tablodaki bitmemiş mutasyonlar
// (system.mutations), merge birikimi (asynchronous_metrics
// MaxPartCountForPartition — CH'nin kendi "parts_to_delay_insert"
// göstergesi), en dolu diskin boş oranı. Okunamayan sinyal = kıs.
func (s *Store) skipMatThrottle(ctx context.Context, j SkipMatJob) string {
	var other uint64
	if err := s.conn.QueryRow(ctx, `SELECT count() FROM `+skipMatSrc(j.Cluster, "mutations")+`
		WHERE database = currentDatabase() AND table = ? AND NOT is_done`+skipMatQS, j.Local).Scan(&other); err != nil {
		return "mutasyon durumu okunamadı: " + err.Error()
	}
	var maxParts float64
	if err := s.conn.QueryRow(ctx, `SELECT max(value) FROM `+skipMatSrc(j.Cluster, "asynchronous_metrics")+`
		WHERE metric = 'MaxPartCountForPartition'`+skipMatQS).Scan(&maxParts); err != nil {
		return "merge birikimi okunamadı: " + err.Error()
	}
	var minRatio float64
	if err := s.conn.QueryRow(ctx, `SELECT min(if(total_space = 0, 1, free_space / total_space)) FROM `+skipMatSrc(j.Cluster, "disks")+skipMatQS).Scan(&minRatio); err != nil {
		return "disk durumu okunamadı: " + err.Error()
	}
	return skipMatThrottleReason(int(other), uint64(maxParts), minRatio)
}
