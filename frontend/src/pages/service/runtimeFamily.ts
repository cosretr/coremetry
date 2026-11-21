// runtimeFamily — 2026-11-21 (operatör: ".NET servis JVM metrikleri
// göstermemeli — Elastic APM bunu iyi yapıyor"). Servisin runtime ailesi TEK
// yerden: önce resource öznitelikleri (telemetry.sdk.language,
// process.runtime.name — Go runtimesig.DetectRuntime'ın ilk iki kademesinin
// ikizi), dil bilinmiyorsa sunucunun metrik kataloğundan tespiti
// (/runtime-signals profile.runtime). Her runtime'a özgü yüzey (JVM heap
// kartı, JMX, .NET paneli) bu aileye göre açılır.
import { useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router';
import { api } from '@/lib/api';

export type RuntimeFamily = 'jvm' | 'dotnet' | 'go' | 'nodejs' | 'python';

/** SAF — dil / runtime adından aile; tanınmazsa null. */
export function familyFromResource(language?: string, runtimeName?: string): RuntimeFamily | null {
  switch ((language || '').trim().toLowerCase()) {
    case 'java': case 'kotlin': case 'scala': case 'jvm': return 'jvm';
    case 'dotnet': case 'csharp': case 'c#': case '.net': case 'fsharp': return 'dotnet';
    case 'go': case 'golang': return 'go';
    case 'nodejs': case 'javascript': case 'js': case 'typescript': return 'nodejs';
    case 'python': return 'python';
  }
  const rn = (runtimeName || '').toLowerCase();
  if (!rn) return null;
  if (rn.includes('.net') || rn.includes('dotnet') || rn.includes('coreclr')) return 'dotnet';
  if (rn.includes('java') || rn.includes('openjdk') || rn.includes('jdk') || rn.includes('jre')) return 'jvm';
  if (rn === 'go' || rn.startsWith('go1') || rn.includes('golang')) return 'go';
  if (rn.includes('node')) return 'nodejs';
  if (rn.includes('python')) return 'python';
  return null;
}

/** SAF — sunucu profil runtime'ı ('' = bilinmiyor) → aile. */
export function familyFromProfile(rt: string | undefined): RuntimeFamily | null {
  return rt === 'jvm' || rt === 'dotnet' || rt === 'go' || rt === 'nodejs' || rt === 'python' ? rt : null;
}

/** Follow-up 2026-11-21 — Topbar ?env= / ?cluster= kapsamı (URL kaynak-of-truth;
 *  tüm runtime yüzeyleri aynı anahtarı paylaşsın diye tek yerden). */
export function useRuntimeScope(): { env: string; cluster: string } {
  const [params] = useSearchParams();
  return { env: params.get('env') ?? '', cluster: params.get('cluster') ?? '' };
}

/** Runtime profili + sinyaller (sunucu 60 s önbellekli). Kapsam çözümü
 *  sunucuda: yanıtın scopeFilters'ı grafik sorgularına aynen eklenir. */
export function useRuntimeProfile(service: string, enabled = true) {
  const { env, cluster } = useRuntimeScope();
  return useQuery({
    queryKey: ['svc-runtime-signals', service, env, cluster],
    queryFn: () => api.serviceRuntimeSignals(service, { env, cluster }),
    enabled: !!service && enabled,
    staleTime: 60_000,
  });
}

/** Servisin runtime ailesi. Dil biliniyorsa profil isteği YOK. */
export function useRuntimeFamily(service: string): { family: RuntimeFamily | null; pending: boolean; language?: string } {
  const rtQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service,
    staleTime: 300_000,
  });
  const fromRes = familyFromResource(rtQ.data?.language, rtQ.data?.runtimeName);
  const needProfile = !!service && !rtQ.isPending && !fromRes;
  const profQ = useRuntimeProfile(service, needProfile);
  const family = fromRes ?? familyFromProfile(profQ.data?.profile?.runtime);
  return { family, pending: rtQ.isPending || (needProfile && profQ.isPending), language: rtQ.data?.language };
}
