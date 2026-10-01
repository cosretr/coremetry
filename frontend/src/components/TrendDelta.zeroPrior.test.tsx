// TrendDelta.zeroPrior — v0.10.1025 (Databases dilim 3).
//
// NE ÇİVİLİYOR: prior===0 sunumu çağırana bağlı bir prop (`zeroPrior`).
//   · Varsayılan / 'new-in-list': v0.9.818'in "listede yeni" rozeti AYNEN
//     (iki TOP-N okumasını kıyaslayan tablolar — Endpoints, Databases,
//     Messaging). Mevcut çağıranların çıktısı bayt bayt aynı kalmalı.
//   · 'was-zero': tek varlık detayında (/database) kıyaslanan liste yok;
//     doğru cümle "önceki pencerede 0'dı". Renk oktaki kuralla aynı:
//     lowerBetter'da 0'dan artış kötüleşme (--err), neutral'da yön tonu.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { TrendDelta } from './TrendDelta';
import { LIST_NEW_LABEL, LIST_NEW_TITLE } from '@/lib/endpointHonesty';

const html = (el: React.ReactElement) => renderToStaticMarkup(el);
const escapeAttr = (s: string) => s.replace(/&/g, '&amp;').replace(/'/g, '&#x27;').replace(/"/g, '&quot;');

describe('TrendDelta zeroPrior — varsayılan davranış DEĞİŞMEDİ', () => {
  it('prop yokken prior 0 → "listede yeni" rozeti ve liste ipucu', () => {
    const out = html(<TrendDelta cur={5} prior={0} kind="lowerBetter" />);
    expect(out).toContain(LIST_NEW_LABEL);
    expect(out).toContain(escapeAttr(LIST_NEW_TITLE));
    expect(out).toContain('badge b-info');
    expect(out).not.toContain('önce 0');
  });

  it("açık 'new-in-list' varsayılanla bayt bayt aynı", () => {
    for (const kind of ['lowerBetter', 'neutral'] as const) {
      expect(html(<TrendDelta cur={5} prior={0} kind={kind} zeroPrior="new-in-list" />))
        .toBe(html(<TrendDelta cur={5} prior={0} kind={kind} />));
    }
  });

  it('sıfır olmayan prior\'da prop çıktıyı DEĞİŞTİRMEZ (yüzde, nokta, ipucu)', () => {
    const pairs: [number, number][] = [[120, 100], [80, 100], [101, 100]];
    for (const [cur, prior] of pairs) {
      for (const kind of ['lowerBetter', 'neutral'] as const) {
        const base = html(<TrendDelta cur={cur} prior={prior} kind={kind} />);
        expect(html(<TrendDelta cur={cur} prior={prior} kind={kind} zeroPrior="was-zero" />)).toBe(base);
      }
    }
  });

  it('prior yok ya da 0→0 → hiçbir şey, iki kipte de', () => {
    for (const z of ['new-in-list', 'was-zero'] as const) {
      expect(html(<TrendDelta cur={5} kind="lowerBetter" zeroPrior={z} />)).toBe('');
      expect(html(<TrendDelta cur={0} prior={0} kind="lowerBetter" zeroPrior={z} />)).toBe('');
    }
  });
});

describe("TrendDelta zeroPrior='was-zero' — tek varlık detayı", () => {
  it('lowerBetter: "önce 0" + "Önceki pencerede 0\'dı." + kötüleşme rengi', () => {
    const out = html(<TrendDelta cur={4} prior={0} kind="lowerBetter" zeroPrior="was-zero" />);
    expect(out).toContain('önce 0');
    expect(out).toContain(escapeAttr("Önceki pencerede 0'dı."));
    expect(out).toContain('color:var(--err)');
    expect(out).toContain('data-trend-delta="was-zero"');
    // Liste dili SIZMAZ.
    expect(out).not.toContain(LIST_NEW_LABEL);
    expect(out).not.toContain('badge b-info');
  });

  it('neutral: yön tonu (--accent2), kırmızı değil', () => {
    const out = html(<TrendDelta cur={4} prior={0} kind="neutral" zeroPrior="was-zero" />);
    expect(out).toContain('önce 0');
    expect(out).toContain('color:var(--accent2)');
    expect(out).not.toContain('var(--err)');
  });
});
