package api

// ml_precision_shadow.go — v0.10.2486 (precision programı ADIM 2,
// docs/anomaly/precision-program.md B.2): otomatik gölge kuralının
// görünürlüğü ve `on` kipinin onay adımı. Yeni uç YOK:
//   - durum karne zarfında (`GET /api/ml/scorecard` → precisionShadow):
//     kip, dedektör başına durum (açık / düşerdi / gölgede / sabitlenmiş),
//     son 30 günün kayıtları, düşerdi / gölgedeki dedektörlerin pencerede
//     beslediği Problem ve bunların onaylı, gürültü olmayan vakalı olanları.
//   - `PUT /api/admin/ml` kipi `on`a ÇEVİRİRKEN `?confirmPrecisionOn=1`
//     ister; yoksa 409 + "düşerdi" listesi (Settings onay penceresi bunu
//     gösterir). Onaylı geçiş ml.config kaydına ek olarak
//     `ml.precision.enable_on` audit'i düşer (liste + Problem sayıları).

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// mlPrecisionDetView — dedektörün rozet durumu.
type mlPrecisionDetView struct {
	Status string `json:"status"` // on | would_demote | shadow | pinned
	Pin    string `json:"pin,omitempty"`
	Since  int64  `json:"since,omitempty"` // düşerdi / gölgede başlangıcı (unix sn)
	Reason string `json:"reason,omitempty"`
}

// mlPrecisionView — karne zarfındaki blok.
type mlPrecisionView struct {
	Mode        string                                   `json:"mode"`
	Settings    chstore.MLPrecisionShadow                `json:"settings"`
	Detectors   map[string]mlPrecisionDetView            `json:"detectors"`
	Records     []chstore.MLPrecisionRecord              `json:"records"`
	Fed         map[string]chstore.MLDetectorProblemsFed `json:"fed"` // yalnız düşerdi / gölgedeki dedektörler
	WouldDemote []string                                 `json:"wouldDemote"`
	LastEvalAt  int64                                    `json:"lastEvalAt,omitempty"`
	LastError   string                                   `json:"lastError,omitempty"`
}

// mlPrecisionStatuses — SAF: ayar + durum → dedektör başına rozet ve
// düşerdi / gölgede listesi (sıralı). Pin her şeyi ezer; `on` kipinde
// gerçek kayıt "gölgede", shadow kipinde (ya da sanal kayıt) "düşerdi".
func mlPrecisionStatuses(cfg chstore.MLPrecisionShadow, st chstore.MLPrecisionState) (map[string]mlPrecisionDetView, []string) {
	out := map[string]mlPrecisionDetView{}
	var listed []string
	on := cfg.Mode == chstore.MLPrecisionModeOn
	for _, n := range chstore.MLDetectorNames {
		v := mlPrecisionDetView{Status: chstore.MLPrecStatusOn}
		d := st.Detectors[n]
		if pin := cfg.Pins[n]; pin != "" {
			v = mlPrecisionDetView{Status: chstore.MLPrecStatusPinned, Pin: pin}
		} else if d.Demoted && cfg.Mode != chstore.MLPrecisionModeOff {
			v.Since, v.Reason = d.DemotedAt, d.Reason
			if on && !d.Virtual {
				v.Status = chstore.MLPrecStatusShadow
			} else {
				v.Status = chstore.MLPrecStatusWouldDemote
			}
			listed = append(listed, n)
		}
		out[n] = v
	}
	sort.Strings(listed)
	return out, listed
}

// mlPrecisionView — karne zarfının bloğu (karne önbelleği arkasında; 1 blob
// + gerekirse 1 küçük CH okuması).
func (s *Server) mlPrecisionView(ctx context.Context, cfg chstore.MLConfig) mlPrecisionView {
	pcfg := cfg.PrecisionShadowSettings()
	st, _, err := s.store.GetMLPrecisionState(ctx)
	v := mlPrecisionView{Mode: pcfg.Mode, Settings: pcfg, Records: chstore.PruneMLPrecisionRecords(st.Records, time.Now()),
		Fed: map[string]chstore.MLDetectorProblemsFed{}, LastEvalAt: st.LastEvalAt, LastError: st.LastError}
	if err != nil {
		v.LastError = err.Error()
	}
	v.Detectors, v.WouldDemote = mlPrecisionStatuses(pcfg, st)
	if len(v.WouldDemote) > 0 {
		if fed, ferr := s.store.MLProblemsFed(ctx, time.Now().Add(-time.Duration(pcfg.WindowDays)*24*time.Hour)); ferr == nil {
			for _, d := range v.WouldDemote {
				v.Fed[d] = fed[d]
			}
		}
	}
	return v
}

// mlPrecisionOnTransition — SAF: kayıt kipi `on`a çeviriyor mu.
func mlPrecisionOnTransition(before, after chstore.MLConfig) bool {
	return before.PrecisionShadowMode() != chstore.MLPrecisionModeOn && after.PrecisionShadowMode() == chstore.MLPrecisionModeOn
}

// mlPrecisionConfirmParam — `on` geçişinin açık onayı.
const mlPrecisionConfirmParam = "confirmPrecisionOn"

// mlPrecisionOnGuard — PUT /api/admin/ml: `on` geçişi onaysızsa 409 + rapor
// yazar ve false döner. Onaylıysa rapor döner (audit için).
func (s *Server) mlPrecisionOnGuard(w http.ResponseWriter, r *http.Request, before, after chstore.MLConfig) (*mlPrecisionView, bool) {
	if !mlPrecisionOnTransition(before, after) {
		return nil, true
	}
	v := s.mlPrecisionView(r.Context(), before)
	if r.URL.Query().Get(mlPrecisionConfirmParam) != "1" {
		writeJSONStatus(w, http.StatusConflict, map[string]any{
			"error":        "Otomatik gölge kuralını on kipine almak açık onay ister (düşerdi listesini ve kesilecek gerçek Problem sayısını gözden geçirin).",
			"needsConfirm": true, "wouldDemote": v.WouldDemote, "fed": v.Fed,
		})
		return nil, false
	}
	return &v, true
}

// mlPrecisionOnAudit — onaylı `on` geçişinin ayrı audit kaydı.
func (s *Server) mlPrecisionOnAudit(r *http.Request, v *mlPrecisionView) {
	if v == nil {
		return
	}
	var fed, real int64
	for _, f := range v.Fed {
		fed += f.Fed
		real += f.Real
	}
	details, _ := json.Marshal(map[string]any{"wouldDemote": v.WouldDemote, "fed": v.Fed, "problemsFed": fed, "problemsReal": real,
		"window": v.Settings.WindowDays})
	s.audit(r, "ml.precision.enable_on", "settings", "ml.precisionShadow", string(details))
}
