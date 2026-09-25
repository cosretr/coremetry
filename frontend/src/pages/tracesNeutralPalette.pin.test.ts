// tracesNeutralPalette.pin.test.ts — v0.10.922 (sade palet adım 1).
// Operatör kararı K5: sağlıklı/normal hâl NÖTR — yeşil "OK" rozeti, sabit
// amber KPI, hatalı satırda rozet + zemin tonu çifti yok. Renk yalnız
// sapmada (ERROR), kelime ekran okuyucuya kalır (sr-only "OK").
// Bu bir BAĞLANMA pini (kaynak grep'i); görünümün kendisi değil.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const trace = readFileSync(resolve(__dirname, 'Trace.tsx'), 'utf8');
const traces = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
const spanDetail = readFileSync(resolve(__dirname, '../components/SpanDetail.tsx'), 'utf8');
const shapes = readFileSync(resolve(__dirname, '../components/traces/ShapesView.tsx'), 'utf8');
const exploreTraces = readFileSync(resolve(__dirname, 'explore/TracesResult.tsx'), 'utf8');

describe('Trace özeti: sağlıklı trace rozetsiz (v0.10.922)', () => {
  it('ERROR rozeti kalır; yeşil OK rozeti yok, kelime sr-only', () => {
    expect(trace).toContain('<span className="badge b-err">ERROR</span>');
    expect(trace).toContain('<span className="sr-only">OK</span>');
    expect(trace).not.toContain("hasErr ? 'b-err' : 'b-ok'");
  });
});

describe('Traces listesi: K5 nötr sağlıklı satır (v0.10.922)', () => {
  it("status hücresi: hata → ERROR rozeti, sağlıklı → görsel boş (sr-only OK)", () => {
    const i = traces.indexOf("case 'status':    return t.hasError");
    expect(i).toBeGreaterThan(-1);
    const cell = traces.slice(i, i + 300);
    expect(cell).toContain('<span className="badge b-err">ERROR</span>');
    expect(cell).toContain('<span className="sr-only">OK</span>');
    expect(traces).not.toContain('<span className="badge b-ok">OK</span>');
  });
  it('hatalı satırda zemin tonu yok — bir gerçek = bir sinyal (rozet)', () => {
    expect(traces).not.toMatch(/background: t\.hasError \?/);
    expect(traces).not.toContain('var(--err) 8%');
  });
  it("HeaderStat 'warn' tonu yok; yanıt süresi nötr, yalnız hata sayıları kırmızı", () => {
    expect(traces).toContain("tone?: 'err';");
    expect(traces).not.toContain('tone="warn"');
    expect(traces).toContain("tone={headerStats.errRate > 0 ? 'err' : undefined}");
  });
  it('agg görünümü: %0 hata nötr b-gray (yeşil değil)', () => {
    expect(traces).toContain("a.errorRate > 0 ? 'b-warn' : 'b-gray'");
    expect(traces).not.toContain("'b-warn' : 'b-ok'");
  });
});

// İnceleme bulgusu (v0.10.922): aynı sayfada iki kural olmasın — /trace'in
// span paneli, /traces'in Shapes görünümü ve Explore'un trace tablosu da
// sağlıklı hâli yeşil basmaz.
describe('Aynı kural komşu yüzeylerde (v0.10.922)', () => {
  it('SpanDetail: ERROR rozeti ya da sr-only OK; baseline "normal" nötr', () => {
    expect(spanDetail).toContain('<span className="badge b-err" style={{ marginLeft: 4 }}>ERROR</span>');
    expect(spanDetail).toContain('<span className="sr-only">OK</span>');
    expect(spanDetail).not.toContain("'b-err' : 'b-ok'");
    expect(spanDetail).toContain('<span className="badge b-gray">normal</span>');
  });
  it('Shapes görünümü: %0 hata nötr, agg ile aynı', () => {
    expect(shapes).toContain("r.errorRate > 0 ? 'b-warn' : 'b-gray'");
    expect(shapes).not.toContain("'b-warn' : 'b-ok'");
  });
  it('Explore trace tablosu: sağlıklı → sr-only OK', () => {
    expect(exploreTraces).toContain('<span className="sr-only">OK</span>');
    expect(exploreTraces).not.toContain('<span className="badge b-ok">OK</span>');
  });
});

// İnceleme bulgusu (v0.10.922): kanıt vurgusu seçim/odak diline girmesin —
// dolgu seçili satırın (.wf-sel), düz --accent çerçeve odak halkasının dili
// (light/redhat'te --accent == --focus). Kanıt: kesikli --accent2, dolgusuz.
describe('Kanıt vurgusu seçime benzemez (v0.10.922)', () => {
  const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
  const rule = (sel: string) => {
    const m = css.match(new RegExp(`(?:^|\\n)${sel.replace(/[.*+?^${}()|[\]\\>]/g, '\\$&')}\\s*\\{([^}]*)\\}`));
    expect(m, `${sel} kuralı globals.css'te`).not.toBeNull();
    return m![1];
  };
  it('.wf-evidence: kesikli --accent2 çerçeve, zemin yok', () => {
    const r = rule('.wf-evidence');
    expect(r).toContain('dashed var(--accent2)');
    expect(r).not.toContain('background');
  });
  it('tr.wf-evidence: kesikli sol kenar, td zemini yok', () => {
    expect(rule('tr.wf-evidence > td:first-child')).toContain('dashed var(--accent2)');
    expect(css).not.toMatch(/tr\.wf-evidence[^{]*\{[^}]*background/);
  });
});
