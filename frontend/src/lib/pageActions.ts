// pageActions.ts — v0.10.542 (Faz 3.4): `action` bloğunu sayfa state'ine
// uygular, SAF karar. Aksiyon yalnız tool sonucundan gelir (sunucu
// chat_actions.go); burada ikinci kapı: kök-göreli href, AYNI pathname —
// operatör cevap geldikten sonra başka sayfaya geçtiyse düğme görünmez
// (etiket "bu sayfada" der, başka sayfaya götürmez). Uygulama mergeOpenHref
// ile URL birleşimi + replace:true navigate: yeni sekme yok, sayfa URL'yi
// kaynak-of-truth olarak yeniden okur (frontend-design-system §4 (eski frontend-conventions)).
import { mergeOpenHref } from './openHref';
import { isSafeAppPath } from './safeAppPath';
import type { CosreRuleScope, CosreRuleScopeKind, SREHypothesisKind } from './types';

export interface ApplyAction { kind: 'apply'; href: string; label: string }

export function parseAction(payload: unknown): ApplyAction | null {
  if (!payload || typeof payload !== 'object') return null;
  const p = payload as Partial<ApplyAction>;
  // v0.10.1314 — kapı isSafeAppPath ("/\evil.com" react-router v6'da dışarı açılırdı).
  if (p.kind !== 'apply' || typeof p.href !== 'string' || !isSafeAppPath(p.href)) return null;
  return { kind: 'apply', href: p.href, label: typeof p.label === 'string' && p.label ? p.label : 'Bu sayfada uygula' };
}

function hrefPath(href: string): string {
  const q = href.indexOf('?');
  return q >= 0 ? href.slice(0, q) : href;
}

/** Düğme yalnız aksiyonun hedef sayfası açıkken görünür. */
export function actionVisible(a: ApplyAction, pathname: string): boolean {
  return hrefPath(a.href) === pathname;
}

/** Uygulanacak URL; hedef sayfa açık değilse null (düğme zaten gizli). */
export function applyActionHref(a: ApplyAction, pathname: string, search: string): string | null {
  if (!actionVisible(a, pathname)) return null;
  return mergeOpenHref(a.href, pathname, search);
}

// v0.10.1169 — "Bulguyu probleme not olarak kaydet": sunucu YALNIZ başarılı
// tool sonucundan üretir (chat_actions.go problemNoteAction); metin + jeton
// sunucudan gelir, istemci değiştiremez (jeton metni imzalar).
export interface ProblemNoteAction { kind: 'problem_note'; problemId: string; text: string; token: string; tool: string; label: string }

export function parseProblemNoteAction(payload: unknown): ProblemNoteAction | null {
  if (!payload || typeof payload !== 'object') return null;
  const p = payload as Partial<ProblemNoteAction>;
  if (p.kind !== 'problem_note' || typeof p.problemId !== 'string' || !p.problemId
    || typeof p.text !== 'string' || !p.text || typeof p.token !== 'string' || !p.token) return null;
  return {
    kind: 'problem_note', problemId: p.problemId, text: p.text, token: p.token,
    tool: typeof p.tool === 'string' ? p.tool : '',
    label: typeof p.label === 'string' && p.label ? p.label : 'Bulguyu probleme not olarak kaydet',
  };
}

// v0.10.1288 (AI SRE Faz 2) — "<hipotez> hipotezini doğrula": sunucu YALNIZ
// başarılı get_problem tool sonucundan, sayfanın problemi için ve editor+
// çağırana üretir (chat_actions.go sreHypothesisAction). Tık-only: düğme
// POST /api/problems/{id}/investigation {kind:'verify', hypothesis} çağırır;
// tür kapalı küme (sunucu yeniden doğrular).
const SRE_HYPOTHESIS_KINDS: readonly SREHypothesisKind[] = ['deploy', 'config', 'saturation', 'dependency', 'external', 'db_slow', 'traffic_surge', 'code_regression'];

export interface SREHypothesisAction { kind: 'sre_hypothesis'; problemId: string; hypothesis: SREHypothesisKind; label: string }

export function parseSREHypothesisAction(payload: unknown): SREHypothesisAction | null {
  if (!payload || typeof payload !== 'object') return null;
  const p = payload as Partial<SREHypothesisAction>;
  if (p.kind !== 'sre_hypothesis' || typeof p.problemId !== 'string' || !p.problemId
    || typeof p.hypothesis !== 'string' || !SRE_HYPOTHESIS_KINDS.includes(p.hypothesis)) return null;
  return {
    kind: 'sre_hypothesis', problemId: p.problemId, hypothesis: p.hypothesis,
    label: typeof p.label === 'string' && p.label ? p.label : 'Hipotezi doğrula',
  };
}

// v0.10.1289 — "bunu hatırla" onay kartı: sunucunun hafıza kademesi üretir
// (chat_memory.go cosreRuleAction). Öneri metni + varsayılan kapsam +
// seçenekler; HİÇBİR ŞEY kaydedilmez — kart metni/kapsamı düzenletir, yalnız
// "Kaydet" tıkı POST /api/ai/rules çağırır (editör+, audit). canSave=false
// (viewer) → düğme gerekçesiyle kapalı.
export interface CosreRuleScopeOption extends CosreRuleScope { label: string }
export interface CosreRuleAction {
  kind: 'cosre_rule';
  text: string;
  scope: CosreRuleScope;
  scopeOptions: CosreRuleScopeOption[];
  canSave: boolean;
  reason?: string;
  label: string;
}

const RULE_SCOPE_KINDS: readonly CosreRuleScopeKind[] = ['global', 'service', 'team'];

function parseRuleScope(v: unknown): CosreRuleScope | null {
  if (!v || typeof v !== 'object') return null;
  const o = v as { kind?: unknown; value?: unknown };
  if (typeof o.kind !== 'string' || !RULE_SCOPE_KINDS.includes(o.kind as CosreRuleScopeKind)) return null;
  const value = typeof o.value === 'string' ? o.value : '';
  if (o.kind !== 'global' && !value) return null;
  return o.kind === 'global' ? { kind: 'global' } : { kind: o.kind as CosreRuleScopeKind, value };
}

export function cosreRuleScopeKey(sc: CosreRuleScope): string {
  return `${sc.kind}:${(sc.value ?? '').toLowerCase()}`;
}

export function parseCosreRuleAction(payload: unknown): CosreRuleAction | null {
  if (!payload || typeof payload !== 'object') return null;
  const p = payload as Record<string, unknown>;
  if (p.kind !== 'cosre_rule' || typeof p.text !== 'string') return null;
  const scope = parseRuleScope(p.scope) ?? { kind: 'global' as const };
  const opts: CosreRuleScopeOption[] = [];
  const seen = new Set<string>();
  const push = (sc: CosreRuleScope, label: unknown) => {
    const k = cosreRuleScopeKey(sc);
    if (seen.has(k)) return;
    seen.add(k);
    opts.push({ ...sc, label: typeof label === 'string' && label ? label : (sc.kind === 'global' ? 'genel' : `${sc.kind === 'service' ? 'servis' : 'takım'} ${sc.value}`) });
  };
  if (Array.isArray(p.scopeOptions)) {
    for (const o of p.scopeOptions) {
      const sc = parseRuleScope(o);
      if (sc) push(sc, (o as { label?: unknown }).label);
    }
  }
  push(scope, undefined);
  push({ kind: 'global' }, 'genel');
  return {
    kind: 'cosre_rule', text: p.text, scope, scopeOptions: opts,
    canSave: p.canSave === true,
    reason: typeof p.reason === 'string' && p.reason ? p.reason : undefined,
    label: typeof p.label === 'string' && p.label ? p.label : 'Kuralı kaydet',
  };
}
