// steps.ts — v0.10.1334 (uygulama içi rehberli tur, R1) · v0.10.1336 (R2:
// adım 3–5 açık + alt adımlar). SAF: DOM yok, React yok. Turun BEŞ adımı
// burada beyan edilir; gösterge her zaman "Adım N / 5".
//
// Bağlam (`TourCtx`) tur BAŞLARKEN bir kez çözülür (TourProvider): adımın
// rotası ve açık/kapalı kararı turun ortasında değişmez — gösterge
// zıplamaz, Geri her zaman aynı adıma döner.
//
// v0.10.1336 — ALT ADIM (frame): bir adım aynı sayfada birden çok hedefi
// sırayla gösterebilir (`frames`). Adımın rotası ve göstergedeki segmenti
// tek; İleri/Geri önce adımın kareleri arasında yürür. `frames` yoksa adımın
// kendi target/placement/bodyKey alanları tek kare olur (R1 adımları aynen).
// Bir kare `effect` taşıyabilir (yalnız 'cosre-prefill': CoSRE çekmecesini
// açıp composer'ı GÖNDERMEDEN doldurur — TourProvider uygular) ve
// `keepFocus` (tur bu karede biterse odak iadesi yerine bu elemana odak).
//
// NOT gerekçeleri: `requires` bir gerekçe döndürür; çoğu adımı ATLATIR
// (gösterge kesik), ama NOTE_REASONS'takiler adımı ortalanmış bir NOT kartı
// olarak GÖSTERİR (copilot_disabled: "CoSRE bu kurulumda kapalı" + Bitir).
//
// Hedef sözleşmesi: `target` bir `[data-tour="…"]` seçicisi (ya da onunla
// başlayan bir alt-seçici) veya null (ortalanmış kart). Çapa kimliklerinin
// sahibi dosyalar TOUR_ANCHORS'ta; targetPin.test her açık karenin çapasının
// sahibinde GERÇEKTEN durduğunu ve src'de sahipsiz data-tour kalmadığını
// pinler.

import { serviceHref } from '@/lib/serviceHref';
import { encodeFilters } from '@/lib/urlState';
import type { FilterExpr } from '@/lib/types';

export type TourStepId = 'welcome' | 'services' | 'operations' | 'traces' | 'cosre';

/** Bir adımın atlanma/not gerekçesi (gösterge ipucu + i18n `tour.note.<reason>`). */
export type TourSkipReason = 'not_yet' | 'no_services' | 'copilot_disabled';

/** v0.10.1336 — adımı ATLATMAYAN, ortalanmış not kartına çeviren gerekçeler. */
export const NOTE_REASONS: ReadonlySet<TourSkipReason> = new Set<TourSkipReason>(['copilot_disabled']);

export type TourPlacement = 'center' | 'top' | 'bottom';

/** v0.10.1336 — karenin girişte tetiklediği yan etki (TourProvider uygular). */
export type TourFrameEffect = 'cosre-prefill';

export interface TourCtx {
  /** En yoğun servis (5 dk); veri yoksa null. */
  service: string | null;
  /** Turu başlatan sayfanın ?range= değeri (yoksa null → sayfa varsayılanı). */
  range: string | null;
  /** Toplam servis; -1 = bilinmiyor (okuma düştü → adımlar açık kalır, zaman aşımı notu). */
  serviceCount: number;
  /** Σ spanCount (son 5 dk, en çok 50 servis). */
  spans5m: number;
  environments: number;
  copilotEnabled: boolean;
  /** Tur bitince dönülecek konum (pathname + search). */
  returnTo: string;
}

/** v0.10.1336 — adımın bir karesi (alt adım). */
export interface TourFrame {
  /** Benzersiz anahtar (zaman aşımı + odak + aria-live bu anahtarla). */
  key: string;
  target: string | null;
  fallbackTarget?: string;
  placement: TourPlacement;
  /** Yoksa adımın titleKey'i. */
  titleKey?: string;
  bodyKey: string;
  effect?: TourFrameEffect;
  /** Tur bu karede (hedef bulunmuşken) biterse odak buraya — örtünün odak iadesi atlanır. */
  keepFocus?: string;
}

export interface TourStep {
  id: TourStepId;
  /** Adımın sayfası; null = bulunduğun yerde kal. */
  route: (ctx: TourCtx) => string | null;
  target: string | null;
  /** Hedef belirmezse (ör. ilk satır yok) halkanın düşeceği seçici. */
  fallbackTarget?: string;
  placement: TourPlacement;
  titleKey: string;
  bodyKey: string;
  /** v0.10.1336 — alt adımlar; varsa target/placement/bodyKey yerine bunlar. */
  frames?: readonly TourFrame[];
  /** null = adım açık; aksi hâlde atlanma (ya da NOTE_REASONS ise not) gerekçesi. */
  requires: (ctx: TourCtx) => TourSkipReason | null;
}

/** Tur içeriği anlamlı biçimde değişince artar (PUT /api/tour/state version). */
export const TOUR_VERSION = 2; // v0.10.1336 — R2: adım 3–5 açıldı

/** Bekleme tavanı: hedef bu sürede görünmezse kart notla sürer (asla takılmaz). */
export const TARGET_TIMEOUT_MS = 8000;

/** v0.10.1336 — copilot yapılandırması bilinmiyorsa en çok bu kadar beklenir, sonra kapalı sayılır. */
export const COPILOT_WAIT_MS = 8000;

/** data-tour çapası → sahibi dosya (src'ye göre). targetPin.test okur. */
export const TOUR_ANCHORS: Record<string, string> = {
  'services-table': 'pages/Services.tsx',
  'services-health-dot': 'pages/Services.tsx',
  'services-col-spans': 'pages/Services.tsx',
  'services-col-errors': 'pages/Services.tsx',
  'services-col-p99': 'pages/Services.tsx',
  'services-col-delta': 'pages/Services.tsx',
  // v0.10.1336 — adım 3 (operations)
  'svc-crumbs': 'pages/Service.tsx',
  'svc-pin': 'pages/Service.tsx',
  'svc-nav': 'pages/Service.tsx',
  'svc-tab-operations': 'pages/Service.tsx',
  'ops-impact-col': 'pages/service/OperationsTable.tsx',
  'ops-normalized': 'pages/service/OperationsTable.tsx',
  'ops-table': 'pages/service/OperationsTable.tsx',
  // v0.10.1336 — adım 4 (traces)
  'traces-service': 'pages/Traces.tsx',
  'traces-min-ms': 'pages/Traces.tsx',
  'traces-quick-chips': 'pages/Traces.tsx',
  'traces-active-chips': 'pages/Traces.tsx',
  'traces-table': 'pages/Traces.tsx',
  // v0.10.1336 — adım 5 (cosre)
  'cosre-fab': 'components/ai/CopilotFab.tsx', // v0.10.1599 — FAB taşındı
  'cosre-drawer': 'components/CopilotChat.tsx',
  'cosre-composer': 'components/CopilotChat.tsx',
};

const noServices = (ctx: TourCtx): TourSkipReason | null => (ctx.serviceCount === 0 ? 'no_services' : null);

function withRange(path: string, ctx: TourCtx): string {
  return ctx.range ? `${path}?range=${encodeURIComponent(ctx.range)}` : path;
}

/** v0.10.1336 — adım 4'ün hazır süzgeci: hızlı çip `status_code = error`. */
export const TOUR_TRACES_FILTERS: readonly FilterExpr[] = [{ k: 'status_code', op: '=', v: ['error'] }];
export const TOUR_TRACES_MIN_MS = 500;

/**
 * v0.10.1336 — adım 4 rotası, Traces.tsx'in okuduğu biçimde: service (~342),
 * filters (decodeFilters, ~365), minMs (~347), view=list, range (ham ?range=
 * değeri zaten encodeRange çıktısı; yoksa sayfa varsayılanı).
 */
export function tracesTourHref(ctx: Pick<TourCtx, 'service' | 'range'>): string {
  const q = new URLSearchParams();
  if (ctx.service) q.set('service', ctx.service);
  q.set('filters', encodeFilters([...TOUR_TRACES_FILTERS]));
  q.set('minMs', String(TOUR_TRACES_MIN_MS));
  q.set('view', 'list');
  if (ctx.range) q.set('range', ctx.range);
  return `/traces?${q.toString()}`;
}

export const TOUR_STEPS: readonly TourStep[] = [
  {
    id: 'welcome',
    route: () => null,
    target: null,
    placement: 'center',
    titleKey: 'tour.welcome.title',
    bodyKey: 'tour.welcome.body',
    requires: () => null,
  },
  {
    id: 'services',
    route: ctx => withRange('/services', ctx),
    // İlk veri satırı (Services.tsx her <tr>'ye data-row-idx basar; toplam
    // satırı .agg-row'da bu damga yok). Satır yoksa tablonun kendisi.
    target: '[data-tour="services-table"] tbody tr[data-row-idx="0"]',
    fallbackTarget: '[data-tour="services-table"]',
    placement: 'bottom',
    titleKey: 'tour.services.title',
    bodyKey: 'tour.services.body',
    requires: noServices,
  },
  // ── v0.10.1336 — R2 ────────────────────────────────────────────────
  {
    id: 'operations',
    // serviceHref(svc, {range}) + tab=operations (Service.tsx ?tab okur).
    route: ctx => (ctx.service ? serviceHref(ctx.service, { range: ctx.range, tab: 'operations' }) : null),
    target: '[data-tour="ops-table"] tr[data-row-idx="0"]',
    placement: 'bottom',
    titleKey: 'tour.operations.title',
    bodyKey: 'tour.operations.body',
    frames: [
      { key: 'operations.header', target: '[data-tour="svc-nav"]', fallbackTarget: '[data-tour="svc-crumbs"]',
        placement: 'bottom', bodyKey: 'tour.operations.header' },
      { key: 'operations.tab', target: '[data-tour="svc-tab-operations"]', placement: 'bottom', bodyKey: 'tour.operations.tab' },
      // Impact sütunu dar ekranda öncelikle gizlenebilir → Raw/Normalized anahtarına düş.
      { key: 'operations.impact', target: '[data-tour="ops-impact-col"]', fallbackTarget: '[data-tour="ops-normalized"]',
        placement: 'bottom', bodyKey: 'tour.operations.impact' },
      // YEDEK YOK (bilinçli): satırlar 8 sn'de gelmezse kart bu sekmede
      // "veri henüz yok" notuyla sürer — adım asla atlanmaz.
      { key: 'operations.rows', target: '[data-tour="ops-table"] tr[data-row-idx="0"]',
        placement: 'bottom', bodyKey: 'tour.operations.body' },
    ],
    // Servis adı yoksa (okuma düştü) gidilecek sayfa yok → atla.
    requires: ctx => noServices(ctx) ?? (ctx.service ? null : 'no_services'),
  },
  {
    id: 'traces',
    route: ctx => tracesTourHref(ctx),
    target: '[data-tour="traces-table"] tbody tr[data-row-idx="0"]',
    fallbackTarget: '[data-tour="traces-table"]',
    placement: 'bottom',
    titleKey: 'tour.traces.title',
    bodyKey: 'tour.traces.body',
    frames: [
      { key: 'traces.filters', target: '[data-tour="traces-active-chips"]', fallbackTarget: '[data-tour="traces-quick-chips"]',
        placement: 'bottom', bodyKey: 'tour.traces.filters' },
      { key: 'traces.minms', target: '[data-tour="traces-min-ms"]', fallbackTarget: '[data-tour="traces-service"]',
        placement: 'bottom', bodyKey: 'tour.traces.minms' },
      { key: 'traces.table', target: '[data-tour="traces-table"] tbody tr[data-row-idx="0"]', fallbackTarget: '[data-tour="traces-table"]',
        placement: 'bottom', bodyKey: 'tour.traces.body' },
    ],
    requires: noServices,
  },
  {
    id: 'cosre',
    route: () => null,
    target: '[data-tour="cosre-fab"]',
    placement: 'top',
    titleKey: 'tour.cosre.title',
    bodyKey: 'tour.cosre.body',
    frames: [
      // Çekmece zaten açıksa FAB çizilmez → çekmecenin kendisi.
      { key: 'cosre.fab', target: '[data-tour="cosre-fab"]', fallbackTarget: '[data-tour="cosre-drawer"]',
        placement: 'top', bodyKey: 'tour.cosre.fab' },
      // Giriş: coremetry:ai-prefill (aç + doldur + GÖNDERME) → çekmece → composer.
      { key: 'cosre.composer', target: '[data-tour="cosre-composer"]', fallbackTarget: '[data-tour="cosre-drawer"]',
        placement: 'top', bodyKey: 'tour.cosre.body', effect: 'cosre-prefill',
        keepFocus: '[data-tour="cosre-composer"] textarea' },
    ],
    // Kapalı kurulumda adım ATLANMAZ: ortalanmış not kartı + Bitir (NOTE_REASONS).
    requires: ctx => (ctx.copilotEnabled ? null : 'copilot_disabled'),
  },
];

export const TOUR_TOTAL = TOUR_STEPS.length;

/** v0.10.1336 — adımın kareleri (alt adımlar; yoksa adımın kendisi tek kare). */
export function stepFrames(step: TourStep): readonly TourFrame[] {
  if (step.frames && step.frames.length > 0) return step.frames;
  return [{
    key: step.id, target: step.target, fallbackTarget: step.fallbackTarget,
    placement: step.placement, bodyKey: step.bodyKey,
  }];
}

/** v0.10.1336 — NOT gerekçeli adımın tek karesi: ortalanmış, hedefsiz. */
export function noteFrame(step: TourStep, reason: TourSkipReason): TourFrame {
  return { key: `${step.id}.note`, target: null, placement: 'center', bodyKey: `tour.note.${reason}` };
}

/** v0.10.1336 — CoSRE composer'ına doldurulan soru şablonu ({svc}). */
export const TOUR_COSRE_PROMPT_KEY = 'tour.cosre.prompt';
export const TOUR_COSRE_PROMPT_FLEET_KEY = 'tour.cosre.promptFleet';

export function formatCosrePrompt(template: string, service: string): string {
  return template.split('{svc}').join(service);
}

/** Turun kendi çerçeve metinleri (i18n.test hepsinin iki katalogda olduğunu pinler). */
export const TOUR_CHROME_KEYS = [
  'tour.chrome.dialog', 'tour.chrome.stepOf', 'tour.chrome.progress',
  'tour.chrome.back', 'tour.chrome.next', 'tour.chrome.finish', 'tour.chrome.skip',
  'tour.chrome.restart', 'tour.chrome.loading',
  'tour.note.timeout', 'tour.note.not_yet', 'tour.note.no_services', 'tour.note.copilot_disabled',
  'tour.welcome.unavailable',
  'tour.welcome.tile.services', 'tour.welcome.tile.spans', 'tour.welcome.tile.spansTop',
  'tour.welcome.tile.busiest', 'tour.welcome.tile.envs',
  TOUR_COSRE_PROMPT_KEY, TOUR_COSRE_PROMPT_FLEET_KEY,
] as const;

/** stepOf şablonu: "Step {n} of {total}". */
export function formatStepOf(template: string, n: number, total: number): string {
  return template.replace('{n}', String(n)).replace('{total}', String(total));
}
