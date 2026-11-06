// logException — v0.10.1880 (Log Search Faz 3 dilim 3.3). Satırın exception
// GÖSTERİM özeti (sunucu: internal/logstore/exception_summary.go) için istemci
// yardımcıları. SAF; logException.test.ts. Gruplama kimliğine dokunmaz.
import type { LogException, LogRow } from '@/lib/types';

const CLASS_LABEL: Record<string, string> = {
  resource: 'kaynak tükenmesi',
  timeout: 'zaman aşımı',
  connectivity: 'bağlantı',
  database: 'veritabanı',
  null: 'boş referans',
  auth: 'yetki',
};

export function exceptionClassLabel(cls?: string): string {
  return (cls && CLASS_LABEL[cls]) || '';
}

/** Nitelikli tür adının son parçası (java.lang.OutOfMemoryError → OutOfMemoryError). */
export function shortExceptionType(t: string): string {
  const i = t.lastIndexOf('.');
  return i >= 0 ? t.slice(i + 1) : t;
}

/** Rozet metni: "▲ OutOfMemoryError · kaynak tükenmesi" (rozet + metin birlikte). */
export function exceptionBadgeText(e: LogException): string {
  const cls = exceptionClassLabel(e.class);
  return `${e.critical ? '▲ ' : ''}${shortExceptionType(e.type)}${cls ? ` · ${cls}` : ''}`;
}

/** Stack metni: özetin işaret ettiği öznitelik, yoksa gövde. */
export function exceptionStackText(l: Pick<LogRow, 'body' | 'attributes'>, e: LogException): string {
  if (e.stackField) {
    const v = l.attributes?.[e.stackField];
    if (v) return v;
  }
  return l.body ?? '';
}

export interface StackLine { text: string; app: boolean; head: boolean }

const HEAD_RE = /^\s*(Caused by:|--->|[A-Za-z_$][\w$]*(\.[A-Za-z_$][\w$]*)+(Exception|Error|Throwable|Fault)\b)/;

/** Satırlar + bayraklar: app = sunucunun uygulama satırı; head = exception başlığı ("Caused by" dahil). */
export function stackLines(stack: string, appLines: readonly number[] = []): StackLine[] {
  const app = new Set(appLines);
  return stack.split('\n').map((text, i) => ({ text: text.replace(/\r$/, ''), app: app.has(i), head: HEAD_RE.test(text) }));
}

/** Katlı görünüm: başlıklar + uygulama satırları görünür; framework satırları gizli. */
export function foldStack(lines: readonly StackLine[]): { shown: StackLine[]; hidden: number } {
  const shown = lines.filter(l => l.app || l.head || l.text.trim() === '');
  return { shown: shown.filter((l, i, a) => !(l.text.trim() === '' && (i === 0 || a[i - 1].text.trim() === ''))), hidden: lines.filter(l => !(l.app || l.head) && l.text.trim() !== '').length };
}
