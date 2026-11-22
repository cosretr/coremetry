// v0.10.2451 — metrik-yalnız servis: penceredeki span'leri yok (upstream tail
// sampling hepsini düşürdü ya da servis trace'lenmiyor), RED VictoriaMetrics'ten.
// Services listesi bu servisleri artık gösteriyor (/api/services-metric-rank
// metricOnly); servis sayfası boş span bölümlerini hata gibi değil, nedeniyle
// ilan eder. Overview metrik RED'i info=null iken de çizer (v0.9.170).
import { serviceHasNoTraces, type NoTracesInput } from './serviceNoTraces';

export function ServiceNoTracesNote(props: NoTracesInput) {
  if (!serviceHasNoTraces(props)) return null;
  return (
    <div className="pod-cap" role="status" data-testid="service-no-traces"
      title="Upstream tail sampling dropped every trace of this service in the window, or the service is not traced. Request rate, errors and latency come from unsampled VictoriaMetrics series when present.">
      No traces in this window — the Overview reads RED from metrics where the service reports them; span-based sections (operations, endpoints, traces) stay empty.
    </div>
  );
}
