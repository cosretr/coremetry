// LoginSummary — v0.10.1907 (Login yeniden tasarımı dilim 2.2, audit §6.1).
// Kartın sağ sütunu: başlık, 3 KPI, "Son 24 saatte" satırı, bileşen satırı.
// Login.tsx bunu `lazy` ile yalnız ≥ 1100 px ve özet hazırken yükler; eksik
// bölüm yalnız kendini gizler. KPI'lar düz `div` (tablo değil — audit §6.1).
// Durum rengi YOK: sağlıklı durum nötr, yalnız `degraded` uyarı tonu (K5 kuralı).
import { Fragment, lazy, Suspense, useMemo } from 'react';
import { cn } from 'cn';
import { Skeleton } from '@/components/shadcn/skeleton';
import { SummaryShell } from './SummaryShell';
import { useBranding } from '@/lib/branding';
import { useT } from '@/lib/i18n';
import {
  activityParts, componentsLine, httpCardUsable, isLive, kpis, leadKey,
} from '@/lib/loginSummary';
import type { LoginSummary as LoginSummaryData } from '@/lib/types';

// Grafik kartı lazy: @grafana chunk'ı yalnız `http_24h` kullanılabilirken iner (audit §6.2).
const LoginHttpCard = lazy(() => import('./LoginHttpCard'));

const KPI_LABEL = {
  spans: 'login.sum.spans',
  traces: 'login.sum.traces.total',
  services: 'login.sum.services',
} as const;

export default function LoginSummary({ data }: { data: LoginSummaryData }) {
  const t = useT();
  const brand = useBranding();
  const live = useMemo(() => isLive(data.generated_at, Date.now()), [data.generated_at]);
  const cards = kpis(data);
  const acts = activityParts(data.activity_24h, t);
  const comp = componentsLine(data.components, t);
  const degraded = data.components?.state === 'degraded';
  return (
    <SummaryShell>
      <div>
        <h2 className="m-0 text-[length:var(--fs-lg)] font-semibold">
          {t('login.sum.title').replace('{app}', brand.appName)}
        </h2>
        <p className="mb-0 mt-1 text-[length:var(--fs-sm)] text-muted-foreground">{t(leadKey(data))}</p>
      </div>

      {cards.length > 0 && (
        <div className="grid grid-cols-3 gap-3">
          {cards.map((k, i) => (
            <div key={k.key} className="min-w-0 rounded-[10px] border border-border bg-card px-3.5 py-3">
              <div className="truncate text-[length:var(--fs-xs)] text-muted-foreground">{t(KPI_LABEL[k.key])}</div>
              <div className="mt-1 text-[length:var(--fs-xl)] font-semibold tabular-nums">
                {k.value}
                {k.key === 'spans' && i === 0 && live && (
                  <span className="ml-1.5 text-[length:var(--fs-xs)] font-normal text-(--text3)">{t('login.sum.live')}</span>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {httpCardUsable(data.http_24h) && (
        <Suspense fallback={<Skeleton className="h-[290px] rounded-[10px]" />}>
          <LoginHttpCard http={data.http_24h} orgName={brand.orgName} />
        </Suspense>
      )}

      {acts.length > 0 && (
        <div className="rounded-[10px] border border-border bg-card px-3.5 py-3 text-[length:var(--fs-sm)] leading-relaxed text-muted-foreground">
          <div className="mb-1 text-[length:var(--fs-xs)]">{t('login.sum.last24')}</div>
          <div>
            {acts.map((p, i) => (
              <Fragment key={i}>
                {i > 0 && ' · '}
                {p.pre}<b className="text-foreground">{p.n}</b>{p.post}
              </Fragment>
            ))}
          </div>
        </div>
      )}

      {comp && (
        <div className="flex items-center gap-2 text-[length:var(--fs-xs)] text-muted-foreground">
          <span aria-hidden="true"
            className={cn('size-2 shrink-0 rounded-full', degraded ? 'bg-(--warn)' : 'bg-(--text3)')} />
          <span>{comp}</span>
        </div>
      )}
    </SummaryShell>
  );
}
