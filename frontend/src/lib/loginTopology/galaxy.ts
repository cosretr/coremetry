// galaxy — v0.10.1928 (operatör: "veriye bağlı galaksi en iyisi"; mockup
// "Login Galaksi" #galaksi). SAF galaksi çekirdeği: DOM / canvas yok.
//
// Topoloji bir sarmal galaksi olarak çizilir ve VERİYE bağlıdır:
//   - Her gateway (giriş) bir KOL; BFF'ler ve servisler kendi gateway'lerinin
//     kolu boyunca (çizgeden çok kaynaklı BFS ile en yakın gateway). Gateway
//     yoksa en ağır düğümler kol başı olur (en az MIN_ARMS kol).
//   - Düğüm boyutu ve parlaklığı = trafik (ağırlık); boyut geniş aralıkta
//     (0,45×–1,95×) — operatör: "gezegenlerin bir kısmı küçük bir kısmı büyük".
//   - Dönüş ve akan noktaların hızı = toplam throughput (`pace`, 0..1).
//   - Disk eğik (TILT0); dönüş + eğim izdüşümü derinlik verir (öndekiler büyük
//     ve parlak). Sürükleme bütün galaksiyi BİRLİKTE döndürür / eğer.
//   - Kolların ve çekirdeğin çevresinde yıldız tozu (yalnız nokta; ad / kenar yok).
// Aynı (düğüm sırası, kenarlar, tohum) → aynı galaksi.
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';
import { mulberry32 } from './prng';

const TAU = Math.PI * 2;

export const MIN_ARMS = 3;
export const MAX_ARMS = 8;
export const TWIST = 2.3;
export const DUST_ARM = 4200;
export const DUST_CORE = 900;
export const FLOW_EDGES = 48;
/** Varsayılan eğim (~62°) ve sürüklemede izin verilen aralık (radyan). */
export const TILT0 = 1.08;
export const TILT_MIN = 0.5;
export const TILT_MAX = 1.35;
/** Gateway / BFF / veri / servis taban yarıçapı (css px). */
export const BASE_R: Readonly<Record<string, number>> = { gw: 6.5, bff: 4, db: 3.4, queue: 3.4, svc: 2.4 };

/**
 * v0.10.1934 (operatör: 360 düğüm) — çizilen düğüm DENSE_NODES'u aşınca noktalar
 * %15 küçülür: kollar dolar ama "iri ve seyrek" görünüm (1927 şikâyeti) geri gelmez.
 */
export const DENSE_NODES = 180;
export const DENSE_SIZE_K = 0.85;

/** Veri katmanı (veritabanı ya da kuyruk). */
export const isBackendTier = (tier: string) => tier === 'db' || tier === 'queue';
/** Veri katmanında sabit etiket tavanı (v0.10.1931). */
export const MAX_BACKEND_LABELS = 12;
/** Üzerine gelinen düğümün büyüme çarpanı. */
export const FOCUS_SCALE = 1.5;

export interface GNode {
  id: string;
  tier: LoginSummaryNode['tier'];
  weight: number;
  name?: string;
  arm: number;
  /** Disk düzleminde kutupsal konum (R biriminde) ve disk kalınlığı. */
  r: number; th: number; h: number;
  /** Salınım evresi. */
  ph: number;
  /** Boyut çarpanı (trafik + düğüme özgü küçük sapma). */
  size: number;
}
export interface GEdge { a: number; b: number; w: number }
export interface Galaxy {
  nodes: GNode[];
  edges: GEdge[];
  arms: number;
  /** Toz: parçacık başına 5 değer (r, th, h, parlaklık, pırıltı evresi). */
  dust: Float32Array;
  /** Akan nokta çizilecek kenarlar (ağırlığa göre ilk FLOW_EDGES) ve evreleri. */
  flows: number[];
  flowPh: number[];
  adj: number[][];
  /** v0.10.1937 — adsız servis yıldızları: yıldız başına 5 değer (r, th, h, yoğunluk, pırıltı evresi). */
  stars: Float32Array;
  /** Yoğun grafikte nokta küçültmesi (DENSE_SIZE_K ya da 1). */
  sizeK: number;
  /** v0.10.1947 — yıldız başına ad (yalnız isimli kipte; yoksa undefined); `stars` ile aynı sıra. */
  starNames: (string | undefined)[];
  /** v0.10.1973 — yıldızın bağlı olduğu düğüm (`nodes` dizini; -1 = yok); `stars` ile aynı sıra. */
  starNear: number[];
}

/** Adsız servis yıldızı girdisi: yoğunluk ve en yakın düğümün `nodes` dizini (-1 = yok). */
export interface StarIn { w: number; near: number; name?: string }
/** v0.10.1937 — istemci tavanı (sunucu da 5000) ve yıldızlar yer kaplarken korunacak en az toz. */
export const MAX_STARS = 5000;
export const MIN_DUST = 1000;

const clamp01 = (v: number) => (Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0);

/** Boyut çarpanı: trafik geniş aralığa yayılır (0,45×–1,95×), `jitter` ∈ [0,1) düğüme özgü sapma (±%15). */
export function sizeOf(weight: number, jitter: number): number {
  return (0.45 + 1.5 * Math.pow(clamp01(weight), 0.9)) * (0.85 + 0.3 * jitter);
}

export function buildGalaxy(
  nodes: readonly LoginSummaryNode[], edges: readonly LoginSummaryEdge[], seed: number,
  dustArm = DUST_ARM, dustCore = DUST_CORE, starsIn: readonly StarIn[] = [],
): Galaxy {
  const rand = mulberry32(seed);
  const gauss = () => (rand() + rand() + rand() - 1.5) / 1.5;
  const n = nodes.length;
  const index = new Map<string, number>();
  nodes.forEach((nd, i) => index.set(nd.id, i));
  const gedges: GEdge[] = [];
  const adj: number[][] = nodes.map(() => []);
  for (const e of edges) {
    const a = index.get(e.from), b = index.get(e.to);
    if (a == null || b == null || a === b) continue;
    gedges.push({ a, b, w: clamp01(e.weight) });
    adj[a].push(b); adj[b].push(a);
  }

  // Kol başları: gateway'ler (ağırlığa göre); yetmezse en ağır diğer düğümler.
  const byWeight = nodes.map((nd, i) => ({ i, w: clamp01(nd.weight) + adj[i].length * 0.001 }))
    .sort((a, b) => b.w - a.w || a.i - b.i);
  let hubs = byWeight.filter(x => nodes[x.i].tier === 'gw').map(x => x.i).slice(0, MAX_ARMS);
  for (const x of byWeight) {
    if (hubs.length >= Math.min(MIN_ARMS, n)) break;
    if (!hubs.includes(x.i) && !isBackendTier(nodes[x.i].tier)) hubs.push(x.i);
  }
  if (hubs.length === 0 && n > 0) hubs = [byWeight[0].i];
  const arms = Math.max(1, hubs.length);

  // Çok kaynaklı BFS: her düğüm en yakın kol başının kolunda; hop = uzaklık.
  const arm = new Array<number>(n).fill(-1);
  const hop = new Array<number>(n).fill(0);
  const branch = new Array<number>(n).fill(-1); // kol başının hangi doğrudan komşusundan (dal) gelindi
  const queue: number[] = [];
  hubs.forEach((hIdx, k) => { arm[hIdx] = k; queue.push(hIdx); });
  for (let q = 0; q < queue.length; q++) {
    const u = queue[q];
    for (const v of adj[u]) if (arm[v] < 0) {
      arm[v] = arm[u]; hop[v] = hop[u] + 1; branch[v] = hop[u] === 0 ? v : branch[u]; queue.push(v);
    }
  }
  rebalanceArms(arm, branch, arms);
  const hubSet = new Set(hubs);
  const sizeK = n > DENSE_NODES ? DENSE_SIZE_K : 1;

  const gnodes: GNode[] = nodes.map((nd, i) => {
    const a = arm[i] >= 0 ? arm[i] : i % arms; // bağlantısız: dizine göre kol
    const tier = nd.tier;
    let t: number;
    if (hubSet.has(i)) t = 0.02 + rand() * 0.05;
    else if (tier === 'gw') t = 0.08 + rand() * 0.06;
    else if (tier === 'bff') t = 0.14 + rand() * 0.18;
    else if (isBackendTier(tier)) t = 0.55 + rand() * 0.4;
    else t = Math.min(0.98, 0.3 + Math.min(hop[i], 4) * 0.06 + Math.pow(rand(), 0.85) * 0.45);
    const r = 0.05 + t * 0.95;
    return {
      id: nd.id, tier, weight: clamp01(nd.weight), name: nd.name, arm: a,
      r, th: (a / arms) * TAU + Math.log(0.18 + r) * TWIST + gauss() * (0.12 + t * 0.1),
      h: gauss() * 0.03 * (1 - t * 0.5), ph: rand() * TAU, size: sizeOf(nd.weight, rand()) * sizeK,
    };
  });

  // v0.10.1937 — gerçek servis yıldızları sentetik tozun yerini alır: toplam nokta ~aynı (en az MIN_DUST toz).
  const nStars = Math.min(MAX_STARS, starsIn.length);
  const dustTotal = dustArm + dustCore;
  if (nStars > 0 && dustTotal > 0) {
    const keep = Math.max(Math.min(MIN_DUST, dustTotal), dustTotal - nStars) / dustTotal;
    dustArm = Math.round(dustArm * keep); dustCore = Math.round(dustCore * keep);
  }
  // Toz: kol çizgisine yakın çoğunluk (sarmal belirgin) + çekirdekte yuvarlak bulut.
  const dust = new Float32Array((dustArm + dustCore) * 5);
  let o = 0;
  for (let k = 0; k < dustArm; k++) {
    const r = Math.pow(rand(), 0.75) * 1.2;
    const sp = (rand() < 0.7 ? 0.13 : 0.4) + r * 0.18;
    dust[o++] = r;
    dust[o++] = ((k % arms) / arms) * TAU + Math.log(0.18 + r) * TWIST + gauss() * sp;
    dust[o++] = gauss() * 0.05; dust[o++] = 0.3 + rand() * 0.7; dust[o++] = rand() * TAU;
  }
  for (let k = 0; k < dustCore; k++) {
    dust[o++] = Math.abs(gauss()) * 0.22; dust[o++] = rand() * TAU;
    dust[o++] = gauss() * 0.06; dust[o++] = 0.45 + rand() * 0.55; dust[o++] = rand() * TAU;
  }

  const flows = gedges.map((e, i) => ({ i, w: e.w })).sort((x, y) => y.w - x.w || x.i - y.i)
    .slice(0, FLOW_EDGES).map(x => x.i);
  const flowPh = gedges.map(() => rand());
  // Adsız servis yıldızları: bağlı olduğu düğümün kolunda, ona yakın yarıçapta; bağsızsa rastgele kol.
  const stars = new Float32Array(nStars * 5);
  const starNames: (string | undefined)[] = [];
  const starNear: number[] = [];
  let so = 0;
  for (let k = 0; k < nStars; k++) {
    const st = starsIn[k], base = st.near >= 0 ? gnodes[st.near] : undefined;
    let a: number, r: number;
    if (base) { a = base.arm; r = Math.min(1.18, Math.max(0.08, base.r + gauss() * 0.07)); }
    else { a = Math.floor(rand() * arms); r = 0.05 + (0.3 + Math.pow(rand(), 0.85) * 0.68) * 0.95; }
    stars[so++] = r;
    stars[so++] = (a / arms) * TAU + Math.log(0.18 + r) * TWIST + gauss() * (0.12 + r * 0.12);
    stars[so++] = gauss() * 0.035; stars[so++] = clamp01(st.w); stars[so++] = rand() * TAU;
    starNames.push(st.name || undefined);
    starNear.push(base ? st.near : -1);
  }
  return { nodes: gnodes, edges: gedges, arms, dust, flows, flowPh, adj, stars, sizeK, starNames, starNear };
}

export interface View { angle: number; tilt: number; cx: number; cy: number; R: number }
export interface Pt { x: number; y: number; z: number }

/** Disk düzlemi (kutupsal, R biriminde) → ekran: dönüş + eğim. z ∈ [-1,1], >0 izleyiciye yakın. */
export function project(v: View, r: number, th: number, h: number): Pt {
  const a = th + v.angle;
  const x = r * Math.cos(a), y0 = r * Math.sin(a);
  const ct = Math.cos(v.tilt), st = Math.sin(v.tilt);
  return { x: v.cx + x * v.R, y: v.cy + (y0 * ct - h * st) * v.R, z: y0 * st + h * ct };
}

/** Derinlik ∈ [0,1] (0 arka, 1 ön). */
export const depthOf = (z: number) => Math.min(1, Math.max(0, (z + 1) / 2));

export function nodeRadius(nd: GNode, depth: number, focus: boolean): number {
  return (BASE_R[nd.tier] ?? BASE_R.svc) * (0.55 + 0.6 * depth) * nd.size * (focus ? FOCUS_SCALE : 1);
}

/** Opaklık: derinlik × trafik (sakin servis soluk); odakta tam. */
export function nodeAlpha(nd: GNode, depth: number, focus: boolean): number {
  if (focus) return 1;
  return (0.2 + 0.7 * depth) * (0.55 + 0.45 * (0.35 + 0.65 * nd.weight));
}

/**
 * Etiket kuralı (isimli kip): gateway önde (derinlik > 0,35), BFF yakın önde
 * (> 0,62), servis yalnız RASTGELE etiket kümesindeyse (v0.10.1918) ve önde
 * (> 0,72). Veri katmanı (db/kuyruk) v0.10.1931'den beri kendi adıyla
 * (veritabanı adı / konu) etiketlenir, ama yalnız `pickBackendLabels`
 * kümesindeyse — her ad BİR kez (v0.10.1926'daki tekrar şikâyeti) — ve önde (> 0,45).
 */
export function wantsLabel(nd: GNode, depth: number, inLabelSet: boolean): boolean {
  if (!nd.name) return false;
  if (isBackendTier(nd.tier)) return inLabelSet && depth > 0.45;
  if (nd.tier === 'gw') return depth > 0.35;
  if (nd.tier === 'bff') return depth > 0.62;
  return inLabelSet && depth > 0.72;
}

/** Rastgele etiket kümesi: adı olan servislerden en çok `max` (Fisher–Yates; her açılışta farklı). */
export function pickLabelSet(g: Galaxy, max: number, rand: () => number): Set<number> {
  const idx = g.nodes.map((nd, i) => ({ nd, i })).filter(x => x.nd.tier === 'svc' && !!x.nd.name).map(x => x.i);
  for (let k = idx.length - 1; k > 0; k--) {
    const j = Math.floor(rand() * (k + 1));
    [idx[k], idx[j]] = [idx[j], idx[k]];
  }
  return new Set(idx.slice(0, max));
}

/**
 * SAF, yerinde (v0.10.1944, operatör: prod'da galaksi tek kola yığılıyordu — bir
 * gateway servislerin çoğunu topluyor). Kol başları kendi kolunda kalır; her DAL
 * (kol başının doğrudan komşusu + BFS'te onun altından ulaşılanlar) büyükten
 * küçüğe en az yüklü kola atanır. branch[i] < 0 olan düğüm (kol başı / bağsız)
 * yerinde kalır. Kol yükleri döner (test için).
 */
export function rebalanceArms(arm: number[], branch: readonly number[], arms: number): number[] {
  const load = new Array<number>(arms).fill(0);
  const size = new Map<number, number>();
  for (let i = 0; i < arm.length; i++) {
    if (branch[i] >= 0) size.set(branch[i], (size.get(branch[i]) ?? 0) + 1);
    else if (arm[i] >= 0 && arm[i] < arms) load[arm[i]]++;
  }
  const order = [...size.entries()].sort((a, b) => b[1] - a[1] || a[0] - b[0]);
  const to = new Map<number, number>();
  for (const [br, sz] of order) {
    let k = 0;
    for (let j = 1; j < arms; j++) if (load[j] < load[k]) k = j;
    to.set(br, k); load[k] += sz;
  }
  for (let i = 0; i < arm.length; i++) if (branch[i] >= 0) arm[i] = to.get(branch[i]) ?? arm[i];
  return load;
}

/**
 * SAF (v0.10.1945, operatör: kollar arası çizgiler ekranı çaprazlamasına
 * kesiyordu). Bağ çizgisi yalnız iki ucu AYNI koldaysa ya da uçlardan biri
 * odaktaysa çizilir; akan noktalar her bağda sürer.
 */
export function edgeVisible(armA: number, armB: number, focus: boolean): boolean {
  return focus || armA === armB;
}

/** Ekran kutusu (px): sol, sağ, üst, alt. */
export interface Box { l: number; r: number; t: number; b: number }
/** Etiket adayı: düğüm dizini, ekran konumu, metin genişliği (px), katman, ağırlık. */
export interface LabelCand { i: number; x: number; y: number; w: number; tier: string; weight: number }

const LABEL_PRI: Readonly<Record<string, number>> = { gw: 0, bff: 1, db: 2, queue: 2, svc: 3 };
/** Kartın çevresinde etiket konmayan pay (px) ve etiketler arası boşluk. */
export const LABEL_CARD_MARGIN = 10;

/** Etiket kutusu: metin noktanın sağında (x+8), taban çizgisi y+3. */
export const labelBox = (c: LabelCand): Box => ({ l: c.x + 6, r: c.x + 10 + c.w, t: c.y - 9, b: c.y + 6 });

/**
 * SAF (v0.10.1943, operatör: prod'da etiketler kartın altına giriyor ve üst üste
 * biniyordu). Öncelik: gateway > BFF > veri > servis, eşitlikte ağırlık. Kartın
 * (avoid, LABEL_CARD_MARGIN payıyla) altına giren ya da yerleşmiş bir etikete
 * binen aday çizilmez. Çizilecek düğüm dizinlerini öncelik sırasıyla döner.
 */
export function placeLabels(cands: readonly LabelCand[], avoid: Box | null): number[] {
  const sorted = [...cands].sort((a, b) => (LABEL_PRI[a.tier] ?? 9) - (LABEL_PRI[b.tier] ?? 9) || b.weight - a.weight || a.i - b.i);
  const placed: Box[] = [];
  const out: number[] = [];
  const M = LABEL_CARD_MARGIN;
  for (const c of sorted) {
    const bx = labelBox(c);
    if (avoid && bx.r > avoid.l - M && bx.l < avoid.r + M && bx.b > avoid.t - M && bx.t < avoid.b + M) continue;
    if (placed.some(q => bx.l < q.r + 4 && bx.r > q.l - 4 && bx.t < q.b + 2 && bx.b > q.t - 2)) continue;
    placed.push(bx); out.push(c.i);
  }
  return out;
}

/**
 * Veri katmanı etiket kümesi (v0.10.1931): her ad için yalnız EN AĞIR düğüm;
 * genel yedek adlar ("db", "queue") hiç etiket almaz; en çok `max`, ağırlığa göre.
 */
export function pickBackendLabels(g: Galaxy, max: number = MAX_BACKEND_LABELS): Set<number> {
  const best = new Map<string, number>();
  g.nodes.forEach((nd, i) => {
    if (!isBackendTier(nd.tier) || !nd.name || nd.name === 'db' || nd.name === 'queue') return;
    const cur = best.get(nd.name);
    if (cur == null || g.nodes[cur].weight < nd.weight) best.set(nd.name, i);
  });
  return new Set([...best.values()].sort((a, b) => g.nodes[b].weight - g.nodes[a].weight || a - b).slice(0, max));
}

/**
 * Throughput temposu ∈ [0,1]: son değer / 24 saatin tepesi (yoğun saatte 1,
 * gece düşük). Veri yoksa 0,6 (orta tempo).
 */
export function paceFrom(values: readonly number[] | undefined): number {
  const v = (values ?? []).filter(x => Number.isFinite(x) && x >= 0);
  if (v.length < 2) return 0.6;
  const max = Math.max(...v);
  return max > 0 ? clamp01(v[v.length - 1] / max) : 0.6;
}

/** Kendiliğinden dönüş hızı (rad/sn): tempo arttıkça hızlanır (0,008–0,024). */
export const spinRate = (pace: number) => 0.016 * (0.5 + clamp01(pace));

/** Akan noktanın hızı (tur/sn): kenar ağırlığı × tempo. */
export const flowSpeed = (edgeW: number, pace: number) => (0.04 + 0.08 * clamp01(edgeW)) * (0.6 + clamp01(pace));

/** Kenar boyunca akan noktanın [0,1) konumu. */
export function flowPos(ph: number, tSec: number, edgeW: number, pace: number): number {
  const p = ph + tSec * flowSpeed(edgeW, pace);
  return p - Math.floor(p);
}

/** Zemin rengi koyu mu (`#rgb`, `#rrggbb`, `rgb()/rgba()`)? Parıltı gücü buna göre. Ayrıştırılamazsa koyu. */
export function isDarkColor(color: string): boolean {
  const c = color.trim();
  let r = 0, g = 0, b = 0;
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(c);
  if (hex) {
    const v = hex[1].length === 3 ? hex[1].split('').map(x => x + x).join('') : hex[1];
    r = parseInt(v.slice(0, 2), 16); g = parseInt(v.slice(2, 4), 16); b = parseInt(v.slice(4, 6), 16);
  } else {
    const m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i.exec(c);
    if (!m) return true;
    r = +m[1]; g = +m[2]; b = +m[3];
  }
  return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255 < 0.45;
}
