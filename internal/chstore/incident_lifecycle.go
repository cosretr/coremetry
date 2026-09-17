package chstore

// incident_lifecycle.go — v0.10.748: incident yaşam döngüsü kancası
// (operatör: "anomali, incident ve problems ayrı ayrı gelsin", Dilim 2).
//
// Incident açılışı/çözümü bugüne dek HİÇ bildirim üretmiyordu. Dört yol
// var — otomatik korelasyon (AttachProblemToIncidentWith), manuel API
// açılışı, otomatik kademeli çözüm (evaluator cascade), manuel çözüm
// (API) — ve dördü de zaten bir IncidentEvent yazıyor. Tek boğaz:
// AppendIncidentLifecycle = olayı yaz + kancayı çağır. Yeni bir açılış
// yolu eklerken ham AppendIncidentEvent değil BU çağrılır; pin
// incident_lifecycle_test.go'da (dört yol + main.go bağlaması).
//
// Kanca boot'ta bağlanır (main.go → notify.Notifier.IncidentLifecycle):
// store notify'ı import edemez (döngü), o yüzden fonksiyon değeri.
// nil → yalnız olay yazılır (testler, notifier'sız roller).

import "context"

// IncidentLifecycleHook — created/resolved olayında çağrılır. Notify
// tarafı kendi `go`suyla arka plana alır; burada çağıranı bloklamaz.
type IncidentLifecycleHook func(ctx context.Context, inc Incident, ev IncidentEvent)

// SetIncidentLifecycleHook — boot'ta, işçiler başlamadan ÖNCE
// (neighborProvider ile aynı disiplin: kilitsiz alan, tek yazım).
func (s *Store) SetIncidentLifecycleHook(h IncidentLifecycleHook) {
	s.incidentLifecycle = h
}

// incidentLifecycleNotifies — SAF: hangi olay türü bildirim üretir.
// created + resolved. ack / note / problem_attached / problem_resolved
// üretmez: ack zaten "biri baktı" demek, diğerleri kanal gürültüsü.
func incidentLifecycleNotifies(kind string) bool {
	return kind == "created" || kind == "resolved"
}

// AppendIncidentLifecycle — olayı yazar, sonra kancayı çağırır. Olay
// satırının yazım HATASI kancayı ENGELLEMEZ: incident satırı bu noktada
// zaten upsert edilmiş durumda, bildirim incident hakkındadır, zaman
// çizelgesi satırı hakkında değil. Hata çağırana aynen döner.
func (s *Store) AppendIncidentLifecycle(ctx context.Context, inc Incident, ev IncidentEvent) error {
	if ev.IncidentID == "" {
		ev.IncidentID = inc.ID
	}
	err := s.AppendIncidentEvent(ctx, ev)
	if h := s.incidentLifecycle; h != nil && incidentLifecycleNotifies(ev.Kind) {
		h(ctx, inc, ev)
	}
	return err
}
