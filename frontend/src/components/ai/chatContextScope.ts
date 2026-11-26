import type { PageContext } from '@/lib/types';
import { tCosre } from './capabilityHints';

// chatContextScope — v0.10.1274 (CoSRE adım 5): composer'ın üstündeki AKTİF
// BAĞLAM şeridi ("Servis: X · Env: prod · Son 1 saat · Trace: abcd…").
//
// Sunucu bu kapsamı artık araç argümanlarına KENDİSİ koyuyor
// (internal/api/chat_tool_scope.go); operatör hangi kapsamın gideceğini
// gönderimden ÖNCE görür ve bir öğeyi ✕ ile o konuşma için çıkarabilir.
// Çıkarılan öğe istekten TAMAMEN düşer: düz alan (service/env/rangeS/trace)
// VE sayfa bağlamındaki ikizi (page.service/env/timeRange/traceId) — yoksa
// sunucu page.service'ten servisi geri kurardı (freeLoopScreenService).
// Saf: vitest'li; durum bileşende (URL'ye yazılmaz).

// v0.10.1400 — 'explain': son açıklama turunun takip bağlamı (CopilotChat;
// explainTurn.ts). Düz alanı yok — çıkarma CopilotChat'te tura bağlı tutulur.
// v0.10.2480 (E) — 'problem': Problem sayfasında pencere Problem'in kendi
// penceresi (sunucu istek anında kurar); ✕ → sayfanın aralığı geri gelir.
export type ContextKey = 'service' | 'env' | 'range' | 'trace' | 'explain' | 'problem';

export interface ContextBarItem {
  key: ContextKey;
  label: string;
  title: string;
}

export interface ChatRequestContext {
  service?: string;
  operation?: string;
  env?: string;
  rangeS?: number;
  toMs?: number;
  trace?: string;
  page?: PageContext;
  pinnedPage?: PageContext;
  /** v0.10.2480 (E) — Problem penceresi çipi açık (sunucuya `problemWindow:true`). */
  problemWindow?: boolean;
}

/** rangeLabel — "Son 1 saat" / "Son 30 dakika"; mutlak pencere "… (14:00'te biter)". */
export function rangeLabel(rangeS: number, toMs?: number): string {
  let base: string;
  if (rangeS >= 86400 && rangeS % 86400 === 0) base = `Son ${rangeS / 86400} gün`;
  else if (rangeS >= 3600 && rangeS % 3600 === 0) base = `Son ${rangeS / 3600} saat`;
  else if (rangeS >= 60) base = `Son ${Math.round(rangeS / 60)} dakika`;
  else base = `Son ${rangeS} saniye`;
  if (!toMs) return base;
  const d = new Date(toMs);
  const hh = String(d.getHours()).padStart(2, '0');
  const mm = String(d.getMinutes()).padStart(2, '0');
  return `${base.replace(/^Son /, '')} · ${hh}:${mm}'de biter`;
}

/** contextBarItems — gönderilecek bağlamın öğeleri (boş alan öğe üretmez). */
export function contextBarItems(c: ChatRequestContext): ContextBarItem[] {
  const out: ContextBarItem[] = [];
  if (c.service) {
    out.push({
      key: 'service',
      label: `Servis: ${c.service}${c.operation ? ` · ${c.operation}` : ''}`,
      title: 'Servis gerektiren araçlar bu servisle çağrılır',
    });
  }
  if (c.env) out.push({ key: 'env', label: `Env: ${c.env}`, title: 'Ortam argümanı verilmeyen araçlara bu ortam uygulanır' });
  if (c.problemWindow) {
    // Sayfada kalmış aralık yerine Problem'in penceresi (başlangıç − 15 dk → bitiş / şimdi);
    // saatleri sunucu adım satırında yazar ("Problem penceresi: 12:35 → şimdi").
    out.push({ key: 'problem', label: tCosre('cosre.ctx.problemWindow'), title: tCosre('cosre.ctx.problemWindowTitle') });
  } else if (c.rangeS && c.rangeS > 0) {
    out.push({ key: 'range', label: rangeLabel(c.rangeS, c.toMs), title: 'Pencere verilmeyen araçlar bu aralığı okur' });
  }
  if (c.trace) {
    out.push({ key: 'trace', label: `Trace: ${c.trace.slice(0, 8)}…`, title: `Seçili trace ${c.trace} — "bu trace" soruları buna bağlanır` });
  }
  return out;
}

function stripPage(p: PageContext | undefined, ex: ReadonlySet<ContextKey>): PageContext | undefined {
  if (!p || ex.size === 0) return p;
  const q: PageContext = { ...p };
  if (ex.has('service')) { delete q.service; delete q.operation; }
  if (ex.has('env')) delete q.env;
  if (ex.has('range')) delete q.timeRange;
  if (ex.has('trace')) { delete q.traceId; delete q.spanId; }
  return q;
}

/** applyContextExclusions — çıkarılan öğeleri istekten düşürür (düz alan + sayfa ikizi). */
export function applyContextExclusions(c: ChatRequestContext, ex: ReadonlySet<ContextKey>): ChatRequestContext {
  if (ex.size === 0) return c;
  return {
    ...c,
    service: ex.has('service') ? '' : c.service,
    operation: ex.has('service') ? '' : c.operation,
    env: ex.has('env') ? '' : c.env,
    rangeS: ex.has('range') ? undefined : c.rangeS,
    toMs: ex.has('range') ? undefined : c.toMs,
    trace: ex.has('trace') ? '' : c.trace,
    problemWindow: ex.has('problem') ? false : c.problemWindow,
    page: stripPage(c.page, ex),
    pinnedPage: stripPage(c.pinnedPage, ex),
  };
}

/**
 * shouldResetExclusions — çıkarmalar KONUŞMAYA bağlı: başka bir konuşmaya
 * geçince / temizleyince sıfırlanır; ilk gönderimde kimliğin oluşması
 * ('' → id) aynı konuşmadır, sıfırlamaz.
 */
export function shouldResetExclusions(prev: string | null | undefined, next: string | null | undefined): boolean {
  return !!prev && prev !== next;
}
