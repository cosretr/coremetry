package api

// inbox_incidents_retired_test.go — v0.10.1827 (Incidents emekliliği, audit
// §16.1 E2; operatör kararı 2026-11-05). Ana anahtar kapalıyken Problems
// listesi incident satırı üretmez, katlamaz, `incident` türü sözlükte ve
// varsayılanda yok, eski `?kind=incident` bağlantısı hatasız varsayılana
// düşer; açıkken v0.10.1796 davranışı aynen.

import (
	"reflect"
	"strings"
	"testing"
)

func TestInboxKindsForSwitch(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		on   bool
		want []string
	}{
		{"açık: boş = hepsi", "", true, []string{"problem", "exception", "httperror", "anomaly", "incident"}},
		{"açık: incident tek başına", "incident", true, []string{"incident"}},
		{"açık: varsayılan", "problem,anomaly,incident", true, []string{"problem", "anomaly", "incident"}},
		{"emekli: boş = hepsi (incident yok)", "", false, []string{"problem", "exception", "httperror", "anomaly"}},
		{"emekli: yalnız incident → varsayılan", "incident", false, []string{"problem", "anomaly"}},
		{"emekli: eski varsayılan linki", "problem,anomaly,incident", false, []string{"problem", "anomaly"}},
		{"emekli: exception+incident", "exception,incident", false, []string{"exception"}},
		{"emekli: anlamsız = hepsi", "garbage", false, []string{"problem", "exception", "httperror", "anomaly"}},
	}
	for _, c := range cases {
		if got := inboxKindsFromQuery(c.raw, c.on); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, istenen %v", c.name, got, c.want)
		}
	}
	if !reflect.DeepEqual(inboxKindsDefaultFor(true), []string{"problem", "anomaly", "incident"}) ||
		!reflect.DeepEqual(inboxKindsDefaultFor(false), []string{"problem", "anomaly"}) {
		t.Error("varsayılan tür kümesi anahtara göre değil")
	}
	for _, k := range inboxKindsAllFor(false) {
		if k == "incident" {
			t.Error("emekli sözlükte incident olmamalı")
		}
	}
}

func TestInboxReadsIncidents(t *testing.T) {
	cases := []struct {
		on              bool
		status, subject string
		want            bool
	}{
		{true, "open", inboxSubjectService, true},
		{true, "all", inboxSubjectService, true},
		{true, "ignored", inboxSubjectService, false},
		{true, "open", "db", false},
		{false, "open", inboxSubjectService, false},
		{false, "all", inboxSubjectService, false},
	}
	for _, c := range cases {
		if got := inboxReadsIncidents(c.on, c.status, c.subject); got != c.want {
			t.Errorf("on=%v status=%s subject=%s → %v, istenen %v", c.on, c.status, c.subject, got, c.want)
		}
	}
}

// Emekliyken rozet = liste: incident kaynağı okunmaz, katlama yok; aynı
// incident'a bağlı iki db-health P1 problemi İKİ ayrı satır.
func TestInboxBadgeDefaultViewRetired(t *testing.T) {
	withIncidentsOn(t, false)
	items, attached := badgeMixedFixture()
	rows, total := inboxDefaultViewRows(t, items, attached, inboxBadgeQuery(""))
	if total != 2 || len(rows) != 2 {
		t.Fatalf("emekli varsayılan görünüm %d satır / %d toplam, istenen 2 (katlamasız)", len(rows), total)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = true
		if r.Kind == "incident" || r.Incident != nil {
			t.Errorf("emekliyken incident satırı/bağı olmamalı: %+v", r)
		}
	}
	if !got["problem:p-orders"] || !got["problem:p-carts"] {
		t.Errorf("iki Problem kendi satırı olmalı: %v", got)
	}
	if q := inboxBadgeQuery(""); q.Get("kind") != "problem,anomaly" {
		t.Errorf("emekli rozet tür kümesi %q", q.Get("kind"))
	}
}

// Kablolama pini: inboxView anahtarı istek başına bir kez okur ve kaynak,
// katlama, anahtar aynı değerle kapılı.
func TestInboxViewIncidentsSwitchWiring(t *testing.T) {
	body := funcBody(readSrc(t, "inbox.go"), "inboxView")
	for _, want := range []string{
		"incidentsOn := chstore.IncidentsEnabled()",
		"kinds := inboxKindsFromQuery(q.Get(\"kind\"), incidentsOn)",
		"if inboxReadsIncidents(incidentsOn, statusFilter, subject) {",
		"if incidentsOn {\n\t\t\titems = s.foldInboxIncidents(ctx, items)",
		"\":inc=\" + strconv.FormatBool(incidentsOn)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("inboxView: %q yok", want)
		}
	}
	if n := strings.Count(body, "chstore.IncidentsEnabled()"); n != 1 {
		t.Errorf("anahtar istek başına bir kez okunmalı, %d", n)
	}
}
