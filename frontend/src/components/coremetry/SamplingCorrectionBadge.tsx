// coremetry/SamplingCorrectionBadge — v0.10.2435 (sampling-weight Phase 2).
// Small marker next to the page title where span-derived numbers are
// sampling-corrected ("örnekleme düzeltilmiş"); when the window straddles
// the switch point it says which older part is still uncorrected. Renders
// nothing with ingest.sampling off (the common case). One cheap,
// process-local endpoint shared by every page (React Query key).
import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Badge } from '@/components/shadcn/badge';
import { api } from '@/lib/api';
import { t, tf, useLang } from '@/lib/i18n';
import { timeRangeToNs, tsShort } from '@/lib/utils';
import type { TimeRange } from '@/lib/types';
import { samplingCorrectionState, type SamplingSurface } from '@/lib/samplingCorrection';

export function SamplingCorrectionBadge({ surface, range }: { surface: SamplingSurface; range: TimeRange }) {
  const lang = useLang();
  const q = useQuery({
    queryKey: ['sampling-correction'],
    queryFn: () => api.samplingCorrection(),
    staleTime: 60_000,
    retry: false,
  });
  const st = useMemo(() => {
    const r = timeRangeToNs(range);
    return samplingCorrectionState(q.data, surface, r.from / 1e6, r.to / 1e6);
  }, [q.data, surface, range]);
  if (st.kind === 'none') return null;
  const since = tsShort(st.sinceMs * 1e6);
  const label = st.kind === 'full'
    ? t('sampling.corrected', lang)
    : tf('sampling.partial', lang, { time: since });
  return (
    <Badge variant="outline" data-sampling={st.kind} className="text-[11px] font-normal"
      title={tf('sampling.corrected.title', lang, { time: since })}>
      {label}
    </Badge>
  );
}
