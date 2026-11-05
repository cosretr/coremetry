// CosrePageV2 — v0.10.1785 (CoSRE v2 tam sayfası, mockup A). pages/CoSRE.tsx
// bayrak açıkken (cosreV2 === true) bunu TEMBEL çeker (v2 yığını ayrı chunk,
// kabuk kapısıyla aynı ilke). Sohbet CosreChatV2'nin kendisi, variant="page":
// aynı state/kancalar, Sheet / FAB / tutamak yok, `?chat=<id>` konuşmayı açar.
import { CosreChatV2 } from '../conversation/CosreChatV2';

export default function CosrePageV2() {
  return <CosreChatV2 variant="page" />;
}
