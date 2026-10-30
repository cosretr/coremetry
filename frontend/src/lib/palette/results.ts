// lib/palette/results — v0.10.1658 (etkileşim dilimi P2, plan §1.1). ⌘K sonuç
// listesinin SAF kurucusu: v0.10.1653 gövdesindeki `results` useMemo'su
// satır satır buraya taşındı (sıra, skorlar, kimlik satırları, "tüm sonuçlar"
// satırı aynı); girdiler artık argüman. Tablo-sürümlü testi results.test.ts.
import { scorePaletteEntry, rankPaletteResults } from '@/lib/paletteScore';
import { filterActions } from '@/lib/actions';
import { traceHref } from '@/lib/traceHref';
import { paletteIdentityQuery, identityTracesHref, paletteProblemId, problemDisplayHref } from '@/lib/paletteIdentity';
import { searchSeeAllHref } from '@/lib/searchUrl'; // v0.10.1335
import type { PaletteResult } from './pages';
import type { LocalActionSpec } from './localActions';

export const TRACE_ID_RE = /^[a-f0-9]{16,32}$/i;
// v0.10.350 — kimlik adayı (function_id gibi): kalıp + değer artık
// lib/paletteIdentity.ts'te (v0.10.674 — değer HAM sorgudan, harfi korunur).

export interface PaletteResultInput {
  /** Ham sorgu (kırpılmamış; kimlik değeri harfi korunarak buradan türer). */
  query: string;
  pages: PaletteResult[];
  services: PaletteResult[];
  endpoints: PaletteResult[];
  oracleOps: PaletteResult[];
  /** Boş sorgu: ★ sabitli + son bakılan servisler. */
  pivotSvcs: PaletteResult[];
  role: string | undefined;
  /** "“{q}” için tüm sonuçlar" şablonu (t('search.seeAll')). */
  seeAllLabel: string;
  /** v0.10.1665 (P3) — sorguya süzülmüş yerel eylemler (filterLocalActions). */
  localActions?: LocalActionSpec[];
}

/** Durum değiştiren eylemlerin görsel etiketi (P3). */
export const CHANGES_STATE = 'changes state';

export function buildPaletteResults(inp: PaletteResultInput): PaletteResult[] {
  const { query, pages: localizedPages, services, endpoints, oracleOps, pivotSvcs } = inp;
  const q = query.trim().toLowerCase();
  const all = [...localizedPages, ...services];
  let scored: PaletteResult[];
  if (!q) {
    // empty query → pivot rotation (pinned ★ + recent services) first,
    // then the page catalog (v0.7.89).
    scored = [...pivotSvcs, ...localizedPages];
  } else {
    // v0.10.126 — varlıklar önce: servis → endpoint → sayfa (lib/paletteScore).
    scored = rankPaletteResults(
      all
        .map(r => ({ ...r, score: scorePaletteEntry(q, r.label, r.aliases) }))
        .filter(r => r.score && r.score > 0),
      // Oracle operasyonları endpoint'lerin hemen ardından (v0.10.1002).
      [...endpoints, ...oracleOps],
    );
  }
  // Action launcher results (v0.5.457). Verb-driven matches like
  // "ack" → Acknowledge problem float ABOVE navigation results
  // because the operator's intent when typing a verb is action,
  // not page nav. filterActions handles role-gating + ranking.
  // v0.10.1665 (P3) — durum değiştiren altı eylemin ipucuna "changes state"
  // etiketi (yalnız görsel); yerel eylemler (localActions) onların ardından.
  const actionMatches = filterActions(inp.role, q).map<PaletteResult>(a => ({
    kind: 'action',
    label: a.label,
    hint: `${a.hint} · ${CHANGES_STATE}`,
    action: a,
    score: 800,
  }));
  for (const a of inp.localActions ?? []) {
    actionMatches.push({ kind: 'action', label: a.label, hint: a.hint, local: a.id, score: 790 });
  }
  if (actionMatches.length > 0) {
    scored = [...actionMatches, ...scored];
  }
  // Trace-id shortcut — looks like 16-32 hex chars → offer a
  // direct jump. Trace IDs are commonly pasted from logs and
  // emails into this kind of search box.
  if (q && TRACE_ID_RE.test(q)) {
    scored = [
      // PENCERESİZ: palet sonuçları navHref üzerinden gidiyor ve pencere
      // katmanı ORADA (v0.9.967/968).
      { kind: 'trace', label: q, hint: 'Open trace', to: traceHref(q), score: 999 },
      ...scored,
    ];
  } else if (q) {
    // v0.10.350 (kuyruk 6) — kimlik değeri (function_id gibi): Traces
    // sayfasının arama kutusuna düşer, sunucu kimlik-önce yolunu koşar.
    // Servis/sayfa eşleşmelerinin ALTINDA — bir servis adı da bu kalıba
    // uyabilir; kimlik seçeneği kaybolmaz ama önüne geçmez.
    // v0.10.674 — değer HAM sorgudan (`query`), küçük harfli `q`dan DEĞİL:
    // sunucu eşitliği harf-duyarlı, "…vzXA…" → "…vzxa…" sıfır satırdı.
    // v0.10.706 — problem görüntü kimliği ("P-3f9a2") → Inbox host'u açar.
    const pid = paletteProblemId(query);
    if (pid) {
      scored = [
        ...scored,
        { kind: 'page', group: 'identity', label: `Problem ${pid}`, hint: 'Görüntü kimliğiyle problemi aç', to: problemDisplayHref(pid), score: 720 },
      ];
    }
    const idv = paletteIdentityQuery(query);
    if (idv) {
      scored = [
        ...scored,
        { kind: 'trace', group: 'identity', label: idv, hint: 'Kimlikle trace ara (function_id gibi)', to: identityTracesHref(idv), score: 700 },
      ];
    }
  }
  // v0.10.1335 — SON satır: "“q” için tüm sonuçlar →" (/search?q=). Skorlanmaz,
  // sıralamaya girmez; Enter/tık diğer sayfa satırları gibi navHref'ten geçer.
  const seeAll = searchSeeAllHref(query);
  if (seeAll) {
    scored = [...scored, { kind: 'page', group: 'search', label: inp.seeAllLabel.replace('{q}', query.trim()), hint: 'Search', to: seeAll }];
  }
  return scored;
}

/** Boş sorgunun pivot satırları: ★ sabitli önce, sonra son bakılanlar (tekrarsız). */
export function pivotServiceRows(pinned: string[], recent: string[], href: (name: string) => string): PaletteResult[] {
  const recents = recent.filter(n => !pinned.includes(n));
  return [
    ...pinned.map(n => ({ kind: 'service' as const, group: 'pinned' as const, label: n, hint: '★ Pinned', to: href(n) })),
    ...recents.map(n => ({ kind: 'service' as const, group: 'recent' as const, label: n, hint: 'Recent', to: href(n) })),
  ];
}
