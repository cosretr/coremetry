// useCosreTeamScope — CoSRE v2 karşılamasının TEK takım kapsamı (mockup B,
// 2026-10-31). Eskiden Takımım ve Exceptions kartlarında iki ayrı TeamPicker
// vardı; artık tek değer, composer bağlam satırında tek çip.
//
// Öncelik (v0.10.1810, operatör kararı): bu OTURUMDA seçilen takım >
// sunucunun çözdüğü kullanıcı takımı (users.team) > bu tarayıcıda SAKLI seçim
// (yalnız sunucu takımı boşken ve sunucu cevap verdikten sonra) > boş.
// Eskiden saklı seçim sunucu takımının önündeydi (v0.10.1402 sırası): eski
// bir localStorage değeri doğru users.team'i her istekte eziyordu.
// `unknown` = sunucu cevap verdi, kullanıcının takımı yok, oturumda ve
// tarayıcıda da seçim yok → "Ekibin tanımlı değil" (sunucu data-scope reason
// no_team ile aynı durum). Yüklenirken unknown DEĞİL.
import { useState } from 'react';
import { readStoredTeam, writeStoredTeam } from '@/components/ai/capabilityHints';

export interface CosreTeamScope {
  team: string;
  /** İsteğe userTeam olarak giden seçim ('' = yok: sunucu kendi çözer). */
  picked: string;
  unknown: boolean;
  choose(name: string): void;
  clear(): void;
}

export interface TeamScopeInput {
  /** null = bu oturumda seçim yok; '' = kullanıcı kaldırdı. */
  sessionPick: string | null;
  /** localStorage'daki son seçim (null = yok). */
  stored: string | null;
  serverTeam: string;
  settled: boolean;
}

/** SAF — tablo testli. */
export function resolveTeamScope(i: TeamScopeInput): Pick<CosreTeamScope, 'team' | 'picked' | 'unknown'> {
  const server = i.serverTeam.trim();
  if (i.sessionPick !== null) {
    const p = i.sessionPick.trim();
    return { team: p, picked: p, unknown: false };
  }
  if (server) return { team: server, picked: '', unknown: false };
  // Sunucu cevap vermeden saklı değere düşme: takım birazdan gelebilir ve
  // o arada giden istek bayat seçimi userTeam diye taşırdı.
  const stored = i.settled ? (i.stored ?? '').trim() : '';
  return { team: stored, picked: stored, unknown: i.settled && stored === '' };
}

export function useCosreTeamScope(serverTeam: string, settled: boolean): CosreTeamScope {
  const [stored] = useState<string | null>(readStoredTeam);
  const [sessionPick, setSessionPick] = useState<string | null>(null);
  return {
    ...resolveTeamScope({ sessionPick, stored, serverTeam, settled }),
    choose: (name: string) => { const n = name.trim(); setSessionPick(n); writeStoredTeam(n); },
    clear: () => { setSessionPick(''); writeStoredTeam(''); },
  };
}
