package ml

// profile_run.go — v0.10.1816: profil revizyonları (ml_learned kind=profile)
// + haftalık koşu (günlük baseline işinin içinde, lider, aynı anahtar).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ProfileParams — ml_learned.params (kind=profile).
type ProfileParams struct {
	Profile string `json:"profile"`
	Pinned  bool   `json:"pinned"`
	Reason  string `json:"reason,omitempty"`
	// Auto — sınıflandırıcının son kararı (sabitleme kalkınca dönülen değer).
	Auto string `json:"auto,omitempty"`
	// Window — değerlendirilen pencerenin sonu (pazartesi, YYYY-MM-DD).
	Window string `json:"window,omitempty"`
}

// ServiceProfile — API satırı (types.ts MLServiceProfile).
type ServiceProfile struct {
	Service   string           `json:"service"`
	Profile   string           `json:"profile"`
	Pinned    bool             `json:"pinned"`
	Reason    string           `json:"reason,omitempty"`
	Source    string           `json:"source"` // auto | manual
	By        string           `json:"by,omitempty"`
	ChangedAt int64            `json:"changedAt"` // unix sn
	Rev       uint64           `json:"rev"`
	Auto      string           `json:"auto,omitempty"`
	Evidence  *ProfileEvidence `json:"evidence,omitempty"`
}

const profileAuthorAuto = "auto"

// ServiceProfileFromRow — SAF: revizyon satırı → API satırı. Boş satır = bilinmiyor.
func ServiceProfileFromRow(r chstore.MLLearnedRow) ServiceProfile {
	out := ServiceProfile{Service: r.Key, Profile: ProfileUnknown, Source: "auto", Rev: r.Rev}
	if r.Params == "" {
		return out
	}
	var p ProfileParams
	if json.Unmarshal([]byte(r.Params), &p) == nil && p.Profile != "" {
		out.Profile, out.Pinned, out.Reason, out.Auto = p.Profile, p.Pinned, p.Reason, p.Auto
	}
	if out.Pinned {
		out.Source = "manual"
	}
	if r.CreatedBy != profileAuthorAuto {
		out.By = r.CreatedBy
	}
	if !r.CreatedAt.IsZero() {
		out.ChangedAt = r.CreatedAt.Unix()
	}
	var ev ProfileEvidence
	if r.Stats != "" && json.Unmarshal([]byte(r.Stats), &ev) == nil {
		out.Evidence = &ev
	}
	return out
}

func profileRow(service string, prevRev uint64, p ProfileParams, stats string, by string, now time.Time) chstore.MLLearnedRow {
	raw, _ := json.Marshal(p)
	return chstore.MLLearnedRow{Kind: chstore.MLLearnedKindProfile, Key: service, Rev: chstore.MLLearnedNextRev(prevRev, now),
		Params: string(raw), Stats: stats, CreatedAt: now, CreatedBy: by}
}

// PinProfileRow — SAF: elle sabitleme revizyonu (kanıt + son otomatik karar taşınır).
func PinProfileRow(service string, prev chstore.MLLearnedRow, have bool, profile, reason, by string, now time.Time) chstore.MLLearnedRow {
	cur := ServiceProfileFromRow(prev)
	auto := cur.Auto
	if !have {
		auto = ""
	} else if auto == "" && !cur.Pinned {
		auto = cur.Profile
	}
	return profileRow(service, prev.Rev, ProfileParams{Profile: profile, Pinned: true, Reason: reason, Auto: auto}, prev.Stats, by, now)
}

// UnpinProfileRow — SAF: sabitlemeyi kaldırır; profil son otomatik karara
// (yoksa bilinmiyor) döner — sınıflandırıcı sonraki haftalarda devralır.
func UnpinProfileRow(service string, prev chstore.MLLearnedRow, by string, now time.Time) chstore.MLLearnedRow {
	cur := ServiceProfileFromRow(prev)
	back := cur.Auto
	if back == "" {
		back = ProfileUnknown
	}
	return profileRow(service, prev.Rev, ProfileParams{Profile: back, Auto: cur.Auto}, prev.Stats, by, now)
}

// profileStore — CH dikişi.
type profileStore interface {
	MLProfileCalls(ctx context.Context, from, to time.Time, slice, slices int, fn func(service string, obs []chstore.MLBaselineObs) error) error
	MLLearnedLatest(ctx context.Context, q chstore.MLLearnedQuery) ([]chstore.MLLearnedRow, int, error)
	InsertMLLearned(ctx context.Context, rows []chstore.MLLearnedRow) error
}

// profileRunResult — haftalık koşu özeti.
type profileRunResult struct {
	Services, Changed int
	Duration          time.Duration
}

// runProfiles — hafta W (pazartesi, UTC tarih) için: [W−5h, W) "_calls"
// gözlemleri 16 dilimde okunur; servis başına bu hafta ve önceki hafta
// değerlendirilir, yalnız değişim (histerezisli) revizyon yazar.
func runProfiles(ctx context.Context, st profileStore, week time.Time, isBatch func(string) bool, now time.Time) (profileRunResult, error) {
	t0 := time.Now()
	var res profileRunResult
	latest, _, err := st.MLLearnedLatest(ctx, chstore.MLLearnedQuery{Kind: chstore.MLLearnedKindProfile})
	if err != nil {
		return res, fmt.Errorf("profil revizyonları okunamadı: %w", err)
	}
	cur := make(map[string]chstore.MLLearnedRow, len(latest))
	for _, r := range latest {
		cur[r.Key] = r
	}
	prevWeek := week.AddDate(0, 0, -7)
	var writes []chstore.MLLearnedRow
	for slice := 0; slice < baselineSlices; slice++ {
		err := st.MLProfileCalls(ctx, week.AddDate(0, 0, -7*(profileWindowWeeks+1)), week, slice, baselineSlices, func(svc string, obs []chstore.MLBaselineObs) error {
			res.Services++
			nowC, ev := ClassifyProfile(svc, obs, week, isBatch)
			prevC, _ := ClassifyProfile(svc, obs, prevWeek, isBatch)
			prev, have := cur[svc]
			cp := ServiceProfileFromRow(prev)
			curProfile := ""
			if have {
				curProfile = cp.Profile
			}
			next, write := profileDecision(curProfile, cp.Pinned, nowC, prevC)
			if !write {
				return nil
			}
			stats, _ := json.Marshal(roundEvidence(ev))
			writes = append(writes, profileRow(svc, prev.Rev, ProfileParams{Profile: next, Auto: next, Window: week.Format("2006-01-02")}, string(stats), profileAuthorAuto, now))
			return nil
		})
		if err != nil {
			res.Duration = time.Since(t0)
			return res, fmt.Errorf("dilim %d/%d: %w", slice+1, baselineSlices, err)
		}
	}
	for i := 0; i < len(writes); i += 5000 {
		end := min(i+5000, len(writes))
		if err := st.InsertMLLearned(ctx, writes[i:end]); err != nil {
			res.Duration = time.Since(t0)
			return res, err
		}
	}
	res.Changed = len(writes)
	res.Duration = time.Since(t0)
	return res, nil
}

// profileDueWeek — SAF: son işlenen günün haftası (pazartesisi işlendiyse
// önceki hafta tamam) henüz değerlendirilmediyse o haftanın pazartesisi.
func profileDueWeek(lastDay, doneWeek string, loc *time.Location) (time.Time, bool) {
	d, err := time.ParseInLocation("2006-01-02", lastDay, loc)
	if err != nil {
		return time.Time{}, false
	}
	_, ws := chstore.MLBaselineSlot(d.Add(12*time.Hour), loc)
	if ws.Format("2006-01-02") == doneWeek {
		return time.Time{}, false
	}
	return ws, true
}
