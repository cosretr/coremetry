// Typed localStorage wrapper (refactor 2026-07). Every ad-hoc
// `localStorage.*` access in src/ routes through here so private-mode
// windows, disabled storage, and quota errors can never crash a page:
// reads fall back, writes are best-effort. The inline boot scripts in
// index.html intentionally do NOT use this module (they run before the
// bundle) — keep them in sync by hand if a boot-critical key changes.
//
// Two API levels, chosen per call site to keep behaviour identical:
//   - getRaw/setRaw/removeRaw — plain strings. Use where the stored
//     value is a bare token ('dark', '1', a name). Existing stored
//     values are NOT valid JSON, so these sites must never migrate to
//     the JSON API (JSON.parse('dark') throws → silent fallback would
//     lose the user's setting).
//   - getItem<T>/setItem<T>  — JSON round-trip with a typed fallback.

/** Known storage keys. Dynamic families (dt.*) get helper builders. */
export const STORAGE_KEYS = {
  theme:            'coremetry-theme',
  density:          'coremetry-density',
  // range — v0.7.124'ten v0.9.936'ya kadar kullanılan localStorage
  // anahtarı. v0.9.937'de seçilen aralık OTURUM kapsamına taşındı
  // (lastRange, aşağıda); bu anahtar artık okunmuyor. Girdi listede
  // KALIYOR ki aynı ad ikinci bir amaç için yeniden kullanılmasın —
  // eski kurulumlarda hâlâ diskte duran bir değer var.
  range:            'coremetry-range',
  // lastRange (v0.9.937) — operatörün SON SEÇTİĞİ aralık, sekme
  // oturumu boyunca. sessionStorage ÇÜNKÜ mutlak (custom:) pencereler
  // de saklanıyor: v0.8.409'da localStorage'a donan bir custom pencere
  // haftalar sonra bile her sayfayı geçmişte açıyordu. Oturum kapsamı
  // o kuyruğu keser — sekme kapanınca kayıt da gider.
  lastRange:        'cm.lastRange',
  env:              'coremetry-env',
  // v0.10.1430 — global auto-refresh interval (Topbar RefreshPicker), the
  // `?refresh=` token mirrored for cross-page continuity (URL > this > 60 s).
  refresh:          'cm.refresh',
  lang:             'coremetry.lang',
  rum:              'coremetry-rum',
  recentServices:   'coremetry.recentServices',
  pinnedServices:   'coremetry.pinnedServices',
  recentMetrics:    'coremetry.recentMetrics',
  // v0.10.1089 — seçicilerin "Son kullanılan" grubu (lib/pickerRecents.ts):
  // { kapsam: string[] }, kapsam başına 5 değer.
  pickerRecents:    'coremetry.pickerRecents',
  // v0.9.301 — /traces histogram height. Dynatrace keeps the trace
  // list's overview chart a THIN brush strip so the table gets the
  // page; ours was a 140px headline that pushed rows below the fold.
  // Slim by default, expandable, and the choice sticks.
  sidebarWidth:     'coremetry-sidebar-w',
  sidebarCollapsed: 'coremetry-sidebar-collapsed',
  sidebarGroups:    'coremetry-sidebar-groups',
  spanPanelWidth:   'coremetry-span-panel-w',
  exploreHistory:   'coremetry-explore-history',
  sqlBackend:       'coremetry-sql-backend',
  sqlOracleSource:  'coremetry-sql-oracle-source', // v0.10.742 — konsolda son seçilen Oracle kaynağı
  // v0.9.225 — son odaklanılan servis. Auto-pick tüm haritanın gelmesini
  // beklediği için ilk boyama gereksiz yere o sorguya bağlanıyordu; hatırlanan
  // odak, komşuluk sorgusunu harita daha yoldayken başlatır.
  topoFocus:        'coremetry-topo-focus',
  announcementDismissed: 'coremetry-announcement-dismissed', // v0.8.486 — kapatılan duyuru revizyonu
  finopsCostPerTbMo: 'coremetry.finops.costPerTbMo',
  inboxPrio:        'inbox.prio',
  inboxKind:        'inbox.kind',
  problemsSev:      'problems.sev',
  problemsPrio:     'problems.prio',
  // v0.10.2132 — Details'in açık bölümleri (JSON kimlik dizisi; tarayıcı başına,
  // servisten bağımsız). Eski `svc.heatmap.collapsed` bunun yerine geçti, okunmaz.
  svcDetailsOpen:      'svc.details.open',
  svcChartsCompare:    'svc.charts.compare',
  // v0.10.577 — Trace Detail Logs sekmesi: gRPC per-message span event'leri
  // (SENT/RECEIVED) varsayılan olarak GİZLİ. Tercih küresel, trace başına
  // değil: operatör bunu bir kez kapatıp her trace'te yeniden kapatmakla
  // uğraşmasın (svcDetailsOpen emsali).
  traceShowGrpcMsgs:   'trace.logs.grpcmsgs',
  // v0.10.432 (CoSRE router boşlukları D8) — trace ilk açılış baloncuğu
  // ("Bu trace'i açıklamamı ister misin?"). declined = kalıcı ret
  // (localStorage: "Bir daha sorma" bir daha sormaz); asked =
  // sekme oturumu (sessionStorage): tab başına bir kez sorulur, cevapsız
  // bırakılsa da tekrar çıkmaz.
  aiTraceNudgeDeclined: 'cm.ai.traceNudge.declined',
  aiTraceNudgeAsked:    'cm.ai.traceNudge.asked',
  // v0.10.1334 — rehberli tur. tourSeen = localStorage ÖNEK (tam anahtar
  // tourSeenKey(uid)): bitirilen/atlanan tur, sunucu PUT'u gecikse ya da
  // düşse de bu tarayıcıda yeniden oto-açılmaz (flaş bekçisi). tourAutoStarted
  // = sessionStorage: sekme oturumu başına en çok bir oto-başlatma.
  tourSeen:             'coremetry.tour.seen',
  tourAutoStarted:      'cm.tour.autoStarted',
  // v0.10.1820 (Log Search Faz 1 dilim 1.4) — /logs sorgu geçmişi ÖNEK (tam
  // anahtar logsQueryHistoryKey(uid)): kullanıcı + tarayıcı başına son 20
  // uygulanan arama (lib/logsQueryHistory.ts). Paylaşılan/ekip durumu DEĞİL.
  logsQueryHistory:     'coremetry.logsQueryHistory',
} as const;

/** v0.10.1334 — kullanıcı-kapsamlı tur flaş bekçisi: `coremetry.tour.seen.<uid>`. */
export const tourSeenKey = (uid: string) => `${STORAGE_KEYS.tourSeen}.${uid}`;

/** v0.10.1820 — kullanıcı-kapsamlı log sorgu geçmişi: `coremetry.logsQueryHistory.<uid>`
 *  (kimliksiz → `.anon`). */
export const logsQueryHistoryKey = (uid: string) => `${STORAGE_KEYS.logsQueryHistory}.${uid || 'anon'}`;

/** DataTable persistence family: sort + column widths per table. */
export const dtSortKey = (storageKey: string) => `dt.${storageKey}.sort`;
export const dtWidthKey = (storageKey: string) => `dt.${storageKey}.widths`;

/** Chart-legend family (v0.9.483): "▶/▼ Series (N)" istatistik tablosunun
 *  açık/kapalı durumu, grafik başına. Görünürlük seçimi ayrı bir ailede
 *  (`cm.legendVis:` — lib/chart/legendVisibility.ts) yaşar; iki kayıt
 *  bilinçli AYRI: seri gizleme ile paneli kapatma farklı niyetler. */
export const legendCollapseKey = (storageKey: string) => `cm.legendCollapsed.${storageKey}`;

export function getRaw(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    // Storage disabled (private mode / iframe policy) — behave as unset.
    return null;
  }
}

export function setRaw(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Quota exceeded / storage disabled — chrome state is best-effort.
  }
}

export function removeRaw(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    // Same best-effort contract as setRaw.
  }
}

// ── Oturum kapsamlı kayıt (v0.9.937) ────────────────────────────────
//
// getRaw/setRaw'ın SEKMEYE bağlı ikizi. Ayrı bir çift olması bilinçli:
// "kalıcı" ile "bu sekme boyunca" farklı sözleşmelerdir ve karışmaları
// gerçek bir olaya mal oldu (v0.8.409 — localStorage'a donan mutlak
// pencere haftalar sonra hâlâ her sayfayı geçmişte açıyordu).
//
// Aynı savunmacı duruş: özel mod / iframe politikası / kota hatası bir
// sayfayı ASLA çökertemez — okuma boşa düşer, yazma sessizce geçer.
// Test ortamlarında (jsdom'un eski sürümleri) sessionStorage tanımsız
// olabilir; typeof kontrolü o vakayı da kapsıyor.
export function getSessionRaw(key: string): string | null {
  try {
    if (typeof sessionStorage === 'undefined') return null;
    return sessionStorage.getItem(key);
  } catch {
    return null;
  }
}

export function setSessionRaw(key: string, value: string): void {
  try {
    if (typeof sessionStorage === 'undefined') return;
    sessionStorage.setItem(key, value);
  } catch {
    /* best-effort */
  }
}

/** JSON read with typed fallback: unset, unparseable, or storage-
 *  disabled all yield `fallback` — a corrupt entry never crashes. */
export function getItem<T>(key: string, fallback: T): T {
  const raw = getRaw(key);
  if (raw === null) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

/** JSON write, best-effort (see setRaw). */
export function setItem<T>(key: string, value: T): void {
  setRaw(key, JSON.stringify(value));
}
