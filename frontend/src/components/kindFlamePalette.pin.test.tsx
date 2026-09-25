// kindFlamePalette.pin.test.tsx — v0.10.922 (sade palet adım 1).
//
// Ne çiviliyor:
//   • KindBadge: frame türü bir KATEGORİ → her tür AYNI nötr rozet
//     (--bg3 zemin, --text2 yazı); etiket kelimesi kalır, CPU rozetsiz.
//   • BreakdownBar/kindColor: grafik dilimleri seri paletinden gelir —
//     durum (--ok) ya da marka (--brand) rengi DEĞİL (K5/K6), beşi ayrık.
//   • FlameGraph: odak çerçevesi marka kırmızısıyla (#E30613) DOLMAZ;
//     kendi seri rengini korur, odak 2px --focus kontur ile işaretlenir.
// Saf render (react-dom/server, node env) — DOM yok.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { ReactElement } from 'react';
import { KindBadge, BreakdownBar, kindColor, kindLabel } from './KindBadge';
import { FlameGraph } from './FlameGraph';
import { SERIES_PALETTES, seriesColor } from '@/lib/chartFmt';
import type { FlameNode, ProfileFrameKind } from '@/lib/types';

const KINDS: ProfileFrameKind[] = ['cpu', 'lock', 'io', 'sleep', 'gc'];

describe('KindBadge — tür kategori, tek nötr stil', () => {
  it('CPU rozetsiz (varsayılan tür, gürültü yok)', () => {
    expect(KindBadge({ kind: 'cpu' })).toBeNull();
  });
  it('diğer her tür aynı nötr zemin/yazı; etiket kelimesi korunur', () => {
    for (const k of KINDS.filter(k => k !== 'cpu')) {
      const el = KindBadge({ kind: k }) as ReactElement<{ style: Record<string, unknown>; children: string }>;
      expect(el.props.style.background).toBe('var(--bg3)');
      expect(el.props.style.color).toBe('var(--text2)');
      expect(el.props.children).toBe(kindLabel(k));
    }
  });
});

describe('BreakdownBar — grafik dilimleri seri paletinden', () => {
  it('kindColor durum/marka tokenı döndürmez, beş tür beş ayrı palet rengi', () => {
    const cs = KINDS.map(kindColor);
    for (const c of cs) {
      expect(c).not.toMatch(/--(ok|brand|err|warn)/);
      expect(SERIES_PALETTES.dark).toContain(c);
    }
    expect(new Set(cs).size).toBe(KINDS.length);
  });
  it('lejant etiket + yüzde yazar (renk tek taşıyıcı değil)', () => {
    const html = renderToStaticMarkup(<BreakdownBar b={{ cpu: 50, lock: 25, io: 25, sleep: 0, gc: 0 }} />);
    expect(html).toContain('Lock');
    expect(html).toContain('25.0%');
    expect(html).not.toContain('var(--brand)');
    expect(html).not.toContain('var(--ok)');
  });
});

describe('FlameGraph — odak konturla, marka kırmızısı yok', () => {
  const root: FlameNode = {
    name: 'root', value: 10,
    children: [{ name: 'main.work', value: 10, children: [] }],
  };
  const html = renderToStaticMarkup(<FlameGraph root={root} totalWidth={400} />);

  it('#E30613 dolgusu yok', () => {
    expect(html.toLowerCase()).not.toContain('#e30613');
  });
  it('odak (kök) kendi seri rengini korur + 2px --focus kontur', () => {
    const rects = html.match(/<rect [^>]*>/g) ?? [];
    expect(rects.length).toBe(2);
    expect(rects[0]).toContain(`fill="${seriesColor('root')}"`);
    expect(rects[0]).toContain('stroke="var(--focus)"');
    expect(rects[0]).toContain('stroke-width="2"');
    expect(rects[1]).not.toContain('var(--focus)');
  });
});
