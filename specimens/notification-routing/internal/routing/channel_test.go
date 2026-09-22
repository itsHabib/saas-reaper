package routing

import (
	"errors"
	"testing"
	"time"
)

func TestNewChannelAcceptsKnownKindsOnly(t *testing.T) {
	channel, err := NewChannel("email", KindSMTP, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !channel.Enabled || channel.Revision != 1 {
		t.Fatalf("channel = %#v", channel)
	}
	if _, err := NewChannel("sms", ChannelKind("twilio"), time.Unix(1, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind error = %v, want invalid", err)
	}
	if _, err := NewChannel("E mail", KindSMTP, time.Unix(1, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe id error = %v, want invalid", err)
	}
}

func TestNewRecipientValidatesAddressesByChannelKind(t *testing.T) {
	channels := []Channel{
		{ID: "email", Kind: KindSMTP, Enabled: true},
		{ID: "chat", Kind: KindSlackWebhook, Enabled: true},
	}
	recipient, err := NewRecipient("cus_acme", []Binding{
		{ChannelID: "email", Address: "billing@acme.example", Enabled: true},
		{ChannelID: "chat", Address: "http://127.0.0.1:19402/services/T0/B0/x", Enabled: false},
	}, channels, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(recipient.Bindings) != 2 || recipient.Bindings[1].Enabled {
		t.Fatalf("recipient = %#v", recipient)
	}
	tests := []struct {
		name     string
		bindings []Binding
		want     error
	}{
		{name: "display name", bindings: []Binding{{ChannelID: "email", Address: "Acme <a@b.example>"}}, want: ErrInvalid},
		{name: "not a mailbox", bindings: []Binding{{ChannelID: "email", Address: "billing"}}, want: ErrInvalid},
		{name: "mailbox on chat", bindings: []Binding{{ChannelID: "chat", Address: "a@b.example"}}, want: ErrInvalid},
		{name: "credential URL", bindings: []Binding{{ChannelID: "chat", Address: "https://u@h.example/x"}}, want: ErrInvalid},
		{name: "unknown channel", bindings: []Binding{{ChannelID: "sms", Address: "+15550100"}}, want: ErrNotFound},
		{name: "duplicate channel", bindings: []Binding{
			{ChannelID: "email", Address: "a@b.example"},
			{ChannelID: "email", Address: "c@d.example"},
		}, want: ErrInvalid},
		{name: "no bindings", bindings: nil, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRecipient("cus_acme", test.bindings, channels, time.Unix(1, 0))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
