import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { EvalRunSummary } from '@/lib/types';

// queries/aiEval.ts — v0.10.940 (Settings › CoSRE › Değerlendirme). Evalset
// panelinin okuma + yazma kancaları.
//
// YOKLAMA: yalnız bir koşu `running` iken 10 s (ev kuralı ≥10 s); koşu
// bitince `false` — boşta duran bir ayar sekmesi sunucuyu dövmez. Gizli
// sekmede RQ zaten durur (refetchIntervalInBackground varsayılan false;
// focusManager `visibilitychange` dinler) — runbooks.ts useRunbookExecution
// emsali, elle document.hidden koruması gereksiz.
//
// Anahtar ailesi keys.ts'e girmedi: tek tüketici bu panel ve copilot.ts
// emsali gibi kendi dosyasında yaşıyor; `aiEvalKeys.all` tek seferde
// hepsini geçersizler.
//
// Okumalar signal'i iletir (queryFn `({ signal })`): sekme ya da çekmece
// kapanınca istek kesilir.

const EVAL_KEY = ['ai-evalset'] as const;
export const aiEvalKeys = {
  all: EVAL_KEY,
  catalog: () => [...EVAL_KEY, 'catalog'] as const,
  runs: () => [...EVAL_KEY, 'runs'] as const,
  run: (id: string) => [...EVAL_KEY, 'run', id] as const,
  compare: (base: string, head: string) => [...EVAL_KEY, 'compare', base, head] as const,
};

/** v0.10.940 — koşu sürerken yoklama aralığı (ev tabanı 10 s). */
export const EVAL_POLL_MS = 10_000;

const anyRunning = (runs: readonly EvalRunSummary[] | undefined) => (runs ?? []).some(r => r.status === 'running');

/** Katalog: yüzeyler + üretimin kullanacağı profil. Ayar değişmedikçe
 *  sabit — 60 s taze (aynı görünümde yeniden okumaz). v0.10.940 — ama
 *  `refetchOnMount: 'always'`: alt sekme dönüşü paneli yeniden bağlar ve
 *  kardeş sekmede (Sağlayıcı ve profiller) kaydedilen ayar — ready,
 *  varsayılan profil, yüzey eşlemesi — 60 s beklemeden başlığa düşer. */
export function useEvalsetCatalog() {
  return useQuery({
    queryKey: aiEvalKeys.catalog(),
    queryFn: ({ signal }) => api.aiEvalsetCatalog(signal),
    staleTime: 60_000,
    refetchOnMount: 'always',
  });
}

/** Son 20 koşu (en yeni önce). Sürmekte olan varsa 10 s'de bir. */
export function useEvalRuns() {
  return useQuery({
    queryKey: aiEvalKeys.runs(),
    queryFn: async ({ signal }) => (await api.aiEvalsetRuns(signal))?.runs ?? [],
    staleTime: 8_000,
    refetchInterval: q => (anyRunning(q.state.data) ? EVAL_POLL_MS : false),
  });
}

/** Tek koşu + vakaları. Koşu sürerken (bu sunucudaysa ara sonuçlarla) 10 s. */
export function useEvalRun(id: string | null) {
  return useQuery({
    queryKey: aiEvalKeys.run(id ?? ''),
    queryFn: ({ signal }) => api.aiEvalsetRun(id ?? '', signal),
    enabled: !!id,
    staleTime: 8_000,
    refetchInterval: q => (q.state.data?.run?.status === 'running' ? EVAL_POLL_MS : false),
  });
}

/** v0.10.940 — iki BİTMİŞ koşunun kıyası. Bitmiş koşu değişmez: 5 dk taze,
 *  yoklama yok; 400/404 yeniden denenmez (koşu seçimi yanlışsa tekrar
 *  sormak aynı cevabı verir). */
export function useEvalCompare(base: string | null, head: string | null) {
  return useQuery({
    queryKey: aiEvalKeys.compare(base ?? '', head ?? ''),
    queryFn: ({ signal }) => api.aiEvalsetCompare(base ?? '', head ?? '', signal),
    enabled: !!base && !!head && base !== head,
    staleTime: 5 * 60_000,
    retry: false,
  });
}

/** v0.10.940 — koşuyu BAŞLAT (202). Başarıda yeni koşu listeye hemen
 *  yazılır (ilerleme satırı bir yoklama beklemeden görünsün); her sonuçta
 *  (409 "zaten sürüyor" dahil) liste tazelenir — başka sekmenin / pod'un
 *  başlattığı koşu böylece ekrana düşer ve yoklama başlar. Hatada katalog
 *  da tazelenir: 503 = `ready` değişti (AI kapandı), Koş'un sebebi yazsın. */
export function useStartEvalRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (surfaces: string[]) => api.aiEvalsetStartRun(surfaces),
    onSuccess: res => {
      if (!res?.run) return;
      qc.setQueryData<EvalRunSummary[]>(aiEvalKeys.runs(), old => [res.run, ...(old ?? []).filter(r => r.id !== res.run.id)]);
    },
    onError: () => qc.invalidateQueries({ queryKey: aiEvalKeys.catalog() }),
    onSettled: () => qc.invalidateQueries({ queryKey: aiEvalKeys.runs() }),
  });
}

/** v0.10.940 — koşuyu DURDUR. Sunucu yalnız kendi sürecindeki koşuyu
 *  durdurabilir (aksi 409); her sonuçta liste + detay tazelenir. */
export function useCancelEvalRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.aiEvalsetCancelRun(id),
    onSettled: (_d, _e, id) => {
      qc.invalidateQueries({ queryKey: aiEvalKeys.runs() });
      qc.invalidateQueries({ queryKey: aiEvalKeys.run(id) });
    },
  });
}
