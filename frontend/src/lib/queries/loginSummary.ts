import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';

// v0.10.1907 — Login özeti (kimliksiz). Sunucu anlık görüntüyü bellekten döner
// (CH/VM'ye gitmez); 60 sn'de bir tazelenir. Gizli sekmede durur: React Query
// varsayılanı `refetchIntervalInBackground: false`. Hata/429 sessizdir
// (`retry: false`): Login tek sütun kalır, yeniden deneme yağmuru yok.
export function useLoginSummary(enabled: boolean) {
  return useQuery({
    queryKey: ['login-summary'] as const,
    queryFn: ({ signal }) => api.loginSummary(signal),
    enabled,
    refetchInterval: 60_000,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}
