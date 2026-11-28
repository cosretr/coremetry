// useMetricsExpand — v0.10.2489 (operatör: "grafiğe tıklayınca büyük olarak
// Metrics'te açılmıyor; tüm grafikler o şekilde olmalı"). Metrik grafiğinin
// TEK açılış deseni: grafik gövdesine tık (CorePanel `onExpandClick`;
// sürükle-yakınlaştır >5 px ve çift tık elenir, 250 ms bekleme) → Metrics
// sayfası panelin sorgusu, sayfanın aralığı ve ortam süzgeciyle.
//
// Panel başına `navigate(metricsLinkHref(...))` kopyası yerine bu kanca:
// tanım (MetricsLinkSpec) panelden, bağlam (servis, aralık, kaynak)
// sayfadan gelir. Açılamayan tanım (null / boş ifade) → undefined: grafik
// tıklanabilir GÖRÜNMEZ (yanlış sorguya götürmez).
import { useCallback } from 'react';
import { useNavigate } from 'react-router';
import { metricsLinkHref, type MetricsLinkContext, type MetricsLinkSpec } from '@/lib/metricsLink';

export type MetricsExpand = (spec: MetricsLinkSpec | null | undefined) => (() => void) | undefined;

export function useMetricsExpand(ctx: MetricsLinkContext): MetricsExpand {
  const navigate = useNavigate();
  const { service, rangeParam, range, src } = ctx;
  return useCallback((spec) => {
    const href = spec ? metricsLinkHref({ service, rangeParam, range, src }, spec) : null;
    return href ? () => navigate(href) : undefined;
  }, [navigate, service, rangeParam, range, src]);
}
