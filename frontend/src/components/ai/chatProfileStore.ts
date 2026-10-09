import { useCallback, useEffect, useMemo, useState } from 'react';
import type { CopilotProfileOption } from '@/lib/types';

// chatProfileStore — v0.10.1138: sohbet model profili seçiminin kullanıcı
// başına kalıcılığı (localStorage, try/catch — özel pencerede/engelli
// depolamada sessizce oturum belleğine düşer) + aynı sekmedeki iki yüzeyin
// (CoSRE penceresi / ✨ Explain çekmecesi sohbeti) eşzamanı.
//
// Seçim yalnız ÇAĞIRANIN rolüne açık profiller içinde geçerlidir
// (/api/copilot/config listesi zaten süzülü gelir); listede olmayan kayıtlı
// seçim (silinmiş profil, rol daraltıldı) sessizce varsayılana ('') döner.
// Sunucu yine de kapıdır: 400/403 gelirse useChatThread onProfileRejected ile
// seçimi buradan temizler.

const KEY_PREFIX = 'cosre.chatProfile.';
const mem = new Map<string, string>();
const listeners = new Set<(user: string, id: string) => void>();

export function chatProfileKey(user: string | undefined | null): string {
  return KEY_PREFIX + (user || 'anon');
}

export function readChatProfile(user: string | undefined | null): string {
  const k = chatProfileKey(user);
  try {
    const v = window.localStorage.getItem(k);
    if (v !== null) return v;
  } catch { /* depolama kapalı */ }
  return mem.get(k) ?? '';
}

export function writeChatProfile(user: string | undefined | null, id: string): void {
  const k = chatProfileKey(user);
  mem.set(k, id);
  try {
    if (id) window.localStorage.setItem(k, id);
    else window.localStorage.removeItem(k);
  } catch { /* depolama kapalı — bellek kopyası yeter */ }
  for (const l of listeners) l(user || 'anon', id);
}

/** resolveChatProfile — SAF: kayıtlı seçim izinli listede değilse '' (varsayılan). */
export function resolveChatProfile(stored: string, profiles: CopilotProfileOption[]): string {
  if (!stored || profiles.length < 2) return '';
  return profiles.some(p => p.id === stored) ? stored : '';
}

/** activeProfileModel — SAF: rozetteki model adı. Seçim yoksa varsayılan
 *  profilin modeli, o da yoksa config'in genel `model`i. */
export function activeProfileModel(profile: string, profiles: CopilotProfileOption[], defaultProfile: string | undefined, fallback: string | undefined): string {
  const id = profile || defaultProfile || '';
  const p = profiles.find(x => x.id === id);
  return p?.model || fallback || '';
}

export interface ChatProfileState {
  /** '' = sunucu varsayılanı / yüzey eşlemesi */
  profile: string;
  setProfile: (id: string) => void;
  /** Rozet: o an cevabı üretecek model. */
  activeModel: string;
}

export function useChatProfile(user: string | undefined | null, profiles: CopilotProfileOption[], defaultProfile: string | undefined, fallbackModel: string | undefined): ChatProfileState {
  const [stored, setStored] = useState(() => readChatProfile(user));
  useEffect(() => { setStored(readChatProfile(user)); }, [user]);
  useEffect(() => {
    const me = user || 'anon';
    const l = (u: string, id: string) => { if (u === me) setStored(id); };
    listeners.add(l);
    return () => { listeners.delete(l); };
  }, [user]);
  const profile = useMemo(() => resolveChatProfile(stored, profiles), [stored, profiles]);
  const setProfile = useCallback((id: string) => writeChatProfile(user, id), [user]);
  const activeModel = activeProfileModel(profile, profiles, defaultProfile, fallbackModel);
  return { profile, setProfile, activeModel };
}
