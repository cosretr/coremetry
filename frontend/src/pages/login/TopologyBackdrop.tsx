// TopologyBackdrop — v0.10.1909 (Login topoloji arka planı, audit §6.3).
// v0.10.1928 (operatör: "veriye bağlı galaksi en iyisi"; mockup "Login Galaksi"):
// topoloji kartın ARKASINDA bir sarmal GALAKSİ olarak çizilir (lib/loginTopology/
// galaxy): her gateway bir kol, BFF ve servisler o kol boyunca; boyut/parlaklık =
// trafik, dönüş ve akan noktaların hızı = toplam throughput (`pace`). Sürükleme
// bütün galaksiyi BİRLİKTE döndürür (yatay) ve eğer (dikey), bırakınca yavaşça
// durur; çift tık başa alır. Üzerine gelince düğüm büyür, bağlantıları parlar,
// adı (isimli kipte) görünür. Kolların çevresinde yıldız tozu.
//
// Durum/uyarı rengi YOK; renkler yalnız tema token'ları (`resolveVar`): yıldız
// v0.10.1933: etiketler zemin renginde (--bg0) ince çerçeveyle çizilir (toz üstünde okunur).
// v0.10.1938 (operatör: "açılış efekti olmasın, direkt açılsın"): 1936'nın açılış hareketi söküldü.
// v0.10.1949 (operatör son kararı: "kart çıkmasına gerek yok, sadece ismi yazsa olur") — üzerine gelince
// YALNIZ ad (zemin çerçeveli etiket); 1946'nın kartı ve 1935'in tür satırı söküldü.
// v0.10.1947: tüm servisler düğüm — servis yıldızları biraz daha belirgin; isimli kipte üzerine gelince
// büyür ve adı yazar (bağlantılı düğüm isabette önce gelir).
// v0.10.1945: kollar arası bağ çizgisi yalnız odakta (edgeVisible); akan noktalar sürer.
// v0.10.1943: etiketler çakışmaz ve kartın altına girmez (placeLabels; kart kutusu `avoidRef`).
// --accent2, kol/veri --purple, akan noktalar --orange (Traces süre çizgisiyle AYNI token; v0.10.1932),
// etiket --text2. v0.10.1931: veritabanı mor YUVARLAK nokta + hafif hale (kare değil),
// kuyruk --teal İÇİ BOŞ halka; ikisi de adıyla (veritabanı adı / konu), her ad bir kez. Parıltı gücü zeminin koyuluğuna göre (koyu temada güçlü).
// Performans: 30 fps tavanı, DPR ≤ 2, `document.hidden`'da rAF durur,
// `prefers-reduced-motion`'da kendiliğinden hareket yok (sürükleme çalışır).
// Galaksi ilk gelen grafikle BİR KEZ kurulur. Dekoratif: aria-hidden; olay alır
// (sürükleme) — kart ve üst çubuk üstte olduğundan yalnız kart DIŞINDA.
import { useEffect, useRef, useState, type RefObject } from 'react';
import { chartMonoFont, resolveVar } from '@/lib/chart/resolveVar';
import { useThemeTick } from '@/lib/useThemeTick';
import { MAX_LABELS, shouldDraw, thinGraph } from '@/lib/loginTopology/frame';
import {
  buildGalaxy, depthOf, edgeVisible, flowPos, isDarkColor, placeLabels, type Box, type LabelCand, nodeAlpha, nodeRadius, pickBackendLabels, pickLabelSet, project, spinRate,
  TILT0, TILT_MAX, TILT_MIN, wantsLabel, type Galaxy, type Pt, type View,
} from '@/lib/loginTopology/galaxy';
import { seedOfIds } from '@/lib/loginTopology/prng';
import { bigBangWarp, BH_TOTAL, NO_WARP, type Warp } from '@/lib/loginTopology/eggs'; // v0.10.1977
import { burstSize, nextStreak, orbitProgress, ORBIT_CLICKS, ORBIT_TARGETS, pruneSparks, sparkProgress, sparkTargets, spawnSparks, type Spark, type Streak } from '@/lib/loginTopology/sparks'; // v0.10.1973 / 1978
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';

export interface BackdropGraph {
  nodes: LoginSummaryNode[]; edges: LoginSummaryEdge[]; seed?: number; stars?: [number, number][];
  star_names?: string[];
}

interface Colors { star: string; arm: string; mq: string; flow: string; otel: string; label: string; bg: string; glow: number; flowBoost: number }

function readColors(): Colors {
  const bg = resolveVar('var(--bg0)');
  const dark = isDarkColor(bg);
  return {
    bg,
    star: resolveVar('var(--accent2)'), arm: resolveVar('var(--purple)'), mq: resolveVar('var(--teal)'),
    // v0.10.1929 (operatör-raporlu: kurumsal temada akan noktalar altın değil) — --warn açık temalarda
    // kahverengi metin tonu (#795600); galaksiye ayrı altın token'ı.
    // v0.10.1932 (operatör: "Traces sayfasındaki süre grafiğinin sarı tonunda değil") — ayrı altın token'ı
    // kalktı; akan noktalar Traces response-time çizgisinin token'ını okur (components/traces/volumeSeries.ts).
    flow: resolveVar('var(--orange)'), otel: resolveVar('var(--otel-blue)') || '#425cc7', label: resolveVar('var(--text2)'),
    glow: dark ? 0.9 : 0.35, flowBoost: dark ? 0.7 : 1.1,
  };
}

const TAU = Math.PI * 2;
const HOVER_PX = 14;

function halo(ctx: CanvasRenderingContext2D, x: number, y: number, r: number, color: string, a: number) {
  const g = ctx.createRadialGradient(x, y, 0, x, y, r);
  g.addColorStop(0, color); g.addColorStop(1, 'rgba(0,0,0,0)');
  ctx.globalAlpha = a; ctx.fillStyle = g;
  ctx.beginPath(); ctx.arc(x, y, r, 0, TAU); ctx.fill();
}

interface Frame {
  g: Galaxy; v: View; w: number; h: number; dpr: number; tSec: number; still: boolean;
  col: Colors; pace: number; hover: number; labels: ReadonlySet<number>;
  /** v0.10.1973 — tıkla gönderilen yıldızlar (kaynak/hedef `hover` kodlamasıyla). */
  sparks: readonly Spark[];
  /** v0.10.1977 — S R E: kara delik → big bang (yarıçap çarpanı, ek dönüş, delik, parlama). */
  warp: Warp;
  /** v0.10.1978 — 10 tık yörünge halkası: nokta (hover kodlaması) + başlangıç (sn); yoksa null. */
  orbit: { at: number; t0: number } | null;
  /** Etiket konmayacak kutu (login kartı), tuval koordinatında; yoksa null. */
  avoid: Box | null;
}

/** Etiket: zemin renginde ince çerçeve + metin (yıldız tozundan ayrışsın). */
function label(ctx: CanvasRenderingContext2D, text: string, x: number, y: number, col: Colors) {
  ctx.lineJoin = 'round'; ctx.lineWidth = 3.5; ctx.strokeStyle = col.bg;
  ctx.strokeText(text, x, y);
  ctx.fillStyle = col.label; ctx.fillText(text, x, y);
}

/** Bir kare: toz → kenarlar → akan noktalar → düğümler (arkadan öne) → etiketler. Düğüm konumlarını döner (isabet testi için). */
function drawFrame(ctx: CanvasRenderingContext2D, f: Frame): { pos: Pt[]; starPos: (Pt | null)[] } {
  const { g, col, hover } = f;
  // v0.10.1977 — her konum çarpıtmadan geçer (k: yarıçap, spin: ek açı); normalde k=1, spin=0.
  const v = f.v, W = f.warp;
  const P = W.k === 1 && W.spin === 0 ? (r: number, th: number, h: number) => project(v, r, th, h)
    : (r: number, th: number, h: number) => project(v, r * W.k, th + W.spin * (1 + (1.2 - Math.min(1.2, r))), h * W.k);
  ctx.setTransform(f.dpr, 0, 0, f.dpr, 0, 0);
  ctx.clearRect(0, 0, f.w, f.h);
  const tw = (ph: number, k = 1.3) => (f.still ? 1 : 0.7 + 0.3 * Math.sin(f.tSec * k + ph));

  // çekirdek parıltısı
  const core = P(0, 0, 0);
  halo(ctx, core.x, core.y, v.R * 0.5, col.star, 0.16 * col.glow);

  // yıldız tozu (1–1,5 px kareler: en ucuz çizim)
  ctx.fillStyle = col.star;
  const d = g.dust;
  for (let o = 0; o < d.length; o += 5) {
    const p = P(d[o], d[o + 1], d[o + 2]);
    if (p.x < -3 || p.y < -3 || p.x > f.w + 3 || p.y > f.h + 3) continue;
    ctx.globalAlpha = Math.max(0.04, (0.18 + 0.22 * p.z) * d[o + 3] * tw(d[o + 4]) * (0.6 + col.glow * 0.5));
    const s = 0.8 + 0.7 * depthOf(p.z);
    ctx.fillRect(p.x, p.y, s, s);
  }

  // v0.10.1937 — tüm servisler: adsız servis yıldızları; parlaklık ve boyut = yoğunluk (span sayısı)
  const sv = g.stars;
  const starPos: (Pt | null)[] = new Array(sv.length / 5).fill(null);
  const nN = g.nodes.length;
  for (let o = 0; o < sv.length; o += 5) {
    const p = P(sv[o], sv[o + 1], sv[o + 2]);
    if (p.x < -3 || p.y < -3 || p.x > f.w + 3 || p.y > f.h + 3) continue;
    const k = o / 5, focus = hover === nN + k;
    starPos[k] = p;
    const depth = depthOf(p.z), lit = 0.3 + 0.7 * sv[o + 3];
    // v0.10.1947 — her servis düğüm: biraz daha belirgin (×1,3 boy, ×1,15 opaklık), odakta ×2 ve tam opak
    ctx.globalAlpha = focus ? 1 : Math.min(1, (0.28 + 0.55 * lit) * (0.45 + 0.55 * depth) * (0.6 + col.glow * 0.5) * tw(sv[o + 4], 0.9) * 1.15);
    const s = (1 + 1.8 * sv[o + 3]) * g.sizeK * (0.8 + 0.4 * depth) * 1.3 * (focus ? 2 : 1);
    if (s < 1.6) { ctx.fillRect(p.x, p.y, s, s); continue; } // küçükler kare (ucuz)
    ctx.beginPath(); ctx.arc(p.x, p.y, s / 2 + 0.3, 0, TAU); ctx.fill();
  }

  // düğüm konumları (hafif nefes)
  const pos = g.nodes.map(nd => P(nd.r + (f.still ? 0 : Math.sin(f.tSec * 0.6 + nd.ph) * 0.004), nd.th, nd.h));

  // kenarlar
  ctx.strokeStyle = col.arm; ctx.lineWidth = 0.7;
  for (const e of g.edges) {
    const focus = hover >= 0 && (e.a === hover || e.b === hover);
    if (!edgeVisible(g.nodes[e.a].arm, g.nodes[e.b].arm, focus)) continue;
    ctx.globalAlpha = focus ? 0.7 : Math.max(0.03, 0.07 + 0.04 * (pos[e.a].z + pos[e.b].z));
    ctx.beginPath(); ctx.moveTo(pos[e.a].x, pos[e.a].y); ctx.lineTo(pos[e.b].x, pos[e.b].y); ctx.stroke();
  }

  // akan noktalar: altın yıldız (çekirdek + küçük hale), hız = kenar ağırlığı × tempo
  for (const k of g.flows) {
    const e = g.edges[k];
    const t = f.still ? 0.5 : flowPos(g.flowPh[k], f.tSec, e.w, f.pace);
    const x = pos[e.a].x + (pos[e.b].x - pos[e.a].x) * t, y = pos[e.a].y + (pos[e.b].y - pos[e.a].y) * t;
    const depth = depthOf((pos[e.a].z + pos[e.b].z) / 2);
    const sp = tw(g.flowPh[k] * 20, 3);
    const hr = 5 * (0.8 + 0.2 * col.flowBoost);
    halo(ctx, x, y, hr, col.flow, Math.min(1, (0.25 + 0.35 * depth) * sp * (0.5 + col.glow * 0.5) * col.flowBoost));
    ctx.globalAlpha = Math.min(1, (0.6 + 0.35 * depth) * sp * col.flowBoost); ctx.fillStyle = col.flow;
    ctx.beginPath(); ctx.arc(x, y, 1.4 * (0.85 + 0.15 * col.flowBoost), 0, TAU); ctx.fill();
  }

  // v0.10.1973 — tıkla gönderilen yıldızlar: kaynaktan bağlı noktaya, kısa kuyrukla, akan noktalarla aynı renk.
  for (const sp of f.sparks) {
    const u = sparkProgress(sp, f.tSec);
    if (u === null) continue;
    const a = sp.from < nN ? pos[sp.from] : starPos[sp.from - nN];
    const b = sp.to < nN ? pos[sp.to] : starPos[sp.to - nN];
    if (!a || !b) continue;
    for (let j = 3; j >= 0; j--) { // kuyruk: geride kalan 3 soluk nokta
      const t = Math.max(0, u - j * 0.035);
      const x = a.x + (b.x - a.x) * t, y = a.y + (b.y - a.y) * t;
      if (j === 0) halo(ctx, x, y, 7, col.flow, Math.min(1, 0.55 * col.flowBoost));
      ctx.globalAlpha = Math.min(1, (j === 0 ? 1 : 0.5 - j * 0.12) * col.flowBoost); ctx.fillStyle = col.flow;
      ctx.beginPath(); ctx.arc(x, y, j === 0 ? 2 : 1.4 - j * 0.25, 0, TAU); ctx.fill();
    }
  }

  // düğümler: arkadan öne
  const order = g.nodes.map((_, i) => i).sort((i, j) => pos[i].z - pos[j].z);
  for (const i of order) {
    const nd = g.nodes[i], p = pos[i], depth = depthOf(p.z), focus = i === hover;
    // v0.10.1983 (operatör: "yıldızların çevresinde gölge olmasın, bir tık daha küçük") — hale yok, %85 boy.
    const r = nodeRadius(nd, depth, focus) * 0.85, a = nodeAlpha(nd, depth, focus);
    if (nd.tier === 'queue') { // kuyruk: turkuaz halka
      ctx.globalAlpha = a; ctx.strokeStyle = col.mq; ctx.lineWidth = 1.4;
      ctx.beginPath(); ctx.arc(p.x, p.y, r * 1.15, 0, TAU); ctx.stroke(); continue;
    }
    if (nd.tier === 'db') { // veritabanı: mor yuvarlak nokta
      ctx.globalAlpha = a; ctx.fillStyle = col.arm;
      ctx.beginPath(); ctx.arc(p.x, p.y, r * 0.9, 0, TAU); ctx.fill(); continue;
    }
    ctx.globalAlpha = a; ctx.fillStyle = col.star;
    ctx.beginPath(); ctx.arc(p.x, p.y, r, 0, TAU); ctx.fill();
  }

  // etiketler (yalnız isimli kipte ad gelir)
  ctx.font = chartMonoFont(11);
  const cands: LabelCand[] = [];
  for (const i of order) {
    if (i === hover) continue;
    const nd = g.nodes[i], depth = depthOf(pos[i].z);
    if (!wantsLabel(nd, depth, f.labels.has(i))) continue;
    cands.push({ i, x: pos[i].x, y: pos[i].y, w: ctx.measureText(nd.name ?? '').width, tier: nd.tier, weight: nd.weight });
  }
  for (const i of placeLabels(cands, f.avoid)) {
    ctx.globalAlpha = Math.min(0.85, (depthOf(pos[i].z) - 0.3) * 1.3);
    label(ctx, g.nodes[i].name ?? '', pos[i].x + 8, pos[i].y + 3, col);
  }
  // v0.10.1949 — üzerine gelince yalnız ad (bağlantılı düğüm ya da servis yıldızı)
  const hp = hover >= 0 && hover < nN ? pos[hover] : hover >= nN ? starPos[hover - nN] : null;
  const hn = hover >= 0 && hover < nN ? g.nodes[hover].name : hover >= nN ? g.starNames[hover - nN] : undefined;
  if (hp && hn) {
    ctx.globalAlpha = 1; ctx.font = chartMonoFont(12);
    label(ctx, hn, hp.x + 9, hp.y + 4, col);
  }
  ctx.globalAlpha = 1;
  // v0.10.1978 — yörünge halkası: noktanın çevresinde dönen kesikli halka + üç uydu, Traces süre rengi (--orange).
  if (f.orbit) {
    const u = orbitProgress(f.orbit.t0, f.tSec);
    const c = f.orbit.at < nN ? pos[f.orbit.at] : starPos[f.orbit.at - nN];
    if (u !== null && c) {
      const fade = u < 0.15 ? u / 0.15 : u > 0.8 ? (1 - u) / 0.2 : 1;
      const rr = 10 + 12 * Math.min(1, u * 4);
      halo(ctx, c.x, c.y, rr * 1.8, col.flow, 0.35 * fade);
      ctx.globalAlpha = 0.9 * fade; ctx.strokeStyle = col.flow; ctx.lineWidth = 1.4;
      ctx.setLineDash([4, 3]); ctx.lineDashOffset = -f.tSec * 20;
      ctx.beginPath(); ctx.ellipse(c.x, c.y, rr, rr * 0.55, -0.35, 0, TAU); ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = col.otel; // uydular OTel mavisi (halka Traces süre rengi)
      for (let m = 0; m < 3; m++) {
        const a = f.tSec * 2.4 + (m * TAU) / 3;
        const ox = Math.cos(a) * rr, oy = Math.sin(a) * rr * 0.55, rot = -0.35;
        const x = c.x + ox * Math.cos(rot) - oy * Math.sin(rot), y = c.y + ox * Math.sin(rot) + oy * Math.cos(rot);
        ctx.beginPath(); ctx.arc(x, y, 2, 0, TAU); ctx.fill();
      }
    }
  }

  // v0.10.1977 — kara delik (koyu disk + turuncu yığılma halkası) ve big bang parlaması.
  if (W.hole > 0) {
    const r = v.R * 0.07 * W.hole;
    halo(ctx, core.x, core.y, r * 3.2, col.flow, 0.55 * W.hole);
    ctx.globalAlpha = W.hole; ctx.fillStyle = '#000';
    ctx.beginPath(); ctx.arc(core.x, core.y, r, 0, TAU); ctx.fill();
    ctx.globalAlpha = 0.8 * W.hole; ctx.strokeStyle = col.flow; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(core.x, core.y, r * 1.15, 0, TAU); ctx.stroke();
  }
  if (W.flash > 0) {
    halo(ctx, core.x, core.y, v.R * (0.15 + 1.4 * (1 - W.flash)), '#ffffff', W.flash);
    halo(ctx, core.x, core.y, v.R * (0.05 + 0.6 * (1 - W.flash)), col.flow, W.flash * 0.8);
  }
  ctx.globalAlpha = 1;
  return { pos, starPos };
}

export default function TopologyBackdrop({ graph, animated, pace = 0.6, avoidRef, bigBang = 0 }: {
  graph: BackdropGraph; animated: boolean;
  /** v0.10.1943 — etiketlerin girmeyeceği öğe (login kartı). */
  avoidRef?: RefObject<HTMLElement | null>;
  /** Toplam throughput temposu ∈ [0,1] (son / 24 sa tepe): dönüş ve akış hızı. */
  pace?: number;
  /** v0.10.1977 — her artışında kara delik → big bang (LoginPage, S R E). 0 = hiç. */
  bigBang?: number;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const bangRef = useRef<(() => void) | null>(null);
  useEffect(() => { if (bigBang > 0) bangRef.current?.(); }, [bigBang]);
  const themeTick = useThemeTick();
  // BİR KEZ: galaksi ilk gelen grafikle kurulur; `graph` sonradan değişse de yeniden kurulmaz.
  const [galaxy] = useState<Galaxy>(() => {
    const t = thinGraph(graph.nodes, graph.edges);
    // v0.10.1937 — yıldızın komşu dizini seyreltilmiş grafiğe çevrilir (düşen komşu → -1).
    const at = new Map(t.nodes.map((n, i) => [n.id, i]));
    const stars = (graph.stars ?? []).map(([w, near], k) => ({
      w, near: at.get(graph.nodes[near]?.id ?? '') ?? -1, name: graph.star_names?.[k] || undefined,
    }));
    return buildGalaxy(t.nodes, t.edges, graph.seed ?? seedOfIds(graph.nodes.map(n => n.id)), undefined, undefined, stars);
  });
  // v0.10.1918 — etiketlenecek servisler her açılışta RASTGELE.
  // v0.10.1931 — veri katmanı: her veritabanı/konu adı bir kez (en ağır düğümde).
  const [labels] = useState<Set<number>>(() => new Set([...pickLabelSet(galaxy, MAX_LABELS, Math.random), ...pickBackendLabels(galaxy)]));
  // Görünüm (açı/eğim/hız) efekt yeniden kurulunca (tema değişimi) korunur.
  const view = useRef({ angle: 0, tilt: TILT0, vel: 0, tiltVel: 0 });
  const paceRef = useRef(pace);
  useEffect(() => { paceRef.current = pace; }, [pace]);

  useEffect(() => {
    const canvas = ref.current;
    const ctx = canvas?.getContext('2d');
    if (!canvas || !ctx) return;
    const col = readColors(); // tema değişince (themeTick) efekt yeniden kurulur
    const st = view.current;
    let w = 0, h = 0, dpr = 1, R = 1;
    let raf = 0, last = 0, prev = performance.now();
    let pos: Pt[] = [];
    let starPos: (Pt | null)[] = [];
    let hover = -1, dragging = false, lx = 0, ly = 0, lt = 0;
    // v0.10.1973 — tık → yıldız: basış konumu/zamanı (sürüklemeden ayırmak için) + son tıklar (hız).
    let sparks: Spark[] = [], downX = 0, downY = 0, downT = 0;
    // v0.10.1977 — big bang başlangıcı (tSec cinsinden; -1 = yok). Nonce değişince tetiklenir.
    let bangT0 = -1;
    // v0.10.1978 — 10 tık: art arda tık sayacı + açık halka.
    let streak: Streak | null = null;
    let orbit: { at: number; t0: number } | null = null;
    const bangHandler = () => { bangT0 = (performance.now() - t0) / 1000; };
    bangRef.current = bangHandler;
    const clicks: number[] = [];
    const t0 = performance.now();

    const fit = () => {
      const r = canvas.getBoundingClientRect();
      w = Math.max(1, r.width); h = Math.max(1, r.height);
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr);
      R = Math.max(w, h) * 0.6;
    };
    const avoidBox = (): Box | null => {
      const el = avoidRef?.current;
      if (!el) return null;
      const a = el.getBoundingClientRect(), c = canvas.getBoundingClientRect();
      return { l: a.left - c.left, r: a.right - c.left, t: a.top - c.top, b: a.bottom - c.top };
    };
    const paint = (tSec: number, still: boolean) => {
      ({ pos, starPos } = drawFrame(ctx, {
        avoid: avoidBox(),
        g: galaxy, v: { angle: st.angle, tilt: st.tilt, cx: w / 2, cy: h / 2, R }, w, h, dpr, tSec, still,
        col, pace: paceRef.current, hover, labels, sparks,
        warp: bangT0 >= 0 ? bigBangWarp(tSec - bangT0) : NO_WARP,
        orbit,
      }));
    };
    const paintStill = () => paint(0, true);
    const tick = (now: number) => {
      raf = 0;
      if (document.hidden) return; // görünür olunca visibilitychange yeniden başlatır
      const dt = Math.min(0.1, (now - prev) / 1000); prev = now;
      if (!dragging) {
        st.angle += st.vel * dt; st.tilt += st.tiltVel * dt;
        const fr = Math.pow(0.12, dt); st.vel *= fr; st.tiltVel *= fr; // bırakınca yavaşça durur
        st.angle += spinRate(paceRef.current) * dt; // dönüş hızı = throughput temposu
        st.tilt += (TILT0 - st.tilt) * Math.min(1, dt * 0.25); // eğim yavaşça varsayılana döner
      }
      if (sparks.length) sparks = pruneSparks(sparks, (now - t0) / 1000);
      // yıldız uçarken kare atlanmaz (akıcı uçuş); aksi kare bütçesi aynen
      if (bangT0 >= 0 && (now - t0) / 1000 - bangT0 > BH_TOTAL) bangT0 = -1;
      if (orbit && orbitProgress(orbit.t0, (now - t0) / 1000) === null) orbit = null;
      if (sparks.length || bangT0 >= 0 || orbit || shouldDraw(now, last)) { last = now; paint((now - t0) / 1000, false); }
      raf = requestAnimationFrame(tick);
    };
    const start = () => { if (animated && !raf && !document.hidden) { prev = performance.now(); raf = requestAnimationFrame(tick); } };
    const onVis = () => { if (document.hidden) { if (raf) cancelAnimationFrame(raf); raf = 0; } else start(); };

    // ── sürükleme: bütün galaksi BİRLİKTE döner (yatay) ve eğilir (dikey) ──
    const local = (e: PointerEvent) => { const r = canvas.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
    const onDown = (e: PointerEvent) => {
      if (e.button !== 0) return;
      e.preventDefault(); // metin seçimi başlamasın
      // v0.10.1982 (operatör: "arka plana tıklayıp yazınca da algılamıyor") — preventDefault odağı giriş
      // kutusunda bırakıyordu; galaksiye tıklayınca odak bırakılır, kısayollar (Space × 3, S R E) çalışır.
      const act = document.activeElement;
      if (act instanceof HTMLElement && act !== document.body) act.blur();
      dragging = true; const p = local(e); lx = p.x; ly = p.y; lt = performance.now(); st.vel = 0; st.tiltVel = 0;
      downX = p.x; downY = p.y; downT = lt;
      canvas.setPointerCapture?.(e.pointerId);
      canvas.style.cursor = 'grabbing';
    };
    const hitAt = (p: { x: number; y: number }) => {
      let best = -1, bd = HOVER_PX;
      for (let i = 0; i < pos.length; i++) { const d = Math.hypot(pos[i].x - p.x, pos[i].y - p.y); if (d < bd) { bd = d; best = i; } }
      // v0.10.1947 — adı olan servis yıldızları; bağlantılı düğüm 4 px öncelikli
      for (let k = 0; k < starPos.length; k++) {
        const q = starPos[k];
        if (!q || !galaxy.starNames[k]) continue;
        const d = Math.hypot(q.x - p.x, q.y - p.y);
        if (d < bd - 4) { bd = d + 4; best = pos.length + k; }
      }
      return best;
    };
    const onMove = (e: PointerEvent) => {
      const p = local(e);
      if (dragging) {
        const now = performance.now(), dts = Math.max(0.008, (now - lt) / 1000);
        const da = (p.x - lx) / (R * 0.9), dtl = (p.y - ly) / (R * 1.6);
        st.angle += da; st.tilt = Math.min(TILT_MAX, Math.max(TILT_MIN, st.tilt - dtl));
        st.vel = st.vel * 0.6 + (da / dts) * 0.4; st.tiltVel = st.tiltVel * 0.6 + (-dtl / dts) * 0.4;
        lx = p.x; ly = p.y; lt = now;
        if (!animated) paintStill();
        return;
      }
      const best = hitAt(p);
      canvas.style.cursor = best >= 0 ? 'pointer' : 'grab';
      if (best !== hover) { hover = best; if (!animated) paintStill(); }
    };
    const onUp = (e: PointerEvent) => {
      if (!dragging) return;
      dragging = false;
      // Kısa ve yerinde basış = tık: üzerindeki noktadan bağlı noktalara yıldız (hızlı tık → daha çok).
      const p = local(e), now = performance.now();
      const hit = animated && now - downT < 350 && Math.hypot(p.x - downX, p.y - downY) < 6 ? hitAt(p) : -1; // dokunmatikte hover yok
      if (hit >= 0) {
        st.vel = 0; st.tiltVel = 0; // tık galaksiyi savurmasın
        clicks.push(now); while (clicks.length > 20) clicks.shift();
        const nowSec = (now - t0) / 1000;
        streak = nextStreak(streak, hit, now);
        if (streak.n === ORBIT_CLICKS) {
          // 10. tık: yörünge halkası + BÜTÜN bağlı noktalara yıldız yağmuru.
          orbit = { at: hit, t0: nowSec };
          const all = sparkTargets(galaxy, hit, ORBIT_TARGETS);
          if (all.length) sparks = pruneSparks([...sparks, ...spawnSparks(hit, all, nowSec, 4)], nowSec);
        } else {
          const targets = sparkTargets(galaxy, hit);
          if (targets.length) sparks = pruneSparks([...sparks, ...spawnSparks(hit, targets, nowSec, burstSize(clicks, now))], nowSec);
        }
      }
      canvas.releasePointerCapture?.(e.pointerId);
      canvas.style.cursor = 'grab';
    };
    const onLeave = () => { if (!dragging && hover >= 0) { hover = -1; if (!animated) paintStill(); } };
    // v0.10.1973 — nokta üzerinde hızlı tık yıldız gönderir; çift tık orada görünümü sıfırlamaz.
    const onDbl = (e: MouseEvent) => {
      const r = canvas.getBoundingClientRect();
      if (hitAt({ x: e.clientX - r.left, y: e.clientY - r.top }) >= 0) return;
      st.angle = 0; st.tilt = TILT0; st.vel = 0; st.tiltVel = 0; if (!animated) paintStill();
    };

    fit();
    canvas.style.cursor = 'grab';
    if (animated) start(); else paintStill();
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(() => { fit(); if (!animated) paintStill(); }) : null;
    ro?.observe(canvas);
    document.addEventListener('visibilitychange', onVis);
    canvas.addEventListener('pointerdown', onDown);
    canvas.addEventListener('pointermove', onMove);
    canvas.addEventListener('pointerup', onUp);
    canvas.addEventListener('pointercancel', onUp);
    canvas.addEventListener('pointerleave', onLeave);
    canvas.addEventListener('dblclick', onDbl);
    return () => {
      if (raf) cancelAnimationFrame(raf);
      ro?.disconnect();
      document.removeEventListener('visibilitychange', onVis);
      canvas.removeEventListener('pointerdown', onDown);
      canvas.removeEventListener('pointermove', onMove);
      canvas.removeEventListener('pointerup', onUp);
      canvas.removeEventListener('pointercancel', onUp);
      canvas.removeEventListener('pointerleave', onLeave);
      canvas.removeEventListener('dblclick', onDbl);
    };
  }, [galaxy, labels, animated, themeTick, avoidRef]);

  return <canvas ref={ref} aria-hidden="true" data-testid="login-topology"
    className="absolute inset-0 size-full select-none touch-none" />;
}
