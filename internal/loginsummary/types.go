// Package loginsummary — v0.10.1903 (login özeti, docs/frontend/login-redesign.md).
//
// Anonim /api/public/login-summary ucunun veri çekirdeği. İki katman:
//
//   - Builder (builder.go): lider kilitli worker'da periyodik tur; CH + VM
//     okur, ADLARI taşıyan bir Snapshot kurar ve yayınlar. Arka uçlara yalnız
//     arayüzle bağlanır.
//   - Project (project.go): SAF tek boğum. Snapshot + ayarlar → Response.
//     Anonim uç yalnız bunu çağırır; istek yolunda CH/VM/Redis YOK.
//
// Sızıntı kuralı: Response'a yalnız bu dosyadaki sözleşme alanları girer;
// servis adı yalnız "isimli" kipte, `ext:` düğümü ve db instance/host hiçbir
// kipte çıkmaz (project.go + topology.go testle çivili).
package loginsummary

import "github.com/cilcenk/coremetry/internal/chstore"

// SnapshotVersion — Redis'teki anlık görüntü biçimi (anahtar sonekiyle uyumlu).
const SnapshotVersion = 1

// ── Uç sözleşmesi (login-contract.md; json etiketleri AYNEN) ────────────

type Response struct {
	Enabled     bool        `json:"enabled"`
	Ready       bool        `json:"ready"`
	GeneratedAt int64       `json:"generated_at,omitempty"` // unix ms
	SpansToday  *uint64     `json:"spans_today,omitempty"`
	TracesToday *uint64     `json:"traces_today,omitempty"`
	Services    *uint64     `json:"services_monitored,omitempty"`
	HTTP24h     *HTTP24h    `json:"http_24h,omitempty"`
	Activity    *Activity   `json:"activity_24h,omitempty"`
	Topology    *Topology   `json:"topology,omitempty"`
	Components  *Components `json:"components,omitempty"`
}

type HTTP24h struct {
	From    int64                      `json:"from"`          // unix ms
	To      int64                      `json:"to"`            // unix ms
	StepS   int                        `json:"step_s"`        // 300
	Status  []chstore.SpanMetricSeries `json:"status"`        // groupKey = ["200"]; points.time unix NS
	Avg     *chstore.SpanMetricSeries  `json:"avg,omitempty"` // saniye
	P95     *chstore.SpanMetricSeries  `json:"p95,omitempty"` // saniye; yalnız AllowUnfilteredPercentiles açıksa
	Summary HTTPSummary                `json:"summary"`
}

type HTTPSummary struct {
	NowRPS  float64  `json:"now_rps"`
	PeakRPS float64  `json:"peak_rps"`
	PeakAt  int64    `json:"peak_at"` // unix ms
	AvgNowS *float64 `json:"avg_now_s,omitempty"`
	P95NowS *float64 `json:"p95_now_s,omitempty"`
}

type Activity struct {
	Rollouts       *uint64 `json:"rollouts_tracked,omitempty"`
	Logins         uint64  `json:"logins"`
	CosreQuestions uint64  `json:"cosre_questions"`
	TracesViewed   uint64  `json:"traces_viewed"`
}

type Topology struct {
	WindowS int    `json:"window_s"` // 900
	Named   bool   `json:"named"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
}

type Node struct {
	ID     string  `json:"id"`             // 10 hex, tuzlu özet
	Tier   string  `json:"tier"`           // gw | bff | svc | db
	Weight float64 `json:"weight"`         // 0..1, 2 hane
	Name   string  `json:"name,omitempty"` // yalnız loginTopology=named
}

type Edge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Weight float64 `json:"weight"`
}

type Components struct {
	State     string  `json:"state"`      // ok | degraded
	CheckedAt int64   `json:"checked_at"` // unix ms
	CHBytes   *uint64 `json:"clickhouse_uncompressed_bytes,omitempty"`
}

// Katman adları ve bileşen durumları (kapalı kümeler).
const (
	TierGateway = "gw"
	TierBFF     = "bff"
	TierService = "svc"
	TierDB      = "db"

	StateOK       = "ok"
	StateDegraded = "degraded"

	TopologyOff       = "off"
	TopologyAnonymous = "anonymous"
	TopologyNamed     = "named"
)

// ── Anlık görüntü (ADLARI taşır; yalnız Project'ten geçerek dışarı çıkar) ─

// Snapshot — Builder'ın yayınladığı, Redis'te taşınan durum. Redis içi ve
// pod belleğidir; anonim uca DOĞRUDAN yazılmaz.
type Snapshot struct {
	V           int    `json:"v"`
	GeneratedAt int64  `json:"generated_at"` // unix ms
	Salt        string `json:"salt"`         // düğüm kimliği tuzu (kararlı: sonraki turlara taşınır)

	SpansToday  *uint64 `json:"spans_today,omitempty"`
	TracesToday *uint64 `json:"traces_today,omitempty"`
	Services    *uint64 `json:"services,omitempty"`

	Ring *Ring `json:"ring,omitempty"`

	// Activity yalnız her iki kaynak (sayaç toplamları + cosre) okunduysa dolu.
	Activity *SnapActivity `json:"activity,omitempty"`

	Topo *SnapTopology `json:"topo,omitempty"`

	State     string  `json:"state"`
	CheckedAt int64   `json:"checked_at"` // unix ms
	CHBytes   *uint64 `json:"ch_bytes,omitempty"`
}

type SnapActivity struct {
	Logins         uint64  `json:"logins"`
	TracesViewed   uint64  `json:"traces_viewed"`
	CosreQuestions uint64  `json:"cosre_questions"`
	Rollouts       *uint64 `json:"rollouts,omitempty"`
}

type SnapTopology struct {
	WindowS int        `json:"window_s"`
	Nodes   []SnapNode `json:"nodes"`
	Edges   []Edge     `json:"edges"`
}

// SnapNode — Name: servis adı ya da db/kuyruk için YALNIZ sistem adı.
type SnapNode struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Tier   string  `json:"tier"`
	Weight float64 `json:"weight"`
}
