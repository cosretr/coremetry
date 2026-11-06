// lib/metricsExplorer/run.ts — v0.10.1863 (Metrics Explorer Faz 2 dilim 2.3).
//
// Saf: modelden "çalıştırma planı" (hangi metin, hangi adım, hangi seri
// sınırı) ve sonuç/hata okuması. Sayfa yalnız planı react-query'ye verir;
// istek anahtarı planın TÜM girdilerini taşır (v0.5.187 dersi: anahtar
// eksik girdiyle çakışırsa bir sorgunun sonucu diğerine sızar).
import { stepForPoints } from '@/lib/chartStep';
import { compileMxFormula, compileMxQuery, substituteIntervals } from './compile';
import type { MxLimit, MxModel, MxQuery, MxRank, MxSrc } from './model';

/** Otomatik adımın nokta bütçesi (grafik ~720 px / 3 px). */
export const AUTO_POINTS = 240;

export function effectiveStepSec(model: Pick<MxModel, 'step'>, rangeSec: number): number {
  return model.step > 0 ? model.step : stepForPoints(rangeSec, AUTO_POINTS);
}

export interface RunItem {
  /** Sorgu kimliği ('A', 'F1', 'code'). */
  id: string;
  kind: 'metric' | 'formula' | 'code';
  /** Yer tutucusu olan metin (ekranda "Üretilen MetricsQL"). */
  text: string;
  /** Yer tutucuları çözülmüş, sunucuya giden metin. */
  query: string;
  legend: string;
  by: string[];
  /** Görüntü birimi (ham; UCUM ya da Grafana kimliği). */
  unit?: string;
  /** Formülse başvurduğu harfler. */
  refs?: string[];
}

export interface RunPlan {
  items: RunItem[];
  /** Çalıştırılamayan satırlar (formül hatası) — satırın altında söylenir. */
  errors: { id: string; error: string }[];
  stepSec: number;
  limit: MxLimit;
  rank: MxRank;
  src: MxSrc;
}

/** Sorgunun görüntü birimi: yüzdelik/ham değer metriğin birimi; hız ve artış birimsiz (sayı/sn). */
export function queryUnit(q: MxQuery): string | undefined {
  if (q.kind === 'formula') return q.unit;
  return q.fn === 'raw' || q.fn.startsWith('p') ? q.unit : undefined;
}

export function planRun(model: MxModel, rangeSec: number): RunPlan {
  const stepSec = effectiveStepSec(model, rangeSec);
  const items: RunItem[] = [];
  const errors: RunPlan['errors'] = [];
  const base = { stepSec, limit: model.limit, rank: model.rank, src: model.src };
  if (model.mode === 'code') {
    const text = model.code.trim();
    if (text) items.push({ id: 'code', kind: 'code', text, query: substituteIntervals(text, stepSec), legend: '', by: [] });
    return { items, errors, ...base, src: model.src };
  }
  for (const q of model.queries) {
    if (q.hidden) continue;
    if (q.kind === 'metric') {
      const text = compileMxQuery(q);
      if (!text) continue;
      items.push({ id: q.id, kind: 'metric', text, query: substituteIntervals(text, stepSec), legend: q.legend, by: q.by, unit: queryUnit(q) });
      continue;
    }
    if (!q.expr.trim()) continue;
    const c = compileMxFormula(q.expr, model.queries);
    if (!c.ok) { errors.push({ id: q.id, error: c.error }); continue; }
    const by = model.queries.find(x => x.id === c.refs[0])?.by ?? [];
    items.push({ id: q.id, kind: 'formula', text: c.expr, query: substituteIntervals(c.expr, stepSec), legend: q.legend, by, unit: q.unit, refs: c.refs });
  }
  // Builder'da kaynak sabitlenmez (kurulum varsayılanı); kod kipi kendi seçimini taşır.
  return { items, errors, ...base, src: '' };
}

// ── Hata gövdesi ────────────────────────────────────────────────────────────

export interface RunError {
  /** 400 = sorgu hatası (operatörün düzelteceği); 502 = kaynağa erişilemedi. */
  status?: number;
  message: string;
  pos?: number;
  line?: number;
  col?: number;
  detail?: string;
}

/**
 * parseRunError — request() hatası `HTTP 400: {"error":…, "pos":…}` biçiminde
 * (lib/api.ts). Gövde JSON değilse düz metin.
 */
export function parseRunError(e: unknown): RunError {
  const raw = e instanceof Error ? e.message : typeof e === 'string' ? e : 'Sorgu başarısız';
  const m = /^HTTP (\d{3}):\s*([\s\S]*)$/.exec(raw);
  const status = m ? Number(m[1]) : undefined;
  const body = (m ? m[2] : raw).trim();
  if (body.startsWith('{')) {
    try {
      const j = JSON.parse(body) as Record<string, unknown>;
      const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
      return {
        status,
        message: typeof j.error === 'string' && j.error ? j.error : body,
        pos: num(j.pos), line: num(j.line), col: num(j.col),
        detail: typeof j.detail === 'string' && j.detail ? j.detail : undefined,
      };
    } catch { /* düz metin */ }
  }
  return { status, message: body || raw };
}

/** Hata sınıfının tek cümlelik başlığı (audit §4 "400 ile 502 ayrı metin"). */
export function runErrorTitle(err: RunError, sourceLabel: string): string {
  if (err.status === 400) return 'Sorgu hatası';
  if (err.status === 502 || err.status === 503 || err.status === 504) return `${sourceLabel} cevap vermedi`;
  if (err.status === 403) return 'Yetki yok';
  return 'Sorgu çalıştırılamadı';
}
