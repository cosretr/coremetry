// TopologyBackdrop — v0.10.1909 (Login topoloji arka planı, audit §6.3).
// Kartın ARKASINDA tam sayfa canvas: servis topolojisinin SİLUETİ (gerçek, ya da
// veri yoksa sentetik). Durum/uyarı rengi YOK; yalnız tema token'ları
// (`resolveVar`). Yerleşim (lib/loginTopology) ilk gelen grafikle BİR KEZ kurulur
// (`useState` başlatıcısı); sonraki yenilemeler yerleşimi yeniden kurmaz.
// Performans: 30 fps tavanı, DPR ≤ 2, `document.hidden`'da rAF durur,
// `prefers-reduced-motion`'da tek durağan kare. Dekoratif: aria-hidden. Yeni
// bağımlılık YOK.
//
// v0.10.1917 (operatör) — düğümler fareyle TUTULUP sürüklenir (lib/loginTopology/
// drag: komşular yaylı takip eder, bırakılan yerde kalır). Canvas yalnız kartın
// DIŞINDA olay alır (kart ve üst çubuk üstte). Çizilen düğüm sayısı seyreltilir
// (thinGraph) ve yerleşim ekrana eşit yayılır (layout.evenOut).
//
// v0.10.1919 (operatör: "derinlik olsun, gerçekten güzel bir deneyim olsun") —
// üç katmanlı derinlik (lib/loginTopology/depth): önemliler önde büyük ve net,
// uzun kuyruk arkada küçük, soluk ve yavaş; çizim arkadan öne; isim etiketi
// yalnız ön katmanda. Üzerine gelinen düğüm büyür, bağlantıları parlar ve adı
// (katmanı ne olursa olsun) görünür; sahne açılışta yumuşakça belirir.
// Parallax YOK (şifre yazarken dikkat dağıtmasın, tutulan nokta kaymasın).
import { useEffect, useRef, useState } from 'react';
import { chartMonoFont, resolveVar } from '@/lib/chart/resolveVar';
import { useThemeTick } from '@/lib/useThemeTick';
import {
  flowEdges, flowPos, labelNodes, MAX_LABELS, nodeStyle, screenPositions, shouldDraw, thinGraph, type Pt,
} from '@/lib/loginTopology/frame';
import { beginDrag, dragTo, hitNode, type Drag } from '@/lib/loginTopology/drag';
import {
  DEPTH_STYLE, depthLayers, drawOrder, edgeDepth, fadeIn, FOCUS_SCALE, type Depth,
} from '@/lib/loginTopology/depth';
import { layoutOf, seedOfIds, type Layout } from '@/lib/loginTopology/layout';
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';

export interface BackdropGraph { nodes: LoginSummaryNode[]; edges: LoginSummaryEdge[]; seed?: number }

interface Colors { edge: string; node: string; db: string; label: string }

function readColors(): Colors {
  return {
    edge: resolveVar('var(--accent2)'), node: resolveVar('var(--accent2)'),
    db: resolveVar('var(--purple)'), label: resolveVar('var(--text2)'),
  };
}

/** Bir karenin girdileri. */
interface Scene {
  lay: Layout; cssW: number; cssH: number; dpr: number;
  pos: readonly Pt[]; tSec: number; col: Colors;
  flows: readonly number[]; labels: ReadonlySet<number>;
  depth: readonly Depth[]; order: readonly number[];
  /** Odaktaki düğüm (üzerine gelinen ya da tutulan); -1 = yok. */
  focus: number;
  /** Odaktaki düğümün komşuları. */
  near: ReadonlySet<number>;
  /** Açılış saydamlık çarpanı [0, 1]. */
  fade: number;
}

/** Bir kareyi çizer: kenarlar (arkadan öne), akan noktalar, düğümler (arkadan öne), etiketler, odak. */
function drawFrame(ctx: CanvasRenderingContext2D, s: Scene) {
  const { lay, pos, col, depth, focus, near, fade } = s;
  ctx.setTransform(s.dpr, 0, 0, s.dpr, 0, 0);
  ctx.clearRect(0, 0, s.cssW, s.cssH);

  // Kenarlar: katman başına tek yol (arka → ön); daha derin uç belirler.
  ctx.strokeStyle = col.edge;
  for (const layer of [2, 1, 0] as const) {
    const st = DEPTH_STYLE[layer];
    ctx.lineWidth = st.edgeWidth;
    ctx.globalAlpha = st.edgeAlpha * fade;
    ctx.beginPath();
    for (const e of lay.edges) {
      if (edgeDepth(depth, e.a, e.b) !== layer) continue;
      ctx.moveTo(pos[e.a].x, pos[e.a].y); ctx.lineTo(pos[e.b].x, pos[e.b].y);
    }
    ctx.stroke();
  }
  // Odaktaki düğümün bağlantıları parlar.
  if (focus >= 0) {
    ctx.lineWidth = 1.2;
    ctx.globalAlpha = 0.55 * fade;
    ctx.beginPath();
    for (const e of lay.edges) {
      if (e.a !== focus && e.b !== focus) continue;
      ctx.moveTo(pos[e.a].x, pos[e.a].y); ctx.lineTo(pos[e.b].x, pos[e.b].y);
    }
    ctx.stroke();
  }

  // Akan noktalar: yalnız ön ve orta katman kenarlarında (durağan karede kenar ortasında).
  ctx.fillStyle = col.edge;
  for (const ei of s.flows) {
    const e = lay.edges[ei];
    const layer = edgeDepth(depth, e.a, e.b);
    if (layer === 2) continue;
    const p = s.tSec === 0 ? 0.5 : flowPos(lay, ei, s.tSec);
    ctx.globalAlpha = (layer === 0 ? 0.55 : 0.3) * fade;
    ctx.beginPath();
    ctx.arc(pos[e.a].x + (pos[e.b].x - pos[e.a].x) * p, pos[e.a].y + (pos[e.b].y - pos[e.a].y) * p,
      layer === 0 ? 1.4 : 1, 0, Math.PI * 2);
    ctx.fill();
  }

  // Düğümler: arkadan öne.
  for (const i of s.order) {
    const n = lay.nodes[i];
    const st = nodeStyle(n);
    const ds = DEPTH_STYLE[depth[i]];
    const focused = i === focus;
    const r = st.r * ds.scale * (focused ? FOCUS_SCALE : 1);
    const alpha = (focused ? 1 : near.has(i) ? Math.min(1, ds.nodeAlpha + 0.3) : ds.nodeAlpha) * fade;
    const { x, y } = pos[i];
    ctx.globalAlpha = alpha;
    if (st.square) {
      ctx.fillStyle = col.db;
      ctx.beginPath();
      // roundRect eski tarayıcılarda yok (Safari < 16, Chrome < 99): düz kareye düş, çizim döngüsü kırılmasın.
      if (typeof ctx.roundRect === 'function') ctx.roundRect(x - r / 2, y - r / 2, r, r, 1.5);
      else ctx.rect(x - r / 2, y - r / 2, r, r);
      ctx.fill();
      continue;
    }
    ctx.fillStyle = col.node;
    ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill();
    if (st.ring && depth[i] < 2) {
      ctx.strokeStyle = col.node; ctx.lineWidth = 1;
      ctx.globalAlpha = alpha * 0.4;
      ctx.beginPath(); ctx.arc(x, y, r + 5 * ds.scale, 0, Math.PI * 2); ctx.stroke();
    }
    if (focused) { // odak halkası: tutulabildiğini söyler
      ctx.strokeStyle = col.node; ctx.lineWidth = 1.5;
      ctx.globalAlpha = 0.5 * fade;
      ctx.beginPath(); ctx.arc(x, y, r + 4, 0, Math.PI * 2); ctx.stroke();
    }
  }

  // Etiketler: ön katman (rastgele alt küme) + odaktaki düğüm (katmanı ne olursa olsun).
  ctx.font = chartMonoFont(11);
  ctx.fillStyle = col.label;
  const label = (i: number, a: number) => {
    const n = lay.nodes[i];
    if (!n.name) return;
    const r = nodeStyle(n).r * DEPTH_STYLE[depth[i]].scale * (i === focus ? FOCUS_SCALE : 1);
    ctx.globalAlpha = a * fade;
    ctx.fillText(n.name, pos[i].x + r + 5, pos[i].y + 3);
  };
  for (const i of s.labels) if (i !== focus) label(i, nodeStyle(lay.nodes[i]).labelAlpha * 0.95);
  if (focus >= 0) label(focus, 1);
  ctx.globalAlpha = 1;
}

export default function TopologyBackdrop({ graph, animated }: { graph: BackdropGraph; animated: boolean }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const themeTick = useThemeTick();
  // BİR KEZ: başlatıcı yalnız ilk render'da çalışır; `graph` sonradan değişse de yerleşim kurulmaz.
  const [layout] = useState<Layout>(() => {
    const g = thinGraph(graph.nodes, graph.edges);
    return layoutOf(g.nodes, g.edges, graph.seed ?? seedOfIds(graph.nodes.map(n => n.id)));
  });
  const [flows] = useState<number[]>(() => flowEdges(layout));
  // v0.10.1919 — derinlik: önem sırasıyla ön / orta / arka katman.
  const [depth] = useState<Depth[]>(() => depthLayers(layout));
  const [order] = useState<number[]>(() => drawOrder(depth));
  const [adj] = useState<Map<number, Set<number>>>(() => {
    const m = new Map<number, Set<number>>();
    const add = (a: number, b: number) => { (m.get(a) ?? m.set(a, new Set()).get(a)!).add(b); };
    for (const e of layout.edges) { add(e.a, e.b); add(e.b, e.a); }
    return m;
  });
  // v0.10.1918 — etiketlenecek servisler her açılışta RASTGELE; v0.10.1919 — yalnız ön katmandan.
  // v0.10.1926 (operatör-raporlu: arka planda birden fazla "oracle" / "queue" yazıyor) —
  // veri katmanı düğümleri (db/kuyruk) SABİT etiket almaz: adları yalnız sistem adıdır,
  // aynı ad onlarca kez tekrar ederdi. Üzerine gelince adı yine görünür.
  const [labels] = useState<Set<number>>(() =>
    new Set(labelNodes(layout, MAX_LABELS, Math.random, i => depth[i] === 0 && layout.nodes[i].tier !== 'db')));

  useEffect(() => {
    const canvas = ref.current;
    const ctx = canvas?.getContext('2d');
    if (!canvas || !ctx) return;
    const col = readColors(); // tema değişince (themeTick) efekt yeniden kurulur
    let cssW = 0, cssH = 0, dpr = 1;
    let raf = 0, last = 0;
    let curT = 0; // son çizilen karenin zamanı (durağan kipte 0): isabet ve sürükleme aynı konumu kullanır
    let drag: Drag | null = null;
    let target: Pt | null = null; // sürüklenen düğümün mantıksal hedefi
    let hover = -1; // üzerine gelinen düğüm
    const t0 = performance.now();
    const motion = (i: number) => ({ amp: DEPTH_STYLE[depth[i]].driftMul, speed: DEPTH_STYLE[depth[i]].speedMul });

    const fit = () => {
      const r = canvas.getBoundingClientRect();
      cssW = Math.max(1, r.width); cssH = Math.max(1, r.height);
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.round(cssW * dpr); canvas.height = Math.round(cssH * dpr);
    };
    const positions = () => screenPositions(layout, curT, cssW / layout.w, cssH / layout.h, motion);
    const paint = (now = performance.now()) => {
      if (drag && target) dragTo(layout, drag, target.x, target.y);
      const focus = drag?.i ?? hover;
      drawFrame(ctx, {
        lay: layout, cssW, cssH, dpr, pos: positions(), tSec: curT, col, flows, labels, depth, order,
        focus, near: focus >= 0 ? adj.get(focus) ?? new Set() : new Set(), fade: fadeIn(now - t0, animated),
      });
    };
    const paintStill = () => { curT = 0; paint(); };
    const tick = (now: number) => {
      raf = 0;
      if (document.hidden) return; // görünür olunca visibilitychange yeniden başlatır
      if (shouldDraw(now, last)) {
        last = now;
        curT = (now - t0) / 1000 + 0.001;
        paint(now);
      }
      raf = requestAnimationFrame(tick);
    };
    const start = () => { if (animated && !raf && !document.hidden) raf = requestAnimationFrame(tick); };
    const onVis = () => { if (document.hidden) { if (raf) cancelAnimationFrame(raf); raf = 0; } else start(); };

    // ── fare / dokunma: tut-sürükle (v0.10.1917) ──
    const local = (e: PointerEvent): Pt => {
      const r = canvas.getBoundingClientRect();
      return { x: e.clientX - r.left, y: e.clientY - r.top };
    };
    /** Ekran noktası → tutulan düğümün mantıksal konumu (o anki kaymayı düşerek; düğüm imlecin altında kalır). */
    const toLogical = (p: Pt, i: number): Pt => {
      const sx = cssW / layout.w, sy = cssH / layout.h;
      const shown = screenPositions(layout, curT, sx, sy, motion)[i];
      const n = layout.nodes[i];
      return { x: n.x + (p.x - shown.x) / sx, y: n.y + (p.y - shown.y) / sy };
    };
    const onDown = (e: PointerEvent) => {
      if (e.button !== 0) return;
      const p = local(e);
      const i = hitNode(layout, positions(), p.x, p.y, undefined, depth);
      if (i < 0) return;
      e.preventDefault(); // metin seçimi / sürükle-bırak başlamasın
      drag = beginDrag(layout, i);
      target = toLogical(p, i);
      canvas.setPointerCapture?.(e.pointerId);
      canvas.style.cursor = 'grabbing';
      if (!animated) paint();
    };
    const onMove = (e: PointerEvent) => {
      const p = local(e);
      if (drag) {
        target = toLogical(p, drag.i);
        if (!animated) paint();
        return;
      }
      const i = hitNode(layout, positions(), p.x, p.y, undefined, depth);
      canvas.style.cursor = i >= 0 ? 'grab' : '';
      if (i !== hover) { hover = i; if (!animated) paint(); }
    };
    const onLeave = () => {
      if (drag || hover < 0) return;
      hover = -1;
      canvas.style.cursor = '';
      if (!animated) paint();
    };
    const onUp = (e: PointerEvent) => {
      if (!drag) return;
      hover = drag.i; // bırakılan düğüm odakta kalır (imleç hâlâ üstünde)
      drag = null; target = null;
      canvas.releasePointerCapture?.(e.pointerId);
      canvas.style.cursor = 'grab';
      if (!animated) paint();
    };

    fit();
    if (animated) start(); else paintStill();
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(() => { fit(); if (!animated) paint(); }) : null;
    ro?.observe(canvas);
    document.addEventListener('visibilitychange', onVis);
    canvas.addEventListener('pointerdown', onDown);
    canvas.addEventListener('pointermove', onMove);
    canvas.addEventListener('pointerup', onUp);
    canvas.addEventListener('pointercancel', onUp);
    canvas.addEventListener('pointerleave', onLeave);
    return () => {
      if (raf) cancelAnimationFrame(raf);
      ro?.disconnect();
      document.removeEventListener('visibilitychange', onVis);
      canvas.removeEventListener('pointerdown', onDown);
      canvas.removeEventListener('pointermove', onMove);
      canvas.removeEventListener('pointerup', onUp);
      canvas.removeEventListener('pointercancel', onUp);
      canvas.removeEventListener('pointerleave', onLeave);
    };
  }, [layout, flows, labels, depth, order, adj, animated, themeTick]);

  // Olay alır (sürükleme); kart ve üst çubuk üstte olduğundan yalnız kart DIŞINDA.
  return <canvas ref={ref} aria-hidden="true" data-testid="login-topology"
    className="absolute inset-0 size-full select-none" />;
}
