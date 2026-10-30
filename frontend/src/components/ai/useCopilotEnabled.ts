import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import type { CopilotCodeRead, CopilotProfileOption } from '@/lib/types';

// useCopilotEnabled — /api/copilot/config'in TEK, paylaşılan cevabı
// (v0.9.477). Önceden her CopilotExplain mount'u kendi isteğini atıyordu;
// anomali listesinde satır başına bir buton var → tek render'da N config
// isteği. Sonuç süreç ömrü boyunca sabit (operatör Settings'ten
// değiştirirse zaten sayfa yenilenir), o yüzden modül düzeyinde
// cache'leniyor: ilk çağrı bir kez uçar, sonrakiler SENKRON okur —
// çekmece açılırken "null render" titremesi de böylece kalkar.
//
// v0.9.1037 — cache artık TÜM cevabı tutuyor (yalnız `enabled` bayrağını
// değil): AI çekmecesinin model çipi aynı cevaptan besleniyor. İkinci bir
// istek YOK — bir uç, bir fetch, iki okuyucu.
export interface CopilotConfig {
  enabled: boolean;
  /** Yalnız Copilot aktifken gelir; kapalı kurulumda alan hiç yok. */
  model?: string;
  /** v0.10.183 — >1 profil varsa sohbet seçicisi için (sırsız: id/label/model);
   *  v0.10.1138 — + kısa açıklama, yalnız çağıranın rolüne açık olanlar. */
  profiles?: CopilotProfileOption[];
  defaultProfile?: string;
  /** v0.10.1128 — sohbet bu kullanıcıya wiki'den cevap verebilir mi (yalnız
   *  boolean; karşılamadaki "wiki'de arayabilirim" ipucu). Yoksa false. */
  wiki?: boolean;
  /** v0.10.1451 / v0.10.1461 — kod okuma durumu (yalnız DevOps bağlıyken); "Kod"
   *  hapı bunu okur: yoksa hap çizilmez, available=false → devre dışı + reason. */
  codeRead?: CopilotCodeRead;
  /** v0.10.1647 (CoSRE v2 Faz 2b B1) — admin bayrağı system_settings("cosre_v2")
   *  (sunucu `cosreV2,omitempty`). true → kabuk kapısı v2 sohbetini seçer
   *  (lib/cosre/pickChatImpl). Modül önbelleği süreç ömrü boyunca sabit →
   *  bayrak değişimi yeni sayfa yüklemesinde görünür. */
  cosreV2?: boolean;
}

const OFF: CopilotConfig = { enabled: false };

// normalizeCopilotConfig — v0.10.1138 (operatör raporu: model seçici hiç
// çizilmiyordu). Önbellek yalnız enabled/model/wiki tutuyor, sunucunun
// gönderdiği profiles/defaultProfile DÜŞÜYORDU — CopilotChat'in
// `profiles.length > 1` kapısı hep false kalıyordu. Şimdi bütün alanlar
// taşınır; geçersiz satırlar (id'siz) elenir.
export function normalizeCopilotConfig(c: Partial<CopilotConfig> | null | undefined): CopilotConfig {
  if (!c) return OFF;
  const profiles = Array.isArray(c.profiles)
    ? c.profiles.filter(p => p && typeof p.id === 'string' && p.id !== '')
    : [];
  return {
    enabled: !!c.enabled,
    model: c.model,
    wiki: !!c.wiki,
    ...(profiles.length > 0 ? { profiles } : {}),
    ...(c.defaultProfile ? { defaultProfile: c.defaultProfile } : {}),
    // v0.10.1461 — codeRead de taşınır (1138'deki profiles düşüşünün ikizi olmasın).
    ...(c.codeRead && typeof c.codeRead === 'object'
      ? { codeRead: { available: !!c.codeRead.available, ...(c.codeRead.reason ? { reason: c.codeRead.reason } : {}), ...(c.codeRead.profileLabel ? { profileLabel: c.codeRead.profileLabel } : {}) } }
      : {}),
    // v0.10.1647 (CoSRE v2 Faz 2b B1) — cosreV2 de taşınır; bilinmeyen alan
    // düşürülüyordu ve bayrak FE'ye hiç ulaşmıyordu (1138/1461'in üçüncü ikizi).
    ...(c.cosreV2 === true ? { cosreV2: true } : {}),
  };
}

let cached: CopilotConfig | null = null;
let inflight: Promise<CopilotConfig> | null = null;

function load(): Promise<CopilotConfig> {
  if (cached !== null) return Promise.resolve(cached);
  if (!inflight) {
    inflight = api.copilotConfig()
      .then(c => { cached = normalizeCopilotConfig(c); return cached; })
      .catch(() => { cached = OFF; return OFF; })
      .finally(() => { inflight = null; });
  }
  return inflight;
}

// v0.10.1336 — aynı tek isteğin Promise yüzü (rehberli tur bağlamı "copilot
// açık mı"yı başlarken bekler; ikinci istek YOK — modül cache'i/inflight).
export function loadCopilotConfig(): Promise<CopilotConfig> {
  return load();
}

// `active=false` iken HİÇ istek atılmaz — kapalı çekmece (ve anonim
// /public/* sayfaları) boşuna config sormasın.
export function useCopilotConfig(active = true): CopilotConfig | null {
  const [cfg, setCfg] = useState<CopilotConfig | null>(() => cached);

  useEffect(() => {
    if (!active || cfg !== null) return;
    let alive = true;
    void load().then(v => { if (alive) setCfg(v); });
    return () => { alive = false; };
  }, [active, cfg]);

  return active ? cfg : cached;
}

// useCopilotEnabled — çağıranların ezici çoğunluğu yalnız bayrağı
// istiyor; sözleşme v0.9.477'deki gibi kalıyor (null = henüz bilinmiyor).
export function useCopilotEnabled(active = true): boolean | null {
  const cfg = useCopilotConfig(active);
  return cfg === null ? null : cfg.enabled;
}

// Testler / hot-reload için: modül cache'ini sıfırla.
export function __resetCopilotEnabledCache() {
  cached = null;
  inflight = null;
}
