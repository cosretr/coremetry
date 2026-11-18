// Package selfobs wires Coremetry's own OTel SDK (traces + metrics)
// so the running binary emits telemetry about itself — appears on
// /services alongside the demo + production traffic it observes.
//
// v0.6.42 — operator-reported gap: the frontend already shipped with
// @opentelemetry/sdk-trace-web (lib/otel.ts) and stamped a W3C
// `traceparent` on every fetch, but the Go backend had no SDK on the
// receiving side, so the trace context died at the HTTP boundary and
// `coremetry-api` / `coremetry-ingest` / `coremetry-worker` /
// `coremetry-mcp` never showed up as services.
//
// Design choices:
//
//   - Opt-in via env. COREMETRY_SELF_OBS_OTLP_ENDPOINT empty → SDK
//     not initialised; binary behaves exactly as before. Set it to
//     `localhost:4317` (the docker-compose otel-collector) to turn
//     on self-observability.
//
//   - One TracerProvider + one MeterProvider per process. They are
//     stored on the global otel package so otelhttp / otelgrpc /
//     chstore.tracedConn pick them up without explicit plumbing.
//
//   - Resource attributes derived from runMode so each role
//     surfaces as a distinct `service.name` on /services:
//
//   - all:    coremetry-monolithic
//
//   - api:    coremetry-api
//
//   - ingest: coremetry-ingest
//
//   - worker: coremetry-worker
//     `service.instance.id` is the hostname (so multiple replicas
//     can be told apart on /services rows).
//
//   - Sampling default: ParentBased(TraceIDRatioBased(0.1)). If the
//     frontend's traceparent indicates "sampled", we follow; root
//     spans the backend creates itself get sampled at 10%. Operator
//     can override via COREMETRY_SELF_OBS_SAMPLE_RATE.
//     v0.10.1463: root spans named in COREMETRY_SELF_OBS_ALWAYS_SAMPLE
//     (default sre.investigation) are always sampled (sampler.go).
//
//   - Self-loop guard: this package is NEVER imported by code that
//     runs inside the OTLP receiver paths (`internal/otlp/*`). The
//     receiver-side instrumentation is enabled only outside ingest
//     mode — see main.go around `mode.ingest` for the gate. Without
//     that, the ingester would emit spans about receiving spans,
//     re-enter itself, and amplify.
//
//   - Metrics — runtime + process counters via go.opentelemetry.io/
//     contrib/instrumentation/runtime. Periodic export every 30s.
//     Custom counters / histograms register against the package's
//     global meter (returned by `Meter()`) so call sites don't
//     repeat the package path.
package selfobs

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	envEndpoint   = "COREMETRY_SELF_OBS_OTLP_ENDPOINT"
	envSampleRate = "COREMETRY_SELF_OBS_SAMPLE_RATE"
	envVersion    = "COREMETRY_VERSION"
)

var (
	enabled bool
	// Default to noop providers so Tracer() / Meter() are ALWAYS safe to
	// call — before Init(), or when self-obs is disabled, or in unit tests
	// that never boot the SDK. Init() overwrites these with the real
	// pipelines. (Pre-v0.8.526 meterSink was a nil-embedding struct that
	// panicked on the first instrument constructor; the first real Meter()
	// caller — the LDAP group-sync engine — surfaced it.)
	tracerSink trace.Tracer = noop.NewTracerProvider().Tracer("coremetry")
	meterSink  metric.Meter = metricnoop.NewMeterProvider().Meter("coremetry")
	tpShutdown func(context.Context) error
	mpShutdown func(context.Context) error
)

// Init constructs the TracerProvider when COREMETRY_SELF_OBS_OTLP_ENDPOINT
// is set; the MeterProvider is built ALWAYS (v0.10.1422 — VM self-export,
// vmexport.go). Returns a shutdown function the caller defers; safe to call
// even when disabled (no-op shutdown).
//
// mode is the running role string ("api", "ingest", "worker", "all")
// — used to derive service.name. version is the binary version.
func Init(ctx context.Context, mode, version string) func(context.Context) error {
	endpoint := strings.TrimSpace(os.Getenv(envEndpoint))

	res, err := buildResource(mode, version)
	if err != nil {
		log.Printf("[selfobs] resource: %v — disabled", err)
		tracerSink = noop.NewTracerProvider().Tracer("coremetry")
		meterSink = metricnoop.NewMeterProvider().Meter("coremetry")
		return func(context.Context) error { return nil }
	}

	// ── Metrics ──────────────────────────────────────────────────
	// v0.10.1422 — MeterProvider artık collector uç noktasından BAĞIMSIZ
	// kurulur: Ayarlar'daki VictoriaMetrics hedefi (vmexport.go) collector'sız
	// da yazabilsin ve trace'i self-loop yüzünden kapalı olan ingest pod'u da
	// kendi metriklerini göndersin. Hedef takılı değilken okuyucu her turu
	// sessizce düşürür. Collector yolu (uç nokta doluysa) gatedExporter
	// arkasında: VM yazımı etkinken metrikler TEK hedefe gider.
	mpOpts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		// otelhttp'nin kendi HTTP metrikleri düşürülür: auth.Middleware isteği
		// kopyaladığı için http.route taşımıyorlar (ve server.address/port
		// gürültüsü ekliyorlar). Yerine instruments.go'daki sınırlı-etiketli
		// http.server.request.duration (internal/api/http_server_metrics.go).
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Scope: instrumentation.Scope{Name: otelhttpScope}},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}},
		)),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(&vmExporter{},
			sdkmetric.WithInterval(metricInterval))),
	}
	if endpoint != "" {
		if metricExp, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(endpoint),
			otlpmetricgrpc.WithInsecure(),
		); err != nil {
			log.Printf("[selfobs] metric exporter: %v — collector metric path off", err)
		} else {
			mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
				gatedExporter{inner: metricExp}, sdkmetric.WithInterval(metricInterval))))
		}
	}
	mp := sdkmetric.NewMeterProvider(mpOpts...)
	otel.SetMeterProvider(mp)
	mpShutdown = mp.Shutdown
	meterSink = mp.Meter("coremetry")
	vmst.running.Store(true)
	// Runtime metrics — heap, goroutines, GC. Same MeterProvider,
	// no extra wiring required at call sites.
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(15 * time.Second)); err != nil {
		log.Printf("[selfobs] runtime metrics: %v", err)
	}

	if endpoint == "" {
		log.Printf("[selfobs] %s unset — traces off; metrics → VictoriaMetrics only when Settings enables it (service=%s)",
			envEndpoint, serviceName(mode))
		tracerSink = noop.NewTracerProvider().Tracer("coremetry")
		return shutdownAll
	}
	enabled = true

	// ── Traces ────────────────────────────────────────────────────
	// v0.6.45 — WithInsecure(), NOT WithDialOption(insecure creds).
	// The first cut (v0.6.42) used WithDialOption which left
	// otlptracegrpc's own TLS default in place, so the client tried
	// a TLS handshake against the collector's plaintext OTLP port →
	// "tls: first record does not look like a TLS handshake" and
	// every export hit the 10s deadline. WithInsecure flips the
	// transport to h2c, matching the metric exporter above.
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		// Block until first connect would deadlock when the collector
		// boots in the same docker-compose; the default async dial
		// retries indefinitely which is what we want for a sidecar
		// collector.
	)
	if err != nil {
		log.Printf("[selfobs] trace exporter: %v — traces off, metrics on", err)
		enabled = false
		tracerSink = noop.NewTracerProvider().Tracer("coremetry")
		return shutdownAll
	}

	sampleRate := parseSampleRate()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExp,
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		// v0.10.1463 (D1) — ada duyarlı kök: sre.investigation HER ZAMAN
		// örneklenir, geri kalan kökler oranla (sampler.go).
		sdktrace.WithSampler(newSelfObsSampler(sampleRate)),
	)
	otel.SetTracerProvider(tp)
	tpShutdown = tp.Shutdown
	tracerSink = tp.Tracer("coremetry")
	// v0.10.1470 — W3C yayıcı + /ai Trace linki kapısı (propagation.go):
	// frontend traceparent'ı backend trace'ine katılır. Yalnız trace hattı
	// kurulunca; kapalıyken süreç geneli yayıcı boş kalır (eski davranış).
	installTraceGlobals(endpoint, os.Getenv(envTracesLocal))

	log.Printf("[selfobs] enabled — endpoint=%s service=%s sample=%.2f always=%v queryable=%v",
		endpoint, serviceName(mode), sampleRate, parseAlwaysSample(os.Getenv(envAlwaysSample)), TracesQueryable())
	return shutdownAll
}

// metricInterval — iki okuyucunun (VM + collector) export aralığı.
const metricInterval = 30 * time.Second

// otelhttpScope — otelhttp'nin meter kapsamı (View ile düşürülür).
const otelhttpScope = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

// Tracer returns the package-wide tracer. Noop when disabled.
func Tracer() trace.Tracer { return tracerSink }

// Meter returns the package-wide meter. Noop when disabled.
func Meter() metric.Meter { return meterSink }

func buildResource(mode, version string) (*resource.Resource, error) {
	host, _ := os.Hostname()
	if version == "" {
		version = strings.TrimSpace(os.Getenv(envVersion))
	}
	if version == "" {
		version = "dev"
	}
	// v0.8.x bug-fix: use NewSchemaless for the custom attrs, NOT
	// NewWithAttributes(semconv.SchemaURL, …). resource.Merge ERRORS when
	// both sides carry a different non-empty schema URL — resource.Default()
	// pins the SDK's bundled semconv schema (e.g. .../1.26.0) while the
	// imported semconv package may be newer (.../1.37.0). When those drifted
	// (a dependency bump, 2026-05-31) the merge failed → buildResource
	// returned an error → selfobs silently fell back to noop and the backend
	// coremetry-api/worker/agent self-telemetry stopped emitting. A schemaless
	// resource has an empty schema URL, so the merge keeps Default()'s URL
	// without conflict and stays robust to future SDK/semconv version skew.
	// (service.name/version/instance keys are stable across semconv versions.)
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName(mode)),
		semconv.ServiceVersion(version),
		semconv.ServiceInstanceID(host),
		semconv.DeploymentEnvironment(strings.TrimSpace(os.Getenv("COREMETRY_DEPLOY_ENV"))),
	}
	// v0.10.91 — K8S KİMLİĞİ downward API'den (chart env'leri).
	//
	// /api/k8s/coverage denetiminde coremetry-monolithic'in YEDİ k8s
	// alanı da sıfırdı: bu resource hiç k8s.* üretmiyordu ve hattaki
	// collector'da k8sattributes processor'ı yok (RBAC'ı da yok). Bu
	// dört alan pod'un KENDİ bilgisi — API sunucusuna sormadan, yetki
	// istemeden downward API'den gelir. deployment.name/container.name
	// downward API'de YOK; onları ancak k8sattributes doldurur (ayrı
	// karar, ClusterRole ister). Env boşsa (docker-compose, çıplak
	// binary) alan HİÇ yazılmaz — boş string yazmak, coverage sayacına
	// sahte doluluk saymaktı.
	for _, kv := range []struct{ env, key string }{
		{"COREMETRY_K8S_NAMESPACE", "k8s.namespace.name"},
		{"COREMETRY_K8S_POD_NAME", "k8s.pod.name"},
		{"COREMETRY_K8S_POD_UID", "k8s.pod.uid"},
		{"COREMETRY_K8S_NODE_NAME", "k8s.node.name"},
	} {
		if v := strings.TrimSpace(os.Getenv(kv.env)); v != "" {
			attrs = append(attrs, attribute.String(kv.key, v))
		}
	}
	return resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
}

func serviceName(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "all", "":
		return "coremetry-monolithic"
	case "api":
		return "coremetry-api"
	case "ingest":
		return "coremetry-ingest"
	case "worker":
		return "coremetry-worker"
	default:
		return "coremetry-" + mode
	}
}

func parseSampleRate() float64 {
	raw := strings.TrimSpace(os.Getenv(envSampleRate))
	if raw == "" {
		return 0.1
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		log.Printf("[selfobs] bad %s=%q — falling back to 0.1", envSampleRate, raw)
		return 0.1
	}
	return v
}

func shutdownAll(ctx context.Context) error {
	var errs []string
	if tpShutdown != nil {
		if err := tpShutdown(ctx); err != nil {
			errs = append(errs, "trace: "+err.Error())
		}
	}
	if mpShutdown != nil {
		if err := mpShutdown(ctx); err != nil {
			errs = append(errs, "metric: "+err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("selfobs shutdown: %s", strings.Join(errs, "; "))
}

// SafeAttr — bir string'i span attribute'una / durum mesajına
// koymadan ÖNCE geçerli UTF-8'e indirger (v0.9.586).
//
// Operator-reported, prod:
//
//	traces export: rpc error: code = Internal desc = grpc: error while
//	marshaling: string field contains invalid UTF-8
//
// protobuf'un string alanı geçerli UTF-8 ŞART koşar ve marshaling
// hatası TÜM BATCH'i düşürür — yani tek bir bozuk bayt yüzünden o
// tur hiçbir self-telemetri span'i gitmiyor. Sessiz ve toplu bir
// kayıp: gözlemlenebilirlik katmanının kendisi kör kalıyor.
//
// Bozuk baytın kaynağı bizim ürettiğimiz metin DEĞİL: ClickHouse hata
// mesajları kusurlu veriyi AYNEN ALINTILAR ("Cannot convert string
// '<bayt>' …"), ve o veri filodan gelir. Yani filodaki tek bir bozuk
// span attribute'u, bizim kendi izlerimizi düşürebiliyor.
//
// Maliyet: geçerli string'de yalnız bir tarama (utf8.ValidString) ve
// SIFIR ayırma. Yalnız bozuk olan yeniden kurulur — sıcak yolda
// pratikte bedelsiz.
func SafeAttr(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}
