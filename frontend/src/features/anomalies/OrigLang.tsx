// OrigLang — v0.10.2298 (sunucu metinleri S1; docs/i18n/server-text-design.md §3).
// Sunucunun ürettiği ve çevrilmeyen metin (LLM özeti, kalıcı gerekçe / adım
// özeti, bildirim günlüğü hatası) İngilizce arayüzde `lang="tr"` ile sarılır
// ve yanına soluk bir "TR" etiketi düşer: karışık sayfa hata gibi değil,
// bilinçli görünsün. Türkçe arayüzde sarmalayıcı YOK — çocuklar aynen
// (Türkçe ekran birebir aynı). Ayar ya da geçiş düğmesi yok.
import type { ReactNode } from 'react';
import { t, useLang } from '@/lib/i18n';
import type { StdMsg } from '@/lib/types';
import { stdMsgView } from '@/lib/problemStandardView';

const TAG_STYLE = {
  marginLeft: 4, padding: '0 3px', fontSize: 9.5, lineHeight: '14px', verticalAlign: 'middle',
  color: 'var(--text3)', border: '1px solid var(--border)', borderRadius: 3, fontWeight: 500,
} as const;

/** Orijinal dil etiketi (EN arayüz). `block`: blok içerik (markdown) için div. */
export function OrigLang({ children, block = false }: { children: ReactNode; block?: boolean }) {
  const lang = useLang();
  if (lang === 'tr') return <>{children}</>;
  const tag = <span data-orig-lang="" style={TAG_STYLE} title={t('orig.tr.title', lang)}>{t('orig.tr', lang)}</span>;
  if (block) return <div lang="tr">{children}{tag}</div>;
  return <span lang="tr">{children}{tag}</span>;
}

/**
 * v0.10.2301 (sunucu metinleri S4) — anahtarlı sunucu metni: TR sunucu metni
 * aynen; EN anahtardan (std.m.*), çizilemezse (eski kayıt / bilinmeyen anahtar)
 * sunucu metni + "TR" işareti.
 */
export function MsgText({ msg, text }: { msg?: StdMsg | null; text: string }) {
  const lang = useLang();
  const v = stdMsgView(msg, text, lang);
  return v.orig ? <OrigLang>{v.text}</OrigLang> : <>{v.text}</>;
}
