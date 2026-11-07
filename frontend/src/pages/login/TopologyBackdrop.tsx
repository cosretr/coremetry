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
import { useEffect, useRef, useState } from 'react';
import { chartMonoFont, resolveVar } from '@/lib/chart/resolveVar';
import { useThemeTick } from '@/lib/useThemeTick';
import {
  flowEdges, flowPos, labelNodes, nodeStyle, screenPositions, shouldDraw, thinGraph, type Pt,
} from '@/lib/loginTopology/frame';
import { beginDrag, dragTo, hitNode, type Drag } from '@/lib/loginTopology/drag';
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

/** Bir kareyi çizer. `tSec` = animasyon zamanı (durağan karede 0). */
function drawFrame(
  ctx: CanvasRenderingContext2D, lay: Layout, cssW: number, cssH: number, dpr: number,
  pos: readonly Pt[], tSec: number, col: Colors, flows: number[], labels: Set<number>, held: number,
) {
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, cssW, cssH);
  ctx.lineWidth = 0.7;
  ctx.strokeStyle = col.edge;
  ctx.globalAlpha = 0.12;
  ctx.beginPath();
  for (const e of lay.edges) { ctx.moveTo(pos[e.a].x, pos[e.a].y); ctx.lineTo(pos[e.b].x, pos[e.b].y); }
  ctx.stroke();
  // akan noktalar (yalnız animasyonda; durağan karede kenarın ortasında tek nokta)
  ctx.fillStyle = col.edge;
  ctx.globalAlpha = 0.4;
  for (const ei of flows) {
    const e = lay.edges[ei];
    const p = tSec === 0 ? 0.5 : flowPos(lay, ei, tSec);
    ctx.beginPath();
    ctx.arc(pos[e.a].x + (pos[e.b].x - pos[e.a].x) * p, pos[e.a].y + (pos[e.b].y - pos[e.a].y) * p, 1.2, 0, Math.PI * 2);
    ctx.fill();
  }
  lay.nodes.forEach((n, i) => {
    const st = nodeStyle(n);
    const { x, y } = pos[i];
    if (st.square) {
      ctx.fillStyle = col.db; ctx.globalAlpha = 0.8;
      ctx.beginPath();
      // roundRect eski tarayıcılarda yok (Safari < 16, Chrome < 99): düz kareye düş, çizim döngüsü kırılmasın.
      if (typeof ctx.roundRect === 'function') ctx.roundRect(x - st.r / 2, y - st.r / 2, st.r, st.r, 1.5);
      else ctx.rect(x - st.r / 2, y - st.r / 2, st.r, st.r);
      ctx.fill();
      return;
    }
    // tutulan düğüm tam opak (geri bildirim); diğerleri soluk
    ctx.fillStyle = col.node; ctx.globalAlpha = i === held ? 1 : st.ring ? 0.7 : 0.55;
    ctx.beginPath(); ctx.arc(x, y, st.r, 0, Math.PI * 2); ctx.fill();
    if (st.ring) {
      ctx.strokeStyle = col.node; ctx.globalAlpha = 0.3; ctx.lineWidth = 1;
      ctx.beginPath(); ctx.arc(x, y, st.r + 5, 0, Math.PI * 2); ctx.stroke();
      ctx.lineWidth = 0.7;
    }
  });
  if (labels.size) {
    ctx.font = chartMonoFont(11);
    ctx.fillStyle = col.label;
    for (const i of labels) {
      const n = lay.nodes[i];
      ctx.globalAlpha = nodeStyle(n).labelAlpha * 0.9;
      ctx.fillText(n.name ?? '', pos[i].x + nodeStyle(n).r + 4, pos[i].y + 3);
    }
  }
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
  const [labels] = useState<Set<number>>(() => new Set(labelNodes(layout)));

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
    const t0 = performance.now();

    const fit = () => {
      const r = canvas.getBoundingClientRect();
      cssW = Math.max(1, r.width); cssH = Math.max(1, r.height);
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.round(cssW * dpr); canvas.height = Math.round(cssH * dpr);
    };
    const positions = () => screenPositions(layout, curT, cssW / layout.w, cssH / layout.h);
    const paint = () => {
      if (drag && target) dragTo(layout, drag, target.x, target.y);
      drawFrame(ctx, layout, cssW, cssH, dpr, positions(), curT, col, flows, labels, drag?.i ?? -1);
    };
    const paintStill = () => { curT = 0; paint(); };
    const tick = (now: number) => {
      raf = 0;
      if (document.hidden) return; // görünür olunca visibilitychange yeniden başlatır
      if (shouldDraw(now, last)) {
        last = now;
        curT = (now - t0) / 1000 + 0.001;
        paint();
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
      const shown = screenPositions(layout, curT, sx, sy)[i];
      const n = layout.nodes[i];
      return { x: n.x + (p.x - shown.x) / sx, y: n.y + (p.y - shown.y) / sy };
    };
    const onDown = (e: PointerEvent) => {
      if (e.button !== 0) return;
      const p = local(e);
      const i = hitNode(layout, positions(), p.x, p.y);
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
      canvas.style.cursor = hitNode(layout, positions(), p.x, p.y) >= 0 ? 'grab' : '';
    };
    const onUp = (e: PointerEvent) => {
      if (!drag) return;
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
    return () => {
      if (raf) cancelAnimationFrame(raf);
      ro?.disconnect();
      document.removeEventListener('visibilitychange', onVis);
      canvas.removeEventListener('pointerdown', onDown);
      canvas.removeEventListener('pointermove', onMove);
      canvas.removeEventListener('pointerup', onUp);
      canvas.removeEventListener('pointercancel', onUp);
    };
  }, [layout, flows, labels, animated, themeTick]);

  // Olay alır (sürükleme); kart ve üst çubuk üstte olduğundan yalnız kart DIŞINDA.
  return <canvas ref={ref} aria-hidden="true" data-testid="login-topology"
    className="absolute inset-0 size-full select-none" />;
}
