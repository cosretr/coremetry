package api

// v0.10.1790 — Davis FAZ 1.2: mevsimsel dilim PUT kapısı (bilinmeyen ad 400,
// mağazaya dokunmadan) + özne/dilim değişikliğinin önce/sonra denetim gövdesi.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPutAnomalySensitivityRejectsUnknownSeasonalTimezone(t *testing.T) {
	s := &Server{} // mağaza yok: 400 mağazadan ÖNCE dönmeli
	for _, tz := range []string{"Mars/Olympus", "Local"} {
		body := `{"dwellBuckets":3,"criticalZ":6,"seasonalTimezone":"` + tz + `"}`
		req := httptest.NewRequest("PUT", "/api/settings/anomaly-sensitivity", strings.NewReader(body))
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Role: auth.RoleAdmin}))
		rec := httptest.NewRecorder()
		s.putAnomalySensitivity(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "seasonalTimezone") {
			t.Errorf("%s: %d %q", tz, rec.Code, rec.Body.String())
		}
	}
}

func TestAnomalySubjectsAuditDetail(t *testing.T) {
	prev := chstore.DefaultAnomalySensitivity()
	if _, changed := anomalySubjectsAuditDetail(prev, prev); changed {
		t.Error("değişmeyen PUT denetim satırı yazdı")
	}
	// Başka alan değişince de satır yok.
	other := prev
	other.DwellBuckets = 5
	if _, changed := anomalySubjectsAuditDetail(prev, other); changed {
		t.Error("ilgisiz alan değişikliği anomaly.subjects satırı yazdı")
	}
	for name, mut := range map[string]func(*chstore.AnomalySensitivityConfig){
		"endpoint açıldı": func(c *chstore.AnomalySensitivityConfig) { c.Subjects.Endpoint.Enabled = true },
		"db topN":         func(c *chstore.AnomalySensitivityConfig) { c.Subjects.DB.TopN = 3 },
		"queue tavanı":    func(c *chstore.AnomalySensitivityConfig) { c.Subjects.Queue.MaxSeriesPerTick = 10 },
		"dilim":           func(c *chstore.AnomalySensitivityConfig) { c.SeasonalTimezone = "UTC" },
		"terfi kipi":      func(c *chstore.AnomalySensitivityConfig) { c.Subjects.PromotionMode = chstore.SubjectPromotionShadow },
	} {
		next := prev
		mut(&next)
		d, changed := anomalySubjectsAuditDetail(prev, next)
		if !changed {
			t.Errorf("%s: satır yazılmadı", name)
			continue
		}
		var got struct {
			From, To struct {
				Subjects         chstore.AnomalySubjectsConfig `json:"subjects"`
				SeasonalTimezone string                        `json:"seasonalTimezone"`
			}
		}
		if err := json.Unmarshal([]byte(d), &got); err != nil {
			t.Fatalf("%s: %v (%s)", name, err, d)
		}
		if got.From.Subjects != prev.Subjects || got.To.Subjects != next.Subjects ||
			got.From.SeasonalTimezone != prev.SeasonalTimezone || got.To.SeasonalTimezone != next.SeasonalTimezone {
			t.Errorf("%s: önce/sonra yanlış: %s", name, d)
		}
	}
}

// v0.10.1797 (Davis FAZ 3 dilim 1) — bilinmeyen terfi kipi 400, mağazadan önce.
func TestPutAnomalySensitivityRejectsUnknownPromotionMode(t *testing.T) {
	s := &Server{}
	for _, m := range []string{"liv", "LIVE", "on"} {
		body := `{"dwellBuckets":3,"criticalZ":6,"subjects":{"promotionMode":"` + m + `"}}`
		req := httptest.NewRequest("PUT", "/api/settings/anomaly-sensitivity", strings.NewReader(body))
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Role: auth.RoleAdmin}))
		rec := httptest.NewRecorder()
		s.putAnomalySensitivity(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "promotionMode") {
			t.Errorf("%s: %d %q", m, rec.Code, rec.Body.String())
		}
	}
}
