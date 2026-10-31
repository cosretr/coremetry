// teamPick — v0.10.1725 (operatör onayı, mockup "Takımını seç"). SAF.
// CoSRE v2'de takım biçimli soru + data-scope no_team → cevabın altında
// sunucu-aramalı takım kartı. Kararın kaynağı SUNUCU (needsTeamScope,
// chat_team_scope_need.go): data-scope `teamPick: true`. Trace / servis /
// açıklama çapalı soruda sunucu işareti koymaz → kart hiç çıkmaz.
import type { CosreScope } from '@/lib/types';

/** Kart bu cevabın altında çizilsin mi. */
export function showTeamPick(scope: CosreScope | null | undefined): boolean {
  return !!scope && scope.reason === 'no_team' && scope.teamPick === true;
}

/** Kartın yeniden-sor isteği: seçilen takım ya da açık kapsamsız bayrağı. */
export type TeamPickResend = { userTeam: string } | { unscoped: true };

/** Gönderim bağlamına kart seçimini ekler (base bayt bayt korunur). */
export function withTeamPick(base: Record<string, unknown> | undefined, r: TeamPickResend | undefined): Record<string, unknown> | undefined {
  if (!r) return base;
  if ('userTeam' in r) {
    const t = r.userTeam.trim();
    return t ? { ...(base ?? {}), userTeam: t } : base;
  }
  return { ...(base ?? {}), unscoped: true };
}

/** Satır etiketi: "N servis". */
export function teamServicesLabel(n: number | undefined): string {
  return n === undefined ? '' : `${n} servis`;
}
