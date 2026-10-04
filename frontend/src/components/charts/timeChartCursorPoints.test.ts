import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

// v0.10.656 — operatör: "/traces histogramında süre çizgisi üzerinde de kayan
// nokta olsun, diğer grafiklerdeki gibi". Nokta YOKTU çünkü TimeChart cursor
// ayarı `points: { show: true }` yazıyordu: uPlot `points.show`u fnOrSelf'ten
// geçirir ve dönen değerin HTMLElement olmasını bekler; `true` element değil
// → nokta hiç oluşturulmaz (CorePanel.tsx'in kendi notu, v0.9.704 civarı).
// Bu pin tuzağın geri gelmesini engeller; boyut CorePanel ile aynı.

describe('TimeChart imleç noktası (uPlot show tuzağı)', () => {
  const src = readFileSync(new URL('./TimeChart.tsx', import.meta.url), 'utf8');
  it('cursor.points `show: true` taşımaz (noktaları kapatır)', () => {
    expect(src).not.toMatch(/points:\s*\{\s*show:\s*true/);
  });
  it('nokta boyutu CorePanel ile aynı (10 px, 2 px kenar)', () => {
    expect(src).toContain('points: { size: 10, width: 2 }');
  });
});

// v0.10.1102 — y oluğu sabit piksel değil, axisSize.ts measuredAxisSize.
//   • OverviewChart: sol eksen (side varsayılanı 3) — çizilen etiketin
//     GENİŞLİĞİ ölçülür (34 px'te "125ms" sığmıyordu).
//   • TimeChart: ana eksen side 0 = ÜST şerit (v0.8.91'den beri; sola taşımak
//     operatör kararı). Orada oluk yüksekliktir: punto → taban 32 px (eski 38).
//     Etiket genişliği yalnız sağ (y2, side 1) eksende ölçülür. Bu kapı
//     side'ları da çiviler: biri değişirse ölçüm dalı sessizce değişmesin.
describe('y ekseni oluğu ölçülür, sabit değil (v0.10.1102)', () => {
  for (const f of ['./TimeChart.tsx', '../../pages/service/charts/OverviewChart.tsx']) {
    it(`${f.split('/').pop()} — size: measuredAxisSize(axisFont)`, () => {
      const src = readFileSync(new URL(f, import.meta.url), 'utf8');
      expect(src).toContain('size: measuredAxisSize(axisFont)');
      expect(src).not.toMatch(/stroke: text3, size: (38|34)\b/);
    });
  }
  it('TimeChart: ana eksen side 0 (üst şerit, yükseklik dalı), y2 side 1 (genişlik dalı)', () => {
    const src = readFileSync(new URL('./TimeChart.tsx', import.meta.url), 'utf8');
    expect(src).toContain("yAxis('y', 0, fmtLeftRef");
    expect(src).toContain("yAxis('y2', 1, fmtRightRef");
  });
});

describe('VolumeChart eksen biçimlendiricisi süreyle birlikte taşındı (v0.10.656)', () => {
  const src = readFileSync(new URL('../traces/VolumeChart.tsx', import.meta.url), 'utf8');
  it('fmtLeft = süre biçimlendiricisi; fmtRight verilmez (sayı kısaltması)', () => {
    expect(src).toContain('fmtLeft={fmtVolumeDuration}');
    expect(src).not.toContain('fmtRight={fmtVolumeDuration}');
  });
});
