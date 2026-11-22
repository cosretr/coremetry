import { describe, expect, it } from 'vitest';
import { reduceGenerations, genPodChoices, type GenPodSeries } from './runtimeGenReduce';

const pts = (...v: (number | null)[]) => v.map((value, i) => ({ time: i * 60, value }));
// Üç pod × iki nesil; pod-c toplamı en yüksek (en kötü).
const FIX: GenPodSeries[] = [
  { pod: 'pod-a', gen: 'gen0', points: pts(1, 2, 3) },
  { pod: 'pod-a', gen: 'gen1', points: pts(1, 1, 1) },
  { pod: 'pod-b', gen: 'gen0', points: pts(2, 3, 4) },
  { pod: 'pod-b', gen: 'gen1', points: pts(1, null, 1) },
  { pod: 'pod-c', gen: 'gen0', points: pts(9, 9, 9) },
  { pod: 'pod-c', gen: 'gen1', points: pts(5, 5, 5) },
];

describe('reduceGenerations', () => {
  it('varsayılan: nesil başına pod ortancası, nesil sayısı kadar çizgi', () => {
    const r = reduceGenerations(FIX, { medianSuffix: 'median' });
    expect(r.reduced).toBe(true);
    expect(r.total).toBe(3);
    expect(r.lines.map(l => l.label)).toEqual(['gen0 · median', 'gen1 · median']);
    expect(r.lines[0].points.map(p => p.value)).toEqual([2, 3, 4]);
    // t=60'ta pod-b gen1 boş → ortanca iki değerden (1, 5) = 3.
    expect(r.lines[1].points.map(p => p.value)).toEqual([1, 3, 1]);
    expect(r.lines.every(l => l.role === 'median')).toBe(true);
  });

  it('pod sırası nesil toplamı tepesine göre — ilk = en kötü', () => {
    expect(reduceGenerations(FIX).pods).toEqual(['pod-c', 'pod-b', 'pod-a']);
  });

  it('pod seçimi: o pod\'un nesil çizgileri, adları nesil', () => {
    const r = reduceGenerations(FIX, { pod: 'pod-b' });
    expect(r.selected).toBe('pod-b');
    expect(r.reduced).toBe(false);
    expect(r.lines.map(l => l.label)).toEqual(['gen0', 'gen1']);
    expect(r.lines[0].points.map(p => p.value)).toEqual([2, 3, 4]);
  });

  it('bilinmeyen pod seçimi yok sayılır → medyan', () => {
    const r = reduceGenerations(FIX, { pod: 'gone' });
    expect(r.selected).toBeNull();
    expect(r.reduced).toBe(true);
  });

  it('Tüm pod\'lar: pod × nesil, seçim yok sayılır', () => {
    const r = reduceGenerations(FIX, { all: true, pod: 'pod-a' });
    expect(r.selected).toBeNull();
    expect(r.lines).toHaveLength(6);
    expect(r.lines[0].label).toBe('pod-c · gen0');
  });

  it('1–2 pod indirgenmez; tek pod etiketsiz nesil adı', () => {
    const one = reduceGenerations(FIX.filter(s => s.pod === 'pod-a'));
    expect(one.reduced).toBe(false);
    expect(one.lines.map(l => l.label)).toEqual(['gen0', 'gen1']);
    const two = reduceGenerations(FIX.filter(s => s.pod !== 'pod-a'));
    expect(two.lines.map(l => l.label)).toEqual(['pod-c · gen0', 'pod-c · gen1', 'pod-b · gen0', 'pod-b · gen1']);
  });

  it('nesil sırası doğal (gen2 < gen10)', () => {
    const r = reduceGenerations([
      { pod: 'p', gen: 'gen10', points: pts(1) },
      { pod: 'p', gen: 'gen2', points: pts(1) },
    ]);
    expect(r.lines.map(l => l.label)).toEqual(['gen2', 'gen10']);
  });

  it('değersiz pod sayılmaz', () => {
    const r = reduceGenerations([...FIX, { pod: 'empty', gen: 'gen0', points: pts(null) }]);
    expect(r.total).toBe(3);
    expect(r.pods).not.toContain('empty');
  });
});

describe('genPodChoices', () => {
  const pods = Array.from({ length: 14 }, (_, i) => `p${i}`);
  it('en çok 10, en kötüden', () => {
    expect(genPodChoices(pods, null)).toEqual(pods.slice(0, 10));
  });
  it('listede olmayan geçerli seçim sona eklenir; geçersiz eklenmez', () => {
    expect(genPodChoices(pods, 'p12').at(-1)).toBe('p12');
    expect(genPodChoices(pods, 'zz')).toHaveLength(10);
  });
});
