// synthetic — v0.10.1909. Ayar açık ama canlı veri yokken (özet kapalı/hatalı/
// hazır değil) arka planda çizilen SENTETİK graf. Gerçek bir sisteme dair hiçbir
// şey söylemez: ad yok, durum yok; yalnız hiyerarşik şekil (giriş → BFF → servis
// → veri). Tohumlu: aynı tohum → aynı graf.
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';
import { mulberry32 } from './prng';

export const SYNTHETIC_SEED = 0x5eed1909;

export interface SyntheticGraph { nodes: LoginSummaryNode[]; edges: LoginSummaryEdge[] }

const r2 = (v: number) => Math.round(v * 100) / 100;

export function syntheticGraph(seed: number = SYNTHETIC_SEED): SyntheticGraph {
  const rand = mulberry32(seed);
  const nodes: LoginSummaryNode[] = [];
  const edges: LoginSummaryEdge[] = [];
  const add = (tier: LoginSummaryNode['tier'], weight: number): number => {
    nodes.push({ id: `s${nodes.length.toString(16).padStart(3, '0')}`, tier, weight: r2(weight) });
    return nodes.length - 1;
  };
  const link = (a: number, b: number, w: number) => edges.push({ from: nodes[a].id, to: nodes[b].id, weight: r2(w) });

  const GW = 6;
  const gws = Array.from({ length: GW }, () => add('gw', 0.7 + rand() * 0.3));
  // v0.10.1931: veri katmanı = 4 veritabanı + 2 kuyruk (ayrı çizilir)
  const dbs = Array.from({ length: 6 }, (_, k) => add(k < 4 ? 'db' : 'queue', 0.3 + rand() * 0.4));
  const bffs: number[] = [];
  for (const g of gws) {
    const nb = 2 + Math.floor(rand() * 2);
    for (let i = 0; i < nb; i++) {
      const b = add('bff', 0.4 + rand() * 0.4);
      bffs.push(b);
      link(g, b, 0.6 + rand() * 0.4);
      const ns = 4 + Math.floor(rand() * 5);
      for (let j = 0; j < ns; j++) {
        const s = add('svc', 0.1 + rand() * 0.5);
        link(b, s, 0.3 + rand() * 0.5);
        if (rand() < 0.55) link(s, dbs[Math.floor(rand() * dbs.length)], 0.2 + rand() * 0.5);
        if (rand() < 0.3) { // yaprak
          const leaf = add('svc', 0.05 + rand() * 0.15);
          link(s, leaf, 0.15 + rand() * 0.25);
        }
      }
    }
  }
  // gateway'ler arası ve BFF'ler arası birkaç çapraz bağ (ağ görünümü)
  for (let i = 0; i < bffs.length; i += 3) link(bffs[i], bffs[(i + 5) % bffs.length], 0.2 + rand() * 0.2);
  return { nodes, edges };
}
