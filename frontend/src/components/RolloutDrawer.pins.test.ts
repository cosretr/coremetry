import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.338 — Operator-reported (rollout çekmecesi): (1) revizyon ve imaj
// "…" ile kırpılıyordu — globals.css'in genel `tbody td { nowrap; max-width:
// 320px; ellipsis }` kuralı; (2) aynı workload birden çok cluster'a çıkıyor
// ama çekmece cluster'ı söylemiyordu. Kaynak pini: kimlik değerleri sarar,
// kırpılmaz; çekmece küme adını çözer ve gösterir.
// v0.10.943 — tablo standardı T1: Geçiş paneli `<th>` satırlı tablodan
// KeyValue'ya geçti; sarma artık `.keyval__val` kuralından (`.td-full` değil).

describe('RolloutDrawer (v0.10.338)', () => {
  const src = readFileSync(resolve(__dirname, 'RolloutDrawer.tsx'), 'utf8');
  const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');

  it('revizyon, imaj ve küme değerleri sarar (KeyValue), kırpılmaz', () => {
    for (const label of ['revizyon', 'imaj', 'küme']) {
      const re = new RegExp(`<KeyValueRow k="${label}" mono\\b`);
      expect(src, `${label} satırı KeyValue kimlik değeri değil`).toMatch(re);
    }
    const rule = css.match(/\n\.keyval__val \{([^}]*)\}/);
    expect(rule, '.keyval__val kuralı globals.css\'te yok').not.toBeNull();
    expect(rule![1]).toMatch(/white-space:\s*pre-wrap/);
    expect(rule![1]).toMatch(/overflow-wrap:\s*anywhere/);
    expect(rule![1]).not.toMatch(/text-overflow|nowrap|overflow:\s*hidden/);
  });

  // v0.10.943 — sabit genişlikli servis tablosunda önce/sonra çifti "…" ile
  // kesilirdi (kırmızı sonraki değer okunmazdı, numeric → ipucu yok).
  it('önce/sonra kolonları kırpılmaz, sarar (truncate wrap)', () => {
    for (const id of ['err', 'p99', 'rps']) {
      const re = new RegExp(`\\{ id: '${id}',[^}]*truncate: 'wrap' \\}`);
      expect(src, `${id} kolonu truncate 'wrap' değil`).toMatch(re);
    }
    expect(css).toMatch(/tbody td\.cell-wrap \{[^}]*white-space:\s*normal/);
  });

  it('tam kimlik kopyalanabilir', () => {
    expect(src).toMatch(/<CopyButton value=\{r\.revision\}/);
    expect(src).toMatch(/<CopyButton value=\{curImage\}/);
  });

  it('çekmece küme adını çözer ve başlıkta + Geçiş panelinde gösterir', () => {
    expect(src).toContain('useEntityClusters()');
    expect(src).toContain('rolloutPlaceLabel(r, clusterName)');
    expect(src).toMatch(/\{clusterName \|\| id\.clusterId\} · \{id\.namespace\}/);
  });
});
