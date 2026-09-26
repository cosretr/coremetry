// toolSteps.ts — v0.10.161: Copilot araç-çağrısı şeffaflık panelinin saf
// çekirdeği (tasarım etüdü seçenek A «Adım listesi», iki yargıçta birinci;
// scratchpad/copilot-tools). Sözleşme toolSteps.test.ts'te pinli.
//
// Yargıç must-fix'leri burada kural oldu:
//   - toplam süre yalnız TÜM yürütülen adımların `durationMs`i varsa (yoksa
//     null → «—»; ölçüm gibi çizilmez),
//   - «ön-yükleme» rozeti `step.origin === 'guided'`ten, delta olayından
//     çıkarım YOK (drawer katmanı da delta yayınlıyor),
//   - hata sayacı ok=false olan her sonuç (timeout + tekrar koruması dâhil).
import type { ChatStepDetail, ChatStepSourceState } from '@/lib/types';

/** Kapalı panelde görünen satır sayısı; kalanı «▸ N daha». */
export const VISIBLE_ROWS = 5;

export interface StepsSummary {
  count: number;
  /** ok === false olan sonuç sayısı (yürütülmeyenler HARİÇ) */
  errors: number;
  /** v0.10.944 — sunucunun yürütmediği çağrı (step-result skipped:true); hata da süre de değil */
  skipped: number;
  /** sonucu henüz gelmemiş adım sayısı (preview undefined) — tur bitmişse 0 */
  pending: number;
  /** tur bitti ama kanıtı hiç yayınlanmamış adım (sunucu boş metinde step-result yayınlamaz) */
  noEvidence: number;
  /** Σ durationMs — yürütülen HER adımın süresi biliniyorsa; aksi null */
  totalMs: number | null;
  /** sonucu gelmiş ama süresi olmayan adım sayısı (guided ön-yükleme, eski sunucu) */
  unknownDuration: number;
  /** tüm adımlar sunucu ön-yüklemesi (step.origin === 'guided') */
  guided: boolean;
  /** v0.10.172 — serbest soru niyet sınıflandırmasından geçti (step.origin === 'intent') */
  intent: boolean;
}

/**
 * @param turnDone tur bitti (pending=false): sonucu hiç gelmemiş adım artık
 * «sürüyor» değil «kanıt yok» (emitStepEvidence boş metinde yayınlamaz).
 */
export function summarizeSteps(details: ChatStepDetail[], turnDone = false): StepsSummary {
  let errors = 0, skipped = 0, pending = 0, noEvidence = 0, total = 0, unknown = 0, guidedN = 0, intentN = 0;
  for (const d of details) {
    if (d.origin === 'guided') guidedN++;
    if (d.origin === 'intent') intentN++;
    if (d.preview === undefined) { if (turnDone) { noEvidence++; unknown++; } else pending++; continue; }
    // v0.10.944 — yürütülmeyen çağrı: sunucunun 0 ms'si bir ÖLÇÜM değil,
    // «⚠ hata» da değil (araç hiç koşmadı). Σ yalnız yürütülenlerin süresi.
    if (d.skipped) { skipped++; continue; }
    if (d.ok === false) errors++;
    if (typeof d.durationMs === 'number' && d.durationMs >= 0) total += d.durationMs; else unknown++;
  }
  return {
    count: details.length,
    errors, skipped, pending, noEvidence,
    totalMs: details.length > skipped && unknown === 0 && pending === 0 ? total : null,
    unknownDuration: unknown,
    guided: details.length > 0 && guidedN === details.length,
    // yalnız sınıflandırma GERÇEKTEN dispatch ettiyse (kalan adımlar ön-yükleme); none/hata → serbest döngü, rozet yalan olurdu
    intent: intentN > 0 && guidedN + intentN === details.length,
  };
}

export interface ToolErrorView { cls: string; retryable?: boolean; hint?: string; detail?: string }

/**
 * v0.10.944 — mcp.ToolErrorJSON sınıflarının Türkçe etiketi (internal/mcp/
 * toolerr.go ile birebir; yedi sınıf). `unauthorized` YENİ: ES/VM 401/403
 * eskiden `internal`a düşüyordu — "tekrar deneme, bu kaynak kapsam dışı"
 * eylemi ayrı. Tanınmayan sınıf ham adıyla gösterilir (uydurma etiket yok).
 */
export const TOOL_ERROR_LABELS: Readonly<Record<string, string>> = {
  timeout: 'zaman aşımı',
  backend_unavailable: 'kaynak erişilemez',
  bad_args: 'hatalı argüman',
  not_found: 'bulunamadı',
  internal: 'iç hata',
  cancelled: 'iptal edildi',
  unauthorized: 'yetki yok',
};

export function toolErrorLabel(cls: string): string {
  return TOOL_ERROR_LABELS[cls] ?? cls;
}

/**
 * mcp.ToolErrorJSON sözleşmesi ({error, retryable, hint, detail}) — çipin
 * önizlemesi bu JSON'sa satır sınıf + ipucu gösterir; değilse null (ham
 * metin olduğu gibi kalır).
 */
export function parseToolError(preview: string | undefined): ToolErrorView | null {
  const s = (preview ?? '').trim();
  if (!s.startsWith('{')) return null;
  try {
    const o = JSON.parse(s) as Record<string, unknown>;
    if (typeof o.error !== 'string' || !o.error) return null;
    const v: ToolErrorView = { cls: o.error };
    if (typeof o.retryable === 'boolean') v.retryable = o.retryable;
    if (typeof o.hint === 'string' && o.hint) v.hint = o.hint;
    if (typeof o.detail === 'string' && o.detail) v.detail = o.detail;
    return v;
  } catch {
    return null;
  }
}

/** Önizlemenin ilk satırı, `max` karaktere kırpılmış («…»); boş → «(boş)». */
export function previewFirstLine(preview: string | undefined, max: number): string {
  if (preview === undefined) return '';
  const line = preview.split('\n')[0].trim();
  if (!line) return '(boş)';
  return line.length > max ? line.slice(0, max) + '…' : line;
}

export function visibleRows<T>(rows: T[], expanded: boolean): T[] {
  return expanded ? rows : rows.slice(0, VISIBLE_ROWS);
}

/**
 * Bütçe aşımı — sunucu yapısal bir `budget` olayı yayınlamıyor (brief §5);
 * chatDeadlineMessageTR metninden («… tavanına dayandı …») tanınır.
 */
export function isDeadlineError(err: string | undefined): boolean {
  return /tavan[ıi]na dayand[ıi]/i.test(err ?? '');
}

export function fmtMs(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
}

// ── v0.10.944 (CoSRE Faz A) — dürüst ilerleme ─────────────────────────────

/**
 * Kaynak durumu (internal/sourcestate) → rozet etiketi. `ok` rozet ALMAZ
 * (sağlıklı durum nötr); `empty` "boş" — hata yok DEMEK DEĞİL, yalnız bu
 * filtre + pencerede kayıt yok.
 */
export const SOURCE_STATE_LABELS: Readonly<Record<string, string>> = {
  empty: 'boş',
  unreachable: 'erişilemedi',
  unauthorized: 'yetki yok',
  timeout: 'zaman aşımı',
  partial: 'kısmi',
  delayed: 'gecikmeli',
  truncated: 'limitli',
  not_configured: 'yapılandırılmamış',
  error: 'hata',
};

/** Rozet tonu: kanıt YOK sınıfları kırmızı, eksik kapsam sarı, boş gri. */
export function sourceStateTone(state: string): 'err' | 'warn' | 'gray' {
  if (state === 'unreachable' || state === 'unauthorized' || state === 'timeout' || state === 'error' || state === 'not_configured') return 'err';
  if (state === 'empty') return 'gray';
  return 'warn';
}

export interface SourceStateView { source: string; state: string; label: string; detail?: string }

// stateViews — bir Status nesnesinin rozetleri: birincil durum + v0.10.944
// ikincil `flags` (sourcestate.Result "kısmi VE limitli" gibi birden çok
// geçerli durumu Flags'te listeler; yalnız birincili çizmek ikincisini
// saklıyordu). Bayrak: birincilden farklı, `ok` değil, etiketi bilinen.
function stateViews(o: Record<string, unknown>): SourceStateView[] {
  const state = typeof o.state === 'string' ? o.state : '';
  if (!state || state === 'ok') return [];
  const label = SOURCE_STATE_LABELS[state];
  if (!label) return [];
  const source = typeof o.source === 'string' ? o.source : '';
  const v: SourceStateView = { source, state, label };
  if (typeof o.detail === 'string' && o.detail) v.detail = o.detail;
  const out = [v];
  if (Array.isArray(o.flags)) {
    const seen = new Set([state]);
    for (const f of o.flags) {
      if (typeof f !== 'string' || f === 'ok' || seen.has(f)) continue;
      const fl = SOURCE_STATE_LABELS[f];
      if (!fl) continue;
      seen.add(f);
      out.push({ source, state: f, label: fl });
    }
  }
  return out;
}

function isPlainObject(x: unknown): x is Record<string, unknown> {
  return !!x && typeof x === 'object' && !Array.isArray(x);
}

// scanSourcesArray — kırpık önizlemede `"sources":[…]` dizisinin TAM
// nesneleri. v0.10.944 — eski `\[([^\]]*)\]` deseni Status'un kendi
// `flags`/`notes` dizisindeki İLK `]`de duruyordu (çok kaynaklı sonuç hiç
// okunmuyordu). Status iç içe süslü parantez taşımaz ama dizi taşır: her
// `{…}` bir öncekinin hemen ardından (`,` sonrası) başlamalı; `]` dizinin
// sonu, başka bir şey (kırpık kuyruk) tarama sonu.
function scanSourcesArray(s: string): string[] {
  const head = /"sources"\s*:\s*\[\s*/.exec(s);
  if (!head) return [];
  const out: string[] = [];
  const obj = /\{[^{}]*\}/g;
  let at = head.index + head[0].length;
  for (;;) {
    obj.lastIndex = at;
    const m = obj.exec(s);
    if (!m || m.index !== at) break;
    out.push(m[0]);
    const tail = /^\s*([,\]])\s*/.exec(s.slice(m.index + m[0].length));
    if (!tail || tail[1] === ']') break;
    at = m.index + m[0].length + tail[0].length;
  }
  return out;
}

/**
 * sourceStates — araç sonucundaki `source` (tek kaynak) ya da `sources[]`
 * (çok kaynak) durumları; `ok` olanlar düşer.
 *
 * v0.10.944 — `structured` (step-result `sources`, sunucunun TAM çıktıdan
 * okuduğu) varsa YALNIZ o kullanılır: yeni araçlar sonucu Go map'iyle kurar,
 * JSON anahtarları alfabetik ve büyük veri anahtarları (logs/series/
 * analysis/problem) `source`tan ÖNCE gelir — veri dolu her sonuç 4 KB
 * önizlemede durumundan önce kırpılıyordu. Önizleme yalnız eski sunucu
 * yedeği: JSON ayrıştırılamazsa yalnız `"source":{…}` / `"sources":[{…}]`
 * nesnelerinin KENDİSİ okunur. Başka bir alandaki "state" (ör. pod durumu)
 * asla okunmaz; bulunamazsa boş liste — uydurma yok.
 */
export function sourceStates(preview: string | undefined, structured?: readonly ChatStepSourceState[]): SourceStateView[] {
  if (structured) {
    return structured.flatMap(st => (isPlainObject(st) ? stateViews(st) : []));
  }
  const s = (preview ?? '').trim();
  if (!s.startsWith('{')) return [];
  try {
    const o = JSON.parse(s) as Record<string, unknown>;
    const out: SourceStateView[] = [];
    if (isPlainObject(o.source)) out.push(...stateViews(o.source));
    if (Array.isArray(o.sources)) {
      for (const it of o.sources) if (isPlainObject(it)) out.push(...stateViews(it));
    }
    return out;
  } catch {
    const out: SourceStateView[] = [];
    const one = /"source"\s*:\s*(\{[^{}]*\})/.exec(s);
    const objs = [...(one ? [one[1]] : []), ...scanSourcesArray(s)];
    for (const raw of objs) {
      try {
        const o = JSON.parse(raw) as unknown;
        if (isPlainObject(o)) out.push(...stateViews(o));
      } catch { /* kırpıktan yarım nesne: okunmaz */ }
    }
    return out;
  }
}

function parsesAsJSON(s: string): boolean {
  try { JSON.parse(s); return true; } catch { return false; }
}

/**
 * windowPrefix — v0.10.944: compare_periods iki pencere için aynı kaynaklı
 * (traces) iki durum döndürür; pencere yalnız `detail`da ("sorun penceresi",
 * fail yolunda "sorun penceresi: …"). Rozet öneki olmadan iki rozet ayırt
 * edilemiyordu. Tanınmayan detay → önek yok.
 */
export function windowPrefix(detail?: string): string {
  const d = (detail ?? '').trim();
  if (d.startsWith('sorun penceresi')) return 'sorun · ';
  if (d.startsWith('referans penceresi')) return 'referans · ';
  return '';
}

/**
 * stateUnknown — v0.10.944: Durum hücresi nötr «ok» DEMESİN. Önizleme kırpık
 * bir JSON nesnesi, sunucu yapısal `sources` göndermemiş (eski sunucu / yol)
 * ve kırpık önizlemede kaynak durumu yok: kaynağın gerçekten «ok» olduğunu
 * bilmiyoruz. JSON olmayan (düz metin) çıktılar hiç durum taşımadığı için
 * kapsam dışı — onlara «okunamadı» demek yeni bir yalan olurdu.
 */
export function stateUnknown(d: Pick<ChatStepDetail, 'truncated' | 'sources' | 'preview'>): boolean {
  if (!d.truncated || d.sources) return false;
  const s = (d.preview ?? '').trim();
  if (!s.startsWith('{')) return false;
  return sourceStates(s).length === 0 && !parsesAsJSON(s);
}

/**
 * isToolName — çip metni gerçek bir araç ADI mı (snake_case / noktalı)?
 * Sunucunun bağlam etiketleri ("bağlam: ekrandaki trace (…)") `tool`
 * alanında gelebiliyor ve hiç step-result almıyor; onlara "çalışıyor…"
 * demek yalan olurdu.
 */
export function isToolName(s: string | undefined): boolean {
  return !!s && /^[A-Za-z][\w.-]*$/.test(s);
}

/**
 * stepRunning — çip "çalışıyor…" desin mi: tur sürüyor, adım gerçek bir
 * araç ve SONUCU henüz gelmedi. Sonuç gelince (ya da tur bitince) düşer.
 */
export function stepRunning(d: ChatStepDetail | undefined, turnDone: boolean): boolean {
  return !turnDone && !!d && d.preview === undefined && isToolName(d.tool);
}
