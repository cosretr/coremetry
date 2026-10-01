import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';

// useOracleFunctionCodes — v0.10.1003: fonksiyon kodu → Oracle operasyon adları
// sözlüğü (GET /api/oracle/function-codes). TEK paylaşılan anahtar: trace
// sayfası çipi ve endpoint "Break down by function_code" tablosu aynı önbelleği
// okur. 5 dk taze (sunucu TTL'iyle aynı) — eşleme dakikada değişen bir şey
// değil. `enabled` çağıranın kapısı: ekranda fonksiyon kodu yoksa istek yok.
export function useOracleFunctionCodes(enabled: boolean) {
  return useQuery({
    queryKey: ['oracle-function-codes'],
    queryFn: () => api.oracleFunctionCodes(),
    staleTime: 5 * 60_000,
    enabled,
  });
}
