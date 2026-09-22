package routing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type acceptedSend struct {
	acceptance  Acceptance
	fingerprint string
}

type managementMemory struct {
	accepted     map[string]acceptedSend
	channels     []Channel
	templates    []Template
	recipients   map[string]Recipient
	notification Notification
	deliveries   []Delivery
	sendCalls    int
}

func (m *managementMemory) RegisterChannel(_ context.Context, channel Channel) error {
	m.channels = append(m.channels, channel)
	return nil
}

func (m *managementMemory) DisableChannel(_ context.Context, id string, _ int64, at time.Time) (Channel, error) {
	for index := range m.channels {
		if m.channels[index].ID != id {
			continue
		}
		m.channels[index].Enabled = false
		m.channels[index].Revision++
		m.channels[index].UpdatedAt = at
		return m.channels[index], nil
	}
	return Channel{}, ErrNotFound
}

func (m *managementMemory) ListChannels(context.Context) ([]Channel, error) {
	return append([]Channel(nil), m.channels...), nil
}

func (m *managementMemory) CreateTemplate(_ context.Context, template Template) error {
	m.templates = append(m.templates, template)
	return nil
}

func (m *managementMemory) Templates(_ context.Context, key string) ([]Template, error) {
	var variants []Template
	for _, template := range m.templates {
		if template.Key == key {
			variants = append(variants, template)
		}
	}
	return variants, nil
}

func (m *managementMemory) CreateRecipient(_ context.Context, recipient Recipient) error {
	if m.recipients == nil {
		m.recipients = map[string]Recipient{}
	}
	m.recipients[recipient.ID] = recipient
	return nil
}

func (m *managementMemory) Recipient(_ context.Context, id string) (Recipient, error) {
	recipient, ok := m.recipients[id]
	if !ok {
		return Recipient{}, ErrNotFound
	}
	return recipient, nil
}

func (m *managementMemory) AcceptedNotification(_ context.Context, key string) (Acceptance, string, error) {
	accepted, ok := m.accepted[key]
	if !ok {
		return Acceptance{}, "", ErrNotFound
	}
	return accepted.acceptance, accepted.fingerprint, nil
}

func (m *managementMemory) Send(_ context.Context, notification Notification, deliveries []Delivery) (Acceptance, error) {
	m.sendCalls++
	m.notification = notification
	m.deliveries = append([]Delivery(nil), deliveries...)
	acceptance := Acceptance{NotificationID: notification.ID}
	for _, item := range deliveries {
		acceptance.Deliveries = append(acceptance.Deliveries, QueuedDelivery{ID: item.ID, ChannelID: item.ChannelID})
	}
	if m.accepted == nil {
		m.accepted = map[string]acceptedSend{}
	}
	deduplicated := acceptance
	deduplicated.Deduplicated = true
	m.accepted[notification.IdempotencyKey] = acceptedSend{
		acceptance: deduplicated, fingerprint: notification.Fingerprint,
	}
	return acceptance, nil
}

func newTestService(t *testing.T, store *managementMemory) *Service {
	t.Helper()
	sequence := 0
	ids := func(prefix string) (string, error) {
		sequence++
		return prefix + strings.Repeat("0", 3-len(itoa(sequence))) + itoa(sequence), nil
	}
	service, err := NewService(store, "configured-actor", func() time.Time { return time.Unix(10, 0) }, ids)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func itoa(value int) string {
	digits := "0123456789"
	if value < 10 {
		return string(digits[value])
	}
	return itoa(value/10) + string(digits[value%10])
}

func seedTargets(t *testing.T, service *Service) {
	t.Helper()
	ctx := context.Background()
	if _, err := service.RegisterChannel(ctx, "email", KindSMTP); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterChannel(ctx, "chat", KindSlackWebhook); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterChannel(ctx, "pager", KindSlackWebhook); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTemplate(ctx, "invoice-paid", "email", "Invoice {{invoice.id}}", "Paid {{invoice.amount}}"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTemplate(ctx, "invoice-paid", "chat", "", "{{customer}} paid {{invoice.amount}}"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTemplate(ctx, "invoice-paid", "pager", "", "page {{invoice.id}}"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRejectsWhitespaceOnlyActor(t *testing.T) {
	_, err := NewService(&managementMemory{}, " \t\n", time.Now, func(string) (string, error) { return "unused", nil })
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want invalid", err)
	}
}

func TestSendFansOutOnlyToEnabledBindingsOnEnabledChannelsWithVariants(t *testing.T) {
	store := &managementMemory{}
	service := newTestService(t, store)
	seedTargets(t, service)
	ctx := context.Background()
	if _, err := service.RegisterChannel(ctx, "fax", KindSMTP); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DisableChannel(ctx, "pager", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateRecipient(ctx, "cus_acme", []Binding{
		{ChannelID: "email", Address: "billing@acme.example", Enabled: true},
		{ChannelID: "chat", Address: "http://127.0.0.1:19402/hook", Enabled: false},
		{ChannelID: "pager", Address: "http://127.0.0.1:19402/pager", Enabled: true},
		{ChannelID: "fax", Address: "fax@acme.example", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"customer":"Acme","invoice":{"id":"inv_1","amount":4200}}`)
	acceptance, err := service.Send(ctx, SendRequest{
		TemplateKey: "invoice-paid", RecipientID: "cus_acme", Payload: payload, IdempotencyKey: "send-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(acceptance.Deliveries) != 1 || acceptance.Deliveries[0].ChannelID != "email" {
		t.Fatalf("acceptance = %#v, want only the enabled email binding", acceptance)
	}
	queued := store.deliveries[0]
	if queued.Subject != "Invoice inv_1" || queued.Body != "Paid 4200" || queued.Address != "billing@acme.example" {
		t.Fatalf("queued delivery = %#v", queued)
	}
	if queued.Actor != "configured-actor" || queued.State != StatePending {
		t.Fatalf("queued delivery = %#v", queued)
	}
	if string(store.notification.Payload) != string(payload) || store.notification.IdempotencyKey != "send-1" {
		t.Fatalf("notification = %#v", store.notification)
	}
}

func TestSendRejectsMissingVariableBeforePersisting(t *testing.T) {
	store := &managementMemory{}
	service := newTestService(t, store)
	seedTargets(t, service)
	ctx := context.Background()
	if _, err := service.CreateRecipient(ctx, "cus_acme", []Binding{
		{ChannelID: "email", Address: "billing@acme.example", Enabled: true},
		{ChannelID: "chat", Address: "http://127.0.0.1:19402/hook", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Send(ctx, SendRequest{
		TemplateKey:    "invoice-paid",
		RecipientID:    "cus_acme",
		Payload:        []byte(`{"invoice":{"id":"inv_1","amount":1}}`),
		IdempotencyKey: "send-2",
	})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `"customer" is missing`) {
		t.Fatalf("error = %v, want invalid missing customer", err)
	}
	if store.sendCalls != 0 {
		t.Fatalf("send calls = %d, want 0 after a rendering rejection", store.sendCalls)
	}
}

// A reused key must answer the same way whether or not the replacement request would
// independently validate, so the key is settled before any rendering happens.
func TestSendSettlesAReusedKeyBeforeRendering(t *testing.T) {
	store := &managementMemory{}
	service := newTestService(t, store)
	seedTargets(t, service)
	ctx := context.Background()
	if _, err := service.CreateRecipient(ctx, "cus_acme", []Binding{
		{ChannelID: "email", Address: "billing@acme.example", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"customer":"Acme","invoice":{"id":"inv_1","amount":4200}}`)
	first, err := service.Send(ctx, SendRequest{
		TemplateKey: "invoice-paid", RecipientID: "cus_acme", Payload: payload, IdempotencyKey: "reused",
	})
	if err != nil {
		t.Fatal(err)
	}
	sendsAfterFirst := store.sendCalls

	repeat, err := service.Send(ctx, SendRequest{
		TemplateKey: "invoice-paid", RecipientID: "cus_acme", Payload: payload, IdempotencyKey: "reused",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !repeat.Deduplicated || repeat.NotificationID != first.NotificationID {
		t.Fatalf("repeat = %#v, want the first acceptance %s", repeat, first.NotificationID)
	}
	if store.sendCalls != sendsAfterFirst {
		t.Fatalf("send calls = %d, want the deduplicated re-send settled without persisting", store.sendCalls)
	}

	// The replacement payload is missing a variable the template needs. Rendering it would
	// answer 400; the reused key must still answer 409.
	_, err = service.Send(ctx, SendRequest{
		TemplateKey:    "invoice-paid",
		RecipientID:    "cus_acme",
		Payload:        []byte(`{"invoice":{"id":"inv_2"}}`),
		IdempotencyKey: "reused",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want conflict rather than a rendering rejection", err)
	}
}

func TestSendRejectsUnknownTemplateRecipientAndKey(t *testing.T) {
	store := &managementMemory{}
	service := newTestService(t, store)
	seedTargets(t, service)
	ctx := context.Background()
	if _, err := service.CreateRecipient(ctx, "cus_acme", []Binding{
		{ChannelID: "email", Address: "billing@acme.example", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	valid := SendRequest{
		TemplateKey: "invoice-paid", RecipientID: "cus_acme", Payload: []byte(`{}`), IdempotencyKey: "k",
	}
	tests := []struct {
		name   string
		mutate func(*SendRequest)
		want   error
	}{
		{name: "unknown template", mutate: func(r *SendRequest) { r.TemplateKey = "missing" }, want: ErrNotFound},
		{name: "unknown recipient", mutate: func(r *SendRequest) { r.RecipientID = "nobody" }, want: ErrNotFound},
		{name: "bad key", mutate: func(r *SendRequest) { r.IdempotencyKey = "has space" }, want: ErrInvalid},
		{name: "empty key", mutate: func(r *SendRequest) { r.IdempotencyKey = "" }, want: ErrInvalid},
		{name: "array payload", mutate: func(r *SendRequest) { r.Payload = []byte(`[]`) }, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			_, err := service.Send(ctx, request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
