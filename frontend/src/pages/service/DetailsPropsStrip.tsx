import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useServiceRollouts7d } from '@/lib/queries/services';
import { useServiceGitOps } from '@/lib/queries/rollouts';
import { imageRef } from '@/lib/rolloutRow';
import { runtimeLabel, newestRollout } from './detailsProps';
import { api } from '@/lib/api';
import { LIVE_META } from '@/lib/autoRefresh';
import { tsRel, rangeToSince, fmtDateTime } from '@/lib/utils';
import { serviceHref } from '@/lib/serviceHref';
import type { TimeRange } from '@/lib/types';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';

// DetailsPropsStrip — v0.8.370 (operator-approved mockup): the
// Dynatrace "Properties and tags" counterpart on the Details tab.
// One compact strip: technology/SDK, version + last rollout,
// clusters, pod count. Every datum comes from an EXISTING endpoint
// (runtime fingerprint is server-cached 5 min; the rest are the
// same reads the panels below make) — no new backend. Each group
// renders independently and simply drops out on error/empty, so
// the strip never blocks the tab.

const STALE = 60_000;

export function DetailsPropsStrip({ service, range }: { service: string; range: TimeRange }) {

  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service, staleTime: 5 * 60_000, retry: false,
  });
  // v0.9.360 — panelle AYNI 7g okuma (tek istek, tek cache girdisi).
  // Bonus: Version çipi artık 7 güne bakıyor — varsayılan 30dk pencerede
  // sürüm-değiştiren rollout neredeyse hiç olmadığı için çip hiç çıkmıyordu.
  const rolloutsQ = useServiceRollouts7d(service);
  // v0.10.1257 (operatör: "envanter ve cluster kısmına gerek yok; Java
  // versiyon, image ve son deploy yazsın") — Envs/Clusters çıktı (aşağıdaki
  // Clusters bölümü zaten aynı bilgiyi taşıyor); image + son deploy GitOps
  // sekmesiyle AYNI rollout okumasından (tek cache girdisi).
  const gitopsQ = useServiceGitOps(service);
  // v0.9.261 — was `range.preset` straight through. The preset vocabulary
  // includes '2d' / '7d' / '30d', which Go's time.ParseDuration rejects, so
  // internal/api.parseDuration fell back to this endpoint's 15-MINUTE default:
  // picking a 7-day window quietly showed a quarter hour of instances. It also
  // never handled 'custom'. rangeToSince covers both and can only emit h/m/s.
  const instancesSince = rangeToSince(range).since;
  const instancesQ = useQuery({
    queryKey: ['svc-instances-strip', service, instancesSince],
    queryFn: () => api.serviceInstances(service, instancesSince),
    enabled: !!service, staleTime: STALE, retry: false,
    meta: LIVE_META, // v0.10.1430 — since anahtarlı; global tik'te tazelenir
  });

  const rt = runtimeQ.data;
  const tech = rt?.language
    ? `${runtimeLabel(rt.language, rt.runtimeName, rt.runtimeVersion)}${rt.sdkVersion ? ' · otel ' + rt.sdkVersion : ''}`
    : null;
  const lastRollout = useMemo(() => newestRollout(gitopsQ.data?.rollouts.rows ?? []), [gitopsQ.data]);
  const image = lastRollout ? imageRef(lastRollout.image, lastRollout.imageTag) : '';
  const rollouts = rolloutsQ.data?.rollouts ?? [];
  // v0.8.405 — the header chip means "the new code shipped": key it
  // on the last VERSION-CHANGING rollout, not a restart/reschedule.
  const deploys = rollouts.filter(r => r.kind !== 'restart' && r.versionAfter);
  const last = deploys.length ? deploys[deploys.length - 1] : null;
  const version = last?.versionAfter || null;
  // v0.9.374 — "Pods" ŞU AN yaşayanlar: strip'te 28 okuyan operatör güncel
  // replika sayısı anlar; 7g penceresinde bu "hafta boyunca var olmuş her
  // pod"du (günlük deploy'lu 4-replika servis 28+ gösteriyordu). `up`
  // (2dk tazelik) zaten wire'da — sıfır yeni sorgu.
  const pods = instancesQ.data?.filter(i => i.up).length ?? 0;

  if (!tech && !version && !lastRollout && pods === 0) return null;

  // v0.10.2133 (servis sekmeleri S2, mockup operatör onayı 2026-11-13) —
  // çerçeveli çip şeridi yerine DÜZ etiket-değer (coremetry/KeyValue);
  // içerik ve kaynaklar aynı (K5, v0.10.1257), satır içi stil yok.
  return (
    <KeyValue className="ov-mb" aria-label="Servis özellikleri">
      {tech && <KeyValueRow k="Technology" v={tech} />}
      {version && (
        <KeyValueRow k="Version" v={<>
          <span className="mono">{version}</span>
          {last && (
            <>
              {' · '}
              <a href="#deploys" className="accent" title="Recent rollouts panel" onClick={reopenDeploys}>
                deployed {tsRel(last.timeUnixNs)}
              </a>
            </>
          )}
        </>} />
      )}
      {image && image !== '—' && (
        <KeyValueRow k="Image" mono title={image} v={image.length > 60 ? '…' + image.slice(-60) : image} />
      )}
      {lastRollout && (
        <KeyValueRow k="Son deploy" v={
          <Link to={serviceHref(service, { tab: 'gitops' })} className="accent"
            title={`${fmtDateTime(new Date(lastRollout.startedAt))} — GitOps sekmesinde rollout'lar`}>
            {tsRel(lastRollout.startedAt * 1e6)}
          </Link>
        } />
      )}
      {pods > 0 && <KeyValueRow k="Pods" v={pods} />}
    </KeyValue>
  );
}

// reopenDeploys — v0.10.2133: adres zaten #deploys iken tık hashchange
// üretmez; Dağıtımlar bölümü kapatılmışsa yeniden açılsın diye olay elle
// yayınlanır (Service.tsx çapa dinleyicisi aynı yoldan açar).
function reopenDeploys() {
  if (window.location.hash === '#deploys') window.dispatchEvent(new HashChangeEvent('hashchange'));
}
