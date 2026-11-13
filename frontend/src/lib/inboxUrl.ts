// inboxUrl — pure codec for the /inbox multi-select facets (priority + kind)
// as URL params (v0.8.291, Option B Slice 2). The inbox moves off localStorage
// onto the URL so a triage view is shareable (Copy link reproduces the exact
// filter), per the house "URL = source of truth" rule. Kept pure so the
// default handling + round-trip is unit-tested away from React.

// decodeCsvSet parses a comma-separated URL param into an ordered, de-duped
// list restricted to `allowed`. A null/empty/all-invalid value falls back to
// `dflt` (so a fresh /inbox lands on the intended default, e.g. P1+P2), which
// is what makes an absent param mean "the default view", not "nothing".
export function decodeCsvSet(raw: string | null, allowed: readonly string[], dflt: readonly string[]): string[] {
  const allow = new Set(allowed);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const tok of (raw ?? '').split(',')) {
    const t = tok.trim();
    if (t && allow.has(t) && !seen.has(t)) {
      seen.add(t);
      out.push(t);
    }
  }
  return out.length > 0 ? out : [...dflt];
}

// INBOX_TEAM_PARAM (v0.9.1246) — the single-axis team filter's param NAME,
// written here once because two independent producers have to agree on it:
// this page (reader) and the server-side chat bridge (guidedAnswerLinks,
// copilot_followup.go) that emits /inbox?kind=exception&team=<team>. A link
// that promises a narrowed view while the page reads a DIFFERENT param opens
// the unfiltered queue instead — the K4 dead-param class (v0.9.1130).
export const INBOX_TEAM_PARAM = 'team';

// readInboxTeam — the ?team= facet as the page consumes it.
//
// NO LENGTH OR SHAPE ASSUMPTION (v0.9.1246, operator): real team names are
// short codes like "SY" / "UG", so anything that required 3+ characters or a
// particular casing would silently drop the operator's actual team. Case is
// NOT normalised here on purpose — the server folds it (chstore.NormTeamName
// via TeamEqual) and echoes the catalogue's own spelling into the chip, so
// lowercasing client-side would only make the chip disagree with the catalogue.
// Whitespace-only means "no filter" (a stray "?team=%20" must not narrow to a
// team that cannot exist).
export function readInboxTeam(sp: URLSearchParams): string {
  return (sp.get(INBOX_TEAM_PARAM) ?? '').trim();
}

// encodeCsvSet serializes a selected set back to a canonical comma string in
// `allowed` order (stable, so the URL doesn't churn on selection order).
// Returns null when the selection equals the default — the caller then DELETES
// the param, keeping a default view's link clean (no ?prio=P1,P2,P3 noise).
export function encodeCsvSet(values: Iterable<string>, allowed: readonly string[], dflt: readonly string[]): string | null {
  const have = new Set(values);
  const ordered = allowed.filter(a => have.has(a));
  const dfltOrdered = allowed.filter(a => new Set(dflt).has(a));
  if (ordered.length === dfltOrdered.length && ordered.every((v, i) => v === dfltOrdered[i])) {
    return null;
  }
  return ordered.join(',');
}

// v0.10.706 — kategori çipi (?cat=): sunucu sözlüğüyle aynı sıra; varsayılan
// TÜMÜ (param yok = süzgeç yok). Codec kind/prio ile aynı (decode/encodeCsvSet).
export const INBOX_CAT_PARAM = 'cat';
export const INBOX_CAT_ALL = ['AVAILABILITY', 'ERROR', 'SLOWDOWN', 'RESOURCE', 'CUSTOM'] as const;
export const INBOX_CAT_LABEL: Record<(typeof INBOX_CAT_ALL)[number], string> = {
  AVAILABILITY: 'Availability', ERROR: 'Error', SLOWDOWN: 'Slowdown', RESOURCE: 'Resource', CUSTOM: 'Custom',
};

// v0.10.2124 (Problems sadeleştirme S4) — "Diğer süzgeçler" bölümündeki
// süzgeçlerden VARSAYILAN-DIŞI olanlar. Bölüm kapalı gelir; bu liste boş
// değilse açık gelir ve düğme sayıyı söyler (gizli süzgeç sessiz kalmaz).
// Semantik sayfanın okuyucularıyla birebir: status (open varsayılan),
// verdict (parseVerdictView: yalnız 'noise'), subject (parseSubjectLane:
// db/external), cat (decodeCsvSet; tümü = varsayılan), since (sabit
// basamaklar), owner/sre (boş = hepsi), minOcc (param VARSA varsayılan-dışı:
// setMinOcc varsayılanda paramı siler).
export const INBOX_SINCE_STEPS = ['30m', '1h', '2h', '24h', '7d'] as const;
export function inboxMoreFiltersActive(sp: URLSearchParams): string[] {
  const out: string[] = [];
  const st = sp.get('status');
  if (st === 'all' || st === 'ignored') out.push('status');
  if (sp.get('verdict') === 'noise') out.push('verdict');
  const subj = sp.get('subject');
  if (subj === 'db' || subj === 'external') out.push('subject');
  if (decodeCsvSet(sp.get(INBOX_CAT_PARAM), INBOX_CAT_ALL, INBOX_CAT_ALL).length < INBOX_CAT_ALL.length) out.push(INBOX_CAT_PARAM);
  if ((INBOX_SINCE_STEPS as readonly string[]).includes(sp.get('since') ?? '')) out.push('since');
  if (sp.get('owner')) out.push('owner');
  if (sp.get('sre')) out.push('sre');
  if (sp.get('minOcc') !== null) out.push('minOcc');
  return out;
}

// v0.10.2124 (S4) — sayfa altı bağlantıları: mevcut görünümü (süzgeçler)
// korur, yalnız `status`u yazar; açık detay paramlarını düşürür.
export function inboxStatusHref(sp: URLSearchParams, status: 'all' | 'ignored'): string {
  const p = new URLSearchParams(sp);
  p.delete('problem'); p.delete('anomaly'); p.delete('item');
  p.set('status', status);
  return `/inbox?${p.toString()}`;
}
