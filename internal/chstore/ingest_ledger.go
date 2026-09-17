package chstore

// ingest_ledger.go — v0.10.767 (trace bütünlüğü Faz B). Çok-podlu ingest'te
// "kabul edilen ↔ saklanan" mutabakatı için pod başına dakikalık sayaç
// defteri.
//
// Sorun: consumer sayaçları (accepted/dropped/write_failed) pod-içi,
// kümülatif ve restart'ta sıfır; /api/health ve Trace hattı sağlığı yalnız
// cevabı veren podun sayısını görüyordu, 12 ingest podunun toplamı yoktu.
//
// Çözüm: her ingest podu 60 s'de bir sayaçları örnekler ve bir önceki
// örneğe göre DELTAYI ingest_ledger'a yazar (signal, pod, dakika kovası).
// İlk örnek boot'tan beri olan her şeyi taşır (sayaç sıfırdan başlar);
// sayaç geri giderse (olmaması gerekir) o dakika atlanır — negatif delta
// üretilmez. Sıfır delta da yazılır: satır aynı zamanda "pod canlı"
// kalp atışıdır (son örnek zamanı). Lider kilidi YOK: her pod yalnız
// kendi satırını yazar, çakışma yok.
//
// Okuma: IngestLedgerFleet — pencerede pod başına toplam (yalnız
// YERLEŞMİŞ kısım: bucket < settledTo), son örnek zamanı, restart sayısı
// (uniqExact boot_id), ve 5 dk kovası başına filo toplamı. RMT → FINAL;
// state tablosu → ana bağlantı (conn_strategy_test stateTables).
//
// Tablo yoksa (küme kipinde CREATE ON CLUSTER ertelenmiş olabilir) yazıcı
// sessizdir: ilk hata ve sonra her 60. hata loglanır — ingest yolunu
// asla etkilemez.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"strconv"
	"time"
)

const (
	ingestLedgerEvery    = 60 * time.Second
	ingestLedgerLogEvery = 60
)

// IngestCounters — bir consumer'ın kümülatif sayaçları (pod-içi).
type IngestCounters struct{ Accepted, Dropped, WriteFailed int64 }

// IngestLedgerRow — deftere yazılan tek satır.
type IngestLedgerRow struct {
	Signal, Pod, BootID string
	Bucket              time.Time
	Accepted            uint64
	Dropped             uint64
	WriteFailed         uint64
	AcceptedTotal       uint64
}

// ingestLedgerDelta — SAF: önceki ve şimdiki örnekten satır üretir.
// ok=false → sayaç geri gitmiş (restart sınırı kaçırılmış); satır yazma,
// prev'i şimdikine eşitle.
func ingestLedgerDelta(signal, pod, bootID string, now time.Time, prev, cur IngestCounters) (IngestLedgerRow, bool) {
	if cur.Accepted < prev.Accepted || cur.Dropped < prev.Dropped || cur.WriteFailed < prev.WriteFailed {
		return IngestLedgerRow{}, false
	}
	return IngestLedgerRow{
		Signal: signal, Pod: pod, BootID: bootID,
		Bucket:        now.UTC().Truncate(time.Minute),
		Accepted:      uint64(cur.Accepted - prev.Accepted),
		Dropped:       uint64(cur.Dropped - prev.Dropped),
		WriteFailed:   uint64(cur.WriteFailed - prev.WriteFailed),
		AcceptedTotal: uint64(max(cur.Accepted, 0)),
	}, true
}

// newBootID — süreç kimliği; restart'ta değişir, okuma tarafı uniqExact
// ile "kaç kez yeniden başladı" der.
func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// ingestLedgerInsertSQL — kolon sırası IngestLedgerRow.Append ile aynı.
func ingestLedgerInsertSQL() string {
	return `INSERT INTO ingest_ledger (signal, pod, bucket, boot_id, accepted, dropped, write_failed, accepted_total)`
}

// RecordIngestSample — tek satır, ana bağlantı (state tablosu).
func (s *Store) RecordIngestSample(ctx context.Context, r IngestLedgerRow) error {
	b, err := s.conn.PrepareBatch(ctx, ingestLedgerInsertSQL())
	if err != nil {
		return err
	}
	if err := b.Append(r.Signal, r.Pod, r.Bucket, r.BootID, r.Accepted, r.Dropped, r.WriteFailed, r.AcceptedTotal); err != nil {
		return err
	}
	return b.Send()
}

// StartIngestLedger — pod başına defter yazıcısı; ctx bitince son örneği
// yazıp döner. read her tikte consumer sayaçlarını verir. Bloklamaz.
func (s *Store) StartIngestLedger(ctx context.Context, signal, pod string, read func() IngestCounters) {
	bootID := newBootID()
	t := time.NewTicker(ingestLedgerEvery)
	defer t.Stop()
	var prev IngestCounters
	fails := 0
	write := func(wctx context.Context, now time.Time) {
		cur := read()
		row, ok := ingestLedgerDelta(signal, pod, bootID, now, prev, cur)
		prev = cur
		if !ok {
			return
		}
		wctx, cancel := context.WithTimeout(wctx, 5*time.Second)
		defer cancel()
		if err := s.RecordIngestSample(wctx, row); err != nil {
			fails++
			if fails == 1 || fails%ingestLedgerLogEvery == 0 {
				log.Printf("[chstore] ingest_ledger yazılamadı (%d. hata; tablo henüz yoksa küme DDL kuyruğu bekleniyor): %v", fails, err)
			}
			return
		}
		fails = 0
	}
	for {
		select {
		case <-ctx.Done():
			// Son örnek: kapanışa kadar kabul edilenler deftere girsin.
			write(context.Background(), time.Now())
			return
		case now := <-t.C:
			write(ctx, now)
		}
	}
}

// IngestFleetPod — pencerede bir ingest podunun toplamı.
type IngestFleetPod struct {
	Pod          string `json:"pod"`
	Accepted     uint64 `json:"accepted"`
	Dropped      uint64 `json:"dropped"`
	WriteFailed  uint64 `json:"writeFailed"`
	LastSampleNs int64  `json:"lastSampleAt"`
	Boots        uint64 `json:"boots"`
}

// IngestFleetBucket — 5 dk kovası başına filo kabulü.
type IngestFleetBucket struct {
	TimeNs   int64  `json:"t"`
	Accepted uint64 `json:"accepted"`
}

// IngestFleet — IngestLedgerFleet sonucu; dilimler hiç nil olmaz.
type IngestFleet struct {
	Pods        []IngestFleetPod
	Buckets     []IngestFleetBucket
	Accepted    uint64
	Dropped     uint64
	WriteFailed uint64
}

// ingestFleetPodsSQL — SAF: toplamlar yalnız YERLEŞMİŞ kısımdan (bucket <
// settledTo), son örnek ve boot sayısı tüm pencereden; FINAL, iki sınır,
// LIMIT, bütçe. Bağlar: settledTo ×3, signal, from, to.
func ingestFleetPodsSQL() string {
	return `
		SELECT pod,
		       sumIf(accepted, bucket < toDateTime(?, 'UTC'))     AS accepted,
		       sumIf(dropped, bucket < toDateTime(?, 'UTC'))      AS dropped,
		       sumIf(write_failed, bucket < toDateTime(?, 'UTC')) AS write_failed,
		       max(bucket)                                        AS last_sample,
		       uniqExact(boot_id)                                 AS boots
		FROM ingest_ledger FINAL
		WHERE signal = ? AND bucket >= toDateTime(?, 'UTC') AND bucket < toDateTime(?, 'UTC')
		GROUP BY pod
		ORDER BY pod
		LIMIT 500
		SETTINGS max_execution_time = 5`
}

// ingestFleetBucketsSQL — SAF: 5 dk kovası başına filo kabulü, tüm pencere.
func ingestFleetBucketsSQL() string {
	return `
		SELECT toStartOfFiveMinute(bucket) AS t, sum(accepted) AS accepted
		FROM ingest_ledger FINAL
		WHERE signal = ? AND bucket >= toDateTime(?, 'UTC') AND bucket < toDateTime(?, 'UTC')
		GROUP BY t
		ORDER BY t
		LIMIT 2000
		SETTINGS max_execution_time = 5`
}

// IngestLedgerFleet — [from, to) penceresinde filo görünümü; toplamlar
// [from, settledTo) üzerinden (span zamanı ≠ kabul zamanı: son dakikalar
// CH'de henüz yerleşmemiştir, mutabakat onları saymaz).
func (s *Store) IngestLedgerFleet(ctx context.Context, signal string, from, settledTo, to time.Time) (IngestFleet, error) {
	out := IngestFleet{Pods: []IngestFleetPod{}, Buckets: []IngestFleetBucket{}}
	st := settledTo.Unix()
	rows, err := s.conn.Query(ctx, ingestFleetPodsSQL(), st, st, st, signal, from.Unix(), to.Unix())
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p IngestFleetPod
		var last time.Time
		if err := rows.Scan(&p.Pod, &p.Accepted, &p.Dropped, &p.WriteFailed, &last, &p.Boots); err != nil {
			rows.Close()
			return out, err
		}
		p.LastSampleNs = last.UnixNano()
		out.Pods = append(out.Pods, p)
		out.Accepted += p.Accepted
		out.Dropped += p.Dropped
		out.WriteFailed += p.WriteFailed
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	brows, err := s.conn.Query(ctx, ingestFleetBucketsSQL(), signal, from.Unix(), to.Unix())
	if err != nil {
		return out, err
	}
	defer brows.Close()
	for brows.Next() {
		var t time.Time
		var b IngestFleetBucket
		if err := brows.Scan(&t, &b.Accepted); err != nil {
			return out, err
		}
		b.TimeNs = t.UnixNano()
		out.Buckets = append(out.Buckets, b)
	}
	return out, brows.Err()
}
