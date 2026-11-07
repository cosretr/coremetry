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
// v0.10.1935: üzerine gelince adın altında soluk TÜR satırı (gateway / BFF / servis / veritabanı / kuyruk, i18n).
// --accent2, kol/veri --purple, akan noktalar --orange (Traces süre çizgisiyle AYNI token; v0.10.1932),
// etiket --text2. v0.10.1931: veritabanı mor YUVARLAK nokta + hafif hale (kare değil),
// kuyruk --teal İÇİ BOŞ halka; ikisi de adıyla (veritabanı adı / konu), her ad bir kez. Parıltı gücü zeminin koyuluğuna göre (koyu temada güçlü).
// Performans: 30 fps tavanı, DPR ≤ 2, `document.hidden`'da rAF durur,
// `prefers-reduced-motion`'da kendiliğinden hareket yok (sürükleme çalışır).
// Galaksi ilk gelen grafikle BİR KEZ kurulur. Dekoratif: aria-hidden; olay alır
// (sürükleme) — kart ve üst çubuk üstte olduğundan yalnız kart DIŞINDA.
import { useEffect, useRef, useState } from 'react';
import { chartMonoFont, resolveVar } from '@/lib/chart/resolveVar';
import { useThemeTick } from '@/lib/useThemeTick';
import { useT } from '@/lib/i18n';
import { MAX_LABELS, shouldDraw, thinGraph } from '@/lib/loginTopology/frame';
import {
  buildGalaxy, depthOf, flowPos, isDarkColor, nodeAlpha, nodeRadius, pickBackendLabels, pickLabelSet, project, spinRate,
  TILT0, TILT_MAX, TILT_MIN, wantsLabel, type Galaxy, type Pt, type View,
} from '@/lib/loginTopology/galaxy';
import { seedOfIds } from '@/lib/loginTopology/prng';
import type { LoginSummaryEdge, LoginSummaryNode } from '@/lib/types';

export interface BackdropGraph { nodes: LoginSummaryNode[]; edges: LoginSummaryEdge[]; seed?: number }

interface Colors { star: string; arm: string; mq: string; flow: string; label: string; bg: string; glow: number; flowBoost: number }

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
    flow: resolveVar('var(--orange)'), label: resolveVar('var(--text2)'),
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
  /** Katman → üzerine gelince gösterilen tür adı (i18n). */
  kinds: Readonly<Record<string, string>>;
}

/** Etiket: zemin renginde ince çerçeve + metin (yıldız tozundan ayrışsın). */
function label(ctx: CanvasRenderingContext2D, text: string, x: number, y: number, col: Colors) {
  ctx.lineJoin = 'round'; ctx.lineWidth = 3.5; ctx.strokeStyle = col.bg;
  ctx.strokeText(text, x, y);
  ctx.fillStyle = col.label; ctx.fillText(text, x, y);
}

/** Bir kare: toz → kenarlar → akan noktalar → düğümler (arkadan öne) → etiketler. Düğüm konumlarını döner (isabet testi için). */
function drawFrame(ctx: CanvasRenderingContext2D, f: Frame): Pt[] {
  const { g, v, col, hover } = f;
  ctx.setTransform(f.dpr, 0, 0, f.dpr, 0, 0);
  ctx.clearRect(0, 0, f.w, f.h);
  const tw = (ph: number, k = 1.3) => (f.still ? 1 : 0.7 + 0.3 * Math.sin(f.tSec * k + ph));

  // çekirdek parıltısı
  const core = project(v, 0, 0, 0);
  halo(ctx, core.x, core.y, v.R * 0.5, col.star, 0.16 * col.glow);

  // yıldız tozu (1–1,5 px kareler: en ucuz çizim)
  ctx.fillStyle = col.star;
  const d = g.dust;
  for (let o = 0; o < d.length; o += 5) {
    const p = project(v, d[o], d[o + 1], d[o + 2]);
    if (p.x < -3 || p.y < -3 || p.x > f.w + 3 || p.y > f.h + 3) continue;
    ctx.globalAlpha = Math.max(0.04, (0.18 + 0.22 * p.z) * d[o + 3] * tw(d[o + 4]) * (0.6 + col.glow * 0.5));
    const s = 0.8 + 0.7 * depthOf(p.z);
    ctx.fillRect(p.x, p.y, s, s);
  }

  // düğüm konumları (hafif nefes)
  const pos = g.nodes.map(nd => project(v, nd.r + (f.still ? 0 : Math.sin(f.tSec * 0.6 + nd.ph) * 0.004), nd.th, nd.h));

  // kenarlar
  ctx.strokeStyle = col.arm; ctx.lineWidth = 0.7;
  for (const e of g.edges) {
    const focus = hover >= 0 && (e.a === hover || e.b === hover);
    ctx.globalAlpha = focus ? 0.7 : Math.max(0.03, 0.08 + 0.04 * (pos[e.a].z + pos[e.b].z));
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

  // düğümler: arkadan öne
  const order = g.nodes.map((_, i) => i).sort((i, j) => pos[i].z - pos[j].z);
  for (const i of order) {
    const nd = g.nodes[i], p = pos[i], depth = depthOf(p.z), focus = i === hover;
    const r = nodeRadius(nd, depth, focus), a = nodeAlpha(nd, depth, focus);
    if (nd.tier === 'queue') { // kuyruk: turkuaz halka
      halo(ctx, p.x, p.y, r * 2.6, col.mq, (0.08 + 0.22 * col.glow) * (0.5 + depth));
      ctx.globalAlpha = a; ctx.strokeStyle = col.mq; ctx.lineWidth = 1.4;
      ctx.beginPath(); ctx.arc(p.x, p.y, r * 1.15, 0, TAU); ctx.stroke(); continue;
    }
    if (nd.tier === 'db') { // veritabanı: mor yuvarlak nokta + hafif hale
      halo(ctx, p.x, p.y, r * 2.6, col.arm, (0.1 + 0.25 * col.glow) * (0.5 + depth));
      ctx.globalAlpha = a; ctx.fillStyle = col.arm;
      ctx.beginPath(); ctx.arc(p.x, p.y, r * 0.9, 0, TAU); ctx.fill(); continue;
    }
    if (nd.tier === 'gw' || nd.weight > 0.55 || focus) {
      halo(ctx, p.x, p.y, r * 3.4, col.star, (0.12 + 0.3 * col.glow) * (0.35 + 0.65 * nd.weight) * (0.5 + depth));
    }
    ctx.globalAlpha = a; ctx.fillStyle = col.star;
    ctx.beginPath(); ctx.arc(p.x, p.y, r, 0, TAU); ctx.fill();
  }

  // etiketler (yalnız isimli kipte ad gelir)
  ctx.font = chartMonoFont(11);
  for (const i of order) {
    if (i === hover) continue;
    const nd = g.nodes[i], depth = depthOf(pos[i].z);
    if (!wantsLabel(nd, depth, f.labels.has(i))) continue;
    ctx.globalAlpha = Math.min(0.85, (depth - 0.3) * 1.3);
    label(ctx, nd.name ?? '', pos[i].x + 8, pos[i].y + 3, col);
  }
  if (hover >= 0 && g.nodes[hover].name) {
    const nd = g.nodes[hover], p = pos[hover];
    ctx.globalAlpha = 1; ctx.font = chartMonoFont(12);
    label(ctx, nd.name ?? '', p.x + 9, p.y + 4, col);
    const kind = f.kinds[nd.tier];
    if (kind) { ctx.globalAlpha = 0.7; ctx.font = chartMonoFont(10.5); label(ctx, kind, p.x + 9, p.y + 18, col); }
  }
  ctx.globalAlpha = 1;
  return pos;
}

export default function TopologyBackdrop({ graph, animated, pace = 0.6 }: {
  graph: BackdropGraph; animated: boolean;
  /** Toplam throughput temposu ∈ [0,1] (son / 24 sa tepe): dönüş ve akış hızı. */
  pace?: number;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const themeTick = useThemeTick();
  const t = useT();
  // Tür adları ref'te: dil değişince efekt yeniden kurulmaz, sonraki karede güncellenir.
  const kindsRef = useRef<Record<string, string>>({});
  kindsRef.current = {
    gw: t('login.topo.kind.gw'), bff: t('login.topo.kind.bff'), svc: t('login.topo.kind.svc'),
    db: t('login.topo.kind.db'), queue: t('login.topo.kind.queue'),
  };
  // BİR KEZ: galaksi ilk gelen grafikle kurulur; `graph` sonradan değişse de yeniden kurulmaz.
  const [galaxy] = useState<Galaxy>(() => {
    const t = thinGraph(graph.nodes, graph.edges);
    return buildGalaxy(t.nodes, t.edges, graph.seed ?? seedOfIds(graph.nodes.map(n => n.id)));
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
    let hover = -1, dragging = false, lx = 0, ly = 0, lt = 0;
    const t0 = performance.now();

    const fit = () => {
      const r = canvas.getBoundingClientRect();
      w = Math.max(1, r.width); h = Math.max(1, r.height);
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr);
      R = Math.max(w, h) * 0.6;
    };
    const paint = (tSec: number, still: boolean) => {
      pos = drawFrame(ctx, {
        g: galaxy, v: { angle: st.angle, tilt: st.tilt, cx: w / 2, cy: h / 2, R }, w, h, dpr, tSec, still,
        col, pace: paceRef.current, hover, labels, kinds: kindsRef.current,
      });
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
      if (shouldDraw(now, last)) { last = now; paint((now - t0) / 1000, false); }
      raf = requestAnimationFrame(tick);
    };
    const start = () => { if (animated && !raf && !document.hidden) { prev = performance.now(); raf = requestAnimationFrame(tick); } };
    const onVis = () => { if (document.hidden) { if (raf) cancelAnimationFrame(raf); raf = 0; } else start(); };

    // ── sürükleme: bütün galaksi BİRLİKTE döner (yatay) ve eğilir (dikey) ──
    const local = (e: PointerEvent) => { const r = canvas.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
    const onDown = (e: PointerEvent) => {
      if (e.button !== 0) return;
      e.preventDefault(); // metin seçimi başlamasın
      dragging = true; const p = local(e); lx = p.x; ly = p.y; lt = performance.now(); st.vel = 0; st.tiltVel = 0;
      canvas.setPointerCapture?.(e.pointerId);
      canvas.style.cursor = 'grabbing';
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
      let best = -1, bd = HOVER_PX;
      for (let i = 0; i < pos.length; i++) { const d = Math.hypot(pos[i].x - p.x, pos[i].y - p.y); if (d < bd) { bd = d; best = i; } }
      canvas.style.cursor = best >= 0 ? 'pointer' : 'grab';
      if (best !== hover) { hover = best; if (!animated) paintStill(); }
    };
    const onUp = (e: PointerEvent) => {
      if (!dragging) return;
      dragging = false;
      canvas.releasePointerCapture?.(e.pointerId);
      canvas.style.cursor = 'grab';
    };
    const onLeave = () => { if (!dragging && hover >= 0) { hover = -1; if (!animated) paintStill(); } };
    const onDbl = () => { st.angle = 0; st.tilt = TILT0; st.vel = 0; st.tiltVel = 0; if (!animated) paintStill(); };

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
  }, [galaxy, labels, animated, themeTick]);

  return <canvas ref={ref} aria-hidden="true" data-testid="login-topology"
    className="absolute inset-0 size-full select-none touch-none" />;
}
