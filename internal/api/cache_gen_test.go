package api

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache/cachetest"
)

// v0.10.2229 (Redis R3) — nesil sayaçlı geçersizleştirme.

func genTestServer(c *cachetest.Mem) *Server {
	return &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats()}
}

// waitL2 — MISS yolunun L2 yazımı ayrık goroutine'de; zarf görünene dek bekle.
func waitL2(t *testing.T, c *cachetest.Mem, key string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok, _ := c.Get(context.Background(), l2ValueKey(key)); ok {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s L2'ye yazılmadı", key)
}

// Ack/resolve sonrası bir sonraki okuma eski girdiyi görmez, SCAN yok;
// aynı Redis'e bağlı ikinci replika artışı görür.
func TestCacheGenInvalidateCrossReplicaNoScan(t *testing.T) {
	ctx := context.Background()
	mem := cachetest.NewMem()
	a, b := genTestServer(mem), genTestServer(mem)
	const ttl = 15 * time.Second
	keys := []string{"problems:v3:status=open", "problems-count:v1:env=", "inbox:count:v4:inc=true:env=", "inbox:list:x"}
	calls := 0
	fn := func(context.Context) (any, error) { calls++; return map[string]int{"n": calls}, nil }

	for _, k := range keys {
		if _, tier, err := a.cachedJSON(ctx, k, ttl, false, fn); err != nil || tier != "MISS" {
			t.Fatalf("%s: ilk okuma MISS olmalı: %s %v", k, tier, err)
		}
		waitL2(t, mem, k)
		if _, tier, _ := b.cachedJSON(ctx, k, ttl, false, fn); tier != "HIT" {
			t.Fatalf("%s: B replikası L2'den HIT almalı, %s", k, tier)
		}
	}
	// ack/resolve'un üçlüsü (api.go ackProblem/resolveProblem).
	a.cacheInvalidatePrefix(ctx, "problems")
	a.cacheInvalidatePrefix(ctx, "inbox:count")
	a.cacheInvalidatePrefix(ctx, inboxListCachePrefix)
	if n := mem.Scans.Load(); n != 0 {
		t.Fatalf("geçersizleştirme SCAN yapmamalı: %d", n)
	}
	b.l1 = newL1Cache(64) // B'nin L1'i üretimde "prefix:" yayınıyla düşer (değişmedi)
	for _, k := range keys {
		if _, tier, _ := b.cachedJSON(ctx, k, ttl, false, fn); tier != "MISS" {
			t.Errorf("%s: geçersizleştirmeden sonra B eski girdiyi servis etti (%s)", k, tier)
		}
	}
	// İlgisiz aile etkilenmez.
	if _, tier, _ := a.cachedJSON(ctx, "svc-bundle:v1:x", ttl, false, fn); tier != "MISS" {
		t.Fatal(tier)
	}
	waitL2(t, mem, "svc-bundle:v1:x")
	a.cacheInvalidatePrefix(ctx, "problems")
	a.l1 = newL1Cache(64)
	if _, tier, _ := a.cachedJSON(ctx, "svc-bundle:v1:x", ttl, false, fn); tier != "HIT" {
		t.Errorf("problems artışı svc-bundle'ı düşürmemeli: %s", tier)
	}
}

// Hesaplama sürerken gelen geçersizleştirme: yazılan sonuç eski nesli
// taşır, bir sonraki okuma miss (DelPrefix'te bu yazım taze sayılıyordu).
func TestCacheGenBumpDuringComputeLeavesEntryStale(t *testing.T) {
	ctx := context.Background()
	mem := cachetest.NewMem()
	a := genTestServer(mem)
	const key = "anomaly:unified:v2:x"
	if _, _, err := a.cachedJSON(ctx, key, 15*time.Second, false, func(context.Context) (any, error) {
		a.cacheInvalidatePrefix(ctx, "anomaly:") // hesaplama sırasında mute
		return 1, nil
	}); err != nil {
		t.Fatal(err)
	}
	waitL2(t, mem, key)
	b := genTestServer(mem)
	if _, tier, _ := b.cachedJSON(ctx, key, 15*time.Second, false, func(context.Context) (any, error) { return 2, nil }); tier != "MISS" {
		t.Fatalf("araya giren artıştan önce hesaplanan girdi servis edilmemeli: %s", tier)
	}
}

// Sayaç TTL'le düşüp yeniden kurulunca eski ömrün değerine dönmez.
func TestCacheGenReseedNeverReusesValue(t *testing.T) {
	ctx := context.Background()
	mem := cachetest.NewMem()
	a := genTestServer(mem)
	gk := cacheGenKeyPrefix + "announcement"
	_ = a.bumpCacheGen(ctx, "announcement")
	v1, _, _ := mem.Get(ctx, gk)
	mem.Expire(gk)
	_ = a.bumpCacheGen(ctx, "announcement")
	v2, _, _ := mem.Get(ctx, gk)
	n1, _ := strconv.ParseInt(string(v1), 10, 64)
	n2, _ := strconv.ParseInt(string(v2), 10, 64)
	if n1 < 1e12 || n2 < n1 {
		t.Fatalf("yeniden kurulan sayaç milisaniye tabanlı ve artan olmalı: %d → %d", n1, n2)
	}
}

// Redis kesik: bump hata loglar, okuma miss, yazım yapılmaz; panik yok.
func TestCacheGenRedisDown(t *testing.T) {
	ctx := context.Background()
	mem := cachetest.NewMem()
	mem.Down.Store(true)
	a := genTestServer(mem)
	a.cacheInvalidatePrefix(ctx, "problems")
	if _, tier, err := a.cachedJSON(ctx, "problems:x", time.Second, false, func(context.Context) (any, error) { return 1, nil }); err != nil || tier != "MISS" {
		t.Fatalf("%s %v", tier, err)
	}
}

// Her cacheInvalidatePrefix çağrısının öneki kayıtlı (kayıt dışı önek
// bayat kalmaz ama SCAN'e döner — audit R3 riski).
func TestCacheInvalidatePrefixCallSitesRegistered(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	consts := map[string]string{}
	var parsed []*ast.File
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, af)
		ast.Inspect(af, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
							consts[name.Name], _ = strconv.Unquote(bl.Value)
						}
					}
				}
			}
			return true
		})
	}
	sites := 0
	for _, af := range parsed {
		ast.Inspect(af, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "cacheInvalidatePrefix" || len(ce.Args) != 2 {
				return true
			}
			sites++
			var p string
			switch a := ce.Args[1].(type) {
			case *ast.BasicLit:
				p, _ = strconv.Unquote(a.Value)
			case *ast.Ident:
				p = consts[a.Name]
			}
			if !isCacheGenPrefix(p) {
				t.Errorf("%s: önek %q cacheGenPrefixes'te yok", fset.Position(ce.Pos()), p)
			}
			return true
		})
	}
	if sites < 40 {
		t.Fatalf("tarama çağrıları bulamadı: %d", sites)
	}
}
