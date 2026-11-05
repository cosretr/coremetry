// MLBaselineSections — Settings › Anomaly › ML altındaki öğrenme döngüsü
// bölümleri (Faz 1: baseline geçmiş doldurma, v0.10.1815). React Query +
// onay kapısı ister; AnomalyTab'ın bölüm testleri bu modülü taklit eder
// (sağlayıcısız render). v0.10.1816: servis profilleri.
import { MLBaselineBackfill } from './MLBaselineBackfill';
import { MLServiceProfiles } from './MLServiceProfiles';

export function MLBaselineSections() {
  return <><MLBaselineBackfill /><MLServiceProfiles /></>;
}
