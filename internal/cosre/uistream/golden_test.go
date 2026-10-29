package uistream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golden_test.go — v0.10.1591 (CoSRE v2 Faz 1, R3): paylaşılan fixture.
// Girdi testdata/cosre/<ad>.events.jsonl (satır başına {"kind","payload"}),
// çıktı <ad>.sse — Emitter'ın yazdığı baytlar (heartbeat'siz). Aynı .sse
// dosyalarını frontend'in fixture testi de okur (operatör kararı §0-5):
// protokol kayması iki tarafı birden kırar.
//
// Yenileme: go test ./internal/cosre/uistream -run TestGoldenFixtures -update
// (ardından frontend vitest'i yeniden koş; kapalı parça kümesi değiştiyse
// protocol.ts aynı commit'te güncellenir).

var update = flag.Bool("update", false, "rewrite testdata/cosre/*.sse")

// ui-stream-v1-reset (v0.10.1606) — yalnız geri alma senaryosu: t1 akar,
// data-reset{textId:t1}, t2 yeni tam metinle açılır ve kapanır. Ayrı dosya
// çünkü dizi tam olarak (kesin parça listesiyle) pinlenir; ana fixture'ın
// çok kanallı akışı buna gürültü katardı.
var goldenFixtures = []string{"ui-stream-v1", "ui-stream-v1-error", "ui-stream-v1-closed", "ui-stream-v1-reset"}

func fixtureDir() string { return filepath.Join("..", "..", "..", "testdata", "cosre") }

func renderFixture(t *testing.T, name string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir(), name+".events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e, ok := NewEmitter(rec, Options{MessageID: GoldenMessageID})
	if !ok {
		t.Fatal("emitter")
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		e.Emit(ev.Kind, ev.Payload)
	}
	e.Close()
	return rec.Body.String(), rec
}

func TestGoldenFixtures(t *testing.T) {
	for _, name := range goldenFixtures {
		t.Run(name, func(t *testing.T) {
			got, rec := renderFixture(t, name)
			if rec.Header().Get(HeaderName) != ProtocolVersion {
				t.Fatalf("protokol başlığı %q", rec.Header().Get(HeaderName))
			}
			path := filepath.Join(fixtureDir(), name+".sse")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v — -update ile üret", err)
			}
			if string(want) != got {
				t.Fatalf("%s.sse bayat:\n%s", name, lineDiff(string(want), got))
			}
		})
	}
}

// lineDiff — ilk farklı satırdan itibaren kısa birleşik görünüm.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	n := len(w)
	if len(g) > n {
		n = len(g)
	}
	for i := 0; i < n; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			b.WriteString("- " + wl + "\n+ " + gl + "\n")
		}
	}
	return b.String()
}
