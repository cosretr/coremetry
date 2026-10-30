import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';
import { ROW_PITCH } from '@/lib/topoLayout';
import { bfsBarycenterLayout, routeKind } from '@/lib/topoBfsLayout';
import { reachPath } from '@/lib/topoReach';
import type { ServiceMap, ServiceMapNode, ServiceMapEdge } from '@/lib/types';
import { isMessagingDep, fmtNum } from '@/lib/utils';
import { edgeWeights } from '@/lib/edgeWeight';
import { edgePathD, lanePathD, pathArrow, cubicD, cubicArrow, type TopoPt, type EdgeArrow } from '@/lib/topoArrow';
import { fitViewport, readableFit, zoomAt, zoomRange, READABLE_MIN_K, type Viewport } from '@/lib/topoViewport';
import { depPillLinesMap } from '@/lib/topoLabels';
import { Button } from '@/components/ui/Button';
import { useServicesMetadata } from '@/lib/queries';
import { seriesPalette } from '@/lib/chartFmt';
import { useThemeTick } from '@/lib/useThemeTick';
import {
  foldTopology, defaultCollapsed, parseNsFold, encodeNsFold,
  isNsNode, nsOfNodeId,
} from '@/lib/topoFold';
import { serviceBands, dependencyBands, bandServiceCounts } from '@/lib/topoBands';
import {
  focusColumnLayout, focusPath, edgeVisibility, focusColumnLabel,
  isMoreNode, moreNodeCol, HEADER_H, noCallersHeader, NO_CALLERS_LABEL, type EdgeClass,
} from '@/lib/topoFocusLayout';

// TopologyFlowGraph — prototipteki "pill düğüm + akış animasyonlu
// bezier kenar" topoloji görünümü. (Öncülü ServiceMapGraph'ın props
// sözleşmesini devraldı; o bileşen v0.9.1114'te söküldü.)
//
// Görsel dil — prototipten birebir:
//   • Düğümler HTML pill'leri (.topo-node): sağlık noktası + isim +
//     mono alt satır (req/s veya span sayısı). Dış bağımlılıklar
//     (db/queue/ext) kesikli çerçeve alır.
//   • Kenarlar SVG kübik bezier; kalınlık ∝ trafik, renk hata
//     durumunda --err. stroke-dasharray akış animasyonu
//     (globals.css'teki topo-edge-flow) canlı trafiği okutur;
//     yoğun kenar daha hızlı akar. prefers-reduced-motion global
//     kuralı animasyonu otomatik kapatır.
//   • Hover: 1-hop komşuluk dışındaki her şey .dim ile söner.
//   • Yerleşim (v0.10.1261): en uzun yol katmanlı soldan-sağa (kapalı
//     form, fizik yok) — her kenar kesin soldan sağa, aynı sütunda
//     birbirini çağıran servis yok; açık namespace'ler tam genişlikte
//     yatay şerit (lib/topoBfsLayout.ts). Döngüyü kapatan geri kenar
//     sağdan sola, noktalı desenle çizilir.
//   • v0.10.1269: >1 sütun atlayan ileri kenar, yerleşimin ara sütun
//     noktalarından (`routes`) geçen yatay-tanjantlı kübik zincirdir —
//     ara düğümlerin arkasından geçmez. Ok, offset-path ve RED chip'i aynı
//     yolu okur (chip yolun orta noktasında).
//
//   • v0.10.1294 — `columns` verilirse (servis Topology sekmesi ve
//     /service-map?focus=, FocusedNeighborhood) yerleşim ODAK SÜTUN
//     yerleşimidir (lib/topoFocusLayout): sabit sütunlar −2 | −1 | odak | +1 |
//     +2, odak ortada, komşu sütun kenarı tek eğri, peer/skip/back kenarlar
//     varsayılan gizli (`showSecondary`), sütun başına "+N more ▸" pili,
//     sütun başlıkları, namespace renk şeridi + ayraç + lejant (şerit/katlama
//     YOK — bayat ?nsfold= yok sayılır, yeniden yazılmaz). animate="hover":
//     akış kenarları durağan düz çizgi, kesik akış yalnız hover/yol vurgusunda.
//
//   • v0.10.1392 — `compact` (CoSRE sohbet kartı; operatör: "Bunu galiba eski
//     service map'ten getiriyor. Onun yerine 1 hop topology getirebilir."):
//     odak kipinin AYNI yerleşimi salt-okur — kutu içerik boyutunda (ölçek 1,
//     viewport/pan/tekerlek-zoom/zoom düğmeleri/lejant yok; sohbetin kaydırması
//     kaçırılmaz), "+N more" tıklanamaz etiket. Taşma çağıranın kabında kayar.
//
//   • v0.10.1305 — Genel servis haritası: sabit sütun, düz zincir, statik
//     kenar. Global yerleşim sütunları sabit 240 px (görünüme gerilmez),
//     şerit hücreleri üstten hizalı, uzun kenar zinciri düzleştirilir
//     (lib/topoBfsLayout routeKind: 'single' → tek eğri, 'lane' → lanePathD).
//     animate varsayılanı artık 'hover' (her çağıran): kenarlar durağan,
//     kesik akış yalnız vurguda. Genel haritada hover YOL vurgusu
//     (lib/topoReach reachPath: ±2 hop, ileri kenarlar, ≤60 düğüm). >150
//     servisli haritada ≥3 üyeli her namespace katlı açılır (lib/topoFold
//     AUTO_FOLD_ALL_GRAPH); "Tümünü aç" ?nsfold=- yazar.
//
// CSS bağımlılıkları globals.css'te zaten mevcut: .topo, .topo-edges,
// .topo-node (+ .focus/.dim), .topo-name, .topo-sub, .topo-dot,
// @keyframes topo-edge-flow. Tek ekleme (aşağıya bakın):
//   .topo-node.ext { border-style: dashed; background: var(--bg0); }

// v0.10.929 (K5) — "yeni" (baseline'dan beri) kenarın kesik deseni: uzun
// çizgi + kısa boşluk, normal akışın kısa kesiklerinden (4 6) okunur biçimde
// ayrı. Periyot 16 = @keyframes topo-edge-flow'un dashoffset'i (-16) → akış
// döngüsü dikişsiz. Inline style gerekli: sınıfın stroke-dasharray'ini SVG
// öznitelik değil yalnız inline stil ezer.
const NEW_EDGE_DASH = '12 4';

export function TopologyFlowGraph({
  data: rawData, focus, hoverNode, onHoverNode, onSelectNode, height = 560,
  dropMessaging = true, columns, animate = 'hover', showSecondary = false, onExpandColumn, legend, compact = false,
}: {
  data: ServiceMap;
  focus: string | null;
  hoverNode: string | null;
  onHoverNode: (s: string | null) => void;
  onSelectNode: (s: string) => void;
  height?: number;
  // /topology'nin odaklı 1-hop görünümü kuyrukları GÖSTERİR (operatör
  // servisi zaten seçti, hairball riski yok) — false geçer. Global
  // /service-map varsayılanı korur: broker düğümleri düşer.
  dropMessaging?: boolean;
  /** v0.10.1294 — odak sütunları (assignFocusColumns, "+N more" dahil): Focus Column Layout */
  columns?: ReadonlyMap<string, number>;
  /** 'hover' = akış kenarları durağan; kesik akış animasyonu yalnız vurguda */
  animate?: 'always' | 'hover';
  /** odak kipinde peer/skip/back kenarlar ("Yan bağlantılar") */
  showSecondary?: boolean;
  /** odak kipinde "+N more ▸" pili tıklandı */
  onExpandColumn?: (col: number) => void;
  /** odak kipi lejant satırı (sağlık + kenar türleri); ns renkleri buraya eklenir */
  legend?: ReactNode;
  /** v0.10.1392 — salt-okur kompakt kip (sohbet kartı): içerik boyutu, ölçek 1, sayfa kontrolü yok */
  compact?: boolean;
}) {
  const focusMode = !!columns && !!focus;
  const wrapRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(900);

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(entries => {
      for (const e of entries) setWidth(Math.max(400, e.contentRect.width));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Mesajlaşma broker'larını düşür — ServiceMapGraph'taki davranışın aynısı
  // (broadcast topic'ler topolojiyi hairball'a çevirir; messaging'in kendi
  // yüzeyi var).
  const preFold = useMemo<ServiceMap>(() => {
    if (!dropMessaging) return rawData;
    if (!rawData.nodes.some(n => isMessagingDep(n.kind, n.subkind))) return rawData;
    const drop = new Set(
      rawData.nodes.filter(n => isMessagingDep(n.kind, n.subkind)).map(n => n.service),
    );
    return {
      ...rawData,
      nodes: rawData.nodes.filter(n => !drop.has(n.service)),
      edges: rawData.edges.filter(e => !drop.has(e.caller) && !drop.has(e.callee)),
    };
  }, [rawData]);

  // ── v0.8.447 (operatör-seçimi Varyant C) — katlanabilir namespace
  // grup kutuları. Katlanmış namespace'in üyeleri layout'tan ÖNCE tek
  // süper-düğüme iner (pure çekirdek lib/topoFold.ts, vitest-pinli) —
  // BFS/barycenter yerleşimi, zoom/pan ve hover kodu küçülmüş düz bir
  // graf görür. Katla/aç state'i URL'de (?nsfold=, house rule §4):
  // param yokken kalabalık haritada büyük namespace'ler otomatik katlı
  // başlar (operatör şikâyeti "çok deployment'lı topolojide hull'lar
  // küçük kalıyor"); herhangi bir toggle açık listeyi yazar, kopyalanan
  // link aynı görünümü açar. Focus'lu servisin namespace'i asla
  // katlanmaz (1-hop görünümde seçili düğüm kaybolmasın).
  const metaQ = useServicesMetadata();
  const nsOf = useCallback(
    (svc: string) => (metaQ.data ?? {})[svc]?.namespace || undefined,
    [metaQ.data],
  );
  const [params, setParams] = useSearchParams();
  const explicitFold = useMemo(() => parseNsFold(params.get('nsfold')), [params]);
  // v0.10.1294 — odak kipinde katlama YOK (sütun başına "+N more" onun
  // yerini aldı); bayat ?nsfold= yok sayılır, URL'e geri yazılmaz. Global
  // haritada odak hiç yok (focus=null) — v0.10.1265'in odak-ns istisnası
  // ölü kod olarak kalktı.
  const collapsed = useMemo(
    () => (focusMode ? NO_FOLD : explicitFold ?? defaultCollapsed(preFold, nsOf)),
    [focusMode, explicitFold, preFold, nsOf],
  );
  const { data, groups, bundled } = useMemo(
    () => foldTopology(preFold, nsOf, collapsed),
    [preFold, nsOf, collapsed],
  );

  // v0.10.837 (Operatör-bildirimli, prod: "altı Oracle düğümü, hepsinin
  // başlığı aynı") — bağımlılık pili etiketleri KÜME-DUYARLI hesaplanır.
  // Tek düğüme bakan depPillLines "bu db.name ayırt edici mi?" sorusunu
  // cevaplayamıyordu; aynı db.name farklı sunucularda tekrarlanınca altı pil
  // aynı yazıyı taşıyordu. Eşlem render edilen KÜMENİN tamamından doğar
  // (katlama sonrası `data.nodes`) — bu yüzden TopologyFlowGraph'ın her
  // çağrı yeri (/service-map ve servis Topology sekmesi) aynı yoldan geçer.
  const depPills = useMemo(
    () => depPillLinesMap(data.nodes, n => depLabel(n.kind!)),
    [data.nodes],
  );
  const writeFold = (next: ReadonlySet<string>) => setParams(prev => {
    const p = new URLSearchParams(prev);
    p.set('nsfold', encodeNsFold(next));
    return p;
  }, { replace: true });
  const collapseNs = (ns: string) => writeFold(new Set([...collapsed, ns]));
  // v0.10.1305 — "Tümünü aç": boş küme '-' (açık "hiçbiri katlı değil").
  const expandAll = () => writeFold(new Set());
  const expandNs = (ns: string) => {
    const c = new Set(collapsed);
    c.delete(ns);
    writeFold(c);
  };

  // Deterministik namespace rengi — TÜM (katlı + açık) ≥2 üyeli
  // namespace'lerin sıralı listesi üzerinden indekslenir ki bir grubun
  // katlanması diğerlerinin rengini kaydırmasın.
  // v0.10.929 (K5) — ad alanı bir KATEGORİ: durum tokenları (--warn/--ok)
  // yerine seri paleti (tema-farkında hex; tema değişince yeniden çözülür).
  const themeTick = useThemeTick();
  const nsColor = useMemo(() => {
    const meta = metaQ.data ?? {};
    const counts = new Map<string, number>();
    for (const n of preFold.nodes) {
      if (n.kind) continue;
      const ns = meta[n.service]?.namespace;
      if (ns) counts.set(ns, (counts.get(ns) ?? 0) + 1);
    }
    const palette = seriesPalette();
    const m = new Map<string, string>();
    [...counts.entries()].filter(([, c]) => c >= 2).map(([ns]) => ns).sort()
      .forEach((ns, i) => m.set(ns, palette[i % palette.length]));
    return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps -- themeTick: palet tema değişiminde yeniden çözülsün
  }, [preFold.nodes, metaQ.data, themeTick]);

  // Çift yönlü kenarları tek çizgiye indir (A→B + B→A) — ServiceMapGraph deseni.
  type RenderedEdge = { forward: ServiceMapEdge; reverse?: ServiceMapEdge };
  const renderedEdges = useMemo<RenderedEdge[]>(() => {
    const byKey = new Map<string, RenderedEdge>();
    for (const e of data.edges) {
      const canon = e.caller < e.callee ? `${e.caller}|${e.callee}` : `${e.callee}|${e.caller}`;
      const ex = byKey.get(canon);
      if (!ex) byKey.set(canon, { forward: e });
      else ex.reverse = e;
    }
    return Array.from(byKey.values());
  }, [data.edges]);

  // Hover sönümü için 1-hop komşuluk.
  const neighbours = useMemo(() => {
    const m = new Map<string, Set<string>>();
    for (const n of data.nodes) m.set(n.service, new Set([n.service]));
    for (const e of data.edges) {
      m.get(e.caller)?.add(e.callee);
      m.get(e.callee)?.add(e.caller);
    }
    return m;
  }, [data]);
  // v0.8.447 review-fix: hover id'si mevcut grafta yoksa hover YOK say.
  // Eski fallback (tek elemanlı set) katlı süper-düğüm genişletilince
  // unmount olur, mouseleave hiç ateşlenmez ve bayat sentetik id her
  // düğümü .dim'e, her kenarı 0.12 opaklığa kilitliyordu. neighbours
  // render edilen HER düğüm için kayıt taşır — fallback yalnız bayat
  // id durumunda tetiklenirdi, güvenle null'a iner.
  const oneHop = hoverNode ? (neighbours.get(hoverNode) ?? null) : null;

  // ── Katmanlı + namespace şeritli yerleşim ───────────────────────────
  // v0.10.1261 (operatör 2026-10-17: "aynı sütunda birbirini çağıran
  // servisler" + iç içe namespace kutuları) — sütun = döngüsü kırılmış
  // DAG'de en uzun yol katmanı; AÇIK namespace (≥2 üye, hull kuralı) tam
  // genişlikte yatay şerit, üyeleri şeridin içinde. Grupsuzlar (bağımlılık,
  // ns'siz, katlı süper-düğüm) en alttaki şeritte. Odaklı servisin şeridi
  // en üstte. Saf çekirdek + değişmez testleri: lib/topoBfsLayout(.test).ts.
  //
  // v0.8.296 — yerleşim artık VIEW alanına değil LAYOUT alanına açılır:
  // kalabalık bir sütun (ör. 30 servis) 560px'e sıkışıp pill'leri üst üste
  // bindirmek yerine sütun başına ~46px satır yüksekliğiyle büyür; viewport
  // transform'u (fit/zoom/pan) bu alanı ekrana sığdırır. Operatör-raporlu
  // "çok fazla servis ekrana sığmıyor" düzeltmesinin yarısı budur (diğer
  // yarısı zoom/pan).
  // Şerit üyeliği hull kuralının aynısı: kind'lı bağımlılık ve katlı
  // süper-düğüm serbest; namespace'i grafta ≥2 servisle temsil edilen
  // servis o namespace'in şeridine girer.
  // v0.10.1268 — operatör 2026-10-17: tek namespace'in bağımlılıkları o
  // namespace şeridinde. Tüm çağıranları aynı adlı şeritte olan db/queue/ext
  // o şeride girer; karışık/grupsuz çağıranlı olan grupsuz kalır. Saf
  // çekirdek + testler: lib/topoBands(.test).ts. Başlık sayısı (`svcCount`)
  // yalnız servisleri sayar — bağımlılık "N svc"yi şişirmez.
  const { bandNs, svcCount } = useMemo(() => {
    if (focusMode) return { bandNs: new Map<string, string>(), svcCount: new Map<string, number>() };
    const meta = metaQ.data ?? {};
    const svc = serviceBands(data.nodes, s => meta[s]?.namespace || undefined);
    const all = new Map(svc);
    for (const [dep, ns] of dependencyBands(data.nodes, data.edges, svc)) all.set(dep, ns);
    return { bandNs: all, svcCount: bandServiceCounts(svc) };
  }, [focusMode, data.nodes, data.edges, metaQ.data]);

  // v0.10.1294 — odak kipi: Focus Column Layout (saf çekirdek + değişmez
  // testleri lib/topoFocusLayout(.test).ts). Genişlik/yükseklik İÇERİKTEN
  // (sütunlar gerilmez); viewport sığdırır.
  const focusLayout = useMemo(
    () => (focusMode
      ? focusColumnLayout({ nodes: data.nodes, edges: data.edges, colOf: columns!, focus: focus!, nsOf, pitch: ROW_PITCH })
      : null),
    [focusMode, data, columns, focus, nsOf],
  );

  const { positioned, layoutW, layoutH, bands, backEdges, routes } = useMemo(() => {
    if (focusLayout) {
      return {
        positioned: focusLayout.pos, layoutW: focusLayout.lw, layoutH: focusLayout.lh,
        bands: [], backEdges: new Set<string>(), routes: new Map<string, TopoPt[]>(),
      };
    }
    // v0.10.133 — komşuluk indeksi, O(E + n log n) (perf probe 500/20k).
    // v0.10.1261 — katman + şerit; aynı karmaşıklık sınıfı.
    const { pos, lw, lh, bands: bs, backEdges: be, routes: rt } = bfsBarycenterLayout(
      data.nodes, data.edges, 0, 0, ROW_PITCH,
      { bandOf: s => bandNs.get(s) },
    );
    return { positioned: pos, layoutW: lw, layoutH: lh, bands: bs, backEdges: be, routes: rt };
    // v0.10.1305 — yerleşim içerikten (sabit sütun); width/height verilmez →
    // pencere yeniden boyutlanınca 500/20k yeniden yerleşmez.
  }, [focusLayout, data, bandNs]);

  // v0.10.1294 — yol vurgusu (odak kipi): hover edilen düğümden odağa giden
  // flow zinciri (2-hop → ±1 ebeveyn → odak). Hover başına bir BFS (≤61 düğüm).
  // v0.10.1305 — genel haritada da yol: reachPath (±2 hop, ileri kenarlar,
  // ≤60 düğüm; lib/topoReach).
  const path = useMemo(() => {
    if (!hoverNode || !oneHop) return null;
    if (focusMode) return focusPath(hoverNode, data.edges, columns!);
    return reachPath(hoverNode, data.edges, backEdges);
  }, [focusMode, hoverNode, oneHop, data.edges, columns, backEdges]);
  const active = oneHop && path ? new Set([...oneHop, ...path.nodes]) : oneHop;

  // v0.10.1269 — operatör 2026-10-17: çok sütun atlayan kenarlar düğümlerin
  // arkasından geçiyordu. Her çizilen kenarın nokta dizisi (a, ara sütun
  // noktaları, b) ve path `d`si bir kez hesaplanır; çizgi, ok offset-path'i,
  // ok yedek konumu ve RED chip'i (yolun ortası) AYNI geometriyi okur.
  // Çift yönlü çizgide ileri yön geri kenar olabilir — o zaman ters yönün
  // rotası tersine çevrilerek kullanılır. Rota yoksa iki nokta = eski eğri.
  //
  // v0.10.1294 — odak kipinde geometri yerleşimden gelir: uçlar pil kenarında
  // (port), skip/back tek şerit noktası, peer dış olukta tek kübik yay (ctrl).
  type Geom = { pts: TopoPt[]; d: string; cls?: EdgeClass; ctrl?: [TopoPt, TopoPt] };
  const edgeGeom = useMemo<(Geom | null)[]>(() => renderedEdges.map(re => {
    const e = re.forward;
    if (focusLayout) {
      const g = focusLayout.edges.get(`${e.caller}|${e.callee}`);
      if (!g) return null;
      const d = g.ctrl ? cubicD(g.pts[0], g.ctrl[0], g.ctrl[1], g.pts[1]) : edgePathD(g.pts);
      return { pts: g.pts, d, cls: g.cls, ctrl: g.ctrl };
    }
    const a = positioned.get(e.caller), b = positioned.get(e.callee);
    if (!a || !b) return null;
    const fwd = routes.get(`${e.caller}|${e.callee}`);
    const rev = !fwd && re.reverse ? routes.get(`${re.reverse.caller}|${re.reverse.callee}`) : undefined;
    const mid = fwd ?? (rev ? [...rev].reverse() : []);
    const pts: TopoPt[] = [a, ...mid, b];
    // v0.10.1305 — düzleşmiş zincir a→b doğrusuna yakınsa tek eğri; değilse
    // şerit yolu (≤2 büküm). pts çizilen yolla aynı → ok/chip aynı geometri.
    if (routeKind(pts, ROW_PITCH) === 'single') {
      const two: TopoPt[] = [a, b];
      return { pts: two, d: edgePathD(two) };
    }
    return { pts, d: lanePathD(pts) };
  }), [renderedEdges, positioned, routes, focusLayout]);

  // ── v0.8.296 — zoom/pan viewport ─────────────────────────────────────
  // Transform matematiği pure seam'de (lib/topoViewport.ts, vitest-pinli).
  // Fit-guard: yalnız düğüm-kümesi imzası (veya boyutlar) değişince auto-Fit —
  // 30sn'lik refetch operatörün zoom/pan'ını asla sıfırlamaz.
  // v0.8.544 — OPEN at a readable scale, not a fitted one. fitViewport has
  // no floor, so a busy neighbourhood used to open at k≈0.4 and read as one
  // clump. readableFit lets the graph overflow instead; kMin below still
  // rides the true fit, so zooming out to "everything" stays available, and
  // ⛶ jumps straight there.
  // v0.10.1294 — odak kipinde açılış ODAĞI ortalar (readableFit centerOn).
  // v0.10.1307 — yerleşim artık asimetrik (her taraf kendi hop sayısı kadar
  // sütun): odak içerik ortasında değil, readableFit sığan içerikte de onu
  // görünümün ortasına koyar (kenara kıstırılı).
  const centerOn = focusLayout?.focusPt;
  const noCallers = focusLayout ? noCallersHeader(focusLayout) : null;
  const openFit = () => readableFit(layoutW, layoutH, width, height, 24, READABLE_MIN_K, centerOn);
  const [vp, setVp] = useState<Viewport>(openFit);
  const fitK = useMemo(() => fitViewport(layoutW, layoutH, width, height).k, [layoutW, layoutH, width, height]);
  const { kMin, kMax } = zoomRange(fitK);
  const sig = useMemo(
    () => data.nodes.map(n => n.service).sort().join('|') + `@${layoutW}x${layoutH}:${width}x${height}`,
    [data.nodes, layoutW, layoutH, width, height],
  );
  useEffect(() => {
    setVp(openFit());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig]);

  // Wheel zoom — native listener: React'in onWheel'i passive olduğundan
  // preventDefault sayfa scroll'unu ancak böyle keser.
  useEffect(() => {
    const el = wrapRef.current;
    // v0.10.1392 — kompakt kipte tekerlek sohbetin/kabın kaydırmasıdır.
    if (!el || compact) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      const factor = Math.exp(-e.deltaY * 0.002);
      const { kMin: lo, kMax: hi } = zoomRange(fitViewport(layoutW, layoutH, r.width, height).k);
      setVp(v => zoomAt(v, e.clientX - r.left, e.clientY - r.top, factor, lo, hi));
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [layoutW, layoutH, height, compact]);

  // Drag pan — pill üstünde başlamaz (tıklama/focus davranışı bozulmaz).
  const [panning, setPanning] = useState(false);
  const panRef = useRef<{ px: number; py: number } | null>(null);
  const onPointerDown = (e: React.PointerEvent) => {
    if ((e.target as HTMLElement).closest('.topo-node, .topo-zoomctl, .topo-nshdr, .topo-more, .topo-foldbar')) return;
    panRef.current = { px: e.clientX, py: e.clientY };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    setPanning(true);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    const p = panRef.current;
    if (!p) return;
    const dx = e.clientX - p.px, dy = e.clientY - p.py;
    panRef.current = { px: e.clientX, py: e.clientY };
    setVp(v => ({ ...v, x: v.x + dx, y: v.y + dy }));
  };
  const endPan = () => { panRef.current = null; setPanning(false); };
  const zoomStep = (factor: number) => {
    const el = wrapRef.current;
    const w = el?.clientWidth ?? width;
    setVp(v => zoomAt(v, w / 2, height / 2, factor, kMin, kMax));
  };

  // v0.8.281 — trafik ağırlığı: örneklenmiş global görünümde traceCount
  // (eski davranış birebir), MV-backed focus görünümünde spanCount (orada
  // traceCount hep 0 → tüm kenarlar minimum kalınlıkta çiziliyordu).
  const { weightOf, max: edgeMax } = useMemo(() => edgeWeights(data.edges), [data.edges]);

  // v0.8.437 (Varyant A) → v0.8.447 (Varyant C): AÇIK namespace'ler
  // hâlâ yarı saydam kutuyla sarılır ama etiket artık TIKLANABİLİR
  // başlık — katlar. Katlı olanlar burada hiç görünmez (üyeleri
  // fold'da süper-düğüme indi). Kurallar aynı: ≥2 üye, kind'lı
  // bağımlılıklar ve namespace'siz servisler SERBEST (operatör
  // kararı: "diğer" kovası yok). Renk nsColor'dan — katla/aç
  // toggle'ı diğer grupların rengini kaydırmaz.
  // v0.10.1261 — kutu artık üye sınırlayıcı kutusu DEĞİL, şeridin kendisi:
  // tam genişlik, şeritler çakışmaz → iç içe/üst üste binen kutu yok.
  const hulls = useMemo(() => {
    const INSET = 6;
    return bands
      .filter((b): b is typeof b & { key: string } => b.key !== null)
      .map(b => ({
        ns: b.key,
        n: svcCount.get(b.key) ?? 0,
        x: INSET,
        y: b.y0,
        w: layoutW - 2 * INSET,
        h: b.y1 - b.y0,
        color: nsColor.get(b.key) ?? 'var(--accent)',
      }));
  }, [bands, layoutW, nsColor, svcCount]);

  // Yoğunluk kapısı (ServiceGraph slice-4 kuralının aynısı): az kenarlı
  // grafikte chip'ler hep açık, kalabalıkta yalnız hover'da.
  const labelAlways = renderedEdges.length < 8;

  // v0.10.1294 — odak kipi: yol vurgusundaki kenar (iki yön anahtarı da).
  const onPath = (re: RenderedEdge) => !!path && (
    path.edges.has(`${re.forward.caller}|${re.forward.callee}`) ||
    (!!re.reverse && path.edges.has(`${re.reverse.caller}|${re.reverse.callee}`)));
  // Kenarın görünürlük çarpanı (null = çizilmez): global haritada hep 1.
  const visOf = (g: Geom, hot: boolean) => (g.cls ? edgeVisibility(g.cls, { showSecondary, incident: hot }) : 1);
  // Ok/chip konumu kenarın KENDİ geometrisinden (peer yayı ya da nokta zinciri).
  const geomAt = (g: Geom, t: number, rev = false): EdgeArrow =>
    (g.ctrl ? cubicArrow(g.pts[0], g.ctrl[0], g.ctrl[1], g.pts[1], t, rev) : pathArrow(g.pts, t, rev));
  // Namespace renk şeridi + lejant (odak kipi): yalnız renkli (≥2 üyeli) ns.
  // v0.10.1392 — kompakt kip: sabit görünüm (ölçek 1, öteleme yok).
  const view = compact ? COMPACT_VIEW : vp;
  const nsLegend = useMemo(() => {
    if (!focusMode) return [];
    const seen = new Set<string>();
    for (const n of data.nodes) {
      if (n.kind || isMoreNode(n.service)) continue;
      const ns = nsOf(n.service);
      if (ns && nsColor.has(ns)) seen.add(ns);
    }
    return [...seen].sort().map(ns => ({ ns, color: nsColor.get(ns)! }));
  }, [focusMode, data.nodes, nsOf, nsColor]);

  return (
    <div
      ref={wrapRef}
      className={'topo ' + (compact ? 'compact' : panning ? 'panning' : 'pannable') + (focusMode ? ' focus-cols' : '') + (animate === 'hover' ? ' anim-hover' : '')}
      style={compact ? { height: layoutH, width: layoutW } : { height }}
      onPointerDown={compact ? undefined : onPointerDown}
      onPointerMove={compact ? undefined : onPointerMove}
      onPointerUp={compact ? undefined : endPan}
      onPointerCancel={compact ? undefined : endPan}
      onDoubleClick={compact ? undefined : e => {
        if ((e.target as HTMLElement).closest('.topo-node, .topo-zoomctl, .topo-nshdr, .topo-more, .topo-foldbar')) return;
        const r = e.currentTarget.getBoundingClientRect();
        const cx = e.clientX - r.left, cy = e.clientY - r.top;
        setVp(v => zoomAt(v, cx, cy, 1.5, kMin, kMax));
      }}>
      <div
        className="topo-viewport"
        style={{ width: layoutW, height: layoutH, transform: `translate(${view.x}px, ${view.y}px) scale(${view.k})` }}>
      <svg className="topo-edges" width={layoutW} height={layoutH}>
        {hulls.map(h => (
          <rect key={`hull-${h.ns}`} x={h.x} y={h.y} width={h.w} height={h.h} rx={12}
            fill={`color-mix(in srgb, ${h.color} 7%, transparent)`}
            stroke={`color-mix(in srgb, ${h.color} 30%, transparent)`}
            strokeWidth={1} />
        ))}
        {/* v0.10.1294 — odak kipi ns ayracı: ±1 sütununda ≥2 üyeli bitişik
            koşunun yanında ince köşeli çizgi (şerit/kutu yok). */}
        {focusLayout?.brackets.map(b => (
          <path key={`br-${b.col}-${b.ns}-${b.y0}`} className="topo-nsbracket"
            d={`M ${b.x - b.side * 4} ${b.y0} L ${b.x} ${b.y0} L ${b.x} ${b.y1} L ${b.x - b.side * 4} ${b.y1}`}
            stroke={nsColor.get(b.ns) ?? 'var(--text3)'} fill="none" />
        ))}
        {renderedEdges.map((re, i) => {
          const e = re.forward;
          const g = edgeGeom[i];
          if (!g) return null;
          const { d } = g;
          const t = (weightOf(e) + weightOf(re.reverse)) / edgeMax;
          const w = 1 + 2.2 * t;
          const errorish = e.errorCount > 0 || (re.reverse?.errorCount ?? 0) > 0;
          // Hover vurgusu: hover edilen düğüme DOĞRUDAN bağlı kenarlar maviye
          // döner ve kalınlaşır. Semantik renk hiyerarşisi: hata kırmızısı >
          // hover mavisi > nötr — hatalı kenar hover'da kırmızı KALIR.
          const incident = !!hoverNode && (e.caller === hoverNode || e.callee === hoverNode);
          const hot = incident || onPath(re);
          const vis = visOf(g, incident);
          if (vis === null) return null;
          const dimmed = active && (!active.has(e.caller) || !active.has(e.callee));
          // Demet sayıları yön başına tutulur (fold sonrası key'ler yönlü);
          // çift yönlü çizgide ters yönün demeti de raporlanır.
          const fwdBundle = bundled.get(`${e.caller}|${e.callee}`) ?? 0;
          const revBundle = re.reverse ? (bundled.get(`${re.reverse.caller}|${re.reverse.callee}`) ?? 0) : 0;
          // v0.10.929 (K5) — "yeni" kenar bir değişim, iyileşme değil: vurgu --accent2.
          // Hiyerarşi: hata kırmızısı > yeni (accent2) > hover mavisi > nötr —
          // YENİ ama hatalı kenar kırmızı kalır (sapma, değişimden önce gelir).
          // accent2 light/redhat temalarında hover'ın --accent'ine çok yakın;
          // bu yüzden yeni kenar RENKTEN bağımsız, kendi kesik deseniyle de
          // ayrışır (hatalı olsa bile "yeni" bilgisi kaybolmasın).
          const stroke = errorish ? 'var(--err)' : e.isNew ? 'var(--accent2)' : hot ? 'var(--accent)' : 'var(--border-strong)';
          const newDash = e.isNew ? NEW_EDGE_DASH : undefined;
          // v0.10.1261 — döngüyü kapatan geri kenar (sağdan sola) noktalı
          // desen alır (.topo-edge-flow.back). Çift yönlü çizgide zaten iki
          // ok var; orada ayrıca işaretlenmez.
          // v0.10.1294 — odak kipinde sınıf yerleşimden (pairClass: çift yönlü
          // çift asla back değil).
          const back = g.cls ? g.cls === 'back' : !re.reverse && backEdges.has(`${e.caller}|${e.callee}`);
          // vis < 1 = hover edilen düğümün kendi ikincil kenarı (soluk).
          const opacity = (dimmed ? 0.12 : hot ? 0.95 : errorish ? 0.85 : 0.55) * vis;
          // animate="hover": yalnız vurgulu kenar akar (globals.css .anim-hover).
          const live = animate === 'hover' && hot ? ' live' : '';
          // peer: ince, yan bilgi.
          const sw = g.cls === 'peer' ? 1 : hot ? w + 0.8 : w;
          // v0.9.1031 — yön oku (operatör isteği). Uca (t=1) konan ok HTML
          // pilinin ALTINDA kalır (düğümler SVG'nin üstünde); hedefe yakın
          // t=0.78 kolon boşluğunda görünür ve t=0.5'teki RED chip'iyle
          // çakışmaz. Çift yönlü kenar kaynak ucunda (t=0.22) ters ok alır.
          // Boyut kenar kalınlığıyla ölçekli; pointer-events yok (dekor).
          // v0.10.1269 — rotalı kenarda ok yolun kendisi üzerinde (pathArrow;
          // iki noktada edgeArrow'un aynısı).
          const arrows = [geomAt(g, 0.78)];
          if (re.reverse) arrows.push(geomAt(g, 0.22, true));
          const as = 3.2 + 1.6 * sw;
          return (
            <g key={i}>
            <path
              d={d}
              fill="none"
              stroke={stroke}
              strokeWidth={sw}
              opacity={opacity}
              className={(back ? 'topo-edge-flow back' : 'topo-edge-flow') + live}
              style={{ animationDuration: `${(2.8 - 1.8 * t).toFixed(2)}s`, transition: 'opacity 120ms, stroke 120ms, stroke-width 120ms', strokeDasharray: newDash }}>
              <title>
                {`${e.caller} → ${e.callee}\n${e.traceCount} traces · ${e.spanCount} spans` +
                  (e.errorCount > 0 ? ` · ${e.errorCount} errors` : '') +
                  (re.reverse ? `\n${re.reverse.caller} → ${re.reverse.callee} (çift yönlü)` : '') +
                  (fwdBundle > 1 ? `\n${fwdBundle} kenar demetlendi (katlı grup)` : '') +
                  (revBundle > 1 ? `\n${revBundle} kenar demetlendi (ters yön, katlı grup)` : '') +
                  (back ? '\n↺ döngü kenarı (sağdan sola)' : '') +
                  (g.cls && g.cls !== 'flow' ? `\n${SECONDARY_LABEL[g.cls]}` : '') +
                  (e.isNew ? '\n[NEW since baseline]' : '')}
              </title>
            </path>
            {arrows.map((ar, j) => (
              // v0.10.729 — ok kenarı boyunca akar (globals.css topo-edge-arrow).
              // Konum/açı CSS değişkenlerinde: offset-path destekleyen tarayıcı
              // yolu kullanır, desteklemeyen aynı noktada sabit çizer.
              // Gecikme kenar indeksinden türer (deterministik): yakınsayan
              // kenarların okları aynı fazda olmasın, hiza dağılsın.
              <path key={`ar-${j}`} className={(j === 0 ? 'topo-edge-arrow' : 'topo-edge-arrow rev') + live}
                d={`M ${-as * 0.9} ${-as * 0.62} L ${as * 0.9} 0 L ${-as * 0.9} ${as * 0.62} Z`}
                fill={stroke} opacity={opacity} pointerEvents="none"
                style={{
                  transition: 'opacity 120ms, fill 120ms',
                  ['--ax' as string]: ar.x, ['--ay' as string]: ar.y, ['--aa' as string]: `${ar.angle}deg`,
                  offsetPath: `path("${d}")`,
                  ['--arw-dur' as string]: `${(2.8 - 1.8 * t).toFixed(2)}s`,
                  ['--arw-delay' as string]: `-${(((i * 37) % 100) / 100 * (2.8 - 1.8 * t)).toFixed(2)}s`,
                }} />
            ))}
            </g>
          );
        })}
      </svg>

      {/* Namespace kutu başlıkları — tıkla: grubu tek karta katla
          (v0.8.447). Pan bu elemanda başlamaz (onPointerDown istisnası).
          Focus'lu servisin namespace'i katlanamaz (derivasyon geri
          açardı) — o başlık buton semantiği olmadan salt etiket kalır. */}
      {hulls.map(h => (
        <div key={`nshdr-${h.ns}`} className="topo-nshdr"
          style={{ left: h.x + 10, top: h.y + 6, color: h.color }}
          // eslint-disable-next-line ui/no-raw-element -- tuval üstü hull etiketi: .topo-nshdr boyar ve pan istisnası closest('.topo-nshdr') ile; DisclosureButton dolgu/hover'ı etiket çerçevesiyle çakışır
          role="button" tabIndex={0}
          title={`ns: ${h.ns} — tıkla: ${h.n} servisi tek karta katla`}
          onClick={() => collapseNs(h.ns)}
          onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') collapseNs(h.ns); }}>
          ns: {h.ns} · {h.n} svc <span className="caret">▾</span>
        </div>
      ))}

      {/* v0.8.281 — kenar RED chip'i: hacim/dk · p99 (· err%). Veri yalnız
          MV yolunda var (adapter enrichment) — örneklenmiş global görünümde
          p99Ms undefined kalır ve chip hiç çizilmez. Yoğunluk kapısı:
          labelAlways (<8 kenar) veya hover. pointer-events yok — dekor. */}
      {renderedEdges.map((re, i) => {
        const e = re.forward;
        if (e.p99Ms == null) return null;
        const g = edgeGeom[i];
        if (!g) return null;
        const incident = !!hoverNode && (e.caller === hoverNode || e.callee === hoverNode);
        const hot = incident || onPath(re);
        if (visOf(g, incident) === null) return null; // gizli ikincil kenar
        const dimmed = active && (!active.has(e.caller) || !active.has(e.callee));
        if (dimmed || (!labelAlways && !hot)) return null;
        const errPct = (e.errorRate ?? 0) * 100;
        // v0.9.1112 (Faz 5) — compare=prior'da kenar p99 Δ'sı. Göreli
        // (%) — /services ve /endpoints p99Delta ile aynı dil. ±%5
        // altı gürültü sayılır ve basılmaz (TrendDelta eşiğiyle aynı).
        const dPct = e.priorP99Ms && e.priorP99Ms > 0
          ? ((e.p99Ms ?? 0) - e.priorP99Ms) / e.priorP99Ms * 100
          : null;
        // v0.10.1269 — chip yolun ORTA noktasında (rotalı kenarda düz
        // a–b ortası düğüm üstüne düşebilirdi; iki noktada aynı yer).
        const m = geomAt(g, 0.5);
        return (
          <div key={`chip-${i}`} className={'topo-chip' + (hot ? ' hot' : '')}
            style={{ left: m.x, top: m.y }}>
            {fmtNum(Math.round(e.rate ?? 0))}/dk · p99 {fmtMs(e.p99Ms)}
            {errPct >= 1 && <span className="chip-err"> · {errPct.toFixed(1)}% err</span>}
            {dPct != null && Math.abs(dPct) >= 5 && (
              <span style={{ color: dPct > 0 ? 'var(--err)' : 'var(--text2)' /* v0.10.929 (K5) — iyileşme nötr */ }}
                title={`p99 önceki pencereye göre ${dPct > 0 ? 'kötüleşti' : 'iyileşti'}`}>
                {' '}· Δ{dPct > 0 ? '+' : ''}{dPct.toFixed(0)}%
              </span>
            )}
          </div>
        );
      })}

      {/* v0.10.1294 — sütun başlıkları ("Çağıranlar · 2 hop" … "Bağımlılıklar")
          + ns ayraç etiketleri; dekor, pointer-events yok. */}
      {focusLayout?.columns.map(c => (
        <div key={`colhdr-${c.col}`} className="topo-colhdr" style={{ left: c.x, top: HEADER_H / 2 }}>
          {focusColumnLabel(c.col)}{c.col !== 0 ? ` · ${c.count}` : ''}
        </div>
      ))}
      {/* v0.10.1307 — çağıran tarafı hiç yer almıyor; boşluk "eksik" diye
          okunmasın diye odağın solunda tek soluk not. */}
      {noCallers && (
        <div className="topo-colhdr topo-colhdr-empty" style={{ left: noCallers.x, top: HEADER_H / 2, width: noCallers.w }}>
          {NO_CALLERS_LABEL}
        </div>
      )}
      {focusLayout?.brackets.map(b => (
        <div key={`brl-${b.col}-${b.ns}-${b.y0}`} className={'topo-nsbracket-label' + (b.side < 0 ? ' left' : '')}
          style={{ left: b.x + b.side * 3, top: (b.y0 + b.y1) / 2, color: nsColor.get(b.ns) ?? 'var(--text3)' }}>
          {b.ns}
        </div>
      ))}

      {data.nodes.map(n => {
        const p = positioned.get(n.service);
        if (!p) return null;
        // v0.10.1294 — sütun başına "+N more ▸" pili: tıkla → o sütunu aç
        // (?texp=, FocusedNeighborhood). Hover inspector'a girmez.
        if (isMoreNode(n.service)) {
          const c = moreNodeCol(n.service);
          // v0.10.1392 — kompakt (sohbet) kipte açılacak veri yok (sunucu
          // kesti): tıklanamaz etiket, tümü Topology sekmesinde.
          if (compact && !onExpandColumn) {
            return (
              <span key={n.service} className="topo-more badge b-gray" style={{ left: p.x, top: p.y }}
                title={`${n.subkind ?? ''} — tümü Topology sekmesinde`}>
                {n.subkind ?? '+ more'}
              </span>
            );
          }
          return (
            <Button key={n.service} variant="secondary" size="xs" className="topo-more"
              style={{ left: p.x, top: p.y }}
              title={`${n.subkind ?? ''} — bu sütundaki tüm düğümleri göster (kenarlar yüklü, yeni istek yok)`}
              onClick={() => onExpandColumn?.(c)}>
              {n.subkind ?? '+ more'} ▸
            </Button>
          );
        }
        const dim = active && !active.has(n.service);
        const level = n.errorRate > 0.05 ? 'red' : n.errorRate > 0.01 ? 'amber' : 'green';
        // v0.8.447 — katlı namespace süper-düğümü: tıkla → genişlet.
        // onSelectNode ÇAĞRILMAZ (drawer servis bekler); hover sönümü
        // ve sağlık noktası normal düğümle aynı dilde çalışır.
        if (isNsNode(n.service)) {
          const ns = nsOfNodeId(n.service);
          const g = groups.find(x => x.ns === ns);
          const nsHue = nsColor.get(ns) ?? 'var(--accent)';
          return (
            <div key={n.service}
              className={'topo-node topo-nsnode' + (dim ? ' dim' : '')}
              style={{
                left: p.x, top: p.y, cursor: 'pointer',
                borderColor: `color-mix(in srgb, ${nsHue} 55%, var(--border))`,
              }}
              // eslint-disable-next-line ui/no-raw-element -- graf düğüm kartı (katlı ns): konumlu .topo-node pill'i, blok içerik; pan istisnası closest('.topo-node') — düğme atomu değil
              role="button" tabIndex={0}
              aria-label={`namespace ${ns} — genişlet`}
              onMouseEnter={() => onHoverNode(n.service)}
              onMouseLeave={() => onHoverNode(null)}
              // Hover'ı genişletmeden ÖNCE temizle — pill unmount olunca
              // mouseleave hiç gelmez, bayat sentetik id kalırdı (dim-kilit).
              onClick={() => { onHoverNode(null); expandNs(ns); }}
              onKeyDown={e => {
                if (e.key === 'Enter' || e.key === ' ') { onHoverNode(null); expandNs(ns); }
              }}
              title={
                `ns: ${ns} — ${g?.members.length ?? 0} servis (tıkla: genişlet)\n` +
                (g?.members.join('\n') ?? '')
              }>
              <span className={`topo-dot ${level}`} />
              <div style={{ minWidth: 0 }}>
                <div className="topo-name" style={{ color: nsHue }}>{ns} ▸</div>
                <div className="topo-sub">
                  {g?.members.length ?? 0} svc · {n.spanCount.toLocaleString()} span
                  {n.errorRate > 0.01 ? ` · ${(n.errorRate * 100).toFixed(1)}% err` : ''}
                </div>
              </div>
            </div>
          );
        }
        const isDep = !!n.kind;
        // v0.10.1294 — odak kipi: namespace rengi pilin sol şeridi (3px).
        const stripe = focusMode && !isDep ? nsColor.get(nsOf(n.service) ?? '') : undefined;
        // Eşlem `data.nodes`ten doğuyor ve burada gezilen liste de o —
        // yani isDep olan her düğümün kaydı vardır. `?? null` yalnızca
        // tipin zorunlu kıldığı savunma.
        const pill = isDep ? (depPills.get(n.service) ?? null) : null;
        return (
          <div key={n.service}
            className={
              'topo-node' +
              (focus === n.service ? ' focus' : '') +
              (dim ? ' dim' : '') +
              (isDep ? ' ext' : '') +
              (stripe ? ' ns-stripe' : '')
            }
            style={{ left: p.x, top: p.y, cursor: 'pointer', ...(stripe ? { ['--ns' as string]: stripe } : {}) }}
            // eslint-disable-next-line ui/no-raw-element -- graf düğüm kartı: konumlu .topo-node pill'i, blok içerik; pan istisnası closest('.topo-node') — düğme atomu değil
            role="button" tabIndex={0}
            aria-label={n.service}
            onMouseEnter={() => onHoverNode(n.service)}
            onMouseLeave={() => onHoverNode(null)}
            onClick={() => onSelectNode(n.service)}
            onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') onSelectNode(n.service); }}
            title={
              `${n.service}${n.kind ? ` · ${n.kind}` : ''}\n` +
              `${n.spanCount.toLocaleString()} spans · ${(n.errorRate * 100).toFixed(2)}% error` +
              (n.dbName ? `\ndb.name: ${n.dbName}` : '') +
              (n.cluster ? `\ncluster: ${n.cluster}` : '') +
              (n.env ? `\nenv: ${n.env}` : '')
            }>
            <span className={`topo-dot ${level}`} />
            <div style={{ minWidth: 0 }}>
              {/* v0.10.517 (operatör: "oracle altta, db name üstte") — bağımlılık
                  pilinde somut kimlik (db.name / @instance) BAŞLIK, sistem adı
                  alt satır. v0.10.837: o "somut kimlik" kümeye göre seçiliyor
                  (lib/topoLabels depPillLinesMap) — aynı db.name farklı
                  sunucularda tekrarlanıyorsa başlığa ana makine adı çıkar. */}
              <div className="topo-name">
                {pill ? pill.title : displayLabel(n)}
                {/* v0.8.383 — env chip: lights up on the MV path where the
                    adapter carries GraphNode.env (deploy_env-led derive,
                    v0.8.380). The sampled global map has no env → no chip. */}
                {n.env && <span className="topo-envchip">{n.env}</span>}
              </div>
              <div className="topo-sub">
                {pill ? pill.sub : `${n.spanCount.toLocaleString()} span`}
              </div>
            </div>
          </div>
        );
      })}
      </div>

      {/* v0.10.1294 — tek lejant satırı (transform DIŞINDA, sol alt): çağıranın
          sağlık + kenar türleri + bu bileşenin bildiği namespace renkleri. */}
      {!compact && (legend || nsLegend.length > 0) && (
        <div className="topo-legend">
          {legend}
          {nsLegend.map(l => (
            <span key={l.ns} className="topo-legend-item">
              <span className="topo-legend-ns" style={{ background: l.color }} />{l.ns}
            </span>
          ))}
        </div>
      )}

      {/* v0.10.1305 — katlı namespace sayısı + "Tümünü aç" (transform DIŞINDA,
          sol alt). >150 servisli haritada otomatik katlama her ≥3 üyeli
          namespace'i katlar; buradan tek tıkla ?nsfold=- (hiçbiri katlı değil). */}
      {!focusMode && groups.length > 0 && (
        <div className="topo-foldbar">
          <span>{groups.length} namespace katlı</span>
          <Button variant="ghost" size="xs" onClick={() => { onHoverNode(null); expandAll(); }}
            title="Katlı tüm namespace'leri aç (?nsfold=-)">
            Tümünü aç
          </Button>
        </div>
      )}

      {/* v0.8.296 — zoom kontrolleri (transform DIŞINDA, sabit köşe); kompakt kipte yok */}
      {!compact && <div className="topo-zoomctl">
        <Button variant="secondary" size="sm" aria-label="Yakınlaştır" title="Zoom in" onClick={() => zoomStep(1.3)}>+</Button>
        <span className="pct">{Math.round(vp.k * 100)}%</span>
        <Button variant="secondary" size="sm" aria-label="Uzaklaştır" title="Zoom out" onClick={() => zoomStep(1 / 1.3)}>−</Button>
        <Button variant="secondary" size="sm" aria-label="Sığdır" title="Fit"
          onClick={() => setVp(fitViewport(layoutW, layoutH, wrapRef.current?.clientWidth ?? width, height))}>⛶</Button>
      </div>}
    </div>
  );
}

const NO_FOLD: ReadonlySet<string> = new Set();
const COMPACT_VIEW: Viewport = { x: 0, y: 0, k: 1 }; // v0.10.1392
const SECONDARY_LABEL: Record<Exclude<EdgeClass, 'flow'>, string> = {
  peer: '↕ aynı sütun içi çağrı (yan bağlantı)',
  skip: '⇢ sütun atlayan çağrı (yan bağlantı)',
  back: '↺ döngü kenarı (yan bağlantı)',
};

function displayLabel(n: ServiceMapNode): string {
  return n.subkind || n.service.replace(/^(db|queue|ext):/, '');
}
// fmtMs — kompakt latency etiketi (ServiceGraph.fmtMs ile aynı biçim; chip
// ile canvas inspector aynı okunur).
function fmtMs(ms: number): string {
  if (!ms) return '—';
  if (ms >= 1000) return (ms / 1000).toFixed(ms >= 10_000 ? 0 : 1) + 's';
  return Math.round(ms) + 'ms';
}
function depLabel(kind: string): string {
  switch (kind) {
    case 'db': return 'database';
    case 'queue': return 'queue';
    case 'external': return 'dış bağımlılık';
    default: return kind;
  }
}
