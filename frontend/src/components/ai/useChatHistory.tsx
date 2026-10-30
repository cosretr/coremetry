// useChatHistory — v0.10.1664 (CoSRE v2 Faz 2b B6, plan IA-7 / Q6). Çekmecenin
// "Geçmiş" verisi + paylaş/sil CopilotChat.tsx'ten SATIR SATIR çıkarıldı; v1
// çekmecesi, /cosre kenar çubuğu ve v2 Sheet'i AYNI kancayı, v1 ile v2 AYNI
// paneli (CopilotChat.tsx ChatHistoryPanel — CosreWelcome / CosreComposerForm
// gibi oradan dışa aktarılır) kullanır (operatör §10: v2 v1'den fakir görünmez).
// Davranış v0.9.1139'daki gibi:
//   • liste yalnız AÇILIŞTA çekilir (aç-üzerine-getir; prefetch / poll yok);
//     /cosre kenar çubuğu (keepStale) yenilerken eski listeyi ekranda tutar.
//   • satır tıkı konuşmayı yükler; 🔗 salt-okunur paylaşım bağlantısı panoya;
//     ✕ onaydan sonra siler (ekrandaki konuşma silindiyse çağıran yeni konuşmaya döner).
//   • yükleniyor / hata / boş üç hâlin hepsi çizilir (ev kuralı: boş panel yok).
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import { toast } from '@/lib/toast';
import type { AiConversationSummary } from '@/lib/types';
import { useConfirm } from '@/components/ui/ConfirmDialog';

export interface ChatHistory {
  threads: AiConversationSummary[] | undefined;
  error: string;
  setError(e: string): void;
  share(t: AiConversationSummary): Promise<void>;
  /** Onaylı silme (silici çağırandan: kırmızı-dil kapısı silme çağrısını
   *  ghost-danger düğmesinin dosyasında ister); true = silindi. */
  remove(t: AiConversationSummary, del: (id: string) => Promise<unknown>): Promise<boolean>;
}

/** active: liste çekilsin mi (çekmece açık); refreshKey değişince yeniden çekilir;
 *  keepStale: yenilerken eski liste kalır (yükleniyor yalnız ilk açılışta). */
export function useChatHistory(active: boolean, refreshKey = '', keepStale = false): ChatHistory {
  const confirm = useConfirm();
  const [threads, setThreads] = useState<AiConversationSummary[] | undefined>(undefined);
  const [error, setError] = useState('');
  useEffect(() => {
    if (!active) return;
    let alive = true;
    if (!keepStale) setThreads(undefined);
    setError('');
    api.aiConversations()
      .then(t => { if (alive) setThreads(t ?? []); })
      .catch(e => {
        if (alive) { setThreads([]); setError(e instanceof Error ? e.message : String(e)); }
      });
    return () => { alive = false; };
  }, [active, keepStale, refreshKey]);

  // v0.10.1167 — salt-okunur paylaşım bağlantısı panoya.
  const share = async (t: AiConversationSummary) => {
    try {
      const r = await api.shareAiConversation(t.id);
      const url = `${window.location.origin}${r.path}`;
      try { await navigator.clipboard.writeText(url); toast.success('Salt-okunur bağlantı panoya kopyalandı'); }
      catch { toast.info(`Salt-okunur bağlantı: ${url}`, 10000); }
    } catch (e) {
      toast.error(`Paylaşılamadı: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  const remove = async (t: AiConversationSummary, del: (id: string) => Promise<unknown>): Promise<boolean> => {
    if (!await confirm({
      title: 'Konuşma silinsin mi?',
      body: <><b>{t.title}</b> konuşması kalıcı olarak silinecek. Yalnız
        sohbet kaydı gider — telemetri verisine dokunulmaz.</>,
      confirmLabel: 'Konuşmayı sil',
      danger: true,
    })) return false;
    try {
      await del(t.id);
      setThreads(prev => (prev ?? []).filter(x => x.id !== t.id));
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      return false;
    }
  };

  return { threads, error, setError, share, remove };
}
