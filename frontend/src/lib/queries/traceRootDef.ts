import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { TraceRootDef } from '@/lib/types';

// v0.10.733 — kök tanımı (strict | entry). Okuma tüm roller (Traces Root
// kutusunun başlığı etkin tanımı yazar), yazma admin (kapsama paneli).
// staleTime sunucunun 30 s yenileme tikiyle hizalı; poll yok.
export const TRACE_ROOT_DEF_KEY = ['trace-root-def'] as const;

export function useTraceRootDef() {
  return useQuery({
    queryKey: TRACE_ROOT_DEF_KEY,
    queryFn: ({ signal }) => api.getTraceRootDef(signal),
    staleTime: 30_000,
  });
}

export function useSaveTraceRootDef() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (def: TraceRootDef) => api.putTraceRootDef(def),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: TRACE_ROOT_DEF_KEY });
      // Kapsama cevabı etkin tanımı taşır; liste/şerit anahtarları tanımı
      // sunucuda taşıdığı için bir sonraki isteklerinde kendiliğinden ayrışır.
      void qc.invalidateQueries({ queryKey: ['ch-root-coverage'] });
    },
  });
}
