import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { SettingsLoadError, useSettingsLoad, ConfigStatusBanner } from './shared';

// Faz 2 dilim 1 R8 — inline stil yerine Tailwind utility (token değişkenleri).
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const LEAD = 'mb-4 text-[length:var(--fs-md)] text-(--text2)';
const CARD = 'mt-[18px] flex flex-col gap-3 rounded-lg border border-(--border) bg-(--bg2) p-4';
const NOTE = 'text-[length:var(--fs-xs)] text-(--text3)';

// LogBridgeTab — dış log sistemine köprü şablonları (v0.9.657).
//
// v0.9.655 backend'i CoSRE cevaplarındaki request_id'lerden tıklanabilir
// linkler üretiyor, ama şablonu girmenin tek yolu `curl` ile admin
// ucuna vurmaktı. Özellik yazıldı, kullanılamıyordu.
//
// Emsal: KibanaTab (v0.5.236) — aynı sınıf, dış derin-link yapılandırması.
//
// ORTAM BAŞINA ŞABLON: aynı kurulum birden çok ortamın verisini
// taşıyabiliyor ve her ortamın log arayüzü ayrı bir adres. Ortam servis
// adının SONEKİNDEN çözülüyor (-int/-uat/-prep); soneksiz ad prod demek
// ve "default" şablonunu alıyor.

// ENV_HINT — her ortam alanının altındaki açıklama. Sıra ekranda
// göründüğü sıra: önce prod (en sık), sonra test ortamları.
const ENV_ROWS: { key: string; label: string; hint: string }[] = [
  { key: 'default', label: 'Prod (varsayılan)', hint: 'Servis adı -int/-uat/-prep ile BİTMEYEN her servis bu adresi kullanır.' },
  { key: 'int', label: 'int', hint: 'Servis adı "-int" ile biten servisler.' },
  { key: 'uat', label: 'uat', hint: 'Servis adı "-uat" ile biten servisler.' },
  { key: 'prep', label: 'prep', hint: 'Servis adı "-prep" ile biten servisler.' },
];

export function LogBridgeTab() {
  const [tpls, setTpls] = useState<Record<string, string>>({});
  const [placeholder, setPlaceholder] = useState('{value}');
  // v0.9.1142 — opsiyonel zaman yer tutucuları + kimliğin saat dilimi.
  const [timePh, setTimePh] = useState<string[]>([]);
  const [tz, setTz] = useState('');
  const [tzDefault, setTzDefault] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getCorrelationLink(),
    s => {
      setTpls(s.templates ?? {});
      if (s.placeholder) setPlaceholder(s.placeholder);
      setTimePh(s.timePlaceholders ?? []);
      setTz(s.reqidTz ?? '');
      setTzDefault(s.reqidTzDefault ?? '');
    },
  );

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      // tz HER ZAMAN gönderiliyor: alanı boşaltmak "varsayılana dön"
      // demek olsun (backend işaretçiyi görmezse saklı değere dokunmaz).
      const r = await api.putCorrelationLink(tpls, tz);
      const n = Object.keys(r.templates ?? {}).length;
      setMsg({ kind: 'ok', text: n > 0
        ? `Kaydedildi — ${n} ortam yapılandırıldı.`
        : 'Kaydedildi — köprü kapalı, link çizilmeyecek.' });
      setTpls(r.templates ?? {});
      setTz(r.reqidTz ?? '');
    } catch (err) {
      // Backend hangi ortamın hatalı olduğunu gövdede söylüyor; olduğu
      // gibi gösteriyoruz — "geçersiz şablon" tek başına yetersiz.
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Kaydedilemedi' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const configured = Object.values(tpls).filter(v => v.trim() !== '').length;

  return (
    <div className="max-w-[720px]">
      <h2 className={H2}>Log sistemi köprüsü</h2>
      <p className={LEAD}>
        CoSRE bir analizde örnek <code>request_id</code> bulduğunda, bu
        şablonlarla dış log arayüzünüze tıklanabilir bir link üretir.
        Coremetry log sisteminize proxy yapmaz; yalnız linki kurar.
      </p>

      {/* v0.10.929 (K5) — kurulu/kurulmamış bir ayar durumu: nötr bant (eskiden yeşil/amber). */}
      <ConfigStatusBanner label={configured > 0 ? 'AÇIK' : 'YAPILANDIRILMADI'}>
        {configured > 0
          ? `${configured} ortam için link üretilecek.`
          : 'Kapalı — cevaplarda log linki çizilmiyor.'}
      </ConfigStatusBanner>

      {/* Faz 2 dilim 1 R8: SettingRow + ham input → coremetry/Field (D1), kart aynı (D3). */}
      <form onSubmit={save} className={CARD}>
        <div className="text-[length:var(--fs-sm)] text-(--text2)">
          Şablon <code>{placeholder}</code> yer tutucusunu taşımak zorunda —
          request_id oraya yazılır (URL-encode edilerek). Örnek şekil:{' '}
          <code className="whitespace-nowrap">
            https://log-host/path?requestId={placeholder}
          </code>
        </div>

        {ENV_ROWS.map(({ key, label, hint }) => (
          <Field key={key} label={label} hint={hint}
            value={tpls[key] ?? ''}
            onChange={e => setTpls(t => ({ ...t, [key]: e.target.value }))}
            placeholder={`https://…?requestId=${placeholder}`} />
        ))}

        <div className={NOTE}>
          Boş bırakılan ortam kapalıdır. Bir ortamın şablonu yoksa prod
          şablonuna düşülür; o da yoksa link çizilmez.
        </div>

        {/* v0.9.1142 — yapılandırılmış request kimliği kendi tarih+saatini
            taşıyor; şablona zaman yer tutucusu koyulursa link doğru
            aralıkta açılır. Liste sunucudan geliyor. */}
        {timePh.length > 0 && (
          <div className={NOTE}>
            Opsiyonel zaman yer tutucuları:{' '}
            {timePh.map((p, i) => (
              <span key={p}>{i > 0 && ' · '}<code>{p}</code></span>
            ))}
            . Yapılandırılmış request kimliği (sabit yapılı, içinde tarih+saat
            olan numara) çözülebiliyorsa bunlar kimliğin damgası ±10dk ile
            doldurulur; çözülemezse son 1 saatle. <code>{'{from}'}</code>/
            <code>{'{to}'}</code> ISO-8601 (ofsetli),{' '}
            <code>{'{from_ms}'}</code>/<code>{'{to_ms}'}</code> epoch
            milisaniyedir.
          </div>
        )}

        <Field label="Kimlik saat dilimi"
          hint={`Request kimliğinin İÇİNDEKİ saatin dilimi (IANA adı). Boş = ${tzDefault || 'varsayılan'}. Yanlış dilim ±10dk'lık arama penceresini tamamen ıskalar.`}
          value={tz}
          onChange={e => setTz(e.target.value)}
          placeholder={tzDefault} />

        {msg && (
          <div className={cn('text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {msg.text}
          </div>
        )}

        {/* D4: eylem satırı sağa yaslı. */}
        <ActionRow confirm={<Button type="submit" loading={busy}>Kaydet</Button>} />
      </form>
    </div>
  );
}
