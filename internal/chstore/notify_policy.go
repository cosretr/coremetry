package chstore

// notify_policy.go — v0.10.1523 (Problem standardı R1): bildirim politikası
// blobu (system_settings "notify_policy") + SAF bildirim kararı NotifiableAt.
//
// v0.10.1533 (R5): `enforce` kipi artık gönderimi yönetir (internal/notify/
// enforce.go; P3 → özet kuyruğu, digest.go). Varsayılan yine `shadow` (D8).
//
// R1'DE HİÇBİR GÖNDERİM DEĞİŞMEZ. Karar yalnız GÖLGE kipte ölçülür
// (internal/notify/policy_shadow.go notification_log'a channel_kind='policy'
// satırı yazar) ve replay ucu (api/notify_policy_replay.go) geçmiş 14 günü
// bu kararla yeniden oynatır. `enforce` değeri R1'de kabul edilir ama
// davranışı shadow ile AYNIDIR — gönderimi değiştiren kablo R5'te gelir
// (7 günlük gölge + %100 recall kriteri, D8).
//
// Operatör kararları (2026-10-24, docs/DECISIONS.md):
//
//	D2  bildirim önceliği = GÖRÜNÜM önceliği (forceNonExceptionP3 + istisna
//	    listesi; NotifyViewPriority aynı kuralı Problem üzerinde uygular)
//	D3  gürültü tekrarı 12 sa; öncelik yükselirse, etki ≥ 2× ise ya da
//	    yüksek güvenli P1 ise baypas
//	D5  mevcut kalıcı "problem değil" işaretleri bildirimde ZAMAN SINIRLI
//	    + baypaslı; kalıcı gizleme yalnız yönetici. v0.10.1531 (R3): pencere
//	    kendi vidasında — noiseMuteHours (varsayılan 168 sa = 7 gün; R1'de
//	    tekrar penceresi noiseDedupeHours'u paylaşıyordu)
//	D6  etki tabanları — hata: ≥30 başarısız / 5 dk YA DA ≥ %1 (yüzde kolu
//	    yalnız ≥30 çağrı ölçülmüşken: "asla yalnız yüzde"); gecikme: p99
//	    ≥ +300 ms VE ≥30 çağrı / 5 dk; trafik düşüşü ≥ %40 VE ≥100 taban çağrı
//	D7  P3 özeti 09:00 ve 16:00 yerel (calendar.timezone)
//	D10 düşük güvenli satır görünür ama bildirilmez
//
// Bilinmiyor bildirimi TUTMAZ: ölçülmemiş taban / kapı geçer. Bildirim
// kaybetmek fazladan bir mailden kötü (NotificationIgnored kapısıyla aynı yön).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// NotifyPolicyKey — system_settings anahtarı.
const NotifyPolicyKey = "notify_policy"

// Kipler.
const (
	NotifyModeOff     = "off"
	NotifyModeShadow  = "shadow"
	NotifyModeEnforce = "enforce"
)

// Bildirim şablonu (v0.10.1532, R4 / D9).
const (
	NotifyTemplateTR     = "tr"     // Türkçe standart şablon + yaşam döngüsü + tek konu (varsayılan)
	NotifyTemplateLegacy = "legacy" // v0.10.1531 öncesi biçim (geri dönüş anahtarı)
)

// P3 davranışı.
const (
	NotifyP3Digest    = "digest"
	NotifyP3Off       = "off"
	NotifyP3Immediate = "immediate"
)

// Tutma (hold) kodları — replay "holds per gate" bu anahtarla sayar.
const (
	NotifyHoldP3Digest  = "p3:digest"
	NotifyHoldP3Off     = "p3:off"
	NotifyHoldHidden    = "hidden"
	NotifyHoldNoise     = "noise"
	NotifyHoldDedupe    = "dedupe"
	NotifyHoldQuiet     = "quiet_hours"
	NotifyHoldImpactErr = "impact:error"
	NotifyHoldImpactLat = "impact:latency"
	NotifyHoldImpactTrf = "impact:traffic"
	// NotifyHoldGatePrefix + "G1".."G4" — ölçülmüş kapı başarısızlığı (low).
	NotifyHoldGatePrefix = "gate:"
)

// NotifyImpactFloors — D6.
type NotifyImpactFloors struct {
	ErrorFailed5m             float64 `json:"errorFailed5m"`
	ErrorPct                  float64 `json:"errorPct"`
	MinCalls5m                float64 `json:"minCalls5m"`
	LatencyP99DeltaMs         float64 `json:"latencyP99DeltaMs"`
	TrafficDropPct            float64 `json:"trafficDropPct"`
	TrafficMinBaselineCalls5m float64 `json:"trafficMinBaselineCalls5m"`
}

// NotifyQuietHours — yerel "HH:MM" aralığı (gece yarısını geçebilir);
// yalnız P2'ye uygulanır.
type NotifyQuietHours struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// NotifyPolicy — blob.
type NotifyPolicy struct {
	Mode             string            `json:"mode"`
	P3               string            `json:"p3"`
	DigestTimes      []string          `json:"digestTimes"`
	QuietHours       *NotifyQuietHours `json:"quietHours,omitempty"`
	NoiseDedupeHours float64           `json:"noiseDedupeHours"`
	// NoiseMuteHours — v0.10.1531 (R3, D5): "problem değil" işaretinin
	// bildirimi susturduğu süre (işaret anından). 0 = varsayılan (168).
	NoiseMuteHours float64 `json:"noiseMuteHours"`
	// Template — v0.10.1532 (R4, D9): "tr" | "legacy"; "" = varsayılan (tr).
	Template     string             `json:"template,omitempty"`
	ImpactFloors NotifyImpactFloors `json:"impactFloors"`
	UpdatedBy    string             `json:"updatedBy,omitempty"`
	UpdatedAt    int64              `json:"updatedAt,omitempty"`
}

// DefaultNotifyPolicy — varsayılan: GÖLGE.
func DefaultNotifyPolicy() NotifyPolicy {
	return NotifyPolicy{
		Mode:             NotifyModeShadow,
		P3:               NotifyP3Digest,
		DigestTimes:      []string{"09:00", "16:00"},
		NoiseDedupeHours: 12,
		NoiseMuteHours:   168,
		Template:         NotifyTemplateTR,
		ImpactFloors: NotifyImpactFloors{
			ErrorFailed5m:             30,
			ErrorPct:                  1,
			MinCalls5m:                30,
			LatencyP99DeltaMs:         300,
			TrafficDropPct:            40,
			TrafficMinBaselineCalls5m: 100,
		},
	}
}

const (
	notifyDigestMax      = 6
	notifyDedupeHoursMax = 168
	notifyMuteHoursMax   = 720 // 30 gün; daha uzunu "kalıcı gizle"nin işi (yönetici)
)

// hhmmMinutes — SAF: "HH:MM" → gün içi dakika (settings.go parseHHMM).
func hhmmMinutes(s string) (int, bool) {
	h, m, ok := parseHHMM(s)
	return h*60 + m, ok
}

// ValidateNotifyPolicy — SAF: PUT kapısı (sessiz kelepçe YOK; operatör
// yazdığının kaydedilmediğini görsün).
func ValidateNotifyPolicy(p NotifyPolicy) error {
	switch p.Mode {
	case NotifyModeOff, NotifyModeShadow, NotifyModeEnforce:
	default:
		return fmt.Errorf("mode must be off|shadow|enforce")
	}
	switch p.P3 {
	case NotifyP3Digest, NotifyP3Off, NotifyP3Immediate:
	default:
		return fmt.Errorf("p3 must be digest|off|immediate")
	}
	if len(p.DigestTimes) > notifyDigestMax {
		return fmt.Errorf("digestTimes: at most %d entries", notifyDigestMax)
	}
	for _, t := range p.DigestTimes {
		if _, ok := hhmmMinutes(t); !ok {
			return fmt.Errorf("digestTimes: %q is not HH:MM", t)
		}
	}
	if q := p.QuietHours; q != nil {
		if _, ok := hhmmMinutes(q.From); !ok {
			return fmt.Errorf("quietHours.from: %q is not HH:MM", q.From)
		}
		if _, ok := hhmmMinutes(q.To); !ok {
			return fmt.Errorf("quietHours.to: %q is not HH:MM", q.To)
		}
	}
	if p.NoiseDedupeHours < 1 || p.NoiseDedupeHours > notifyDedupeHoursMax {
		return fmt.Errorf("noiseDedupeHours must be between 1 and %d", notifyDedupeHoursMax)
	}
	switch p.Template {
	case "", NotifyTemplateTR, NotifyTemplateLegacy:
	default:
		return fmt.Errorf("template must be tr|legacy")
	}
	// 0 = alan gönderilmedi (R3 öncesi istemci) → varsayılan; aksi aralıkta.
	if p.NoiseMuteHours != 0 && (p.NoiseMuteHours < 1 || p.NoiseMuteHours > notifyMuteHoursMax) {
		return fmt.Errorf("noiseMuteHours must be between 1 and %d", notifyMuteHoursMax)
	}
	f := p.ImpactFloors
	for name, v := range map[string]float64{
		"errorFailed5m": f.ErrorFailed5m, "minCalls5m": f.MinCalls5m,
		"latencyP99DeltaMs": f.LatencyP99DeltaMs, "trafficMinBaselineCalls5m": f.TrafficMinBaselineCalls5m,
	} {
		if v < 0 || v > 1e7 {
			return fmt.Errorf("impactFloors.%s out of range", name)
		}
	}
	if f.ErrorPct < 0 || f.ErrorPct > 100 {
		return fmt.Errorf("impactFloors.errorPct must be between 0 and 100")
	}
	if f.TrafficDropPct < 0 || f.TrafficDropPct > 100 {
		return fmt.Errorf("impactFloors.trafficDropPct must be between 0 and 100")
	}
	return nil
}

// NormalizeNotifyPolicy — SAF: okuma tarafı; bozuk alan VARSAYILANA döner
// (kelepçe değil), boş özet listesi `[]` kalır.
func NormalizeNotifyPolicy(p NotifyPolicy) NotifyPolicy {
	d := DefaultNotifyPolicy()
	switch p.Mode {
	case NotifyModeOff, NotifyModeShadow, NotifyModeEnforce:
	default:
		p.Mode = d.Mode
	}
	switch p.P3 {
	case NotifyP3Digest, NotifyP3Off, NotifyP3Immediate:
	default:
		p.P3 = d.P3
	}
	if p.DigestTimes == nil {
		p.DigestTimes = d.DigestTimes
	}
	clean := make([]string, 0, len(p.DigestTimes))
	for _, t := range p.DigestTimes {
		if _, ok := hhmmMinutes(t); ok && len(clean) < notifyDigestMax {
			clean = append(clean, strings.TrimSpace(t))
		}
	}
	p.DigestTimes = clean
	if q := p.QuietHours; q != nil {
		_, ok1 := hhmmMinutes(q.From)
		_, ok2 := hhmmMinutes(q.To)
		if !ok1 || !ok2 {
			p.QuietHours = nil
		}
	}
	if p.NoiseDedupeHours < 1 || p.NoiseDedupeHours > notifyDedupeHoursMax {
		p.NoiseDedupeHours = d.NoiseDedupeHours
	}
	if p.NoiseMuteHours < 1 || p.NoiseMuteHours > notifyMuteHoursMax {
		p.NoiseMuteHours = d.NoiseMuteHours
	}
	if p.Template != NotifyTemplateLegacy {
		p.Template = NotifyTemplateTR
	}
	if p.ImpactFloors == (NotifyImpactFloors{}) {
		p.ImpactFloors = d.ImpactFloors
	}
	return p
}

var notifyPolicyCfg atomic.Pointer[NotifyPolicy]

// CurrentNotifyPolicy — hidrate edilmemişse varsayılan (shadow).
func CurrentNotifyPolicy() NotifyPolicy {
	if c := notifyPolicyCfg.Load(); c != nil {
		return *c
	}
	return DefaultNotifyPolicy()
}

// SetNotifyPolicy — normalize edilmiş hâli yayınlar.
func SetNotifyPolicy(p NotifyPolicy) {
	n := NormalizeNotifyPolicy(p)
	notifyPolicyCfg.Store(&n)
}

// ReadNotifyPolicy — kayıtlı blob; satır yok → varsayılan + nil; CH/JSON
// hatası → hata (ayar ekranı varsayılanla dolup kaydı ezmesin).
func (s *Store) ReadNotifyPolicy(ctx context.Context) (NotifyPolicy, error) {
	raw, err := s.GetSetting(ctx, NotifyPolicyKey)
	if err != nil {
		return NotifyPolicy{}, err
	}
	return decodeNotifyPolicy(raw)
}

// decodeNotifyPolicy — SAF: blob → politika (eksik alan varsayılanında).
func decodeNotifyPolicy(raw []byte) (NotifyPolicy, error) {
	if len(raw) == 0 {
		return DefaultNotifyPolicy(), nil
	}
	p := DefaultNotifyPolicy()
	if err := json.Unmarshal(raw, &p); err != nil {
		return NotifyPolicy{}, fmt.Errorf("notify_policy blob'u çözülemedi: %w", err)
	}
	return NormalizeNotifyPolicy(p), nil
}

// SaveNotifyPolicy — normalize edilmiş hâli yazar.
func (s *Store) SaveNotifyPolicy(ctx context.Context, p NotifyPolicy) error {
	raw, err := json.Marshal(NormalizeNotifyPolicy(p))
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, NotifyPolicyKey, raw)
}

var notifyPolicyReadFailing atomic.Bool

// LoadNotifyPolicyWith — boot + 30 sn yenileme gövdesi; okuma hatasında SON
// İYİ DEĞER korunur (problem_priority emsali).
func LoadNotifyPolicyWith(read func() (NotifyPolicy, error)) NotifyPolicy {
	p, err := read()
	if err != nil {
		if notifyPolicyReadFailing.CompareAndSwap(false, true) {
			log.Printf("[settings] notify_policy okunamadı: %v — son yayınlanan değer korunuyor", err)
		}
		return CurrentNotifyPolicy()
	}
	notifyPolicyReadFailing.Store(false)
	SetNotifyPolicy(p)
	return CurrentNotifyPolicy()
}

// ── D2 — bildirim önceliği = görünüm önceliği ──────────────────────────

// NotifyViewPriority — SAF: Problem'in /inbox GÖRÜNÜM önceliği
// (api.forceNonExceptionP3With'in Problem üzerindeki ikizi; pin
// api/notify_view_priority_pin_test.go). p.Priority önceden hesaplanmış
// olmalı (notify withPriority / EnrichProblemsWithPriority).
//
//	exception / HTTP grubu → kendi merdiveni (dokunulmaz)
//	zaten P3                → P3
//	istisna listesinde      → kaynak önceliği (incident: "incident:<severity>")
//	diğer                   → P3
func NotifyViewPriority(p Problem, keep []string) string {
	prio := p.Priority
	if prio == "" {
		prio = "P3"
	}
	if strings.HasPrefix(p.RuleID, ExceptionGroupRulePrefix) {
		return prio
	}
	if prio == "P3" {
		return "P3"
	}
	id := p.RuleID
	if p.Kind == NotifyKindIncident || strings.HasPrefix(p.RuleID, "incident:") {
		id = "incident:" + p.Severity
	}
	if _, ok := MatchInboxKeepSourcePriority(keep, id); ok && (prio == "P1" || prio == "P2") {
		return prio
	}
	return "P3"
}

// ── NotifiableAt ────────────────────────────────────────────────────────

// NotifyRecord — aynı imzanın yeni politikaya göre son "gönderilir" kararı.
type NotifyRecord struct {
	At        int64   `json:"at"` // unix ns
	Priority  string  `json:"priority"`
	Magnitude float64 `json:"magnitude"`
}

// NoiseMark — "problem değil" işareti. Permanent = yönetici kalıcı gizleme
// (R3); mevcut işaretlerin hepsi Permanent=false (D5: zaman sınırlı).
// Priority / Magnitude işaret anındaki değer (bilinmiyorsa boş / 0).
type NoiseMark struct {
	At        int64   `json:"at"`
	Permanent bool    `json:"permanent,omitempty"`
	Priority  string  `json:"priority,omitempty"`
	Magnitude float64 `json:"magnitude,omitempty"`
}

// NotifyHistory — NotifiableAt'ın "kararlar" girdisi: imzanın işareti ve
// son gönderim kaydı. "Gerçek problem" işareti BURADA YOK (D4: işaret karar
// değiştirmez, değerlendirme etiketi değildir).
type NotifyHistory struct {
	Noise    *NoiseMark
	LastSend *NotifyRecord
	// Merged — v0.10.2341 (entity grafı S4): sonradan bağlanan küme üyesi;
	// yalnız S4 açıkken dolar (nil = bugünkü karar, bayt bayt).
	Merged *MergeHold
	// Frequent — v0.10.2342 (entity grafı S5): "sık tekrar eden" kararı;
	// yalnız S5 açıkken dolar (nil = bugünkü karar).
	Frequent *FrequentVerdict
	// HeldPriority — bu Problem'in açılışı S4/S5 kuralıyla tutulduğunda
	// görünüm önceliği (süreç içi defter); yükselirse baypas. "" = yok.
	HeldPriority string
}

// NotifyDecision — karar.
type NotifyDecision struct {
	Notifiable bool    `json:"notifiable"`
	Hold       string  `json:"hold,omitempty"`
	Bypass     string  `json:"bypass,omitempty"`
	Digest     bool    `json:"digest,omitempty"`
	Priority   string  `json:"priority"`
	Confidence string  `json:"confidence"`
	Magnitude  float64 `json:"magnitude"`
}

func prioRank(p string) int {
	switch p {
	case "P1":
		return 3
	case "P2":
		return 2
	case "P3":
		return 1
	}
	return 0
}

// StdMagnitude — SAF: dedupe / baypas kıyasının büyüklüğü (oran; yoksa değer).
func StdMagnitude(std ProblemStandard) float64 {
	if r := std.Impact.Primary.Ratio; r > 0 {
		return r
	}
	return std.Impact.Primary.Value
}

// notifyBypass — SAF (D3): tekrar/işaret penceresini aşan değişiklik.
func notifyBypass(std ProblemStandard, prio, refPrio string, refMag float64) string {
	// v0.10.2383 — politika seviyesi (ML kaynak işareti kararı değiştirmez).
	if prio == "P1" && std.Confidence.policyLevel() == ConfidenceHigh {
		return "high_conf_p1"
	}
	if refPrio != "" && prioRank(prio) > prioRank(refPrio) {
		return "priority_up"
	}
	if m := StdMagnitude(std); refMag > 0 && m >= 2*refMag {
		return "impact_2x"
	}
	return ""
}

// impactFloorHold — SAF (D6): ölçülmüş taban ihlali → hold kodu; bilinmeyen
// GEÇER.
func impactFloorHold(std ProblemStandard, f NotifyImpactFloors) string {
	m := std.Impact.Floor
	switch std.Family {
	case StdFamilyError:
		// Sayı kolu YA DA yüzde kolu; yüzde kolu yalnız çağrı tabanı ölçülüp
		// geçilmişken (asla yalnız yüzde). İki kol da ölçülüp geçilmediyse tut.
		countKnown := m.Failed5m != nil
		countMet := countKnown && *m.Failed5m >= f.ErrorFailed5m
		pctKnown := m.ErrorPct != nil && m.Calls5m != nil
		pctMet := pctKnown && *m.ErrorPct >= f.ErrorPct && *m.Calls5m >= f.MinCalls5m
		if countMet || pctMet {
			return ""
		}
		if countKnown && pctKnown {
			return NotifyHoldImpactErr
		}
		// Bir kol bilinmiyor: ölçülen kol tutmadı ama bilinmeyen kol tutabilir
		// → geçer. İstisna: çağrı sayısı ölçülmüş ve tabanın altındaysa yüzde
		// kolu imkânsızdır; sayı kolu da ölçülmüş ve tutmamışsa yukarıda.
		if countKnown && m.Calls5m != nil && *m.Calls5m < f.MinCalls5m {
			return NotifyHoldImpactErr
		}
		return ""
	case StdFamilyLatency:
		if m.P99DeltaMs != nil && *m.P99DeltaMs < f.LatencyP99DeltaMs {
			return NotifyHoldImpactLat
		}
		if m.Calls5m != nil && *m.Calls5m < f.MinCalls5m {
			return NotifyHoldImpactLat
		}
	case StdFamilyTraffic:
		if m.DropPct == nil {
			return "" // artış: taban yok
		}
		if *m.DropPct < f.TrafficDropPct {
			return NotifyHoldImpactTrf
		}
		if m.BaselineCalls5m != nil && *m.BaselineCalls5m < f.TrafficMinBaselineCalls5m {
			return NotifyHoldImpactTrf
		}
	}
	return ""
}

// inQuietHours — SAF: now (yerel) [from, to) içinde mi; gece yarısını geçen
// aralık desteklenir; from == to = boş aralık.
func inQuietHours(q *NotifyQuietHours, now time.Time) bool {
	if q == nil {
		return false
	}
	from, ok1 := hhmmMinutes(q.From)
	to, ok2 := hhmmMinutes(q.To)
	if !ok1 || !ok2 || from == to {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if from < to {
		return cur >= from && cur < to
	}
	return cur >= from || cur < to
}

// NotifiableAt — SAF: yeni politikanın bu açılış için kararı. now YEREL
// saatte gelmeli (calendar.timezone; sessiz saat yerel duvar saatiyle
// ölçülür). prio = görünüm önceliği (NotifyViewPriority).
//
// Sıra (ilk tutan kazanır, replay bu kodu sayar):
//
//	kalıcı gizleme → P3 (özet / kapalı) → ölçülmüş kapı başarısızlığı (low)
//	→ etki tabanı → sık tekrar (S5) → küme üyesi (S4; ikisi yalnız açıkken) → "problem değil"
//	penceresi → tekrar penceresi → sessiz saat
func NotifiableAt(std ProblemStandard, prio string, hist NotifyHistory, pol NotifyPolicy, now time.Time) NotifyDecision {
	pol = NormalizeNotifyPolicy(pol)
	d := NotifyDecision{Priority: prio, Confidence: std.Confidence.policyLevel(), Magnitude: StdMagnitude(std)}
	hold := func(code string) NotifyDecision {
		d.Hold = code
		return d
	}
	if hist.Noise != nil && hist.Noise.Permanent {
		return hold(NotifyHoldHidden)
	}
	if prioRank(prio) <= 1 {
		switch pol.P3 {
		case NotifyP3Off:
			return hold(NotifyHoldP3Off)
		case NotifyP3Digest:
			d.Digest = true
			return hold(NotifyHoldP3Digest)
		}
	}
	if std.Confidence.policyLevel() == ConfidenceLow {
		for _, g := range std.Confidence.Gates {
			if g.Status == GateFail && (g.ID == "G1" || g.ID == "G2" || g.ID == "G3" || g.ID == "G4") {
				return hold(NotifyHoldGatePrefix + g.ID)
			}
		}
		return hold(NotifyHoldGatePrefix + "low")
	}
	if h := impactFloorHold(std, pol.ImpactFloors); h != "" {
		return hold(h)
	}
	// v0.10.2342 (S5, tasarım §6) — sık tekrar eden açılış; Frequent nil = adım
	// yok. Yalnız açılış: imzanın tekrar penceresinde gönderimi varsa tekrar
	// penceresi (D3) karar verir.
	if v := hist.Frequent; v != nil && v.Frequent && hist.LastSend == nil {
		if b := frequentBypass(std, prio, hist.HeldPriority, *v); b != "" {
			d.Bypass = b
		} else {
			return hold(NotifyHoldFrequent)
		}
	}
	// v0.10.2341 (S4) — sonradan bağlanan küme üyesi; Merged nil = adım yok.
	if m := hist.Merged; m != nil {
		if b := mergeBypass(std, prio, hist.HeldPriority, *m); b != "" {
			d.Bypass = b
		} else {
			return hold(NotifyHoldMerged)
		}
	}
	nowNs := now.UnixNano()
	muteWin := int64(pol.NoiseMuteHours * float64(time.Hour)) // v0.10.1531 (R3)
	if n := hist.Noise; n != nil && n.At > 0 && n.At <= nowNs && nowNs-n.At < muteWin {
		if b := notifyBypass(std, prio, n.Priority, n.Magnitude); b != "" {
			d.Bypass = b
		} else {
			return hold(NotifyHoldNoise)
		}
	}
	win := int64(pol.NoiseDedupeHours * float64(time.Hour))
	if l := hist.LastSend; l != nil && l.At > 0 && l.At <= nowNs && nowNs-l.At < win {
		if b := notifyBypass(std, prio, l.Priority, l.Magnitude); b != "" {
			d.Bypass = b
		} else {
			return hold(NotifyHoldDedupe)
		}
	}
	if prio == "P2" && inQuietHours(pol.QuietHours, now) {
		return hold(NotifyHoldQuiet)
	}
	d.Notifiable = true
	return d
}

// NoiseSilences — SAF (v0.10.1531, Problem standardı R3 / D5): "problem değil"
// işareti bu bildirimi susturur mu. Bugünkü gönderim kapısı (notify
// verdict_silence.go, yönetici muteNotifications politikası açıkken) bunu
// çağırır; NotifiableAt'ın "noise" adımıyla AYNI kural:
//
//	kalıcı (yönetici)              → her zaman susturur
//	işaret noiseMuteHours'tan eski → susturmaz (zaman sınırlı)
//	pencere içinde                 → baypas yoksa susturur (öncelik yükseldi /
//	                                 etki ≥ 2× / yüksek güvenli P1 → gider)
func NoiseSilences(m *NoiseMark, std ProblemStandard, prio string, pol NotifyPolicy, now time.Time) bool {
	if m == nil {
		return false
	}
	if m.Permanent {
		return true
	}
	pol = NormalizeNotifyPolicy(pol)
	nowNs := now.UnixNano()
	win := int64(pol.NoiseMuteHours * float64(time.Hour))
	if m.At <= 0 || m.At > nowNs || nowNs-m.At >= win {
		return false
	}
	return notifyBypass(std, prio, m.Priority, m.Magnitude) == ""
}
