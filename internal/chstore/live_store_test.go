package chstore

import (
	"os"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/cilcenk/coremetry/internal/config"
)

// liveStore — CANLI küme testleri için ortak kapı (v0.9.1312'de 0009
// sihirbazının canlı testiyle doğdu; sihirbaz v0.10.765'te kalktı, kapı
// state_repartition_test.go'nun P1 davranış kanıtı için burada yaşıyor).
// VARSAYILAN OLARAK ATLANIR; operatör ya da geliştirici lokal dağıtık
// kümeye açıkça yönlendirir:
//
//	COREMETRY_LIVE_CH=localhost:9100 COREMETRY_LIVE_CLUSTER=coremetry \
//	COREMETRY_LIVE_DB=coremetry go test ./internal/chstore/ -run Live -v
//
// Bu kapı SALT OKUNUR bir sözleşme taşır (operatör onu prod'a
// doğrultabilir); yazan testler AYRICA COREMETRY_LIVE_CH_WRITE ister
// (liveWriteStore).
func liveStore(t *testing.T) (*Store, string) {
	t.Helper()
	host := os.Getenv("COREMETRY_LIVE_CH")
	if host == "" {
		t.Skip("COREMETRY_LIVE_CH ayarlı değil — canlı küme testi atlanıyor")
	}
	cluster := os.Getenv("COREMETRY_LIVE_CLUSTER")
	db := os.Getenv("COREMETRY_LIVE_DB")
	if db == "" {
		db = "coremetry"
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{host},
		Auth: clickhouse.Auth{Database: db},
	})
	if err != nil {
		t.Fatalf("CH bağlantısı kurulamadı: %v", err)
	}
	return &Store{conn: conn, cfg: config.CHConfig{ClusterName: cluster, Database: db}}, cluster
}
