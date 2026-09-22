package chstore

import (
	"context"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// v0.10.848 — 833'ün kuyruk maddesi (ii): kartın GET'i envanteri (system.tables
// MV + iç tablo taraması) istek başına BİR kez okur. Kontrol dalı tekillerin
// ÜÇ kez okuduğunu da pinler — zarfın var olma sebebi ölçülü kalsın.
type mvCountConn struct {
	driver.Conn
	inventory int
}

type mvDBRow struct{}

func (mvDBRow) Err() error           { return nil }
func (mvDBRow) ScanStruct(any) error { return nil }
func (mvDBRow) Scan(dest ...any) error {
	if len(dest) == 1 {
		if p, ok := dest[0].(*string); ok {
			*p = "coremetry"
		}
	}
	return nil
}

type mvEmptyRows struct{}

func (mvEmptyRows) Next() bool                       { return false }
func (mvEmptyRows) Scan(...any) error                { return nil }
func (mvEmptyRows) ScanStruct(any) error             { return nil }
func (mvEmptyRows) ColumnTypes() []driver.ColumnType { return nil }
func (mvEmptyRows) Totals(...any) error              { return nil }
func (mvEmptyRows) Columns() []string                { return nil }
func (mvEmptyRows) Close() error                     { return nil }
func (mvEmptyRows) Err() error                       { return nil }
func (mvEmptyRows) HasData() bool                    { return false }

func (c *mvCountConn) QueryRow(context.Context, string, ...any) driver.Row { return mvDBRow{} }
func (c *mvCountConn) Query(_ context.Context, q string, _ ...any) (driver.Rows, error) {
	if strings.Contains(q, "name LIKE '.inner_id.%'") { // envanter SQL'ine özgü (mvTargetUUIDs de MaterializedView süzer)
		c.inventory++
	}
	return mvEmptyRows{}, nil
}

func TestMVAdminReportReadsInventoryOnce(t *testing.T) {
	ctx := context.Background()
	c := &mvCountConn{}
	s := &Store{conn: c} // tek düğüm: adres çözücü hiç çağrılmaz
	rep, err := s.MVAdminReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.inventory != 1 {
		t.Fatalf("envanter %d kez okundu, 1 beklenir", c.inventory)
	}
	if rep.Leftovers == nil {
		t.Fatal("Leftovers nil: kart \"yok\"u \"ölçülmedi\"den ayıramaz")
	}
	// Kontrol: tekiller ayrı ayrı çağrılınca üç okuma — zarfın gerekçesi.
	c.inventory = 0
	if _, _, err := s.DanglingMVs(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MVCoverage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MVLeftovers(ctx, MVTargetSet{}); err != nil {
		t.Fatal(err)
	}
	if c.inventory != 3 {
		t.Fatalf("tekiller %d kez okudu, 3 beklenir (kontrol)", c.inventory)
	}
}
