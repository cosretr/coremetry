package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.777 — dağıtık gönderici tavanı INSERT ayarlarında yaşar (spool
// dosyası başlığına yazılır). Prod 2026-09-17: havuz beklemesi süresizken
// gönderici sustu, FLUSH ilerlemedi, bir akşam gitti. Bu pin ayarı ve
// gerekçesini chOpts'ta tutar; ayar "distributed_ddl_task_timeout" ile aynı
// haritada olmalı ki her havuz (ana / ingest / okuma) taşısın.
func TestDistributedSenderPoolWaitIsBounded(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "chOpts := func() *clickhouse.Options {")
	if i < 0 {
		t.Fatal("chOpts kurucusu yok")
	}
	block := s[i:]
	if j := strings.Index(block, "\n\t}\n"); j > 0 {
		block = block[:j]
	}
	if !strings.Contains(block, `"connection_pool_max_wait_ms": 30000,`) {
		t.Error("connection_pool_max_wait_ms tavanı chOpts ayar haritasında olmalı (0 = süresiz bekleme, gönderici asılır)")
	}
	if !strings.Contains(block, `"distributed_ddl_task_timeout": ddlTaskTimeoutSeconds,`) {
		t.Error("aynı haritada olmalı — ayrı bir haritaya kayarsa ingest havuzu taşımaz")
	}
}

// v0.10.778 — senkron INSERT yalnız bayrakla ve zaman aşımıyla birlikte.
func TestInsertDistributedSyncIsOptInAndBounded(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "if cfg.InsertDistributedSync {\n\t\t\to.Settings[\"insert_distributed_sync\"] = 1")
	if i < 0 {
		t.Fatal("insert_distributed_sync yalnız cfg.InsertDistributedSync altında ayarlanmalı")
	}
	if !strings.Contains(s[i:i+400], `o.Settings["insert_distributed_timeout"] = 60`) {
		t.Error("senkron INSERT zaman aşımı tavanı olmadan açılamaz (hedef yanıt vermezse süresiz bekler)")
	}
	if strings.Count(s, `"insert_distributed_sync"`) != 1 {
		t.Error("ayar tek yerde, bayrağın altında")
	}
}
