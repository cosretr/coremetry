package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

// cache_envelope.go — L2 (Redis) value envelope.
//
// v1 (JSON, until R6): {"w":ns,"b":body,"g":gen} under the bare cache key.
// Every hit re-parsed the whole body, values had no size cap.
//
// v2 (binary, Redis R6 / audit O7, 2026-11-20), stored under
// l2ValueKey(key) = key + "|b2":
//
//	[0..3]  magic "\x00CM2" (0x00 never starts JSON)
//	[4]     flags (bit0 = body zstd-compressed)
//	[5..12] written, unix ns, big-endian
//	uvarint len(gen) + gen
//	body (raw, or zstd frame when > l2CompressMin and it actually shrinks)
//
// Values whose encoded size exceeds l2MaxValueBytes are NOT written to L2
// (L1 still serves them on this pod): one oversized response must not crowd
// out the lock / claim keys that share the Redis under volatile-lru (K3).
//
// WHY A NEW KEY (rolling upgrade): pods from the previous release read the
// bare key and serve any non-JSON value raw ("HIT-LEGACY"); writing binary
// there would hand them zstd bytes as a JSON response for the minutes both
// versions run. New pods therefore write and read only the "|b2" key, old
// pods only the bare key — neither sees the other's format. Invalidation
// stays correct across versions: generation counters (cache_gen.go) are
// key-independent; DelPrefix(prefix) matches the "|b2" suffix too; exact
// cacheInvalidate deletes both keys, and the pub/sub subscriber also drops
// the "|b2" key on an exact-key message (an old pod's invalidation only
// deletes the bare key). Old bare-key entries age out with their Redis TTL
// (3 × soft TTL).

const (
	l2ValueSuffix = "|b2"
	l2Magic       = "\x00CM2"
	l2FlagZstd    = 1

	l2CompressMin   = 8 << 10  // below this zstd is not worth the CPU
	l2MaxValueBytes = 1 << 20  // encoded value cap (audit O7: 1 MB)
	l2MaxRawBytes   = 16 << 20 // skip encoding outright (≫ any 16:1 ratio)
	l2HeaderLen     = len(l2Magic) + 1 + 8
)

// errL2Oversize — value not stored in L2 (counted, see cacheStats).
var errL2Oversize = errors.New("cache value exceeds L2 size cap")

func l2ValueKey(key string) string { return key + l2ValueSuffix }

var (
	zstdEnc = sync.OnceValue(func() *zstd.Encoder {
		e, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
		return e
	})
	zstdDec = sync.OnceValue(func() *zstd.Decoder {
		d, _ := zstd.NewReader(nil, zstd.WithDecoderConcurrency(0), zstd.WithDecoderMaxMemory(l2MaxRawBytes))
		return d
	})
)

// cacheEnvelope — v1 JSON shape; still decoded (tests, stray bare-key reads).
type cacheEnvelope struct {
	Written int64           `json:"w"` // unix nanoseconds at write time
	Body    json.RawMessage `json:"b"`
	// Gen — v0.10.2229 (R3): hesaplamadan önce okunan nesil dizisi
	// (cache_gen.go); kayıtlı aileye ait olmayan anahtarda boş.
	Gen string `json:"g,omitempty"`
}

// wrapEnvelope — v2 binary envelope, or errL2Oversize past the cap.
func wrapEnvelope(body []byte, gen string) ([]byte, error) {
	return encodeEnvelope(body, gen, time.Now())
}

func encodeEnvelope(body []byte, gen string, now time.Time) ([]byte, error) {
	if len(body) > l2MaxRawBytes {
		return nil, errL2Oversize
	}
	payload, flags := body, byte(0)
	if len(body) > l2CompressMin {
		if z := zstdEnc().EncodeAll(body, make([]byte, 0, len(body)/4)); len(z) < len(body) {
			payload, flags = z, l2FlagZstd
		}
	}
	out := make([]byte, 0, l2HeaderLen+binary.MaxVarintLen64+len(gen)+len(payload))
	out = append(out, l2Magic...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint64(out, uint64(now.UnixNano()))
	out = binary.AppendUvarint(out, uint64(len(gen)))
	out = append(out, gen...)
	out = append(out, payload...)
	if len(out) > l2MaxValueBytes {
		return nil, errL2Oversize
	}
	return out, nil
}

// decodeBinaryEnvelope — v2 only; ok=false on anything malformed.
func decodeBinaryEnvelope(raw []byte) (time.Time, []byte, string, bool) {
	if len(raw) < l2HeaderLen || string(raw[:len(l2Magic)]) != l2Magic {
		return time.Time{}, nil, "", false
	}
	flags := raw[len(l2Magic)]
	written := int64(binary.BigEndian.Uint64(raw[len(l2Magic)+1 : l2HeaderLen]))
	rest := raw[l2HeaderLen:]
	n, w := binary.Uvarint(rest)
	if w <= 0 || n > uint64(len(rest)-w) {
		return time.Time{}, nil, "", false
	}
	gen := string(rest[w : w+int(n)])
	body := rest[w+int(n):]
	if flags&l2FlagZstd != 0 {
		b, err := zstdDec().DecodeAll(body, nil)
		if err != nil {
			return time.Time{}, nil, "", false
		}
		body = b
	}
	if written == 0 || len(body) == 0 {
		return time.Time{}, nil, "", false
	}
	return time.Unix(0, written), body, gen, true
}

// unwrapEnvelopeGen — (written, body, gen, true) for a v2 or v1 envelope;
// (zero, nil, "", false) for anything else.
func unwrapEnvelopeGen(raw []byte) (time.Time, []byte, string, bool) {
	if len(raw) > 0 && raw[0] == 0 {
		return decodeBinaryEnvelope(raw)
	}
	var env cacheEnvelope
	if err := json.Unmarshal(raw, &env); err != nil ||
		env.Written == 0 || len(env.Body) == 0 {
		return time.Time{}, nil, "", false
	}
	return time.Unix(0, env.Written), env.Body, env.Gen, true
}

// unwrapEnvelope — unwrapEnvelopeGen without the generation.
func unwrapEnvelope(raw []byte) (time.Time, []byte, bool) {
	w, b, _, ok := unwrapEnvelopeGen(raw)
	return w, b, ok
}

// oversizeWarned — one log line per key family (families are bounded).
var oversizeWarned sync.Map

// oversize — R6: counts a body skipped by the L2 cap; logs once per family.
func (cs *cacheStats) oversize(key string, size int) {
	cs.oversizeN.Add(1)
	fam := cacheKeyFamily(key)
	if _, dup := oversizeWarned.LoadOrStore(fam, true); !dup {
		log.Printf("[cache] %s: %d B gövde L2 tavanını (%d B kodlanmış) aşıyor — yalnız L1'de tutulur", fam, size, l2MaxValueBytes)
	}
}

// dropL2ValueOnPeerInvalidate — exact-key invalidation received over
// pub/sub. A new-release publisher already deleted the "|b2" key; a
// previous-release publisher (rolling upgrade) deleted only the bare key,
// so the binary value would outlive the invalidation. One idempotent DEL
// per pod per exact invalidation (rare: admin writes) closes that gap.
func (s *Server) dropL2ValueOnPeerInvalidate(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.cache.Del(ctx, l2ValueKey(key)); err != nil {
		log.Printf("[cache] peer invalidate L2 del %s: %v", key, err)
	}
}
