// servicesTeamHref — v0.10.2418 (operatör 2026-11-21: "Takımını seçtiğinde
// otomatik olarak filtreli services sayfasını açsın arkada"). /services'in
// takım süzgeci URL'de iki eksenli (servicesUrlSpec: ?ownerTeam= / ?sreTeam=);
// eksen ve katalog yazımı exceptionsTeamAxis ile aynı kuraldan gelir.
import type { ExceptionsTeamAxis } from '../exceptionsTeamHref';

/** SAF — takım süzgeçli /services adresi. */
export function servicesTeamHref(team: string, axis: ExceptionsTeamAxis = 'owner'): string {
  const q = new URLSearchParams();
  q.set(axis === 'sre' ? 'sreTeam' : 'ownerTeam', team.trim());
  return `/services?${q.toString()}`;
}
