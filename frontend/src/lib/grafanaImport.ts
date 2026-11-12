// lib/grafanaImport — v0.10.2002 (Dashboards Faz 3 dilim 3.9; audit §5.5,
// operatör S11 "dar kapsam + kuru çalıştırma raporu").
//
// Grafana pano JSON'u → Coremetry panosu. SAF ve istemcide: sunucuya yalnız
// sonuç gider (var olan POST /api/dashboards, audit dashboard.create). Önce
// RAPOR: alınan her panel (notlarıyla) ve atlanan her panel/değişken nedeniyle
// listelenir; operatör onaylamadan hiçbir şey yazılmaz.
//
// ALINAN (§5.5): Prometheus / VictoriaMetrics kaynaklı timeseries / graph,
// stat, gauge, heatmap (histogram metriği çözülebilirse), text, row.
// targets[].expr → promql paneli (stat / gauge'da MetricsQL kaynağı);
// gridPos.w / 2 → span (24 → 12 kolon); gridPos.h → S/M/L;
// label_values() → etiket-değeri değişkeni; custom → custom değişken.
// ALINMAYAN: transformations, overrides, karışık kaynak, Loki / Tempo /
// diğer kaynaklar, repeat, alarm, annotation sorguları, desteklenmeyen panel
// tipleri — panel atlanır ya da alınan panelin notuna yazılır.
import type { DashboardVariable, Panel, PanelHeight, PanelVizType } from './types';
import { widthForSpan } from '@/components/coremetry/dashboard/grid';

export interface GrafanaPanelReport { title: string; as: string; notes: string[] }
export interface GrafanaSkip { title: string; reason: string }
export interface GrafanaVarSkip { name: string; reason: string }
export interface GrafanaImportReport {
  panels: GrafanaPanelReport[];
  skipped: GrafanaSkip[];
  variables: string[];
  skippedVariables: GrafanaVarSkip[];
  /** Pano düzeyi notlar (annotation sorguları, sürüm). */
  notes: string[];
}
export interface GrafanaImportResult {
  dashboard: { name: string; description: string; tags: string[]; panels: Panel[]; variables: DashboardVariable[] };
  report: GrafanaImportReport;
}

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => v !== null && typeof v === 'object' && !Array.isArray(v);
const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

const PROM_TYPES = new Set(['prometheus', 'victoriametrics-datasource', 'victoriametrics-metrics-datasource']);
const UNIT_MAP: Record<string, string> = {
  ms: 'ms', s: 's', µs: 'µs', us: 'µs', ns: 'ns', percent: '%', bytes: 'bytes', decbytes: 'bytes',
  reqps: 'reqps', short: '', none: '',
};

/** Kaynak türü: 'prom' | 'mixed' | 'other:<tür>' | '' (belirsiz — şablon değişkeni ya da yok). */
function dsKind(ds: unknown): string {
  if (ds == null) return '';
  if (typeof ds === 'string') {
    if (ds === '-- Mixed --') return 'mixed';
    if (ds.startsWith('$') || ds === '' || ds === 'default') return '';
    return /prom|victoria/i.test(ds) ? 'prom' : `other:${ds}`;
  }
  if (isObj(ds)) {
    const t = str(ds.type);
    const uid = str(ds.uid);
    if (uid === '-- Mixed --' || t === 'datasource' && uid === '-- Mixed --') return 'mixed';
    if (!t || t.startsWith('$') || uid.startsWith('$')) return t && PROM_TYPES.has(t) ? 'prom' : '';
    return PROM_TYPES.has(t) ? 'prom' : `other:${t}`;
  }
  return '';
}

/** Grafana değişken / aralık sözdizimini Coremetry ${ad} biçimine çevirir. */
export function normalizeExpr(expr: string, notes: string[]): string {
  let out = expr;
  const builtins: [RegExp, string][] = [
    [/\$__rate_interval|\$\{__rate_interval\}/g, '5m'],
    [/\$__interval|\$\{__interval\}/g, '1m'],
    [/\$__range|\$\{__range\}/g, '1h'],
  ];
  for (const [re, rep] of builtins) {
    if (re.test(out)) {
      notes.push(`${re.source.split('|')[0].replace('\\', '')} → ${rep} (pano sabit pencere kullanır)`);
      out = out.replace(re, rep);
    }
  }
  out = out
    .replace(/\[\[(\w+)(?::\w+)?\]\]/g, '${$1}')
    .replace(/\$\{(\w+):\w+\}/g, '${$1}')
    .replace(/\$(\w+)/g, (_m, n: string) => '${' + n + '}');
  return out;
}

function unitOf(panel: Obj, notes: string[]): string | undefined {
  const fc = isObj(panel.fieldConfig) ? panel.fieldConfig : {};
  const defaults = isObj(fc.defaults) ? fc.defaults : {};
  const u = str(defaults.unit);
  if (!u) return undefined;
  if (u in UNIT_MAP) return UNIT_MAP[u] || undefined;
  notes.push(`birim "${u}" alınmadı`);
  return undefined;
}

function heightOf(h: number | undefined): PanelHeight | undefined {
  if (h === undefined) return undefined;
  if (h <= 6) return 's';
  if (h <= 9) return undefined; // m = varsayılan (alan yazılmaz)
  return 'l';
}

function vizOf(panel: Obj): PanelVizType | undefined {
  const fc = isObj(panel.fieldConfig) ? panel.fieldConfig : {};
  const d = isObj(fc.defaults) ? fc.defaults : {};
  const c = isObj(d.custom) ? d.custom : {};
  const opts = panel;
  const stacked = (isObj(c.stacking) && str(c.stacking.mode) !== '' && str(c.stacking.mode) !== 'none') || opts.stack === true;
  const bars = str(c.drawStyle) === 'bars' || opts.bars === true;
  if (bars) return stacked ? 'stacked-bar' : 'bar';
  const filled = (num(c.fillOpacity) ?? 0) > 0 || (num(opts.fill) ?? 0) > 0;
  if (stacked) return 'stacked-area';
  if (filled) return 'area';
  return undefined;
}

let idSeq = 0;
function nextId(): string {
  idSeq += 1;
  return `g${idSeq.toString(36)}`;
}

/** Panelin ilk görünür Prometheus sorgusu + not; yoksa atlama nedeni. */
function pickTarget(panel: Obj, notes: string[]): { expr: string } | { skip: string } {
  const pk = dsKind(panel.datasource);
  if (pk === 'mixed') return { skip: 'karışık kaynak' };
  if (pk.startsWith('other:')) return { skip: `kaynak desteklenmiyor (${pk.slice(6)})` };
  const targets = arr(panel.targets).filter(isObj).filter(t => t.hide !== true);
  const prom = targets.filter(t => {
    const k = dsKind(t.datasource);
    return (k === 'prom' || (k === '' && pk !== 'mixed')) && str(t.expr).trim() !== '';
  });
  if (prom.length === 0) {
    const other = targets.map(t => dsKind(t.datasource)).find(k => k.startsWith('other:'));
    return { skip: other ? `kaynak desteklenmiyor (${other.slice(6)})` : 'Prometheus sorgusu yok' };
  }
  if (targets.length > 1) notes.push(`${targets.length} sorgudan yalnız ${str(prom[0].refId) || 'ilki'} alındı`);
  return { expr: normalizeExpr(str(prom[0].expr).trim(), notes) };
}

function featureNotes(panel: Obj, notes: string[]) {
  if (arr(panel.transformations).length > 0) notes.push('dönüşümler (transformations) alınmadı');
  const fc = isObj(panel.fieldConfig) ? panel.fieldConfig : {};
  if (arr(fc.overrides).length > 0) notes.push('alan geçersiz kılmaları (overrides) alınmadı');
  if (str(panel.repeat)) notes.push(`repeat ("${str(panel.repeat)}") alınmadı: panel bir kez`);
  if (isObj(panel.alert)) notes.push('panel alarmı alınmadı');
}

function layout(panel: Obj): Pick<Panel, 'span' | 'width' | 'height'> {
  const gp = isObj(panel.gridPos) ? panel.gridPos : {};
  const w = num(gp.w) ?? 12;
  const span = Math.max(2, Math.min(12, Math.round(w / 2)));
  const out: Pick<Panel, 'span' | 'width' | 'height'> = { span, width: widthForSpan(span) };
  const h = heightOf(num(gp.h));
  if (h) out.height = h;
  return out;
}

function convertPanel(panel: Obj, rep: GrafanaImportReport): Panel | null {
  const title = str(panel.title) || '(başlıksız)';
  const type = str(panel.type);
  const notes: string[] = [];
  const base = { id: nextId(), title, ...layout(panel) };
  const desc = str(panel.description).trim();
  const withDesc = <P extends Panel>(p: P): P => (desc ? { ...p, description: desc } : p);
  const take = (p: Panel, as: string) => { rep.panels.push({ title, as, notes }); return withDesc(p); };

  switch (type) {
    case 'timeseries':
    case 'graph': {
      const t = pickTarget(panel, notes);
      if ('skip' in t) { rep.skipped.push({ title, reason: t.skip }); return null; }
      featureNotes(panel, notes);
      const unit = unitOf(panel, notes);
      const viz = vizOf(panel);
      return take({ ...base, type: 'promql', config: { query: t.expr, ...(unit ? { unit } : {}), ...(viz ? { viz } : {}) } }, 'MetricsQL grafiği');
    }
    case 'stat':
    case 'gauge': {
      const t = pickTarget(panel, notes);
      if ('skip' in t) { rep.skipped.push({ title, reason: t.skip }); return null; }
      featureNotes(panel, notes);
      const unit = unitOf(panel, notes);
      const metric = { metricName: '', promql: t.expr };
      if (type === 'stat') {
        return take({ ...base, type: 'stat', config: { source: 'metric', metric, ...(unit ? { unit } : {}) } }, 'tek değer (MetricsQL)');
      }
      const fc = isObj(panel.fieldConfig) ? panel.fieldConfig : {};
      const d = isObj(fc.defaults) ? fc.defaults : {};
      const min = num(d.min);
      const max = num(d.max);
      return take({ ...base, type: 'gauge', config: { source: 'metric', metric, ...(unit ? { unit } : {}), ...(min !== undefined ? { min } : {}), ...(max !== undefined ? { max } : {}) } }, 'gösterge (MetricsQL)');
    }
    case 'heatmap': {
      const t = pickTarget(panel, notes);
      if ('skip' in t) { rep.skipped.push({ title, reason: t.skip }); return null; }
      const m = /([a-zA-Z_:][\w:]*)_bucket\b/.exec(t.expr);
      if (!m) { rep.skipped.push({ title, reason: 'ısı haritası sorgusu bir histogram (_bucket) metriğine çözülemedi' }); return null; }
      notes.push('ısı haritası metriğin tüm dağılımını çizer; sorgudaki süzgeç ve toplama alınmadı');
      featureNotes(panel, notes);
      return take({ ...base, type: 'heatmap', config: { metricName: m[1] } }, `ısı haritası (${m[1]})`);
    }
    case 'text': {
      const opts = isObj(panel.options) ? panel.options : {};
      const text = str(opts.content) || str(panel.content);
      if (str(opts.mode) === 'html') notes.push('HTML içerik düz metin olarak alındı');
      return take({ ...base, type: 'markdown', config: { text } }, 'not (markdown)');
    }
    default:
      rep.skipped.push({ title, reason: type ? `desteklenmeyen panel tipi: ${type}` : 'panel tipi yok' });
      return null;
  }
}

function convertVariable(v: Obj, rep: GrafanaImportReport): DashboardVariable | null {
  const name = str(v.name).replace(/[^a-zA-Z0-9_]/g, '');
  const type = str(v.type);
  if (!name) { rep.skippedVariables.push({ name: '(adsız)', reason: 'ad yok' }); return null; }
  const label = str(v.label) || undefined;
  const multi = v.multi === true ? true : undefined;
  if (type === 'query') {
    const q = isObj(v.query) ? str(v.query.query) : str(v.query);
    const m = /^\s*label_values\(\s*(?:([^,()]+?)\s*,\s*)?([\w.]+)\s*\)\s*$/.exec(q);
    if (!m) { rep.skippedVariables.push({ name, reason: `sorgu desteklenmiyor: ${q || '(boş)'}` }); return null; }
    const metric = (m[1] ?? '').trim();
    rep.variables.push(name);
    return { name, label, type: 'label', labelName: m[2], ...(metric && !metric.includes('$') ? { metric } : {}), ...(multi ? { multi } : {}) };
  }
  if (type === 'custom') {
    const options = str(v.query).split(',').map(s => s.trim()).filter(Boolean);
    rep.variables.push(name);
    return { name, label, type: 'custom', options, ...(multi ? { multi } : {}) };
  }
  rep.skippedVariables.push({ name, reason: `değişken tipi desteklenmiyor: ${type || '(yok)'}` });
  return null;
}

/** SAF: Grafana JSON metni → pano + rapor. Biçim bozuksa okunur bir hata fırlatır. */
export function importGrafanaDashboard(text: string): GrafanaImportResult {
  idSeq = 0;
  let raw: unknown;
  try { raw = JSON.parse(text); } catch { throw new Error('geçerli JSON değil'); }
  if (!isObj(raw)) throw new Error('JSON nesnesi bekleniyor');
  const dash = isObj(raw.dashboard) ? raw.dashboard : raw;
  if (!Array.isArray(dash.panels)) {
    if (Array.isArray(dash.rows)) throw new Error('eski Grafana şeması (rows): Grafana 5+ dışa aktarımı gerekli');
    throw new Error('"panels" dizisi yok: Grafana pano JSON\'u değil');
  }
  const rep: GrafanaImportReport = { panels: [], skipped: [], variables: [], skippedVariables: [], notes: [] };

  // Akış sırası: gridPos y, sonra x (Coremetry ızgarası x/y taşımaz).
  const byPos = (a: Obj, b: Obj) => {
    const pa = isObj(a.gridPos) ? a.gridPos : {};
    const pb = isObj(b.gridPos) ? b.gridPos : {};
    return (num(pa.y) ?? 0) - (num(pb.y) ?? 0) || (num(pa.x) ?? 0) - (num(pb.x) ?? 0);
  };
  const panels: Panel[] = [];
  for (const p of arr(dash.panels).filter(isObj).sort(byPos)) {
    if (str(p.type) === 'row') {
      panels.push({ id: nextId(), type: 'row', title: str(p.title) || 'Satır', width: 4, config: p.collapsed === true ? { collapsed: true } : {} });
      rep.panels.push({ title: str(p.title) || 'Satır', as: 'satır', notes: [] });
      if (str(p.repeat)) rep.notes.push(`"${str(p.title)}" satırının repeat'i alınmadı`);
      // Kapalı satırın panelleri satırın İÇİNDE durur.
      for (const c of arr(p.panels).filter(isObj).sort(byPos)) {
        const out = convertPanel(c, rep);
        if (out) panels.push(out);
      }
      continue;
    }
    const out = convertPanel(p, rep);
    if (out) panels.push(out);
  }

  const templ = isObj(dash.templating) ? arr(dash.templating.list) : [];
  const variables: DashboardVariable[] = [];
  for (const v of templ.filter(isObj)) {
    const out = convertVariable(v, rep);
    if (out) variables.push(out);
  }
  const ann = isObj(dash.annotations) ? arr(dash.annotations.list).filter(isObj).filter(a => a.builtIn !== 1) : [];
  if (ann.length > 0) rep.notes.push(`${ann.length} annotation sorgusu alınmadı`);

  const tags = arr(dash.tags).map(str).map(t => t.trim()).filter(Boolean);
  return {
    dashboard: {
      name: str(dash.title).trim() || 'Grafana panosu',
      description: str(dash.description),
      tags: [...new Set([...tags, 'grafana'])],
      panels,
      variables,
    },
    report: rep,
  };
}
