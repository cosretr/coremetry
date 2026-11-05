package chstore

// query_counter.go — v0.10.1812 (öğrenme döngüsü audit §10 dilim 1.1): bir
// arka plan işinin attığı CH sorgularını SAYMAK için ctx sayacı. Sayım
// tracedConn'un tek kapısında (applyQueryTag: Query/Exec/QueryRow/Select/
// PrepareBatch) — Store metotlarına tek tek dokunulmaz. Sayacı olmayan ctx
// (her HTTP yolu) için maliyet tek ctx.Value okuması.

import (
	"context"
	"sync/atomic"
)

type queryCounterKey struct{}

// WithQueryCounter — ctx'ten türeyen her CH sorgusu c'yi bir artırır.
func WithQueryCounter(ctx context.Context, c *atomic.Int64) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, queryCounterKey{}, c)
}

func countQuery(ctx context.Context) {
	if c, ok := ctx.Value(queryCounterKey{}).(*atomic.Int64); ok {
		c.Add(1)
	}
}
