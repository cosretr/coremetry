// AnomaliesListRedirect — v0.10.2481: eski /anomalies (liste) adresi Problems
// sayfasının Sinyaller sekmesine iner; görünüm ve süzgeç paramları korunur,
// eski ?event= / ?replay= tam sayfaya (lib/anomaliesRedirect).
// Detay rotaları (/anomalies/:id, /anomalies/ml/:id) değişmedi.
import { Navigate, useLocation } from 'react-router';
import { anomaliesListRedirect } from '@/lib/anomaliesRedirect'; // yaprak: zod girişe girmez

export function AnomaliesListRedirect() {
  const loc = useLocation();
  return <Navigate to={anomaliesListRedirect(loc.search)} replace />;
}
