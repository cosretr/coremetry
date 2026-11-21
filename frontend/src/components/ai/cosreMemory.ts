// cosreMemory.ts — CoSRE Faz E (ekip notları / hafıza): not paneli, onay
// kartı ve "Düzelt" formunun SAF yardımcıları (tavan aynası, etiketler, tarih
// dönüşümü). Bileşen dosyaları yalnız bileşen dışa aktarsın diye ayrı.
import { tf, type Lang } from '@/lib/i18n';
import type { CosreMemoryItem, CosreRuleScope } from '@/lib/types';

// Sunucu tavanlarının aynası (internal/ai/cosrememory Min/MaxStatementRunes).
export const MEM_MIN = 8;
export const MEM_MAX = 240;

export const COSRE_MEMORY_KEY = ['cosre-memory'] as const;

export function memStatementError(text: string, lang: Lang): string | undefined {
  const n = Array.from(text.trim()).length;
  if (n < MEM_MIN) return tf('cmem.form.min', lang, { n: MEM_MIN });
  if (n > MEM_MAX) return tf('cmem.form.max', lang, { n: MEM_MAX });
  return undefined;
}

export function memScopeLabel(sc: CosreRuleScope, lang: Lang): string {
  if (sc.kind === 'service') return tf('cmem.scope.service', lang, { v: sc.value ?? '' });
  if (sc.kind === 'team') return tf('cmem.scope.team', lang, { v: sc.value ?? '' });
  return tf('cmem.scope.global', lang);
}

export function memSourceLabel(it: CosreMemoryItem, lang: Lang): string {
  const s = it.source;
  if (s.kind === 'rootcause') return tf('cmem.src.rootcause', lang, { v: s.displayId ?? '' }).trim();
  return tf(s.kind === 'correction' ? 'cmem.src.correction' : 'cmem.src.manual', lang);
}

export type MemState = 'active' | 'retired' | 'expired';

/** Görünen durum: emekli > süresi dolmuş > etkin (sunucu Live ile aynı kural). */
export function memState(it: CosreMemoryItem, nowMs = Date.now()): MemState {
  if (it.status === 'retired') return 'retired';
  if (it.expiresAt && it.expiresAt <= nowMs * 1e6) return 'expired';
  return 'active';
}

export function memTs(it: CosreMemoryItem): number {
  return Math.max(it.updatedAt ?? 0, it.source.at);
}

/** unix ns → <input type="date"> değeri (yerel gün); 0 → ''. */
export function nsToDateInput(ns?: number): string {
  if (!ns) return '';
  const d = new Date(ns / 1e6);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/** <input type="date"> → o günün SONU (yerel 23:59:59), unix ns; '' → 0. */
export function dateInputToNs(v: string): number {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v);
  if (!m) return 0;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), 23, 59, 59).getTime() * 1e6;
}

/** Tek satır (sunucu da normalize eder). */
export function memSingleLine(s: string): string {
  return s.replace(/[\r\n\t]+/g, ' ');
}
