// useOpenTeamServices — v0.10.2418: takım seçilince CoSRE paneli açık
// kalırken ARKADAKİ sayfa o takımla süzülü /services olur. Tam sayfa
// /cosre'de arkada sayfa yok → gezinme yapılmaz. Katalog (eksen için) RQ
// önbelleğinden ya da tek istekle; okunamazsa owner ekseni.
import { useNavigate, useLocation } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { exceptionsTeamAxis } from '@/lib/exceptionsTeamHref';
import { servicesTeamHref } from '@/lib/cosre/servicesTeamHref';
import type { ServiceMetadata } from '@/lib/types';

export function useOpenTeamServices(): (team: string) => void {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const qc = useQueryClient();
  return (team: string) => {
    const name = team.trim();
    if (!name || pathname.startsWith('/cosre')) return;
    void qc.fetchQuery({
      queryKey: keys.services.metadata,
      queryFn: async () => (await api.servicesMetadata()) ?? {},
      staleTime: 60_000,
    })
      .catch(() => undefined)
      .then(cat => {
        const r = exceptionsTeamAxis(cat as Record<string, ServiceMetadata> | undefined, name);
        navigate(servicesTeamHref(r.name, r.axis));
      });
  };
}
