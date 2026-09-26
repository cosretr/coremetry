package logstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// v0.8.232 — pins the ESManager contract: apply-first save (an
// unconnectable/invalid config is rejected and NOTHING persists),
// clickhouse backend swaps the Switchable to the CH fallback, and
// LoadPersisted skips an unchanged blob (no ES client churn on the
// 30s refresh tick).

type fakeSettingsStore struct {
	raw  []byte
	puts int
}

func (f *fakeSettingsStore) GetLogstoreESSettingsRaw(context.Context) ([]byte, error) {
	return f.raw, nil
}
func (f *fakeSettingsStore) PutLogstoreESSettingsRaw(_ context.Context, raw []byte) error {
	f.raw = append([]byte(nil), raw...)
	f.puts++
	return nil
}

func TestESManagerSaveApplyFirst(t *testing.T) {
	ctx := context.Background()
	chFallback := &ESStore{} // stand-in Store; only identity matters
	sw := NewSwitchable(&ESStore{})
	m := NewESManager(sw, chFallback, nil, ESSettings{Backend: "clickhouse"})
	fs := &fakeSettingsStore{}

	// Invalid: ES backend without addresses → rejected, nothing persisted.
	err := m.SavePersisted(ctx, fs, ESSettings{Backend: "elasticsearch"})
	if err == nil {
		t.Fatal("ES backend without addresses must be rejected")
	}
	if fs.puts != 0 {
		t.Fatalf("failed apply must not persist (puts=%d)", fs.puts)
	}

	// Valid: clickhouse backend → swaps to the fallback + persists.
	if err := m.SavePersisted(ctx, fs, ESSettings{Backend: "clickhouse"}); err != nil {
		t.Fatalf("clickhouse save: %v", err)
	}
	if fs.puts != 1 {
		t.Fatalf("puts = %d, want 1", fs.puts)
	}
	if sw.Current() != Store(chFallback) {
		t.Fatal("Switchable must point at the CH fallback after a clickhouse save")
	}
	if snap := m.Snapshot(); snap.Source != "ui" || snap.Backend != "clickhouse" {
		t.Fatalf("snapshot after save = %+v", snap)
	}
}

func TestESManagerLoadPersistedSkipsUnchanged(t *testing.T) {
	ctx := context.Background()
	sw := NewSwitchable(&ESStore{})
	first := &ESStore{}
	m := NewESManager(sw, first, nil, ESSettings{Backend: "clickhouse"})
	fs := &fakeSettingsStore{}

	if err := m.SavePersisted(ctx, fs, ESSettings{Backend: "clickhouse"}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	// Simulate a swap-behind (another apply) then an unchanged reload:
	// the blob equals lastRaw so LoadPersisted must NOT re-apply.
	other := &ESStore{}
	sw.Swap(other)
	if err := m.LoadPersisted(ctx, fs); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if sw.Current() != Store(other) {
		t.Fatal("unchanged blob must be a no-op (store was re-applied)")
	}
}

func TestESManagerNoBlobKeepsEnvConfig(t *testing.T) {
	sw := NewSwitchable(&ESStore{})
	m := NewESManager(sw, &ESStore{}, nil, ESSettings{Backend: "elasticsearch", Addresses: []string{"http://env:9200"}})
	if err := m.LoadPersisted(context.Background(), &fakeSettingsStore{}); err != nil {
		t.Fatalf("empty blob: %v", err)
	}
	snap := m.Snapshot()
	if snap.Source != "env" || len(snap.Addresses) != 1 {
		t.Fatalf("env seed must survive an empty blob: %+v", snap)
	}
}

// v0.10.944 — PUT sözleşmesi: "" rol alanını keşfe döndürür. NewES her
// kurulumda env tohumunu yeniden uyguluyordu: COREMETRY_ES_FIELD_POD varken
// UI/API ile temizlenen pod alanı canlı store'da env'den geri doluyor, GET
// 'discover' derken tool'ların `mapping`i env alanını 'configured'
// raporluyordu. Env yalnız boot yapılandırmasını tohumlar; blob/PUT değeri
// (açık "" dahil) otoritedir.
func TestESManagerBlobFieldWinsOverEnvSeed(t *testing.T) {
	t.Setenv("COREMETRY_ES_FIELD_POD", "labels.pod")
	stub := &esStubServer{searchStatus: 200, searchBody: `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		mappingStatus: 200, fieldCapsStatus: 200, fieldCapsBody: `{"indices":[],"fields":{}}`}
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer srv.Close()
	ctx := context.Background()
	boot := ESSettings{Backend: "elasticsearch", Addresses: []string{srv.URL}}

	sw := NewSwitchable(&ESStore{})
	m := NewESManager(sw, &ESStore{}, nil, boot)
	if got := m.Snapshot().Fields.Pod; got != "labels.pod" {
		t.Fatalf("boot snapshot env tohumunu göstermeli: %q", got)
	}
	cfg := m.CurrentSettings()
	cfg.Fields.Pod = "" // PUT {"fields":{"pod":""}} → keşfe dön
	fs := &fakeSettingsStore{}
	if err := m.SavePersisted(ctx, fs, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	live, ok := sw.Current().(*ESStore)
	if !ok {
		t.Fatalf("canlı store ES olmalı: %T", sw.Current())
	}
	if live.rawFields.Pod != "" || live.fields.Pod != "" {
		t.Fatalf("temizlenen pod alanı env'den geri dolmamalı: raw=%q eff=%q", live.rawFields.Pod, live.fields.Pod)
	}
	if src := live.FieldMapping(ctx, false).Roles[RolePod].Source; src == FieldConfigured {
		t.Fatalf("pod rolü 'configured' raporlanmamalı (GET 'discover' diyor): %s", src)
	}
	if got := m.Snapshot().Fields.Pod; got != "" {
		t.Fatalf("snapshot ile canlı store ayrışmamalı: %q", got)
	}

	// Aynı blob'u yükleyen taze yönetici (peer pod / yeniden başlatma).
	sw2 := NewSwitchable(&ESStore{})
	m2 := NewESManager(sw2, &ESStore{}, nil, boot)
	if err := m2.LoadPersisted(ctx, fs); err != nil {
		t.Fatalf("load: %v", err)
	}
	if live2, ok := sw2.Current().(*ESStore); !ok || live2.rawFields.Pod != "" {
		t.Fatalf("blob'daki \"\" env ile ezilmemeli: %+v", sw2.Current())
	}

	// Blob'daki dolu değer de env'i yener.
	cfg.Fields.Pod = "k8s.pod"
	if err := m.SavePersisted(ctx, fs, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if live3 := sw.Current().(*ESStore); live3.rawFields.Pod != "k8s.pod" {
		t.Fatalf("blob değeri kazanmalı: %q", live3.rawFields.Pod)
	}

	// Doğrudan NewES (main.go boot kurulumu, blob yok) env tohumunu korur.
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}})
	if err != nil {
		t.Fatalf("NewES: %v", err)
	}
	if es.rawFields.Pod != "labels.pod" {
		t.Fatalf("doğrudan çağıran env tohumunu almalı: %q", es.rawFields.Pod)
	}
}
