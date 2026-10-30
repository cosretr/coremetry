import { useEffect, useState, type FormEvent } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import type { SMTPSettings } from '@/lib/types';
import { FlashBox, humanize } from './shared';

// ── SMTP tab ────────────────────────────────────────────────────────────────
// Faz 2 dilim 1 R9 (shadcn): inline stil yok (Tailwind utility), coremetry/
// Field ailesi + shadcn Button/Input + ActionRow. Alan sırası, metinler ve
// parola sözleşmesi aynı (SmtpTab.secret.test.tsx).
const ROW = 'mb-3 flex flex-wrap items-start gap-3';
// Etiketsiz satır-içi girdi (test alıcısı): coremetry/Field'ın denetim zemini.
const BARE_INPUT = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

export function SMTPTab() {
  const [s, setS] = useState<SMTPSettings | null | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [testTo, setTestTo] = useState('');

  const load = () => {
    setS(undefined);
    api.getSMTP().then(setS).catch(() => setS(null));
  };
  useEffect(load, []);

  if (s === undefined) return <Spinner />;
  if (s === null) return <Empty icon="⚠" title="Failed to load SMTP settings" />;

  const update = <K extends keyof SMTPSettings>(k: K, v: SMTPSettings[K]) => setS({ ...s, [k]: v });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      // If the password field is still the masked sentinel, send empty
      // string — the backend treats "empty / ********" as "keep current".
      const payload = { ...s, password: s.password === '********' ? '' : s.password };
      const next = await api.putSMTP(payload);
      setS(next);
      setMsg({ kind: 'ok', text: 'Saved.' });
    } catch (err) {
      setMsg({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const sendTest = async () => {
    if (!testTo) { setMsg({ kind: 'err', text: 'Enter a recipient first' }); return; }
    setBusy(true); setMsg(null);
    try {
      await api.testSMTP(testTo);
      setMsg({ kind: 'ok', text: `Test email sent to ${testTo}.` });
    } catch (err) {
      setMsg({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  // Yüzey kabı (denetim D7.4): bu sekme zemin/kenarlık taşımayan tek
  // başına bir formdu, yani açık temada sayfa zeminine yapışıyordu —
  // kardeş sekmelerin (Kibana/Tempo/Elastic/LDAP…) hepsi bir yüzeyin
  // içinde. `.card-static`, `.card`ın etkileşimsiz ikizi (v0.9.921):
  // ikinci `.card` tanımı `cursor: pointer` + accent hover taşıyor ve
  // tıklanmayan bir form kutusu onu devralmamalı.
  //
  // maxWidth 640 → 670: kabın dolgusu (14×2) + kenarlığı (1×2) içeriği
  // 30px daraltırdı; alan genişlikleri PİKSEL AYNI kalsın diye fark
  // kaba eklendi. Yani bu değişiklik yalnız zemin+kenarlık ekler.
  return (
    <div className="card-static max-w-[670px]">
    <form onSubmit={save}>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Outbound mail settings used by every email notification channel.
        Changes take effect immediately — no restart needed.
      </p>

      <div className={ROW}>
        <Field className="flex-[2]" label="SMTP host" required value={s.host} placeholder="smtp.example.com"
          onChange={e => update('host', e.target.value)} />
        <Field className="flex-1" label="Port" required type="number" value={s.port || ''} placeholder="587"
          onChange={e => update('port', parseInt(e.target.value || '0'))} />
      </div>
      <div className={ROW}>
        <Field className="flex-1" label="Username" value={s.username}
          onChange={e => update('username', e.target.value)} />
        <Field className="flex-1" label="Password" type="password" value={s.password}
          placeholder={s.configured && !s.password ? '(unchanged)' : ''}
          onChange={e => update('password', e.target.value)} />
      </div>
      <div className={ROW}>
        <Field className="flex-[2]" label="From address" required type="email" value={s.from} placeholder="coremetry@yourcorp.com"
          onChange={e => update('from', e.target.value)} />
        <Field className="flex-1" label="From name (optional)" value={s.fromName} placeholder="Coremetry Alerts"
          onChange={e => update('fromName', e.target.value)} />
      </div>
      <div className={ROW}>
        <CheckboxField checked={s.startTLS} onCheckedChange={v => update('startTLS', v)}
          label="Use STARTTLS (recommended for ports 587/25)" />
        <CheckboxField checked={s.skipVerify} onCheckedChange={v => update('skipVerify', v)}
          label="Skip TLS verification (self-signed only)" />
      </div>

      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}

      {/* R9 (D4): birincil (Save settings) EN SAĞDA; test alıcısı + "Send
          test email" yardımcı eylem olarak onun solunda. */}
      <div className="mt-2">
        <ActionRow
          secondary={<>
            <Input type="email" value={testTo} placeholder="recipient@example.com" aria-label="Test recipient"
              onChange={e => setTestTo(e.target.value)} className={`w-[240px] ${BARE_INPUT}`} />
            <Button variant="outline" onClick={sendTest} disabled={busy || !s.configured}>
              Send test email
            </Button>
          </>}
          confirm={<Button type="submit" loading={busy}>Save settings</Button>} />
      </div>
      {!s.configured && (
        <div className="mt-1.5 text-[length:var(--fs-xs)] text-(--text3)">
          Save valid SMTP settings before testing.
        </div>
      )}
    </form>
    </div>
  );
}
