import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { clusterSearch } from './clusterSearch';

// v0.10.928 (Y1) — cluster kartı `div onClick` → `CardLink`. Hedef sorgu
// dizesi saf fonksiyonda; davranış eski `openCluster` ile birebir.
describe('clusterSearch', () => {
  it('cluster yazar, diğer parametreleri korur', () => {
    const s = clusterSearch(new URLSearchParams('from=now-1h&env=prod'), 'ocp-a');
    const p = new URLSearchParams(s);
    expect(s.startsWith('?')).toBe(true);
    expect(p.get('cluster')).toBe('ocp-a');
    expect(p.get('from')).toBe('now-1h');
    expect(p.get('env')).toBe('prod');
  });
  it('önceki cluster\'ın sekme/drawer kimliklerini siler (v0.9.17)', () => {
    const p = new URLSearchParams(clusterSearch(
      new URLSearchParams('cluster=old&tab=x&section=y&pod=z&ns=w&namespace=keep'), 'new'));
    expect(p.get('cluster')).toBe('new');
    for (const k of ['tab', 'section', 'pod', 'ns']) expect(p.has(k)).toBe(false);
    expect(p.get('namespace')).toBe('keep');
  });
  it('girdiyi değiştirmez', () => {
    const prev = new URLSearchParams('tab=x');
    clusterSearch(prev, 'a');
    expect(prev.toString()).toBe('tab=x');
  });
  it('ad kaçışlanır', () => {
    expect(new URLSearchParams(clusterSearch(new URLSearchParams(), 'a b&c')).get('cluster')).toBe('a b&c');
  });
});

// Kaynak pini: tek tıklanabilir kart gerçek bir link — div onClick'e
// ya da elle cursor'a geri dönmesin.
describe('Clusters — cluster kartı CardLink', () => {
  const src = readFileSync(resolve(__dirname, '../Clusters.tsx'), 'utf8');
  it('kart CardLink + clusterSearch + replace', () => {
    expect(src).toMatch(/<CardLink key=\{name\}\s+to=\{\{ search: clusterSearch\(params, name\) \}\}\s+replace/);
  });
  it('eski div onClick yolu yok', () => {
    expect(src).not.toMatch(/<Card key=\{name\}[\s\S]{0,40}onClick/);
    expect(src).not.toContain('openCluster');
  });
});
