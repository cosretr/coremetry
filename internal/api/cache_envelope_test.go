package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache/cachetest"
)

// R6 — binary envelope round trip: small raw, large compressed, cap.
func TestEnvelopeRoundTrip(t *testing.T) {
	now := time.Unix(0, 1_700_000_000_123_456_789)
	small := []byte(`{"n":1}`)
	large := []byte(`[` + strings.Repeat(`{"service":"checkout","p99":123.4},`, 4000) + `{}]`)
	for name, body := range map[string][]byte{"small": small, "large": large} {
		env, err := encodeEnvelope(body, "7:9", now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if env[0] != 0 {
			t.Fatalf("%s: binary envelope must not look like JSON", name)
		}
		w, b, g, ok := unwrapEnvelopeGen(env)
		if !ok || !w.Equal(now) || g != "7:9" || !bytes.Equal(b, body) {
			t.Fatalf("%s: round trip ok=%v w=%v g=%q", name, ok, w, g)
		}
		compressed := env[len(l2Magic)]&l2FlagZstd != 0
		if compressed != (name == "large") {
			t.Fatalf("%s: compressed=%v", name, compressed)
		}
		if name == "large" && len(env)*5 > len(body) {
			t.Fatalf("large JSON should shrink ≥5×: %d → %d", len(body), len(env))
		}
	}
	// Incompressible > 1 MB → not stored.
	noise := make([]byte, l2MaxValueBytes+1)
	_, _ = rand.Read(noise)
	if _, err := encodeEnvelope(noise, "", now); err != errL2Oversize {
		t.Fatalf("oversize: %v", err)
	}
	// Compressible > 1 MB raw that fits once compressed → stored.
	big := bytes.Repeat([]byte(`{"k":"v"},`), (2<<20)/10)
	if env, err := encodeEnvelope(big, "", now); err != nil || len(env) > l2MaxValueBytes {
		t.Fatalf("compressible 2 MB should fit: err=%v", err)
	}
	// v1 JSON envelope still decodes; garbage does not.
	v1, _ := json.Marshal(cacheEnvelope{Written: now.UnixNano(), Body: small, Gen: "g"})
	if _, b, g, ok := unwrapEnvelopeGen(v1); !ok || g != "g" || !bytes.Equal(b, small) {
		t.Fatal("v1 JSON envelope must still decode")
	}
	for _, bad := range [][]byte{nil, []byte("\x00CM2"), append([]byte(l2Magic+"\x01"), make([]byte, 9)...), []byte(`{"n":1}`)} {
		if _, _, _, ok := unwrapEnvelopeGen(bad); ok {
			t.Fatalf("malformed %q decoded", bad)
		}
	}
}

// R6 — mixed versions: a previous-release pod's JSON value under the bare
// key is never served by a new pod (and the new pod's binary value never
// lands where an old pod reads); an old pod's exact invalidation reaches
// the "|b2" key through the pub/sub hook; oversized bodies stay L1-only.
func TestEnvelopeMixedVersionAndCap(t *testing.T) {
	ctx := context.Background()
	mem := cachetest.NewMem()
	const key = "svc-test:v1:x"
	oldEnv, _ := json.Marshal(cacheEnvelope{Written: time.Now().UnixNano(), Body: json.RawMessage(`{"old":true}`)})
	_ = mem.Set(ctx, key, oldEnv, time.Minute)

	s := genTestServer(mem)
	body, tier, err := s.cachedJSON(ctx, key, 15*time.Second, false, func(context.Context) (any, error) {
		return map[string]bool{"new": true}, nil
	})
	if err != nil || tier != "MISS" || string(body) != `{"new":true}` {
		t.Fatalf("new pod must not read the bare key: %s %s %v", tier, body, err)
	}
	waitL2(t, mem, key)
	if raw, _, _ := mem.Get(ctx, key); !bytes.Equal(raw, oldEnv) {
		t.Fatal("new pod must not overwrite the bare key old pods read")
	}

	// Old pod's exact invalidation: bare-key DEL + pub/sub "key".
	_ = mem.Del(ctx, key)
	s.dropL2ValueOnPeerInvalidate(key)
	if mem.Has(l2ValueKey(key)) {
		t.Fatal("peer invalidation must drop the |b2 value")
	}

	// New pod's own exact invalidation drops both.
	waitStore := genTestServer(mem)
	waitStore.storeCached(ctx, key, []byte(`{"a":1}`), 15*time.Second, "")
	_ = mem.Set(ctx, key, oldEnv, time.Minute)
	waitStore.cacheInvalidate(ctx, key)
	if mem.Has(key) || mem.Has(l2ValueKey(key)) {
		t.Fatal("cacheInvalidate must drop bare and |b2 keys")
	}

	// Oversize: not written to L2, counted, claim key still cleared.
	noise := make([]byte, l2MaxValueBytes+10)
	_, _ = rand.Read(noise)
	_ = mem.Set(ctx, cacheClaimKeyPrefix+key, []byte("1"), time.Minute)
	waitStore.storeL2(ctx, key, noise, 15*time.Second, "")
	if mem.Has(l2ValueKey(key)) || mem.Has(cacheClaimKeyPrefix+key) {
		t.Fatal("oversize body must not reach L2; claim must be released")
	}
	if n := waitStore.stats.snapshot(waitStore.l1).L2Oversize; n != 1 {
		t.Fatalf("L2Oversize = %d", n)
	}
}
