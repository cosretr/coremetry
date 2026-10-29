import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// depPins.test.ts — v0.10.283 (docs/audit/chart-layer.md Dilim 0, SÖZLEŞME).
//
// `@grafana/ui` `^13.1.2` caret'i npm `latest` 13.2.x'i kapsıyordu ve o
// sürümler React ≥19 peer istiyor; repo React 18.3.1 pin. Lock koruyordu,
// sözleşme korumuyordu: temiz `npm install` / `npm update` peer çakışması
// ya da `--legacy-peer-deps` ile React-19-hedefli ağaç. İkinci risk: uPlot
// çift kopyası — `@grafana/ui` exact 1.6.32, bizim caret drift ederse
// `uPlot.sync` registry'si modül düzeyinde ikiye bölünür; hata yok, uyarı
// yok, imleç sadece geçmez. Bu test package.json'u SÖZLEŞME olarak okur.
const pkg = JSON.parse(readFileSync(resolve(__dirname, '../../../package.json'), 'utf8')) as {
  dependencies: Record<string, string>;
  overrides?: Record<string, string | Record<string, string>>;
};

describe('chart layer dependency pins (Dilim 0)', () => {
  it('@grafana/* ve uplot TAM pin — caret/tilde yok', () => {
    for (const name of ['@grafana/ui', '@grafana/data', 'uplot']) {
      const v = pkg.dependencies[name];
      expect(v, name).toBeDefined();
      expect(v, `${name} caret/tilde taşımamalı (${v})`).toMatch(/^\d+\.\d+\.\d+$/);
    }
    expect(pkg.dependencies['@grafana/ui']).toBe(pkg.dependencies['@grafana/data']);
  });
  it('uplot override = @grafana/ui\'nin exact pin\'i (tek kopya)', () => {
    expect(pkg.overrides?.uplot).toBe(pkg.dependencies.uplot);
    expect(pkg.overrides?.uplot).toBe('1.6.32');
  });
  // v0.10.1600 — React 19'a geçildi. @grafana/ui + @grafana/data 13.1.x peer'ı
  // hâlâ `react ^18.0.0` (13.2+ `>=19` ister, 18'i bırakır). Uyumsuzluk global
  // --legacy-peer-deps ile DEĞİL, package.json `overrides`'ta paket-kapsamlı
  // `$react` / `$react-dom` ile çözülür → `npm ci` deterministik, ağaçta tek
  // React kopyası. CorePanel + tüm chart testleri React 19 altında yeşil.
  // Bu blok @grafana/ui 13.2+'ya geçince (peer >=19) silinmeli.
  it('React 19 exact pin; @grafana/* 13.1.x peer uyumsuzluğu overrides ile kapsamlı', () => {
    const deps = pkg.dependencies;
    expect(deps.react).toMatch(/^19\.\d+\.\d+$/);
    expect(deps['react-dom']).toBe(deps.react);
    expect(deps['@grafana/ui']).toMatch(/^13\.1\./);
    const ov = pkg.overrides ?? {};
    for (const name of ['@grafana/ui', '@grafana/data', 'react-custom-scrollbars-2']) {
      expect(ov[name], name).toEqual({ react: '$react', 'react-dom': '$react-dom' });
    }
    expect(ov['react-table']).toEqual({ react: '$react' });
  });
});
