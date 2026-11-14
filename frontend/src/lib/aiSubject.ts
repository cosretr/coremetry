// aiSubject — `?ai=<kind>:<id>[:<extra>]` URL kodeği (v0.9.477, onaylı
// AI-drawer mockup'ı). Yüzey-başına inline CopilotExplain panelleri TEK
// sağ-kenar çekmeceye taşındı; çekmecenin ne göstereceğini artık ADRES
// belirliyor — "URL = tek doğruluk kaynağı" ev kuralı (kopyalanan link
// aynı AI açıklamasını açar).
//
// Neden ayrı, saf bir modül: parse/format React'ten bağımsız test
// edilebilsin (vitest node ortamı). Emsal: lib/inboxUrl.ts (v0.8.291).
//
// Biçim:
//   trace | problem | incident | anomaly | runbook | exception
//       → "<kind>:<encodeURIComponent(id)>"
//   span            → "span:<enc(traceId)>:<enc(spanId)>"
//   service-health  → "service-health:<enc(service)>:<fromNs>:<toNs>"
//   charts          → "charts:<enc(service)>:<fromNs>:<toNs>:<scope>"
//   log-spike       → "log-spike:<enc(service)|*>:<fromNs>:<toNs>:<sel|auto>:<enc(env)>:<enc(q≤200)>:<all|error>"
//                     (v0.10.1284 — Logs histogramının ✨ "artışı açıkla"; son
//                     segment isteğe bağlı, yoksa all)
//   log             → "log:<tsNs>:<enc(service)>:<enc(pod)>:<traceId>:<hash8>"
//                     (v0.10.1291 — tek log satırının ✨'i; tsNs ONDALIK METİN
//                     kalır — 1.7e18 JS sayısına sığmaz; hash = gövde önünün
//                     FNV-1a 32'si, lib/logLineSubject.ts ↔ mcptools/log_line_subject.go)
//   endpoint        → "endpoint:<enc(service)>:<enc(route)>:<fromNs>:<toNs>[:<METHOD>]"
//                     (v0.10.1367 — endpoint'in tek tık "CoSRE'ye sor"u; METHOD
//                     beyaz listeli, bilinmeyen fiil REDDEDİLMEZ düşer; env kodekte yok)
//   metric_panel    → "metric_panel:<konum>:<fromNs>:<toNs>:<bFromNs|0>:<bToNs|0>:<enc(seri),…>[:cmp]"
//                     (v0.10.1435 — panelden "Bu grafiği açıkla". Kodek SORGU
//                     taşımaz, KONUM taşır: d.<pano>.<panel> | x.<A-D> | m; sorgu
//                     niyetin yapısal yükünde (ChatExplainAsk.panel). Brush ya
//                     0:0 ya da pencerenin içinde; ≤3 seçili seri, her biri ≤80;
//                     kodek 400'ü aşarsa sondaki seriler düşer. Go ikizi
//                     internal/api/ai_subject.go parseAiMetricPanel)
//
// id HER ZAMAN encodeURIComponent'ten geçer: servis adı / fingerprint
// içinde ':' geçse bile ayraç belirsizleşmez (parse ':' üzerinden böler).

// v0.10.948 — trace başlığı i18n'den ("CoSRE’ye sor" / "Ask CoSRE"); modül
// hâlâ saf: dil bir ARGÜMAN, verilmezse currentLang() (hook yok).
import { currentLang, t, type Lang } from './i18n';

export const AI_PARAM = 'ai';

// AI_CODE_PARAM (v0.10.81, operatör-bildirimli): "Kodu da incele"
// seçimi URL'de yaşar. Paylaşılan bir Explain linki kodsuz açılıyordu
// ve alıcı kutuya yeniden basmak zorunda kalıyordu — ikinci bir yerel
// LLM turu. URL-tek-gerçek-kaynak ailesinin (v0.8.256/265/267,
// v0.10.76) çekmece üyesi.
//
// ⚠ v0.10.60 kararıyla ÇELİŞMEZ, birleşir: kutu uygulama İÇİNDE her
// açılışta kapalı başlar (gizli kalıcılık yok — useAiSubject.setSubject
// her kullanıcı-açılışında paramı SİLER); param yalnız paylaşılan /
// yapıştırılan linkte ve sayfa yenilemede hayatta kalır. Yani varsayılan
// kapalı kalır, paylaşım ise gördüğünü aynen taşır.
export const AI_CODE_PARAM = 'aicode';

// v0.10.1407 — readAiCodeParam / writeAiCodeParam (✨ kartının "Kodu da
// incele" kutusu) silindi: kart özne kipiyle gitti; ?aicode=1 artık yalnız
// sohbetin kod turu niyeti (CopilotChat ?ai= effect'i, v0.10.1406).

// AI_SRC_PARAM (v0.10.432, CoSRE router boşlukları D8) — çekmecenin
// hangi AFFORDANS'tan açıldığı: "nudge" = trace ilk açılış baloncuğu.
// Explain isteğine `?src=` olarak taşınır, sunucu yüzey etiketini
// "explain-trace:nudge" yapar (/ai ayrı sayar). Beyaz listeli: bilinmeyen
// değer HİÇ gönderilmez. aicode gibi yalnız o açılışta yaşar — useAiSubject
// her özne değişimi/kapanışta siler.
export const AI_SRC_PARAM = 'aisrc';
export const AI_SRC_VALUES = ['nudge', 'chat'] as const; // chat (v0.10.460) — sohbetten açılan Explain
export type AISrc = typeof AI_SRC_VALUES[number];

export const AI_KINDS = [
  'trace', 'span', 'problem', 'incident', 'anomaly',
  'service-health', 'runbook', 'exception', 'charts',
  'log-spike', // v0.10.1284 — AI SRE Faz 1a: log artışını ne açıklıyor
  'log',       // v0.10.1291 — tek log satırını açıkla
  'endpoint',  // v0.10.1367 — endpoint "CoSRE'ye sor" (sohbet ön-getirmesiyle)
  'metric_panel', // v0.10.1435 — panelden "Bu grafiği açıkla" (Metrics / Explore / dashboard)
] as const;

export type AIKind = typeof AI_KINDS[number];

// Grafik kapsamı (v0.9.1033, onaylı ServiceCharts AI mockup'ı):
// Ⓐ toolbar düğmesi 'all', Ⓑ kart başlığındaki ✨ tek kartın kapsamı.
// Değerler backend'in normalizeChartScope'u ile BİREBİR aynı
// (internal/api/explain_service_charts.go) — ayrışırlarsa çekmecedeki
// çip ile modelin odaklandığı grafik farklı olur.
export const CHART_SCOPES = ['all', 'rps', 'err', 'dur'] as const;
export type ChartScope = typeof CHART_SCOPES[number];

const CHART_SCOPE_SET = new Set<string>(CHART_SCOPES);

// Kapsam etiketleri ServiceCharts'ın kart başlıklarıyla aynı sözcüklerdi:
// çekmecedeki çip, operatörün az önce tıkladığı kartın adını gösteriyordu.
// v0.10.2154 — o kartlar (Service › Details › Performance) operatör kararıyla
// kalktı; uygulamada `charts` öznesi üreten düğme kalmadı. Özne + kodek +
// etiketler DURUYOR: paylaşılmış `?ai=charts:…` linkleri ve kayıtlı sohbet
// turları (ChartsSignalsCard) hâlâ bu yoldan çizilir; sunucu ucu da yerinde.
export function chartScopeLabel(s: ChartScope): string {
  switch (s) {
    case 'rps': return 'RPS by operation';
    case 'err': return 'Error rate by operation';
    case 'dur': return 'P99 latency by operation';
    default:    return 'Tüm RED grafikleri';
  }
}

// Basit özneler: tek bir id yeter (backend geri kalanını kendi toplar).
type SimpleKind = Exclude<AIKind, 'span' | 'service-health' | 'charts' | 'log-spike' | 'log' | 'endpoint' | 'metric_panel'>; // v0.10.1367 — endpoint; v0.10.1435 — metric_panel

// v0.10.1284 — log-spike öznesinin kipi: sel = operatörün zoom'ladığı aralık
// (verilen artış), auto = pencerede tepe otomatik bulunur.
export type LogSpikeMode = 'sel' | 'auto';
export type LogSpikeLevel = 'all' | 'error';
export const LOG_SPIKE_QUERY_MAX = 200;

// v0.10.1367 — endpoint öznesinin HTTP fiili beyaz listesi (sunucu
// chat_endpoint_prefetch.go httpVerbSet ile aynı küme). Bilinmeyen fiil
// özneyi reddettirmez, yalnız DÜŞER: fiilsiz endpoint de sorulabilir.
export const ENDPOINT_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const;
const ENDPOINT_METHOD_SET = new Set<string>(ENDPOINT_METHODS);

/** v0.10.1367 — fiili büyük harfe çevirir; beyaz listede değilse undefined. */
export function endpointAiMethod(raw: string | null | undefined): string | undefined {
  const m = (raw ?? '').trim().toUpperCase();
  return ENDPOINT_METHOD_SET.has(m) ? m : undefined;
}

export type AISubject =
  | { kind: SimpleKind; id: string }
  // span → id = traceId, spanId = trace içindeki hedef span (v0.5.144).
  | { kind: 'span'; id: string; spanId: string }
  // service-health → id = servis adı; prompt CANLI RED serisini istediği
  // için pencere de linkte taşınır (aksi halde paylaşılan link başka bir
  // pencereyi açıklar).
  | { kind: 'service-health'; id: string; fromNs: number; toNs: number }
  // charts → id = servis adı; pencere linkte taşınır (service-health ile
  // aynı gerekçe) + hangi kartın sorulduğu.
  | { kind: 'charts'; id: string; fromNs: number; toNs: number; scope: ChartScope }
  // log-spike → id = servis adı ('' = tüm servisler, adreste '*'); pencere +
  // kip + env + arama + seviye linkte taşınır (paylaşılan link AYNI artışı açar).
  | { kind: 'log-spike'; id: string; fromNs: number; toNs: number; mode: LogSpikeMode; env: string; q: string; level: LogSpikeLevel }
  // log → id = servis adı; satır zaman (ns, ondalık metin) + pod + trace + gövde
  // özetiyle kimliklenir — sunucu satırı logstore'dan YENİDEN okur (v0.10.1291).
  | { kind: 'log'; id: string; tsNs: string; pod: string; traceId: string; hash: string }
  // endpoint → id = servis adı; rota + (isteğe bağlı) fiil + pencere linkte
  // taşınır (v0.10.1367). Çekmece görünür bir ilk soru sorar; sunucunun
  // operasyon/endpoint ön-getirmesi onu deterministik çözer.
  | { kind: 'endpoint'; id: string; route: string; method?: string; fromNs: number; toNs: number }
  // metric_panel → id = panel KONUMU (d.<pano>.<panel> | x.<harf> | m); pencere +
  // brush (0 = yok) + ≤3 seçili seri (+ önceki pencereyle kıyas). Sorgu kodekte
  // YOK (v0.10.1435).
  | { kind: 'metric_panel'; id: string; fromNs: number; toNs: number; brushFromNs: number; brushToNs: number; series: string[]; cmp?: boolean };

// v0.10.1435 — metric_panel kodeğinin tavanları (Go: ai_subject.go).
export const METRIC_PANEL_SERIES_MAX = 80;
export const METRIC_PANEL_SELECTED_MAX = 3;
const AI_SUBJECT_MAX = 400;
const METRIC_PANEL_ID_RE = /^[A-Za-z0-9_-]{1,64}$/;

/** v0.10.1435 — panel konumu: `m` | `x.<A-D>` | `d.<pano>.<panel>` (Go parseMetricPanelLoc ikizi). */
export function isMetricPanelLoc(loc: string): boolean {
  if (loc === 'm') return true;
  if (/^x\.[A-D]$/.test(loc)) return true;
  if (!loc.startsWith('d.')) return false;
  const parts = loc.slice(2).split('.');
  return parts.length === 2 && METRIC_PANEL_ID_RE.test(parts[0]) && METRIC_PANEL_ID_RE.test(parts[1]);
}

/** v0.10.1435 — dashboard panelinin konumu (kimlikler biçimsizse null → ✨ yok). */
export function dashboardPanelLoc(dashId: string, panelId: string): string | null {
  const loc = `d.${dashId}.${panelId}`;
  return isMetricPanelLoc(loc) ? loc : null;
}

const KIND_SET = new Set<string>(AI_KINDS);

// Bozuk yüzdeli dizide decodeURIComponent atar; çekmece bir URL yüzünden
// asla patlamamalı → boş string = "geçersiz" (parse null döner).
function safeDecode(s: string): string {
  try { return decodeURIComponent(s); } catch { return ''; }
}

// ── SELF biçimi (v0.10.731, operator-reported) ───────────────────────────
// "Explain trace diyince sonuna tekrar trace id ekliyor; onun yerine
// &explain=1 gibi bir query string olsa."
//
// Haklı: /trace?id=<32 karakter> sayfasında çekmece `?ai=trace:<AYNI 32
// karakter>` yazıyordu — adres iki katına çıkıyor, kopyalanan link
// okunmaz oluyordu. Özne SAYFANIN KENDİ kimliği olduğunda id'yi ikinci kez
// yazmanın bilgi değeri yok: `?ai=trace` yeter, id sayfanın `?id=`
// parametresinden çözülür.
//
// Kapsam bilinçli DAR: yalnız sayfa kimliği URL'de tek bir parametrede
// yaşayan basit özneler. span (spanId), service-health/charts (pencere,
// kapsam) fazladan veri taşır, uzun biçimde kalır. Yeni bir kind katmak
// tek satır (aşağıdaki harita).
//
// Geriye dönük: uzun biçim AYNEN parse edilir (eski linkler, paylaşılan
// adresler, CoSRE'nin ürettiği derin linkler kırılmaz) ve sunucuya giden
// özne parametresi (AIDrawerBody) her zaman KANONİK uzun biçimdir.
// v0.10.743 — harita genişledi: problem (Inbox ?problem=), exception
// (/problems ?exc= = fingerprint) , incident (/incident ?id=). span KISMİ:
// `span:<spanId>` — trace id sayfanın ?id='sinden, spanId açık kalır (aynı
// sayfada başka span seçilince çekmece o span'e KAYMAZ; özne sabit).
const SELF_PARAM: Partial<Record<AIKind, string>> = { trace: 'id', span: 'id', problem: 'problem', exception: 'exc', incident: 'id' };

/** Bu kind sayfa kimliğinden çözülebiliyorsa o parametrenin adı. */
export function aiSelfParam(kind: string): string | null {
  return SELF_PARAM[kind as AIKind] ?? null;
}

/**
 * formatAiParamForUrl — ADRESE yazılacak biçim. Özne sayfanın kendi
 * kimliğiyse kısa (`trace`), değilse kanonik uzun biçim.
 * Karşılaştırma/anahtar/sunucu için formatAiParam (kanonik) kullanılır.
 */
export function formatAiParamForUrl(s: AISubject, pageParams: URLSearchParams | null | undefined): string {
  const self = SELF_PARAM[s.kind];
  if (self && pageParams && s.id && pageParams.get(self) === s.id) {
    // v0.10.743 — span kısmi biçim: trace id sayfadan, spanId açık.
    if (s.kind === 'span') return `span:${encodeURIComponent(s.spanId)}`;
    return s.kind;
  }
  return formatAiParam(s);
}

export function formatAiParam(s: AISubject): string {
  const head = `${s.kind}:${encodeURIComponent(s.id)}`;
  if (s.kind === 'span') return `${head}:${encodeURIComponent(s.spanId)}`;
  if (s.kind === 'service-health') return `${head}:${s.fromNs}:${s.toNs}`;
  if (s.kind === 'charts') return `${head}:${s.fromNs}:${s.toNs}:${s.scope}`;
  if (s.kind === 'endpoint') { // v0.10.1367 — fiil yalnız beyaz listedeyse (kanonik)
    const m = endpointAiMethod(s.method);
    return `${head}:${encodeURIComponent(s.route)}:${s.fromNs}:${s.toNs}${m ? ':' + m : ''}`;
  }
  if (s.kind === 'log') {
    return `log:${s.tsNs}:${encodeURIComponent(s.id)}:${encodeURIComponent(s.pod)}:${s.traceId}:${s.hash}`;
  }
  if (s.kind === 'log-spike') {
    const q = s.q.slice(0, LOG_SPIKE_QUERY_MAX);
    return `log-spike:${s.id ? encodeURIComponent(s.id) : '*'}:${s.fromNs}:${s.toNs}:${s.mode}`
      + `:${encodeURIComponent(s.env)}:${encodeURIComponent(q)}:${s.level}`;
  }
  if (s.kind === 'metric_panel') return formatMetricPanel(s); // v0.10.1435
  return head;
}

// formatMetricPanel — v0.10.1435: seri adı 80 karaktere (kod noktası) kırpılır,
// ≤3 seri; kodek 400'ü aşarsa sondaki seriler düşer (Go formatAiMetricPanel ikizi).
function formatMetricPanel(s: Extract<AISubject, { kind: 'metric_panel' }>): string {
  const brush = s.brushFromNs > 0 && s.brushToNs > s.brushFromNs ? `${s.brushFromNs}:${s.brushToNs}` : '0:0';
  const items: string[] = [];
  for (const v of s.series) {
    if (items.length >= METRIC_PANEL_SELECTED_MAX) break;
    const cut = [...v].slice(0, METRIC_PANEL_SERIES_MAX).join('');
    if (cut) items.push(encodeURIComponent(cut));
  }
  const tail = s.cmp ? ':cmp' : '';
  const head = `metric_panel:${encodeURIComponent(s.id)}:${s.fromNs}:${s.toNs}:${brush}:`;
  while (items.length > 0 && head.length + items.join(',').length + tail.length > AI_SUBJECT_MAX) items.pop();
  return head + items.join(',') + tail;
}

export function parseAiParam(
  raw: string | null | undefined,
  // v0.10.731 — SELF biçimi (`?ai=trace`) için sayfanın kendi parametreleri.
  // Verilmezse kısa biçim çözülemez ve null döner (sessizce YANLIŞ özne
  // açmaktansa hiç açmamak).
  pageParams?: URLSearchParams | null,
): AISubject | null {
  if (!raw) return null;
  const parts = raw.split(':');
  const kind = parts[0];
  if (!KIND_SET.has(kind)) return null;
  if (parts.length === 1) {
    if (kind === 'span') return null; // span kısmi biçimde en az spanId ister
    const self = SELF_PARAM[kind as AIKind];
    const v = self && pageParams ? (pageParams.get(self) ?? '') : '';
    return v ? { kind: kind as SimpleKind, id: v } : null;
  }
  // v0.10.743 — span kısmi biçimi: `span:<spanId>` (2 parça) → trace id
  // sayfanın ?id='sinden; yoksa null (sessizce yanlış trace açmaktansa hiç).
  if (kind === 'span' && parts.length === 2) {
    const traceID = pageParams ? (pageParams.get(SELF_PARAM.span ?? 'id') ?? '') : '';
    const spanId = safeDecode(parts[1] ?? '');
    return traceID && spanId ? { kind: 'span', id: traceID, spanId } : null;
  }
  if (kind === 'log-spike') return parseLogSpike(parts);
  if (kind === 'log') return parseLogLine(parts);
  if (kind === 'endpoint') return parseEndpoint(parts); // v0.10.1367
  if (kind === 'metric_panel') return parseMetricPanel(parts); // v0.10.1435
  const id = safeDecode(parts[1] ?? '');
  if (!id) return null;

  if (kind === 'span') {
    if (parts.length !== 3) return null;
    const spanId = safeDecode(parts[2] ?? '');
    if (!spanId) return null;
    return { kind: 'span', id, spanId };
  }
  if (kind === 'service-health' || kind === 'charts') {
    // charts bir segment daha taşır (kapsam); pencere doğrulaması ORTAK.
    if (parts.length !== (kind === 'charts' ? 5 : 4)) return null;
    const fromNs = Number(parts[2]);
    const toNs = Number(parts[3]);
    // Pencere hem sonlu hem ARTAN olmalı; ters/sıfır pencere backend'e
    // anlamsız bir sorgu attırır, bunu URL katmanında kes.
    if (!Number.isFinite(fromNs) || !Number.isFinite(toNs)) return null;
    if (fromNs <= 0 || toNs <= fromNs) return null;
    if (kind === 'charts') {
      // Kapsam TANIMAZ isek reddetmiyoruz: elle düzenlenmiş bir linkte
      // çekmeceyi hiç açmamaktansa EN GENİŞ kapsamı açmak doğru davranış
      // (backend'in normalizeChartScope'u da aynısını yapar). Bu yüzden
      // parse GEVŞEK, format KANONİK: parse→format bilinmeyen kapsamı
      // 'all'a sabitler.
      const raw = parts[4] ?? '';
      const scope = (CHART_SCOPE_SET.has(raw) ? raw : 'all') as ChartScope;
      return { kind: 'charts', id, fromNs, toNs, scope };
    }
    return { kind: 'service-health', id, fromNs, toNs };
  }
  // Basit özneler fazladan segment taşımaz — taşıyorsa bozuk/elle
  // düzenlenmiş bir link demektir, sessizce yanlış özne açmaktansa reddet.
  if (parts.length !== 2) return null;
  return { kind: kind as SimpleKind, id };
}

// parseLogSpike — v0.10.1284: 7 ya da 8 parça; pencere service-health ile aynı
// katılıkta (sonlu, artan, pozitif). Bilinmeyen kip → null (yanlış soruyu
// sormaktansa hiç açmamak); bilinmeyen seviye → all (en geniş).
function parseLogSpike(parts: string[]): AISubject | null {
  if (parts.length !== 7 && parts.length !== 8) return null;
  const rawId = parts[1] ?? '';
  const id = rawId === '*' ? '' : safeDecode(rawId);
  if (rawId !== '*' && !id) return null;
  const fromNs = Number(parts[2]);
  const toNs = Number(parts[3]);
  if (!Number.isFinite(fromNs) || !Number.isFinite(toNs) || fromNs <= 0 || toNs <= fromNs) return null;
  const mode = parts[4];
  if (mode !== 'sel' && mode !== 'auto') return null;
  const env = safeDecode(parts[5] ?? '');
  const q = safeDecode(parts[6] ?? '').slice(0, LOG_SPIKE_QUERY_MAX);
  const level: LogSpikeLevel = parts[7] === 'error' ? 'error' : 'all';
  return { kind: 'log-spike', id, fromNs, toNs, mode, env, q, level };
}

// parseLogLine — v0.10.1291: TAM 6 parça; zaman pozitif ondalık (≤20 basamak,
// metin kalır), servis zorunlu, trace boş ya da 32-hex, özet 8-hex. Bozuk →
// null (yanlış satırı açıklamaktansa hiç açmamak).
const LOG_TS_RE = /^[1-9][0-9]{0,19}$/;
const LOG_TRACE_RE = /^[0-9a-f]{32}$/;
const LOG_HASH_RE = /^[0-9a-f]{8}$/;
function parseLogLine(parts: string[]): AISubject | null {
  if (parts.length !== 6) return null;
  const [, tsNs, rawSvc, rawPod, traceId, hash] = parts;
  if (!LOG_TS_RE.test(tsNs ?? '') || !LOG_HASH_RE.test(hash ?? '')) return null;
  if (traceId && !LOG_TRACE_RE.test(traceId)) return null;
  const id = safeDecode(rawSvc ?? '');
  if (!id) return null;
  const pod = rawPod ? safeDecode(rawPod) : '';
  if (rawPod && !pod) return null;
  return { kind: 'log', id, tsNs: tsNs ?? '', pod, traceId: traceId ?? '', hash: hash ?? '' };
}

// parseEndpoint — v0.10.1367: 5 ya da 6 parça; servis + rota zorunlu, pencere
// service-health ile aynı katılıkta (sonlu, artan, pozitif). Bilinmeyen fiil
// → düşer (fiilsiz özne), reddedilmez.
function parseEndpoint(parts: string[]): AISubject | null {
  if (parts.length !== 5 && parts.length !== 6) return null;
  const id = safeDecode(parts[1] ?? '');
  const route = safeDecode(parts[2] ?? '');
  if (!id || !route) return null;
  const fromNs = Number(parts[3]);
  const toNs = Number(parts[4]);
  if (!Number.isFinite(fromNs) || !Number.isFinite(toNs) || fromNs <= 0 || toNs <= fromNs) return null;
  const method = endpointAiMethod(parts[5]);
  return { kind: 'endpoint', id, route, ...(method ? { method } : {}), fromNs, toNs };
}

// parseMetricPanel — v0.10.1435: TAM 7|8 parça; konum biçimli, pencere artan,
// brush 0:0 ya da pencerenin içinde artan, ≤3 seri (her biri 1..80, kontrol
// karakteri yok), 8. parça yalnız "cmp". Go parseAiMetricPanel ikizi.
// eslint-disable-next-line no-control-regex
const CONTROL_RE = /[\u0000-\u001f\u007f-\u009f]/;
const NS_RE = /^[0-9]{1,20}$/;
function parseMetricPanel(parts: string[]): AISubject | null {
  if (parts.length !== 7 && parts.length !== 8) return null;
  const loc = safeDecode(parts[1] ?? '');
  if (!isMetricPanelLoc(loc)) return null;
  if (!NS_RE.test(parts[2] ?? '') || !NS_RE.test(parts[3] ?? '')) return null;
  const fromNs = Number(parts[2]);
  const toNs = Number(parts[3]);
  if (!(fromNs > 0) || !(toNs > fromNs)) return null;
  let brushFromNs = 0, brushToNs = 0;
  if (parts[4] !== '0' || parts[5] !== '0') {
    if (!NS_RE.test(parts[4] ?? '') || !NS_RE.test(parts[5] ?? '')) return null;
    brushFromNs = Number(parts[4]); brushToNs = Number(parts[5]);
    if (!(brushFromNs > 0) || !(brushToNs > brushFromNs) || brushFromNs < fromNs || brushToNs > toNs) return null;
  }
  const series: string[] = [];
  if (parts[6]) {
    const items = parts[6].split(',');
    if (items.length > METRIC_PANEL_SELECTED_MAX) return null;
    for (const it of items) {
      const v = safeDecode(it);
      if (!v || [...v].length > METRIC_PANEL_SERIES_MAX || CONTROL_RE.test(v)) return null;
      series.push(v);
    }
  }
  let cmp = false;
  if (parts.length === 8) {
    if (parts[7] !== 'cmp') return null;
    cmp = true;
  }
  return { kind: 'metric_panel', id: loc, fromNs, toNs, brushFromNs, brushToNs, series, ...(cmp ? { cmp } : {}) };
}

// aiSubjectTitle — özne türünün görünen adı (eski çekmece başlığı; v0.10.1407
// sonrası yalnız aiSubjectTopic'in yedeği — TOPIC'te olmayan türler).
// v0.10.948 (CoSRE Faz B) — trace adı düğmenin adıyla aynı ("CoSRE’ye sor",
// EN "Ask CoSRE"). `lang` verilmezse etkin dil.
export function aiSubjectTitle(s: AISubject, lang: Lang = currentLang()): string {
  switch (s.kind) {
    case 'trace':          return t('ai.askCosre', lang);
    // v0.10.1338 (operatör) — her AI açıklama affordance'ı "Ask CoSRE" /
    // "CoSRE’ye sor" adını taşır (trace v0.10.944, exception v0.10.1326);
    // modelin KONU satırı aiSubjectTopic'te semantik kalır.
    case 'span':
    case 'problem':
    case 'incident':
    case 'anomaly':        return t('ai.askCosre', lang);
    case 'exception':      return t('ai.askCosre', lang); // v0.10.1326 (operatör) — düğme adıyla aynı
    case 'runbook':        return 'Runbook AI';
    case 'service-health': return 'AI triage';
    // Mockup başlığı: çekmece bir GRAFİĞİ anlatıyor, servis sağlığını
    // triyaj etmiyor — iki yüzey aynı sayfada yaşadığı için ad ayrımı
    // operatörün hangi cevabı okuduğunu belirler.
    case 'charts':         return 'AI grafik özeti';
    // v0.10.1338 (operatör) — log / log-spike başlıkları da "CoSRE’ye sor";
    // eski ifadeler aiSubjectTopic'te modelin KONU satırı olarak kalır.
    case 'log-spike':                                    // v0.10.1284
    case 'log':            return t('ai.askCosre', lang); // v0.10.1291
    case 'endpoint':       return t('ai.askCosre', lang); // v0.10.1367
    case 'metric_panel':   return t('ai.askCosre', lang); // v0.10.1435
  }
}

// aiSubjectTopic — v0.10.1326: MODEL bağlamındaki konu adı (drawerChat KONU
// satırı). Çekmece başlığı exception için "CoSRE’ye sor" oldu; model ise
// sorunun bir kök-neden açıklaması olduğunu bilmeye devam etmeli.
// v0.10.1338 — span/problem/incident/anomaly başlıkları da "CoSRE’ye sor"
// oldu; model eski semantik konuları görmeye devam eder.
const TOPIC: Partial<Record<AIKind, string>> = {
  exception: 'Explain root cause',
  span: 'Explain span',
  problem: 'Explain problem',
  incident: 'Explain incident',
  anomaly: 'Explain anomaly',
  'log-spike': 'Log artışını açıkla', // v0.10.1338
  log: 'Log satırını açıkla',         // v0.10.1338
  endpoint: 'Endpoint analizi',       // v0.10.1367
  metric_panel: 'Grafiği açıkla',     // v0.10.1435
};
export function aiSubjectTopic(s: AISubject): string {
  return TOPIC[s.kind] ?? aiSubjectTitle(s, 'tr');
}
