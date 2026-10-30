// Shared primitives for the Settings area (extracted verbatim from the
// former monolithic Settings.tsx during the v0.8.x tabbed-area split).
//
// These helpers were defined once in Settings.tsx and consumed by several
// tabs. Pulling them here keeps the per-section files importing a single
// stable module instead of re-declaring the same markup. Behaviour is
// unchanged — these are the exact functions the tabs used before.
//
// Faz 2 dilim 1 R18 — Settings kapanışı: kullanılmayan yerel kopyalar
// (`Field2`, `Row`, `SettingRow`) silindi; kalan görsel atomlar
// (`Field` — alerts modallarının etiketi, `SectionTitle`, `SettingsSection`,
// `FlashBox`, `ConfigStatusBanner`) inline stil yerine Tailwind utility
// (aynı değerler, token'lar); kullanılmayan `Field.flex` /
// `SettingsSection.maxWidth` prop'ları düştü. Davranış (rol / aria-live,
// okuma kapısı) aynı.
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { QueryError } from '@/components/QueryError';

// ── Tiny shared form atoms ──────────────────────────────────────────────────

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="mb-3 block">
      <div className="mb-1 text-[11px] text-(--text2)">{label}</div>
      {children}
    </label>
  );
}

// v0.8.303 (quality bar U4) — same severity tokens as the toast system
// (color-mix over --ok/--err) so inline settings alerts and toasts read
// as one alert family; the old dark-palette rgba literals never
// re-resolved under the light/redhat themes.
export function FlashBox({ kind, children }: { kind: 'ok' | 'err'; children: ReactNode }) {
  const tone = kind === 'ok' ? 'text-(--ok) border-[color-mix(in_srgb,var(--ok)_30%,transparent)] bg-[color-mix(in_srgb,var(--ok)_8%,transparent)]' : 'text-(--err) border-[color-mix(in_srgb,var(--err)_30%,transparent)] bg-[color-mix(in_srgb,var(--err)_8%,transparent)]';
  // v0.10.379 (dış skill denetimi D6) — 20+ ayar sekmesinin kaydet/hata
  // geri bildirimi buradan geçer; role + aria-live olmadan ekran okuyucu
  // sonucu hiç duymuyordu. Hata "alert" (assertive), başarı "status".
  return (
    <div role={kind === 'err' ? 'alert' : 'status'} aria-live={kind === 'err' ? 'assertive' : 'polite'}
      className={`mt-3 rounded-[4px] border px-2.5 py-1.5 text-[12px] ${tone}`}>{children}</div>
  );
}

// ── Ayar durum bandı (v0.10.929, K5) ────────────────────────────────────────
// Sekmelerin üst bandı (ENABLED / NOT CONFIGURED / KAPALI / CLICKHOUSE …) bir
// KONFİGÜRASYON durumu — ne sağlık ne sapma. Eskiden kurulu = yeşil
// (.status-banner-operational), kurulmamış = amber (-degraded) boyanıyordu;
// K5: iki uç da nötr, ayrım etiket + cümlede. Sağlık modifier'ları
// (-operational / -degraded / -outage) gerçek sağlık gösteren Monitors /
// Admin durum yüzeylerine kalır. v0.10.929 (K5, lider kararı) — burada inline
// renk yok: semantik takma ad sınıfları .status-banner-neutral /
// .status-pill-neutral globals.css'te -operational ile TEK kuralı paylaşır
// (bg2 / border / text2, hap bg3 / text2) — ayar bandı "sağlık" adı taşımaz,
// değerler de ayrışamaz.
export function ConfigStatusBanner({ label, children, aside }: {
  label: ReactNode;
  children: ReactNode;
  aside?: ReactNode;
}) {
  return (
    <div className="status-banner status-banner-neutral">
      <span className="status-pill status-pill-neutral">{label}</span>
      <span className="text-[14px] font-semibold">{children}</span>
      {aside}
    </div>
  );
}

// ── shared form atoms (LDAP tab — kept alongside the rest of the
//    Settings shared primitives during the split).
export function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <div className="mt-4 mb-2 text-[12px] font-semibold tracking-[0.5px] text-(--text2) uppercase">{children}</div>
  );
}

// humanize — pull a clean message out of a fetch error. Strips the
// "HTTP NNN:" prefix our api client prepends and, if the body is a
// JSON {error} envelope, surfaces just the message.
export function humanize(err: unknown): string {
  const msg = err instanceof Error ? err.message : String(err);
  const body = msg.replace(/^HTTP \d+:\s*/, '');
  try {
    const j = JSON.parse(body);
    if (j && typeof j.error === 'string') return j.error;
  } catch {}
  return body || msg;
}

// ── Ayar okuma kapısı (v0.9.938, UX denetimi C4) ────────────────────────────
//
// SEKİZ Settings sekmesi ayar okumasını `.catch(() => setLoaded(true))` ile
// yutuyordu: GET başarısız olunca form YÜKLENDİ sayılıp BOŞ çiziliyordu ve
// Kaydet aktifti. Operatörün tek tıkla mevcut ayarın üstüne boş değerler
// yazması mümkündü — denetimdeki tek VERİ KAYBETTİREN bulgu. Sessiz olması
// daha da kötüsüydü: hiçbir yerde "okuyamadım" yazmıyordu, form gerçekten
// boş sanılıyordu.
//
// Kapının üç garantisi:
//   1. Hata AYRI bir hâl — "boş form" ile "okunamadı" bir daha karışamaz.
//   2. Kaydet ERİŞİLEMEZ — hata dalında form hiç çizilmez, yani "kaydete
//      basma" disiplinine değil, render ağacına bağlı.
//   3. Tekrar dene — ileri giden bir yol olmayan hata ekranı yarım çözüm.
//
// Yarış koruması da burada: sekme hızlı değiştirilince geç dönen bir yanıt
// sökülmüş bileşene yazmaya çalışırdı (C3 sınıfı). `alive` bayrağı onu keser.
export function useSettingsLoad<T>(
  load: () => Promise<T>,
  apply: (value: T) => void,
): { loaded: boolean; error: string | null; retry: () => void } {
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  // Ref'ler: çağıranlar bu iki fonksiyonu her render'da yeniden kuruyor
  // (setter fan-out'u closure'da). Dep listesine koysaydık efekt her
  // render'da yeniden koşar, yani sonsuz GET döngüsü olurdu.
  const loadRef = useRef(load); loadRef.current = load;
  const applyRef = useRef(apply); applyRef.current = apply;

  useEffect(() => {
    let alive = true;
    setError(null);
    loadRef.current()
      .then(v => { if (!alive) return; applyRef.current(v); setLoaded(true); })
      // loaded BİLEREK false kalıyor: "okuduk ama boştu" ile "okuyamadık"
      // ayrı hâller ve ikincisinde form çizilmemeli.
      .catch(e => { if (!alive) return; setError(humanize(e)); });
    return () => { alive = false; };
  }, [attempt]);

  const retry = useCallback(() => setAttempt(a => a + 1), []);
  return { loaded, error, retry };
}

// SettingsLoadError — kapının hata ekranı. QueryError'ın (v0.9.858) Settings
// bağlamındaki yüzü: neden tehlikeli olduğunu da söylüyor, çünkü operatör
// "boş görünüyor, kaydedeyim" refleksinden korunmalı.
export function SettingsLoadError({ error, onRetry }: { error: string; onRetry: () => void }) {
  return (
    <QueryError title="Ayarlar okunamadı" message={error} onRetry={onRetry}>
      Bu sekmenin kayıtlı ayarı sunucudan alınamadı. Form <b>bilerek</b>{' '}
      çizilmiyor: boş bir formu kaydetmek mevcut ayarın üstüne boş değerler
      yazardı.
    </QueryError>
  );
}

// ── New reusable section/row wrappers (operator request) ────────────────────
//
// SettingsSection — a titled card wrapper used to group related controls.
// Mirrors the inline `padding:16; borderRadius:8; background:var(--bg2);
// border:1px solid var(--border)` card the tabs hand-rolled, plus an
// optional heading + description so each section reads consistently.
//
export function SettingsSection({
  title, description, children,
}: {
  title?: ReactNode;
  description?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div>
      {title && <h2 className={`text-[14px] font-semibold ${description ? 'mb-1.5' : 'mb-3'}`}>{title}</h2>}
      {description && (
        <p className="mb-4 text-[13px] text-(--text2)">{description}</p>
      )}
      <div className="rounded-lg border border-(--border) bg-(--bg2) p-4">
        {children}
      </div>
    </div>
  );
}
