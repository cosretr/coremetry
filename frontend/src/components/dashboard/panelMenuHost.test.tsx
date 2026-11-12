// @vitest-environment jsdom
//
// v0.10.1998 (Dashboards Faz 3 dilim 3.4) — panel başına TEK ⋯.
// Çerçeve, içeride menüyü üstlenen bir grafik yaşadığı sürece `hosted`
// görür (kendi ⋯'ünü çizmez); grafik gidince (hata / yükleniyor / veri yok)
// çerçevenin menüsü geri gelir. Ayrıca: Dashboard.tsx'te ikinci bir menü
// tetiği (eski PanelMenu) kalmadı; tek panel bağlantısı ?panel= taşır.
import { describe, it, expect, afterEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { PanelMenuProvider, usePanelMenuFrame, usePanelMenuHost } from './panelMenuHost';
import { DASHBOARD_RESERVED_PARAMS, panelShareUrl } from '@/lib/dashboardUrl';
import { metricsExplorerCodeHref } from '@/lib/metricsExplorer/panels';
import { decodeMx } from '@/lib/metricsExplorer/model';
import type { PanelMenuAction } from '@/lib/chart/panelMenu';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ACTIONS: PanelMenuAction[] = [{ key: 'edit', label: 'Düzenle', onClick: () => {} }];
let seen: PanelMenuAction[] | undefined;
function Chart() { seen = usePanelMenuHost(); return <i data-chart="" />; }
let setChart: (v: boolean) => void = () => {};
function Frame() {
  const f = usePanelMenuFrame();
  const [chart, set] = useState(true);
  setChart = set;
  return (
    <div>
      {!f.hosted && <b data-frame-menu="" />}
      <PanelMenuProvider actions={ACTIONS} claim={f.claim}>{chart ? <Chart /> : <span>Veri yok</span>}</PanelMenuProvider>
    </div>
  );
}

let root: Root | null = null;
afterEach(() => { if (root) act(() => root!.unmount()); root = null; document.body.innerHTML = ''; });

describe('panelMenuHost — tek ⋯', () => {
  it('grafik varken çerçeve menüsü yok, grafik eylemleri alır; grafik gidince geri gelir', () => {
    const host = document.createElement('div');
    document.body.appendChild(host);
    act(() => { root = createRoot(host); root.render(<Frame />); });
    expect(host.querySelector('[data-frame-menu]')).toBeNull();
    expect(seen).toBe(ACTIONS);
    act(() => setChart(false));
    expect(host.querySelector('[data-frame-menu]')).not.toBeNull();
  });

  it('çerçeve dışında grafik eylem almaz', () => {
    const host = document.createElement('div');
    document.body.appendChild(host);
    seen = ACTIONS;
    act(() => { root = createRoot(host); root.render(<Chart />); });
    expect(seen).toBeUndefined();
  });

  it('Dashboard.tsx eski çift menüyü (PanelMenu + role=menu) taşımaz', () => {
    const src = readFileSync(resolve(__dirname, '../../pages/Dashboard.tsx'), 'utf8').replace(/^\s*\/\/.*$/gm, '');
    expect(src).not.toMatch(/function PanelMenu\b|<PanelMenu\b/);
    expect(src).toContain('<PanelFrameMenu');
    expect(src).toContain('<LazyMount');
  });
});

describe('tek panel bağlantısı', () => {
  it('panel rezerve; bağlantı paramları korur, edit düşer', () => {
    expect(DASHBOARD_RESERVED_PARAMS.has('panel')).toBe(true);
    const u = new URL(panelShareUrl('https://x/dashboard?id=d1&range=1h&service=api&edit=1', 'p9'));
    expect(u.searchParams.get('panel')).toBe('p9');
    expect(u.searchParams.get('service')).toBe('api');
    expect(u.searchParams.get('range')).toBe('1h');
    expect(u.searchParams.has('edit')).toBe(false);
  });
  it('promql paneli Metrics Explorer kod kipinde açılır', () => {
    const href = metricsExplorerCodeHref('sum(rate(x[5m]))', '6h', 'vm')!;
    const sp = new URL(href, 'https://x').searchParams;
    expect(sp.get('range')).toBe('6h');
    const m = decodeMx(sp.get('mx'))!;
    expect(m.mode).toBe('code');
    expect(m.code).toBe('sum(rate(x[5m]))');
    expect(m.src).toBe('vm');
    expect(metricsExplorerCodeHref('   ')).toBeNull();
  });
});
