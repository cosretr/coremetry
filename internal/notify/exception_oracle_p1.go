package notify

// exception_oracle_p1.go — v0.10.1109 (operatör: "P1 tipinde gözüken oracle
// teknik hataları notifikasyon da gönderilmiş olur").
//
// Oracle grubunun P1'i (api.oraclePriorityAt) yapışkan değil, saatlik
// toplamlardan doğan bir OLAY: patlama (son 1 sa ≥ eşik ve ≥ 3× önceki saat) ya
// da P1 penceresinde yeni grup. (kaynak, kod, operasyon) grupları kalıcı
// olduğundan patlama hemen her zaman günler önce doğmuş bir grupta olur; span
// grubunun tazelik kapısı (`new` yalnız ilk 15 dk) onu hiç geçirmiyordu.
//
//   - Aday: merdivende P1 VE (gecikme kaydırılmış) son 10 dk içinde görülmüş;
//     ilk görülme yaşına bakılmaz (exceptionChannelKey).
//   - Kimlik `exception-group:ora:<hex>:p1` (Sustur bağlantısı kalıcı); grubun
//     yeni-grup Sustur'u da susturur. Grup başına exOracleP1Cooldown'da en çok
//     bir kez (lider defteri + notification_log'un soğuma penceresi kadar okunan
//     son gönderimi — restart'ta çift yok, ertesi patlama yeniden).
//   - Susturulmuş grup defterde de "alındı" sayılır: patlayan susturulmuş grup
//     4 sa boyunca her tik iki CH okuması yapmasın.
//   - Gönderildiğinde grubun taban kimliği (new / regressed) de defterde: aynı
//     bölümün P2'si ayrıca çalmasın (claimRegressed'in P1-doğan emsali).
//   - Kaynak özeti (fırtına): aynı tikte bir kaynağın > exOracleP1RollupMin
//     grubu P1'e girerse (DB kesintisi, deploy, kaynak shadow → live) tek tek
//     N kritik yerine TEK özet — `exception-group:ora-source:<kaynak>:p1`, aynı
//     soğuma; içerdiği grupların `:p1` kimlikleri talep edilmiş sayılır, sonraki
//     tiklerde tek tek çalmazlar.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// exOracleP1Cooldown — Oracle P1 olay bildiriminin grup (ve kaynak özeti)
	// başına soğuma penceresi. Varsayılan P1 penceresiyle (exception_triage.
	// p1FreshHours = 4) aynı boy: yeni grup P1'i en çok bu kadar sürer, patlama ~1 sa.
	exOracleP1Cooldown = 4 * time.Hour
	// exOracleP1RollupMin — bir kaynaktan aynı tikte bundan FAZLA P1 grubu → özet.
	exOracleP1RollupMin = 5
	// exOracleRollupTop — özet gövdesinde listelenen grup sayısı.
	exOracleRollupTop = 10
	// exOracleScanLookback — Oracle kanal taramasının son görülme sınırı:
	// 10 dk tazelik + en çok 30 dk kapanmış dakika gecikmesi.
	exOracleScanLookback = exChannelActiveWin + 30*time.Minute
	// oracleSourceRollupTag — kaynak özeti kimliğinin öneki (önek sonrası).
	oracleSourceRollupTag = "ora-source:"
)

// OracleGroupRef — Oracle grubunun kaynağı + son 1 sa ağırlığı (main.go
// oracle.GroupStatsCache'ten enjekte eder; notify oracle'ı içe aktarmaz).
type OracleGroupRef struct {
	SourceID, SourceName string
	LastHour             uint64
}

// SetOracleGroupRef — Start'tan ÖNCE çağrılır.
func (e *ExceptionNotifier) SetOracleGroupRef(f func(g chstore.ExceptionGroup) (OracleGroupRef, bool)) {
	e.oracleRef = f
}

// exceptionOracleP1ID — SAF: Oracle P1 olayının bildirim kimliği.
func exceptionOracleP1ID(fp string) string { return chstore.ExceptionGroupRulePrefix + fp + ":p1" }

// exceptionOracleSourceP1ID — SAF: kaynak özetinin bildirim kimliği.
func exceptionOracleSourceP1ID(sourceID string) string {
	return chstore.ExceptionGroupRulePrefix + oracleSourceRollupTag + sourceID + ":p1"
}

// isOracleSourceRollupID — SAF: kimlik bir kaynak özeti mi (bağlantı Exceptions'ın
// Oracle çipine gider; tek grup parmak izi yok).
func isOracleSourceRollupID(id string) bool {
	return strings.HasPrefix(id, chstore.ExceptionGroupRulePrefix+oracleSourceRollupTag)
}

// oracleP1Cand — bu tikte P1 olayı adayı (kaynak bilgisi routeOracleP1'de).
type oracleP1Cand struct {
	g      chstore.ExceptionGroup
	state  string
	reason string
	ref    OracleGroupRef
	known  bool // kaynak çözüldü
}

// oracleRollup — bir kaynağın özetlenen adayları.
type oracleRollup struct {
	ref   OracleGroupRef
	items []oracleP1Cand
}

// planOracleP1 — SAF: uygun adaylar → kaynak özetleri + tek tek gidenler.
// Kaynağı bilinmeyen aday hep tek tek; özet sırası kaynağın ilk görülüş sırası.
func planOracleP1(cands []oracleP1Cand) (rollups []oracleRollup, singles []oracleP1Cand) {
	bySrc := map[string]int{}
	var groups []oracleRollup
	for _, c := range cands {
		if !c.known || c.ref.SourceID == "" {
			singles = append(singles, c)
			continue
		}
		i, ok := bySrc[c.ref.SourceID]
		if !ok {
			i = len(groups)
			bySrc[c.ref.SourceID] = i
			groups = append(groups, oracleRollup{ref: c.ref})
		}
		groups[i].items = append(groups[i].items, c)
	}
	for _, r := range groups {
		if len(r.items) > exOracleP1RollupMin {
			rollups = append(rollups, r)
			continue
		}
		singles = append(singles, r.items...)
	}
	return rollups, singles
}

// oracleSourceRollupProblem — SAF: kaynak özetinin sentetik Problem'i. Başlık
// "<etiket> · <kaynak> · N grup P1"; gövde son 1 sa'e göre ilk 10 grup.
func oracleSourceRollupProblem(r oracleRollup, label string) chstore.Problem {
	items := append([]oracleP1Cand(nil), r.items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ref.LastHour != items[j].ref.LastHour {
			return items[i].ref.LastHour > items[j].ref.LastHour
		}
		return items[i].g.Fingerprint < items[j].g.Fingerprint
	})
	name := r.ref.SourceName
	if name == "" {
		name = r.ref.SourceID
	}
	var lines []string
	started := int64(0)
	for i, c := range items {
		if started == 0 || (c.g.FirstSeen > 0 && c.g.FirstSeen < started) {
			started = c.g.FirstSeen
		}
		if i >= exOracleRollupTop {
			continue
		}
		head := c.g.Type
		if op := strings.TrimSpace(c.g.Message); op != "" {
			head += " · " + op
		}
		lines = append(lines, fmt.Sprintf("%s — son 1 sa %d", head, c.ref.LastHour))
	}
	if extra := len(items) - exOracleRollupTop; extra > 0 {
		lines = append(lines, fmt.Sprintf("… +%d grup", extra))
	}
	n := len(items)
	return chstore.Problem{
		ID:             exceptionOracleSourceP1ID(r.ref.SourceID),
		RuleID:         chstore.ExceptionGroupRulePrefix + chstore.ExStateNew,
		RuleName:       fmt.Sprintf("%s · %s · %d grup P1", label, name, n),
		Service:        chstore.OracleGroupFallbackService(name),
		Severity:       "critical",
		Status:         "open",
		Metric:         "exception",
		Priority:       "P1",
		PriorityReason: fmt.Sprintf("aynı anda %d %s grubu P1 (kaynak özeti, > %d grup)", n, label, exOracleP1RollupMin),
		Description:    strings.Join(lines, "; "),
		StartedAt:      started,
	}
}

// sentWithin — id son `win` içinde gönderildi mi (soğuma). Önce lider-yerel
// defter, sonra notification_log'un YALNIZ son `win`'i (restart / lider
// değişimi). Okunamadı = gönderilmemiş say (v0.10.782 yönü).
func (e *ExceptionNotifier) sentWithin(ctx context.Context, id string, now time.Time, win time.Duration) bool {
	if at, ok := e.sent[id]; ok && now.UnixNano()-at < int64(win) {
		return true
	}
	if e.lastSent == nil {
		return false
	}
	at, err := e.lastSent(ctx, id, win)
	if err != nil || at.IsZero() {
		return false
	}
	e.sent[id] = at.UnixNano()
	return now.Sub(at) < win
}

// oracleP1Eligible — grup bu tikte P1 olayı olarak çalabilir mi: soğumada
// değil ve grubun yeni-grup bildirimi Sustur'lanmamış. Susturulmuş grup
// defterde "alındı" işaretlenir (negatif önbellek: 4 sa boyunca CH okuması yok).
func (e *ExceptionNotifier) oracleP1Eligible(ctx context.Context, g chstore.ExceptionGroup, now time.Time) bool {
	id := exceptionOracleP1ID(g.Fingerprint)
	if e.sentWithin(ctx, id, now, exOracleP1Cooldown) {
		return false
	}
	if e.ignored != nil {
		if ok, err := e.ignored(ctx, exceptionGroupID(g.Fingerprint, chstore.ExStateNew)); err == nil && ok {
			e.sent[id] = now.UnixNano()
			return false
		}
	}
	return true
}

// markOracleP1 — P1 olayı (tek tek ya da özet içinde) gönderildi: `:p1` ve
// grubun bu durumdaki taban kimliği defterde (aynı bölümün P2'si çalmasın).
func (e *ExceptionNotifier) markOracleP1(c oracleP1Cand, now time.Time) {
	e.sent[exceptionOracleP1ID(c.g.Fingerprint)] = now.UnixNano()
	e.sent[exceptionGroupID(c.g.Fingerprint, c.state)] = now.UnixNano()
}

func (e *ExceptionNotifier) deliver(ctx context.Context, p chstore.Problem) {
	if e.send != nil {
		e.send(ctx, p)
		return
	}
	e.n.SendProblemAlert(ctx, p)
}

// routeOracleP1 — tik sonu: uygun adaylar kaynak başına özetlenir ya da tek
// tek gider; tik tavanı (sent) paylaşılır. Dönen: güncel gönderim sayısı.
func (e *ExceptionNotifier) routeOracleP1(ctx context.Context, now time.Time, cands []oracleP1Cand, sent int) int {
	seen := map[string]bool{}
	var ok []oracleP1Cand
	for _, c := range cands {
		if seen[c.g.Fingerprint] || !e.oracleP1Eligible(ctx, c.g, now) {
			continue
		}
		seen[c.g.Fingerprint] = true
		if e.oracleRef != nil {
			c.ref, c.known = e.oracleRef(c.g)
		}
		ok = append(ok, c)
	}
	rollups, singles := planOracleP1(ok)
	label := chstore.CurrentOracleGroupLabel()
	for _, r := range rollups {
		if sent >= exChannelMaxPerTick {
			log.Printf("[exception-notifier] tik tavanı (%d) — Oracle P1 özetleri sonraki tikte", exChannelMaxPerTick)
			return sent
		}
		// Gruplar özete dahil: tek tek ÇALMAZLAR (özet soğumadaysa da).
		for _, c := range r.items {
			e.markOracleP1(c, now)
		}
		id := exceptionOracleSourceP1ID(r.ref.SourceID)
		if e.sentWithin(ctx, id, now, exOracleP1Cooldown) {
			continue
		}
		e.sent[id] = now.UnixNano()
		e.deliver(ctx, oracleSourceRollupProblem(r, label))
		sent++
	}
	for _, c := range singles {
		if sent >= exChannelMaxPerTick {
			log.Printf("[exception-notifier] tik tavanı (%d) — kalan Oracle P1 grupları sonraki tikte", exChannelMaxPerTick)
			return sent
		}
		e.markOracleP1(c, now)
		p := exceptionGroupProblem(c.g, c.state, "P1", c.reason)
		p.ID = exceptionOracleP1ID(c.g.Fingerprint)
		e.deliver(ctx, p)
		sent++
	}
	return sent
}
