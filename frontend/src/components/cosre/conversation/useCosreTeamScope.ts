// useCosreTeamScope — CoSRE v2 karşılamasının TEK takım kapsamı (mockup B,
// 2026-10-31). Eskiden Takımım ve Exceptions kartlarında iki ayrı TeamPicker
// vardı; artık tek değer, composer bağlam satırında tek çip.
// Öncelik v1 Takımım kartıyla aynı (v0.10.1402): bu tarayıcıda seçilen takım
// (localStorage, kolaylık) > sunucunun çözdüğü kullanıcı takımı > boş.
// `unknown` = sunucu cevap verdi, kullanıcının takımı yok ve tarayıcıda da
// seçim yok → "Ekibin tanımlı değil" (sunucu data-scope reason no_team ile
// aynı durum). Yüklenirken unknown DEĞİL.
import { useState } from 'react';
import { readStoredTeam, writeStoredTeam } from '@/components/ai/capabilityHints';

export interface CosreTeamScope {
  team: string;
  unknown: boolean;
  choose(name: string): void;
  clear(): void;
}

export function useCosreTeamScope(serverTeam: string, settled: boolean): CosreTeamScope {
  // null = bu tarayıcıda seçim yok (sunucu takımı geçerli); '' = kullanıcı kaldırdı.
  const [picked, setPicked] = useState<string | null>(readStoredTeam);
  const team = (picked ?? serverTeam).trim();
  return {
    team,
    unknown: settled && picked === null && serverTeam.trim() === '',
    choose: (name: string) => { const n = name.trim(); setPicked(n); writeStoredTeam(n); },
    clear: () => { setPicked(''); writeStoredTeam(''); },
  };
}
