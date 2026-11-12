// v0.10.2039 — servis sağlık kontrolü (GET /api/services/{name}/healthcheck).
//
// Yük kuralı (operatör 2026-11-12): tur YALNIZ kullanıcı tetikleyince koşar —
// `enabled` panel açıkken true; sayfa açılışına istek eklenmez. Aralıklı
// yenileme YOK (cluster başına ~13 Thanos sorgusu), odakta yeniden çekme YOK,
// yeniden deneme YOK (tek koşu ≤15 s; hata satırı kullanıcıya söylenir).
// staleTime sunucu TTL'i (60 s) ile aynı.
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import { keys } from './keys';
import type { HealthcheckResult } from '../types';

export function useServiceHealthcheck(svc: string, env: string, window: string, enabled: boolean) {
  return useQuery<HealthcheckResult>({
    queryKey: keys.healthcheck.run(svc, env, window),
    queryFn: ({ signal }) => api.serviceHealthcheck(svc, { env, window }, signal),
    staleTime: 60_000,
    retry: false,
    refetchOnWindowFocus: false,
    enabled: enabled && !!svc,
  });
}
