// TopbarShare — v0.10.1230 (paylaşılabilir URL programı, Faz 1-C): her
// sayfada "linki kopyala". ShareButton'ın ikon nüshası; aynı sözleşme:
// adres çubuğu (sayfanın tüm state'i URL'de) + göreli `range` o anki saate
// göre mutlak pencereye dondurulur (absoluteShareHref) — alıcı gönderenin
// baktığı saati görür. Adres çubuğunun kendisi göreli kalır (canlı görünüm).
import { useState } from 'react';
import { IconButton } from './ui';
import { IconLink, IconCheck } from './icons';
import { copyToClipboard } from '@/lib/clipboard';
import { absoluteShareHref } from '@/lib/shareUrl';
import { t, useLang } from '@/lib/i18n'; // v0.10.2312

export function TopbarShare() {
  const lang = useLang();
  const [copied, setCopied] = useState(false);
  const onClick = async () => {
    if (await copyToClipboard(absoluteShareHref(window.location.href, Date.now()))) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  };
  return (
    <IconButton size="sm" aria-label={t('tb.share', lang)}
      tooltip={t(copied ? 'exc.trace.copied' : 'tb.shareTip', lang)}
      icon={copied ? <IconCheck size={13} /> : <IconLink size={13} />}
      onClick={onClick} />
  );
}
