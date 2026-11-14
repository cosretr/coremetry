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
// Clusters S4 (v0.10.2169) — dondurulmuş ui/CardLink yerine aynı işaretleme
// elle: `<Link className="card card-link …">`; hedef, replace ve "gerçek link"
// sözleşmesi aynı.
describe('Clusters — cluster kartı gerçek link', () => {
  const src = readFileSync(resolve(__dirname, '../Clusters.tsx'), 'utf8');
  it('kart <Link> + .card-link + clusterSearch + replace', () => {
    expect(src).toContain('const detail = { search: clusterSearch(params, name) };');
    expect(src).toMatch(/<Link key=\{name\} to=\{detail\} replace\s+className=\{state === 'critical' \? 'card card-link cl-card cl-card--crit' : 'card card-link cl-card'\}>/);
  });
  it('eski div onClick yolu yok', () => {
    expect(src).not.toMatch(/<(Card|div) key=\{name\}[\s\S]{0,80}onClick/);
    expect(src).not.toContain('openCluster');
  });
  it('yanıt vermeyen kart tıklanabilir görünmez; içindeki iki bağlantı gerçek link', () => {
    expect(src).toContain('<div key={name} className="card cl-card">');
    expect(src).toContain('<Link className="cl-card__name" to={detail} replace>{nameEl}</Link>');
    expect(src).toContain('<Link className="cl-card__action" to="/settings/clusters">Bağlantı ayarını aç</Link>');
  });
});
