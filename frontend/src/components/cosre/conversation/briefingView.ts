// briefingView — v0.10.1700 (CoSRE v2 karşılaması, mockup B; Briefing Faz 2 FE).
// SAF: GET /api/cosre/v2/briefing cevabı → BriefingFeed görünümü.
//   ready + silence      → silence (takım + izlenen servis sayısı)
//   ready + lines        → findings (kalem başına bir satır; sunucu sırası)
//   unscoped / no_team   → unknownTeam (yalnız bu tarayıcıda takım seçilmemişse)
//   pending / disabled / unavailable / hata → null (yuva çizilmez; uydurma yok)
// Kart kullanıcının users.team'i için üretilir; operatör bu tarayıcıda BAŞKA bir
// takım seçtiyse kart o takımı anlatmaz → gösterilmez.
// Renk YALNIZ sapan sayıda: sunucu şablonundaki (internal/cosre/briefing
// items.go) ilk birimli değer — parantez DIŞINDA (parantez içi "önceki …").
import { isSafeAppPath } from '@/lib/safeAppPath';
import { agoTR } from '@/lib/utils';
import type { CosreBriefingItem, CosreBriefingLine, CosreBriefingResponse } from '@/lib/types';
import { tCosre as t } from '@/components/ai/capabilityHints';
import type { BriefingFeedLine, BriefingPart, BriefingSeverity, BriefingView } from './BriefingFeed';

const KIND: Record<CosreBriefingItem, string> = {
  red: 'RED', client: 'CLIENT', exception: 'EXC', log: 'LOG', oracle: 'ORACLE', problem: 'PROBLEM', rollout: 'ROLLOUT',
};
const SEVERITY: Record<CosreBriefingItem, BriefingSeverity> = {
  red: 'critical', client: 'warn', exception: 'warn', log: 'warn', oracle: 'warn', problem: 'warn', rollout: 'info',
};

/** Sunucu humanWindow ile aynı: "Son 3 sa" / "Son 45 dk". */
export function briefingWindowLabel(windowS: number): string {
  const n = windowS >= 3600 && windowS % 3600 === 0 ? `${windowS / 3600} sa` : `${Math.round(windowS / 60)} dk`;
  return t('cosre.welcome.briefing.window').replace('{n}', n);
}

// Birimli sayı: %2.1 · %40 · 980 ms · 1.2 s · 5.2×
const NUM_RE = /%\d+(?:[.,]\d+)?|\d+(?:[.,]\d+)?(?:\s?ms\b|\s?s\b|×)/g;

/** SAF — satır metni → parçalar: baştaki servis adı `subject`, parantez dışındaki
 *  ilk birimli sayı `num`, sondaki "(+N daha)" `muted`; gerisi düz metin. */
export function briefingParts(text: string, service?: string): BriefingPart[] {
  let rest = text;
  const out: BriefingPart[] = [];
  const svc = service?.trim();
  if (svc && rest.startsWith(svc)) {
    out.push({ text: svc, tone: 'subject' });
    rest = rest.slice(svc.length);
  }
  let tail = '';
  const more = /\s\(\+\d+ daha\)$/.exec(rest);
  if (more) { tail = more[0]; rest = rest.slice(0, more.index); }
  let depth = 0, numAt = -1, numLen = 0;
  for (let i = 0; i < rest.length && numAt < 0; i++) {
    const c = rest[i];
    if (c === '(') { depth++; continue; }
    if (c === ')') { depth = Math.max(0, depth - 1); continue; }
    if (depth > 0) continue;
    NUM_RE.lastIndex = i;
    const m = NUM_RE.exec(rest);
    if (m && m.index === i) { numAt = i; numLen = m[0].length; }
  }
  if (numAt >= 0) {
    if (numAt > 0) out.push({ text: rest.slice(0, numAt) });
    out.push({ text: rest.slice(numAt, numAt + numLen), tone: 'num' });
    if (numAt + numLen < rest.length) out.push({ text: rest.slice(numAt + numLen) });
  } else if (rest) {
    out.push({ text: rest });
  }
  if (tail) out.push({ text: tail, tone: 'muted' });
  return out;
}

function feedLine(l: CosreBriefingLine, i: number, briefingId: string): BriefingFeedLine {
  const href = l.page_url && isSafeAppPath(l.page_url) ? l.page_url : '';
  return {
    id: `${l.item}-${i}`,
    kind: KIND[l.item] ?? l.item.toUpperCase(),
    severity: SEVERITY[l.item] ?? 'muted',
    parts: briefingParts(l.text, l.rows[0]?.service),
    ask: t('cosre.welcome.briefing.askPrompt').replace('{text}', l.text),
    ...(briefingId ? { ref: { briefingId, line: i } } : {}),
    ...(href ? { pivot: { label: t(`cosre.welcome.briefing.pivot.${l.item}`), href } } : {}),
  };
}

const sameTeam = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();

/** SAF — cevap → görünüm. `pickedTeam`: bu tarayıcıda seçilen takım ('' = seçim yok). */
export function briefingView(resp: CosreBriefingResponse | null | undefined, pickedTeam: string, nowMs = Date.now()): BriefingView | null {
  if (!resp) return null;
  const picked = pickedTeam.trim();
  if (resp.status === 'unscoped') {
    return resp.reason === 'no_team' && !picked ? { state: 'unknownTeam' } : null;
  }
  const b = resp.briefing;
  if (resp.status !== 'ready' || !b) return null;
  if (picked && !sameTeam(picked, b.team) && !sameTeam(picked, b.team_key)) return null;
  const windowLabel = briefingWindowLabel(b.window_s);
  const agoLabel = b.generated_at_ms > 0 ? agoTR(b.generated_at_ms * 1e6, nowMs) : undefined;
  if (b.silence || b.lines.length === 0) {
    return { state: 'silence', windowLabel, agoLabel, team: b.team, serviceCount: b.services };
  }
  return { state: 'findings', windowLabel, agoLabel, lines: b.lines.map((l, i) => feedLine(l, i, b.id)) };
}
