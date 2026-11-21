package chstore

import (
	"context"
	"fmt"
	"time"
)

// spans_ngram_admin.go — v0.10.2425: spans.db_statement alt dize araması
// için n-gram bloom indeksi.
//
// Sorgu şekli: filtre dili `db.statement LIKE x` → `db_statement LIKE
// '%x%'` (filterexpr.go; büyük/küçük harf DUYARLI). ngrambf_v1 LIKE'ı ham
// kolonda budar; ILIKE / positionCaseInsensitive'i BUDAMAZ (26.2.4.23'te
// ölçüldü) — uygulamada ham spans.db_statement üzerinde büyük/küçük harf
// duyarsız arama yok (dbqueries MV'nin sample_stmt'ine bakar), o yüzden
// indeks lower() üzerine değil kolonun kendisine.
//
// spans.name'e n-gram EKLENMEDİ: mevcut idx_name set(0) GRANULARITY 4,
// LowCardinality adlar üzerinde LIKE / ILIKE / positionCaseInsensitiveUTF8
// koşullarını zaten budar (ölçüm: 10.9 M satır → 32 765; ngram yalnız 8 192'ye
// iner ve ILIKE'ı hiç budamaz) — ek disk karşılıksız.
//
// Parametreler (ölçüm, docs/clickhouse-cluster.md "n-gram"): n=3 (3 harfli
// iğne de budanır; n=4 aynı budamayı verdi), bloom 8192 bayt / 3 hash,
// GRANULARITY 1. Boot (uygulama yönetimli) ALTER listesi indeksi ekler
// (store.go; metadata, yalnız yeni part'lar); dış Distributed kurulumda bu
// kart ekler. Eski partition'lar opsiyonel: arka plan işi
// (skipidx_materialize.go), en yeniden eskiye.

const (
	SpansNgramIndex = "idx_db_stmt_ngram"
	spansNgramType  = "ngrambf_v1(3, 8192, 3, 0)"
	spansNgramGran  = 1
)

// spansNgramAddSQL — SAF.
func spansNgramAddSQL(local, cluster string) string {
	oc := ""
	if cluster != "" {
		oc = " ON CLUSTER " + cluster
	}
	return fmt.Sprintf("ALTER TABLE %s%s ADD INDEX IF NOT EXISTS %s db_statement TYPE %s GRANULARITY %d",
		local, oc, SpansNgramIndex, spansNgramType, spansNgramGran)
}

// SpansNgramStatus — kartın durumu.
type SpansNgramStatus struct {
	Cluster    string          `json:"cluster"`
	Local      string          `json:"local"`
	Hosts      int             `json:"hosts"`
	HaveHosts  int             `json:"haveHosts"`
	IndexState string          `json:"indexState"` // ok | partial | missing | unknown
	AddSQL     string          `json:"addSql"`
	Partitions int             `json:"partitions"`
	Job        *SkipMatJobView `json:"job,omitempty"`
	// Pending — indeks her host'ta yok YA DA eski partition'lar için karar
	// verilmedi (iş yok). Kart yalnız o zaman görünür.
	Pending   bool   `json:"pending"`
	Blocker   string `json:"blocker,omitempty"`
	Err       string `json:"err,omitempty"`
	Generated int64  `json:"generated"`
}

// spansNgramPending — SAF.
func spansNgramPending(indexState string, job *SkipMatJobView) bool {
	return indexState != "ok" || job == nil
}

// SpansNgramStatus — indeks × host + iş kaydı.
func (s *Store) SpansNgramStatus(ctx context.Context) SpansNgramStatus {
	out := SpansNgramStatus{Generated: time.Now().Unix(), IndexState: "unknown"}
	out.Local, out.Cluster = s.skipMatTarget(ctx, "spans")
	out.AddSQL = spansNgramAddSQL(out.Local, out.Cluster)
	out.Hosts = 1
	if out.Cluster != "" {
		var n uint64
		if err := s.conn.QueryRow(ctx, `SELECT count() FROM system.clusters WHERE cluster = ?`, out.Cluster).Scan(&n); err == nil && n > 0 {
			out.Hosts = int(n)
		}
	}
	var have uint64
	if err := s.conn.QueryRow(ctx, `SELECT count() FROM `+skipMatSrc(out.Cluster, "data_skipping_indices")+`
		WHERE database = currentDatabase() AND table = ? AND name = ?`+skipMatQS, out.Local, SpansNgramIndex).Scan(&have); err != nil {
		out.Err = err.Error()
	} else {
		out.HaveHosts = int(have)
		out.IndexState = entityLayerObjectState(out.HaveHosts, out.Hosts)
	}
	if ids, err := s.skipMatPartitions(ctx, out.Local, out.Cluster); err == nil {
		out.Partitions = len(ids)
	}
	if st, err := s.SkipMatStatus(ctx); err == nil {
		id := SkipMatJobID("spans", SkipMatKindIndex, SpansNgramIndex)
		for i := range st.Jobs {
			if st.Jobs[i].ID == id {
				j := st.Jobs[i]
				out.Job = &j
			}
		}
	} else if out.Err == "" {
		out.Err = err.Error()
	}
	out.Pending = spansNgramPending(out.IndexState, out.Job)
	if out.IndexState == "unknown" {
		out.Blocker = "indeks durumu okunamadı — körlemesine DDL gönderilmez"
	}
	return out
}

// SpansNgramRun — indeks eksikse ekler (metadata; yalnız yeni part'lar),
// sonra eski partition'lar için kararı kaydeder: materialize → arka plan
// işi sıraya, değilse "declined" (kart bir daha sormaz; TTL eski veriyi
// zaten döndürür).
func (s *Store) SpansNgramRun(ctx context.Context, materialize bool, by string) (SpansNgramStatus, error) {
	st := s.SpansNgramStatus(ctx)
	if st.Blocker != "" {
		return st, fmt.Errorf("%s", st.Blocker)
	}
	if st.IndexState != "ok" {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := s.conn.Exec(qctx, spansNgramAddSQL(st.Local, st.Cluster))
		cancel()
		if err != nil {
			return st, fmt.Errorf("ADD INDEX: %w", err)
		}
		if again := s.SpansNgramStatus(ctx); again.IndexState != "ok" {
			return again, fmt.Errorf("ADD INDEX gönderildi ama indeks her host'ta görünmüyor (%d/%d; DDL kuyruğu?) — tekrar koşmak güvenli", again.HaveHosts, again.Hosts)
		}
	}
	var err error
	if materialize {
		_, err = s.SkipMatEnqueue(ctx, "spans", SkipMatKindIndex, SpansNgramIndex, by)
	} else {
		err = s.SkipMatDecline(ctx, "spans", SkipMatKindIndex, SpansNgramIndex, by)
	}
	return s.SpansNgramStatus(ctx), err
}
