// kafkaFilterSpec — v0.10.2467: Service → Messaging sekmesinin süzgeç URL
// durumu (lib/urlSchema). ?khost / ?kcons / ?kprod (v0.10.1540) + ?ktopic
// (Topic seçicisi). Boş = hepsi (param silinir); aşırı uzun değer URL'e yazılmaz.
import { urlSpec, zParam, zText } from './urlSchema';

export const KAFKA_FILTER_SPEC = urlSpec({
  khost: zParam(zText(256), ''),
  kcons: zParam(zText(256), ''),
  kprod: zParam(zText(256), ''),
  ktopic: zParam(zText(512), ''),
});
