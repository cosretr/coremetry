import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1430 — global auto-refresh kaynak pinleri. Operatör: "Sağ üstte zaman
// yanında Grafana gibi ayrı refresh olabilir. 60 saniye default olabilir."
//   • RefreshPicker Topbar'da ve ContextBar'da TimeRangePicker'dan SONRA;
//   • kayma tek yerden: useUrlRange memo'sunun anchor bağımlılığı (194 çağrı
//     yerine dokunulmadı; timeRangeToNs JSX'te çıplak değil);
//   • saat AppShell'de bir kez, useEventStream ile aynı kapı;
//   • donmuş-pencere poll'ları kalktı (endpoints 30 s, service map 30 s →
//     LIVE_META); Clusters ?live=1 emekli.
const read = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');
const code = (s: string) => s.replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/^\s*\/\/.*$/gm, '');

describe('RefreshPicker yerleşimi', () => {
  it('Topbar: TimeRangePicker → RefreshPicker; env-only sayfada da; ayraç üçünü de kapsar', () => {
    const src = code(read('Topbar.tsx'));
    expect(src).toMatch(/<TimeRangePicker value=\{range\} onChange=\{onRangeChange\} \/>[\s\S]*?\{\(range \|\| showEnv\) && <RefreshPicker \/>\}/);
    expect(src).toContain('{(range || context || showEnv) && <div className="topbar-prefs-sep" />}');
  });
  it('ContextBar: TimeRangePicker → RefreshPicker', () => {
    const src = code(read('ContextBar/ContextBar.tsx'));
    expect(src).toMatch(/<TimeRangePicker value=\{params\.range\}[^\n]*\n\s*<RefreshPicker \/>/);
  });
  it('yalnız ui atomları + yerel select; ham <button> yok; lucide ikonu', () => {
    const src = read('RefreshPicker.tsx');
    expect(src).not.toMatch(/<button\b/);
    expect(src).toContain("import { IconButton } from './ui/IconButton';");
    expect(src).toContain("import { RefreshCw } from 'lucide-react';");
    expect(src).toContain('aria-live="polite"');
    expect(src).toContain('useIsNarrow()');
  });
});

describe('kayma mekanizması ve saat', () => {
  it('useUrlRange: memo bağımlılığı anchor; custom: kaymaz', () => {
    const src = code(read('../lib/useUrlRange.ts'));
    expect(src).toContain('const anchor = useRefreshAnchor();');
    expect(src).toContain("const slide = effective.startsWith('custom:') ? 0 : anchor;");
    expect(src).toMatch(/useMemo\(\s*\(\) => \{ void slide; return decodeRange\(effective, \{ preset \}\); \},\s*\[effective, preset, slide\],\s*\)/);
  });
  it('AppShell saati bir kez, SSE ile aynı kapıdan mount eder', () => {
    const src = code(read('AppShell.tsx'));
    expect(src).toContain('useAutoRefresh(!!user && !isPublic && !kioskBare && !cosreBare);');
    expect(src.match(/useAutoRefresh\(/g)?.length).toBe(1);
  });
  it('donmuş-pencere poll\'ları kalktı; since anahtarlılar LIVE_META', () => {
    const ep = code(read('../lib/queries/endpoints.ts'));
    expect(ep).not.toContain('refetchInterval');
    const sv = code(read('../lib/queries/services.ts'));
    expect(sv).not.toContain('refetchInterval');
    expect(sv.match(/meta: LIVE_META,/g)?.length).toBe(3);
    const strip = code(read('../pages/service/DetailsPropsStrip.tsx'));
    expect(strip).toContain('meta: LIVE_META,');
  });
  // v0.10.2436 — ServiceLatencyHeatmap (Service › Metrics Latency) kalktı.
  it('ağır okumalar rung\'lu pencere alır (endpoint downstream/callers)', () => {
    const ed = code(read('../pages/EndpointDetail.tsx'));
    expect(ed).toContain('const heavy = useMemo(() => snapHeavyWindow(range, { from, to }), [range, from, to]);');
    expect(ed).toMatch(/<WhereTheTimeGoesSection refObj=\{refObj\} from=\{heavy\.from\} to=\{heavy\.to\}/);
    expect(ed).toMatch(/<CallersSection refObj=\{refObj\} from=\{heavy\.from\} to=\{heavy\.to\}/);
  });
  it('Services: sayfa sıfırlama efekti range KİMLİĞİNE değil dizgesine bağlı', () => {
    const src = code(read('../pages/Services.tsx'));
    expect(src).toContain('const rangeSig = encodeRange(range);');
    expect(src).toMatch(/setPage\(0\);\n\s*\}, \[committedFilter, rangeSig,/);
  });
  it('i18n: rp.* anahtarları EN ve TR\'de eşit', () => {
    const keys = (s: string) => Array.from(s.matchAll(/'(rp\.[a-zA-Z.]+)':/g)).map(m => m[1]);
    // v0.10.2307/2400 — EN gövdesi i18n.en.ts (+ EN_EAGER i18n.common.ts), TR i18n.tr.ts.
    const en = keys(read('../lib/i18n.en.ts') + read('../lib/i18n.common.ts'));
    const tr = keys(read('../lib/i18n.tr.ts'));
    expect(en.length).toBeGreaterThan(10);
    expect(tr).toEqual(en);
  });
});
