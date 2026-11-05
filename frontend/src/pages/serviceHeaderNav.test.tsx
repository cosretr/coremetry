// @vitest-environment jsdom
//
// serviceHeaderNav.test.tsx — v0.10.1320 (servis başlığı, operatör onaylı
// mockup "Öneri A"; operatör değişikliği: Backtrace ↔ Traces yer değişti,
// Traces sağda tek başına).
//
// Çivilenen: (1) "← All services" yerine başlığın ÜSTÜNDE kırıntı, aynı
// navHref hedefi (pencere + env taşınır, v0.9.1320); (2) Pin metinli buton
// değil, h1'in yanında IconButton yıldız anahtarı (aria-pressed); (3) drill
// çipleri üç bitişik role=group, sıra Backtrace < Logs < Endpoints <
// Problems < Anomalies < Traces, Traces son grupta tek; çip rengi düz --text
// (`subtle`); (4) Problems rozeti yalnız açık problem > 0 iken; (5) eski
// ikinci satır yok. Sayfa render'ı ağır (bundle + ~10 sorgu) — başlık
// kaynağı taranır, parçalar (DrillButton / IconButton / navHref) render'la.

import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { DrillButton } from '@/components/DrillButton';
import { IconButton } from '@/components/ui/IconButton';
import { ButtonGroup } from '@/components/ui/ButtonGroup';
import { navHref } from '@/lib/navHref';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');
const page = read('./Service.tsx');
const drill = read('../components/DrillButton.tsx');
const css = read('../styles/globals.css');

// Başlık bölgesi: `.svc-head` kabından dikkat şeridine kadar.
const headStart = page.indexOf('<div className="svc-head">');
const headEnd = page.indexOf('<ServiceAttentionStrip');
const head = page.slice(headStart, headEnd);

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode, url = '/service?name=checkout'): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter initialEntries={[url]}>{node}</MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

describe('v0.10.1320 — servis başlığı kaynağı', () => {
  it('başlık bölgesi bulundu ve dikkat şeridinden önce', () => {
    expect(headStart).toBeGreaterThan(0);
    expect(headEnd).toBeGreaterThan(headStart);
  });

  it('başlık artık `info` beklemiyor (gezinme yükleme/hata anında da var)', () => {
    expect(page).not.toMatch(/\{info && \(\s*<div className="svc-head">/);
  });

  it('eski ikinci satır ve "← All services" metni yok', () => {
    expect(page).not.toContain('← All services');
    expect(page).not.toContain('☆ Pin');
    expect(page).not.toContain('★ Pinned');
    expect(page).not.toMatch(/marginBottom: 14, flexWrap: 'wrap'/);
  });

  it('kırıntı: nav[aria-label=Breadcrumb], Services halkası navHref ile, geçerli öğe aria-current', () => {
    // v0.10.1336 — rehberli tur çapası (data-tour) ek öznitelik olarak izinli.
    expect(head).toMatch(/<nav className="crumbs svc-head__crumbs" aria-label="Breadcrumb"(?: data-tour="svc-crumbs")?>/);
    expect(head).toContain("<Link to={navHref('/services', searchParams.toString())}>Services</Link>");
    expect(head).toMatch(/<span className="crumbs__cur" aria-current="page"[^>]*>\{svc\}<\/span>/);
    // Kırıntı h1'den ÖNCE.
    expect(head.indexOf('aria-label="Breadcrumb"')).toBeLessThan(head.indexOf('<h1>'));
  });

  it('Pin: h1\'den hemen sonra IconButton yıldız, active={pinned}, aynı toggleServicePin', () => {
    const h1 = head.indexOf('<h1>{svc}</h1>');
    const pin = head.indexOf('<IconButton');
    expect(h1).toBeGreaterThan(0);
    expect(pin).toBeGreaterThan(h1);
    const pinSrc = head.slice(pin, head.indexOf('/>', head.indexOf('onClick=', pin)) + 2);
    expect(pinSrc).toContain('variant="ghost"');
    expect(pinSrc).toContain('size="sm"');
    expect(pinSrc).toContain('active={pinned}');
    expect(pinSrc).toContain("'Pin to Cmd-K quick access'");
    expect(pinSrc).toContain("'Unpin from Cmd-K quick access'");
    expect(pinSrc).toContain('onClick={() => setPinned(toggleServicePin(svc))}');
    // Pin ile h1 arasında başka öğe yok (yalnız yorum).
    const between = head.slice(h1 + '<h1>{svc}</h1>'.length, pin).replace(/\{\/\*[\s\S]*?\*\/\}/g, '').trim();
    expect(between).toBe('');
  });

  it('drill sırası: Backtrace < Logs < Endpoints < Problems < Anomalies < Traces', () => {
    const order = ['/service/backtrace', '/logs', '/endpoints', '/problems', '/anomalies', '/traces']
      .map(to => head.indexOf(`<DrillButton to="${to}"`));
    for (const i of order) expect(i).toBeGreaterThan(0);
    for (let k = 1; k < order.length; k++) expect(order[k]).toBeGreaterThan(order[k - 1]);
  });

  it('üç bitişik ButtonGroup (role=group); Traces son grupta tek başına', () => {
    const groups = [...head.matchAll(/<ButtonGroup attached aria-label="([^"]+)">([\s\S]*?)<\/ButtonGroup>/g)];
    expect(groups.map(g => g[1])).toEqual(['Bağımlılık ve sinyaller', 'Sağlık', 'Traces']);
    const drillsIn = (s: string) => [...s.matchAll(/<DrillButton to="([^"]+)"/g)].map(m => m[1]);
    expect(drillsIn(groups[0][2])).toEqual(['/service/backtrace', '/logs', '/endpoints']);
    expect(drillsIn(groups[1][2])).toEqual(['/problems', '/anomalies']);
    expect(drillsIn(groups[2][2])).toEqual(['/traces']);
    // primitiveClasses: taban sınıf ui/ dışında elle yazılmaz.
    expect(page).not.toContain('className="btn-group');
  });

  it('drill çipleri düz --text (subtle) — gezinme CTA değil', () => {
    const drills = [...head.matchAll(/<DrillButton\b[^>]*?(?:\/>|label=)/g)];
    expect(drills.length).toBe(6);
    for (const d of drills) expect(d[0]).toContain('variant="subtle"');
  });

  it('bağlantılar değişmedi (to/params/range/carry)', () => {
    expect(head).toContain('<DrillButton to="/service/backtrace" params={{ name: svc }}');
    for (const to of ['/traces', '/logs', '/endpoints']) {
      expect(head).toContain(`<DrillButton to="${to}" params={{ service: svc }} range={range} carry={SERVICE_PIVOT_CARRY}`);
    }
    for (const to of ['/problems', '/anomalies']) {
      expect(head).toMatch(new RegExp(`<DrillButton to="${to}" params=\\{\\{ service: svc \\}\\} variant="subtle"`));
    }
  });

  it('Problems rozeti yalnız açık problem > 0 iken; sayı bundle problemlerinden', () => {
    expect(page).toContain("problems.filter(p => p.status === 'open').length");
    expect(head).toContain('{openProblems > 0 && <span className="badge b-err">{openProblems}</span>}');
    // Anomali sayısı yok (sayfada veri yok); başlıkta RED sayısı yok (v0.9.211).
    expect(head).not.toMatch(/info\??\.(errorRate|p99|rps|throughput)/);
  });
});

describe('v0.10.1320 — parçalar render', () => {
  it('Problems çipi rozeti: açık > 0 → .badge.b-err, 0 → rozet yok; href aynı', () => {
    const chip = (n: number) => (
      <DrillButton to="/problems" params={{ service: 'checkout' }} variant="subtle"
        title="Open problems for this service"
        label={<>⚠ Problems{n > 0 && <span className="badge b-err">{n}</span>}</>} />
    );
    let el = render(chip(3));
    let a = el.querySelector('a')!;
    expect(a.getAttribute('href')).toBe('/problems?service=checkout');
    expect(a.getAttribute('title')).toBe('Open problems for this service');
    expect(a.querySelector('.badge.b-err')?.textContent).toBe('3');
    expect(a.style.color).toBe('var(--text)');
    act(() => { root?.unmount(); }); host?.remove();
    el = render(chip(0));
    a = el.querySelector('a')!;
    expect(a.querySelector('.badge')).toBeNull();
    expect(a.textContent).toBe('⚠ Problems');
  });

  it('bitişik ButtonGroup bağlantı çocuklarıyla: role=group + ad + a.sec çocuklar', () => {
    const el = render(
      <ButtonGroup attached aria-label="Sağlık">
        <DrillButton to="/problems" params={{ service: 'checkout' }} variant="subtle" label="⚠ Problems" />
        <DrillButton to="/anomalies" params={{ service: 'checkout' }} variant="subtle" label="∿ Anomalies" />
      </ButtonGroup>,
    );
    const g = el.querySelector<HTMLElement>('[role="group"]')!;
    expect(g.getAttribute('aria-label')).toBe('Sağlık');
    expect(g.className).toBe('btn-group is-attached');
    expect([...g.children].map(c => `${c.tagName}.${c.className}`)).toEqual(['A.sec', 'A.sec']);
  });

  it('DrillButton satır içi borderRadius taşımıyor (bitişik iç köşeleri ezmesin)', () => {
    const el = render(<DrillButton to="/logs" params={{ service: 'checkout' }} variant="subtle" label="≡ Logs" />);
    expect(el.querySelector('a')!.style.borderRadius).toBe('');
    expect(drill.replace(/^\s*\/\/.*$/gm, '')).not.toMatch(/borderRadius/);
  });

  it('Pin yıldızı aria-pressed pinned durumunu yansıtır', () => {
    for (const pinned of [false, true]) {
      const el = render(
        <IconButton variant="ghost" size="sm" className="ib-star" active={pinned}
          aria-label={pinned ? 'Unpin from Cmd-K quick access' : 'Pin to Cmd-K quick access'}
          icon={pinned ? '★' : '☆'} />,
      );
      const b = el.querySelector('button')!;
      expect(b.getAttribute('aria-pressed')).toBe(String(pinned));
      expect(b.textContent).toBe(pinned ? '★' : '☆');
      act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
    }
  });

  it('kırıntı hedefi pencereyi + env\'i taşır, servis kimliğini taşımaz', () => {
    const href = navHref('/services', 'name=checkout&range=custom:1000-2000&env=prod&tab=details');
    const q = new URLSearchParams(href.slice(href.indexOf('?')));
    expect(href.startsWith('/services?')).toBe(true);
    expect(q.get('range')).toBe('custom:1000-2000');
    expect(q.get('env')).toBe('prod');
    expect(q.get('name')).toBeNull();
  });
});

describe('v0.10.1320 — CSS', () => {
  const rules = css.replace(/\/\*[\s\S]*?\*\//g, '');
  it('bitişik grup a.sec birleşimi (buton kurallarının ikizi)', () => {
    expect(rules).toMatch(/\.btn-group\.is-attached > a\.sec:not\(:first-of-type\) \{\s*margin-left: -1px; border-top-left-radius: 0; border-bottom-left-radius: 0;/);
    expect(rules).toMatch(/\.btn-group\.is-attached > a\.sec:not\(:last-of-type\) \{\s*border-top-right-radius: 0; border-bottom-right-radius: 0;/);
  });
  it('.svc-head ortalanır ve sarar; gezinme sağda, telefonda sola yaslı', () => {
    expect(rules).toMatch(/\.svc-head \{[^}]*align-items: center;[^}]*flex-wrap: wrap;/);
    expect(rules).toMatch(/\.svc-head__nav \{[^}]*margin-left: auto;[^}]*flex-wrap: wrap;/);
    expect(rules).toMatch(/@media \(max-width: 640px\) \{\s*\.svc-head__nav \{ margin-left: 0; flex-basis: 100%;/);
  });
  // v0.10.1824 — operatör: düğmeler gri sayfa zemininde seçilmiyordu.
  it('başlık gezinme düğmeleri kart yüzeyinde ve belirgin kenarlı', () => {
    expect(rules).toMatch(/\.svc-head__nav a\.sec \{\s*background: var\(--bg1\); border-color: var\(--border-strong\);\s*font-weight: 500; box-shadow: var\(--shadow-sm\);/);
    expect(rules).toMatch(/\.svc-head__nav a\.sec:hover, \.svc-head__nav a\.sec:focus-visible \{\s*background: var\(--accent-bg\); border-color: var\(--accent\);/);
  });
});
