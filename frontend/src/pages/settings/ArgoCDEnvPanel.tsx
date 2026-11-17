import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2313 — metinler arayüz dilinde (TR aynı)
import { useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { ENV_RE, MAX_ENVS, type Issue } from './argocdForm';
import { ArgoCDSectionPanel } from './ArgoCDSectionPanel';

// ArgoCDEnvPanel — v0.10.974 — `envList` çipleri (mockup Main "Ortamlar").
// Argo uygulama adındaki ortam jetonu TEK tire-sınırlı jeton: tireli değer ad
// ayrıştırmayı bozar, sunucu da reddeder (`envList[i]`). Karar 16 (operatör,
// 2026-09-27): ortamın asıl kaynağı servis adı eki; ek yoksa ya da tanınmazsa
// uygulama adındaki bu jeton — ipucu satırı bunu söyler (mockup'ın "karar
// 16'da açık" cümlesinin yerine, spec §3.9.2).
// v0.10.974 — çipin ×'i kendi çipiyle DOM'dan düşer: odak ekleme kutusuna
// taşınır (<body>'ye kaçmasın; goTo'nun env hedefi de bu kutu).
// Faz 2 dilim 1 R15: ui Chip → shadcn Badge outline + ghost icon-xs ×
// (coremetry/FilterBar çipiyle aynı anatomi), ham input → shadcn Input.

export function ArgoCDEnvPanel({ envList, issues, onChange }: {
  envList: string[];
  issues: Issue[];
  onChange: (next: string[]) => void;
}) {
  const lang = useLang();
  const [text, setText] = useState('');
  const [err, setErr] = useState('');
  const inRef = useRef<HTMLInputElement>(null);
  const issue = issues.find(i => i.target.kind === 'env');

  const add = () => {
    const raw = text.trim();
    const e = raw.toLowerCase();
    if (!e) return;
    if (!ENV_RE.test(e)) { setErr(tf('acd.env.invalid', lang, { raw })); return; }
    if (envList.includes(e)) { setErr(tf('acd.env.dup', lang, { e })); return; }
    if (envList.length >= MAX_ENVS) { setErr(tf('acd.env.max', lang, { n: MAX_ENVS })); return; }
    onChange([...envList, e]);
    setText(''); setErr('');
  };
  const shown = err || (issue ? `${issue.path}: ${issue.message}` : '');

  return (
    <ArgoCDSectionPanel id="acd-env-h" title={<>{t('acd.env.title', lang)}<span className="font-mono text-(--text3)">envList</span></>}
      desc={<>{t('acd.env.desc.a', lang)}<code>{t('acd.env.desc.prefix', lang)}<b>env</b>{t('acd.env.desc.suffix', lang)}</code>{t('acd.env.desc.b', lang)}</>}>
      <div className="flex flex-wrap items-center gap-3">
        {envList.map(e => (
          <Badge key={e} variant="outline" className="font-mono">
            {e}
            <Button size="icon-xs" variant="ghost" aria-label={tf('acd.env.remove', lang, { e })}
              onClick={() => { onChange(envList.filter(x => x !== e)); inRef.current?.focus(); }}>
              <X />
            </Button>
          </Badge>
        ))}
        <label htmlFor="acd-env-in" className="sr-only">{t('acd.env.addLabel', lang)}</label>
        <Input id="acd-env-in" ref={inRef} className="h-7 w-[140px] bg-field dark:bg-field font-mono text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)]" value={text}
          placeholder={t('acd.env.addPh', lang)} autoComplete="off" spellCheck={false}
          aria-invalid={shown ? 'true' : undefined} aria-describedby="acd-env-hint"
          onChange={e => { setText(e.target.value); setErr(''); }}
          onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add(); } }} />
        <Button variant="outline" size="xs" onClick={add}>{t('acd.env.add', lang)}</Button>
      </div>
      <div id="acd-env-hint" className={shown ? 'text-[length:var(--fs-xs)] text-status-critical' : 'text-[length:var(--fs-xs)] text-(--text3)'}>
        {shown || t('acd.env.hint', lang)}
      </div>
    </ArgoCDSectionPanel>
  );
}
