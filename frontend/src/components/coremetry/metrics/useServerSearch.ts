// coremetry/metrics/useServerSearch — v0.10.1862 (Metrics Explorer Faz 2
// dilim 2.2). Seçicilerin sunucu araması: 180 ms debounce (PICKER_DEBOUNCE_MS
// ile aynı), anahtar değişince uçuştaki istek İPTAL (AbortSignal), yalnız
// açıkken (`enabled`). Katalog önden çekilmez (hard constraint: picker =
// sunucu araması). İptal hata değildir.
import { useEffect, useRef, useState } from 'react';
import { isCanceled } from '@/lib/api';

export interface ServerSearch<T> {
  data: T | undefined;
  loading: boolean;
  error: string | null;
}

export const SERVER_SEARCH_DEBOUNCE_MS = 180;

export function useServerSearch<T>(
  enabled: boolean,
  key: string,
  run: (signal: AbortSignal) => Promise<T>,
  debounceMs = SERVER_SEARCH_DEBOUNCE_MS,
): ServerSearch<T> {
  const [state, setState] = useState<ServerSearch<T>>({ data: undefined, loading: false, error: null });
  const runRef = useRef(run);
  runRef.current = run;
  useEffect(() => {
    if (!enabled) return;
    const ctl = new AbortController();
    setState(s => ({ ...s, loading: true, error: null }));
    const t = window.setTimeout(() => {
      runRef.current(ctl.signal)
        .then(data => { if (!ctl.signal.aborted) setState({ data, loading: false, error: null }); })
        .catch((e: unknown) => {
          if (ctl.signal.aborted || isCanceled(e)) return;
          setState(s => ({ ...s, loading: false, error: e instanceof Error ? e.message : 'Arama başarısız' }));
        });
    }, debounceMs);
    return () => { ctl.abort(); window.clearTimeout(t); };
  }, [enabled, key, debounceMs]);
  return state;
}
