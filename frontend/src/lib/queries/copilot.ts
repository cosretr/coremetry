import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';

// queries/copilot.ts — v0.10.702. Boş sohbetin veri çipleri: YALNIZ çekmece
// açık + sohbet boşken (aç-üzerine-getir; liste prefetch'i yok, poll yok).
// staleTime sunucu cache'iyle aynı (60 s). Hata → boş liste: statik çipler
// zaten var, veri çipi bir ek.
// v0.10.1382 — gruplu yanıt; env (useUrlEnv) sorguya VE anahtara girer
// (sunucu anahtarı da env'i özetler). Env yalnız P1 grubuna uygulanır.
// v0.10.1390 — pencere (rangeS) kalktı: yalnız kaldırılan servis/operasyon
// gruplarının girdisiydi; anahtar artık yalnız env.
// v0.10.1402 — `team` da döner (sunucunun çözdüğü kullanıcı takımı): Takımım
// kartının varsayılan takımı; ikinci istek yok.
// v0.10.1515 — exceptions grubu kalktı (sunucu `groups: []`); kullanılan tek
// alan `team` (Takımım + exceptions kartlarının varsayılan takımı).
export function useCopilotStarters(enabled: boolean, env = ''): { team: string; settled: boolean } {
  const q = useQuery({
    queryKey: ['copilot', 'starters', env],
    queryFn: ({ signal }) => api.copilotStarters(env, signal),
    enabled,
    staleTime: 60_000,
    retry: false,
  });
  // v2 karşılaması — `settled`: cevap (ya da hata) geldi; yüklenirken takım
  // "bilinmiyor" sayılmaz ("Ekibin tanımlı değil" yanıp sönmesin).
  return { team: q.data?.team ?? '', settled: q.isFetched || q.isError };
}

// v0.10.1289 — CoSRE operatör kuralları (GET /api/ai/rules, her rol). Yalnız
// panel / modal açıkken (enabled); sunucu önbelleği yok, staleTime 30 s
// (süreç-içi yenileme aralığı). Yazımdan sonra çağıran anahtarı geçersizler.
export const COSRE_RULES_KEY = ['copilot', 'rules'] as const;

export function useCosreRules(enabled = true) {
  return useQuery({
    queryKey: COSRE_RULES_KEY,
    queryFn: ({ signal }) => api.cosreRules(signal),
    enabled,
    staleTime: 30_000,
    retry: false,
  });
}
