// loginSummary — v0.10.1907 (Login yeniden tasarımı dilim 2.2, audit §6.1/§6.4/§7).
// SAF yardımcılar: sayı biçimleri, satır metinleri, düşüş kararı. React/DOM yok;
// çevirmen `t` dışarıdan gelir. Sözleşme: login-summary uç cevabı (lib/types.ts
// `LoginSummary*`).
import { fmtBytes } from '@/lib/utils';
import type { LoginSummary, LoginSummaryActivity, LoginSummaryComponents } from '@/lib/types';

/** Kartın iki sütuna geçtiği alt genişlik (px). Altında özet çağrısı/yüklemesi YOK. */
export const LOGIN_WIDE_MIN_PX = 1100;
export const LOGIN_WIDE_QUERY = `(min-width: ${LOGIN_WIDE_MIN_PX}px)`;

/** "canlı" etiketi: anlık görüntü bu kadar tazeyse (audit §7-9, 3 dk). */
export const LIVE_WINDOW_MS = 3 * 60_000;

// Biçim dile bağlı DEĞİL (mockup: 1,214M / 48.2M / 2,148) — sabit en-US gruplama.
const group = (n: number): string => Math.round(n).toLocaleString('en-US');

/**
 * Büyük sayı: ≥ 1M → `48.2M` (< 100M, tek ondalık) ya da `1,214M` (≥ 100M, tam
 * sayı + binlik ayraç); < 1M → `2,148`. Negatif/NaN → `0`.
 */
export function fmtMillions(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0';
  if (Math.round(n) < 1e6) return group(n);
  const m = n / 1e6;
  const tenth = Math.round(m * 10) / 10;
  if (tenth >= 100) return `${group(m)}M`;
  return `${tenth.toFixed(1)}M`;
}

/** Aktivite sayıları: < 1K düz, < 1M `12.4K`, ≥ 1M `fmtMillions`. */
export function fmtCount(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0';
  if (Math.round(n) < 1000) return group(n);
  if (n < 1e6) {
    const k = Math.round(n / 100) / 10; // 0.1K adım
    if (k >= 1000) return '1.0M'; // 999 950+ → yuvarlama 1M'ye taşar
    return k >= 100 ? `${Math.round(k)}K` : `${k.toFixed(1)}K`;
  }
  return fmtMillions(n);
}

/** `HH:mm` (yerel saat). */
export function fmtClock(ms: number): string {
  const d = new Date(ms);
  const p = (x: number) => String(x).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Anlık görüntü `LIVE_WINDOW_MS`'den tazeyse true (`generated_at` unix ms). */
export function isLive(generatedAt: number | undefined, now: number): boolean {
  return !!generatedAt && now - generatedAt >= 0 && now - generatedAt <= LIVE_WINDOW_MS;
}

/**
 * Düşüş kararı: özet yalnız `enabled && ready` iken çizilir. Kapalı, hazır
 * değil (`ready:false`), hata/429 (veri yok) → tek sütun.
 */
export function summaryUsable(s: LoginSummary | null | undefined): s is LoginSummary {
  return !!s && s.enabled === true && s.ready === true;
}

/**
 * Özet çağrısı yapılsın mı? Yalnız geniş ekran VE marka ayarı `off` değil.
 * (`loginSummary` boş = açık.)
 */
export function shouldFetchSummary(wide: boolean, loginSummary: string | undefined): boolean {
  return wide && loginSummary !== 'off';
}

/** Metin parçası: `{n}` yer tutucusu `n` (kalın çizilir) olarak ayrılır. */
export interface RowPart { pre: string; n: string; post: string }

export function splitTemplate(tpl: string, n: string): RowPart {
  const at = tpl.indexOf('{n}');
  if (at < 0) return { pre: tpl, n: '', post: '' };
  return { pre: tpl.slice(0, at), n, post: tpl.slice(at + 3) };
}

/**
 * "Son 24 saatte" satırı: eksik alan (ör. `rollouts_tracked`) yalnız kendini
 * düşürür. Sıra: rollout · giriş · CoSRE · trace.
 */
export function activityParts(a: LoginSummaryActivity | undefined, t: (k: string) => string): RowPart[] {
  if (!a) return [];
  const out: RowPart[] = [];
  if (a.rollouts_tracked != null) out.push(splitTemplate(t('login.sum.rollouts'), fmtCount(a.rollouts_tracked)));
  out.push(splitTemplate(t('login.sum.logins'), fmtCount(a.logins)));
  out.push(splitTemplate(t('login.sum.cosre'), fmtCount(a.cosre_questions)));
  out.push(splitTemplate(t('login.sum.traces'), fmtCount(a.traces_viewed)));
  return out;
}

/**
 * Bileşen satırı. `ok`: "Tüm bileşenler çalışıyor · ClickHouse 4.20 TiB · son
 * kontrol 20:34"; `degraded`: "Bazı bileşenler sorunlu · ayrıntı giriş sonrasında".
 * `ok` değeri anlamlı DEĞİL ise (bilinmeyen durum) satır yok.
 */
export function componentsLine(c: LoginSummaryComponents | undefined, t: (k: string) => string): string | null {
  if (!c) return null;
  if (c.state === 'degraded') return t('login.sum.degraded');
  if (c.state !== 'ok') return null;
  const parts = [t('login.sum.allOk')];
  if (c.clickhouse_uncompressed_bytes != null && c.clickhouse_uncompressed_bytes > 0) {
    parts.push(`ClickHouse ${fmtBytes(c.clickhouse_uncompressed_bytes)}`);
  }
  if (c.checked_at) parts.push(`${t('login.sum.checked')} ${fmtClock(c.checked_at)}`);
  return parts.join(' · ');
}

/** Alt başlık anahtarı: isimli topoloji kipinde servis adları görünüyor demektir (audit §7-5). */
export function leadKey(s: LoginSummary): 'login.sum.leadNamed' | 'login.sum.lead' {
  return s.topology?.named ? 'login.sum.leadNamed' : 'login.sum.lead';
}

/** Üç KPI; eksik alan yalnız kendi kutusunu düşürür. */
export interface Kpi { key: 'spans' | 'traces' | 'services'; value: string }
export function kpis(s: LoginSummary): Kpi[] {
  const out: Kpi[] = [];
  if (s.spans_today != null) out.push({ key: 'spans', value: fmtMillions(s.spans_today) });
  if (s.traces_today != null) out.push({ key: 'traces', value: fmtMillions(s.traces_today) });
  if (s.services_monitored != null) out.push({ key: 'services', value: fmtMillions(s.services_monitored) });
  return out;
}
