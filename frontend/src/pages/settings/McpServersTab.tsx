import { useState } from 'react';
import { parseEnvText } from './mcpEnv';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { NativeSelect } from '@/components/shadcn/native-select';
import { Field, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import type { McpServerInput, McpServerStatus, McpServerTestResult } from '@/lib/types';
import { SettingsLoadError, useSettingsLoad, ConfigStatusBanner, FlashBox } from './shared';

// McpServersTab — dış MCP sunucu listesi (v0.10.87, MCP istemci dilim ②).
//
// Sohbet döngüsüne dış tool katmanının AYAR yarısı: operatör izin
// listesine sunucu yazar (http ya da stdio), token'ı saklanır ve asla
// geri okunmaz (hasToken + "********" sentineli — DevOps sekmesinin
// sözleşmesi). "Sına" kaydetmeden TEK sunucuyu prova eder; başarısız
// bağlantı {ok:false} ile döner, HTTP hatası değil.
//
// Köprünün kendisi (tool'ların sohbete girmesi) dilim ③ — bu sekme
// kimlikleri ve erişilebilirliği ondan bağımsız oturtur.

const SECRET_KEPT = '********';

// Faz 2 dilim 1 R13 — inline stil → Tailwind utility; SettingRow → coremetry
// Field ailesi; ui/Button → shadcn. Satır başı denetimleri (ad / taşıma)
// etiketsiz olduğundan aria-label taşır. Token sentineli, gövde ve alan
// sırası aynı (McpServersTab.secret.test).
const CTL = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

interface Row extends McpServerInput {
  hasToken: boolean;
  // v0.10.803 — KEY=VALUE satırları (metin alanı); saklı değer "********".
  envText: string;
}

const emptyRow = (): Row => ({
  name: '', transport: 'http', url: '', token: '', command: '', args: [],
  enabled: true, hasToken: false, envText: '',
});


export function McpServersTab() {
  const [rows, setRows] = useState<Row[]>([]);
  const [status, setStatus] = useState<McpServerStatus[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [testing, setTesting] = useState<number | null>(null);
  const [testRes, setTestRes] = useState<Record<number, McpServerTestResult>>({});

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getMcpServers(),
    s => {
      setRows((s.servers || []).map(sv => ({
        name: sv.name, transport: sv.transport, url: sv.url || '',
        // Saklı token geri OKUNMAZ; sentinel round-trip'i korur.
        token: sv.hasToken ? SECRET_KEPT : '',
        command: sv.command || '', args: sv.args || [],
        enabled: sv.enabled, hasToken: sv.hasToken,
        allowTools: sv.allowTools, denyTools: sv.denyTools,
        insecureSkipVerify: sv.insecureSkipVerify,
        envText: (sv.envKeys || []).map(k => `${k}=${SECRET_KEPT}`).join('\n'),
      })));
      setStatus(s.status || []);
    },
  );

  const upd = (i: number, patch: Partial<Row>) =>
    setRows(rs => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  const toInput = (r: Row): McpServerInput => ({
    name: r.name, transport: r.transport, url: r.url || undefined,
    token: r.token || undefined, command: r.command || undefined,
    args: r.args?.length ? r.args : undefined, enabled: r.enabled,
    allowTools: r.allowTools?.length ? r.allowTools : undefined,
    denyTools: r.denyTools?.length ? r.denyTools : undefined,
    insecureSkipVerify: r.insecureSkipVerify || undefined,
    env: r.transport === 'stdio' && r.envText.trim() ? parseEnvText(r.envText) : undefined,
  });

  const save = async () => {
    setBusy(true); setMsg(null);
    try {
      const s = await api.putMcpServers(rows.map(toInput));
      setMsg({ kind: 'ok', text: `Kaydedildi — ${s.servers.length} sunucu.` });
      setStatus(s.status || []);
      setRows(rs => rs.map(r => ({ ...r, hasToken: r.token !== '' || r.hasToken })));
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Kaydedilemedi' });
    } finally {
      setBusy(false);
    }
  };

  const test = async (i: number) => {
    setTesting(i);
    try {
      const r = await api.testMcpServer(toInput(rows[i]));
      setTestRes(t => ({ ...t, [i]: r }));
    } catch (err) {
      setTestRes(t => ({ ...t, [i]: {
        ok: false, tools: 0,
        error: err instanceof Error ? err.message : 'sınama başarısız',
      } }));
    } finally {
      setTesting(null);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const enabledCount = rows.filter(r => r.enabled).length;

  return (
    <div className="max-w-[720px]">
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">MCP sunucuları</h2>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Sohbet asistanının çağırabileceği <b>dış</b> MCP sunucuları — izin
        listesi. Tool'lar sohbete <code>{'<sunucu>__<tool>'}</code> adıyla
        girer; yalnız burada tanımlı sunuculara bağlanılır. stdio taşıması
        bir alt süreç başlatır; kısıtlı ortamlarda http kullanın.
      </p>

      {/* v0.10.929 (K5) — kurulu/kurulmamış bir ayar durumu: nötr bant (eskiden yeşil/amber). */}
      <ConfigStatusBanner label={enabledCount ? `${enabledCount} ETKİN` : 'KAPALI'}>
        {enabledCount
          ? 'Etkin sunucuların tool katalogları sohbet döngüsüne eklenecek.'
          : "Dış sunucu yok — sohbet yalnız yerli tool'larla çalışır."}
      </ConfigStatusBanner>

      {rows.map((r, i) => {
        const st = status.find(s => s.server === r.name.toLowerCase().replace(/[^a-z0-9-]+/g, '-'));
        const tr = testRes[i];
        return (
          <div key={i} role="group" aria-label={`MCP sunucusu ${i + 1}`}
            className="mt-3.5 flex flex-col gap-3 rounded-lg border border-(--border) bg-(--bg2) p-3.5">
            <div className="flex items-center gap-2.5">
              <Input value={r.name} placeholder="ad (ör. runbook-kb)" aria-label="Sunucu adı"
                onChange={e => upd(i, { name: e.target.value })}
                className={cn(CTL, 'flex-1')} />
              <NativeSelect size="sm" value={r.transport} aria-label="Taşıma"
                onChange={e => upd(i, { transport: e.target.value })}
                className={CTL}>
                <option value="http">http</option>
                <option value="stdio">stdio</option>
              </NativeSelect>
              <CheckboxField label="etkin" checked={r.enabled}
                onCheckedChange={v => upd(i, { enabled: v })} />
              <Button variant="ghost" size="sm"
                onClick={() => setRows(rs => rs.filter((_, j) => j !== i))}>
                Sil
              </Button>
            </div>

            {r.transport === 'http' ? (
              <>
                <Field label="URL" value={r.url} placeholder="https://mcp.internal/endpoint"
                  onChange={e => upd(i, { url: e.target.value })} />
                <Field label="Token" type="password" value={r.token}
                  placeholder={r.hasToken ? SECRET_KEPT : ''}
                  onChange={e => upd(i, { token: e.target.value })}
                  hint={r.hasToken
                    ? <>Kayıtlı bir token var — boş bırakmak ya da {SECRET_KEPT} korur.</>
                    : <>Opsiyonel; Authorization: Bearer olarak gider.</>} />
              </>
            ) : (
              <Field label="Komut"
                value={[r.command, ...(r.args || [])].filter(Boolean).join(' ')}
                placeholder="/opt/mcp/fs-server --root /data"
                onChange={e => {
                  const parts = e.target.value.split(/\s+/).filter(Boolean);
                  upd(i, { command: parts[0] || '', args: parts.slice(1) });
                }}
                hint={<>Alt süreç yolu + argümanlar (boşlukla). Yalnız buraya yazılan komut çalıştırılır.</>} />
            )}
            {r.transport === 'stdio' && (
              /* v0.10.803 — alt süreç Coremetry'nin ortamını miras almaz; yalnız
                 PATH/HOME/LANG/TZ + buradakiler. Değerler saklı (******** korur). */
              <TextareaField label="Ortam" value={r.envText} rows={3} spellCheck={false}
                placeholder={'API_KEY=…\nMCP_ROOT=/data'}
                onChange={e => upd(i, { envText: e.target.value })}
                controlClassName="font-mono"
                hint={<>Satır başına <code>KEY=VALUE</code>. Alt süreç yalnız PATH/HOME/LANG/TZ ve buradakileri görür; Coremetry'nin kendi ortamı (anahtarlar, kimlikler) geçmez. Kayıtlı değer {SECRET_KEPT} olarak görünür, aynen bırakmak korur.</>} />
            )}

            <div className="flex items-center gap-2.5">
              <Button variant="outline" size="sm" loading={testing === i}
                onClick={() => { void test(i); }}>
                Sına
              </Button>
              {tr && (
                <span className={cn('text-[length:var(--fs-sm)]', tr.ok ? 'text-(--ok)' : 'text-(--err)')}>
                  {tr.ok
                    ? `✓ bağlandı — ${tr.tools} tool${tr.truncated ? ' (katalog kesildi)' : ''}`
                    : `✗ ${tr.error || 'bağlanamadı'}`}
                </span>
              )}
              {!tr && st && (
                <span className={cn('text-[length:var(--fs-sm)] tabular-nums', st.err ? 'text-(--warn)' : 'text-(--text3)')}>
                  {st.err ? `son durum: ${st.err}` : `katalog: ${st.tools} tool`}
                </span>
              )}
            </div>
          </div>
        );
      })}

      <div className="mt-3.5">
        <ActionRow
          secondary={
            <Button variant="outline" onClick={() => setRows(rs => [...rs, emptyRow()])}
              disabled={rows.length >= 8}>
              + Sunucu ekle
            </Button>
          }
          confirm={<Button loading={busy} onClick={() => { void save(); }}>Kaydet</Button>} />
        {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
      </div>
    </div>
  );
}
