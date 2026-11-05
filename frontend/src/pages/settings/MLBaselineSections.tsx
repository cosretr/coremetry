// MLBaselineSections — Settings › Anomaly › ML altındaki öğrenme döngüsü
// bölümleri (Faz 1: baseline geçmiş doldurma, v0.10.1815). React Query +
// onay kapısı ister; AnomalyTab'ın bölüm testleri bu modülü taklit eder
// (sağlayıcısız render).
import { MLBaselineBackfill } from './MLBaselineBackfill';

export function MLBaselineSections() {
  return <MLBaselineBackfill />;
}
