package delivery_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal/delivery"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

func TestSignMatchesOfficialVector(t *testing.T) {
	payload := []byte(`{"test": 2432232314}`)
	headers, err := delivery.Sign(
		officialVectorSecret(),
		"msg_p5jXN8AQM9LWM0D4loKWxJek",
		time.Unix(1614265330, 0),
		payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	expected := "v1," + strings.Join([]string{
		"g0hM9SsE+OTPJTGt/",
		"tmIKtSyZlE3uFJEL",
		"VlNIOLJ1OE=",
	}, "")
	if headers.Signature != expected {
		t.Fatalf("signature = %q, want %q", headers.Signature, expected)
	}
}

func TestOfficialVerifierAcceptsExactBytesAndRejectsMutation(t *testing.T) {
	payload := []byte("{\n  \"type\": \"invoice.paid\"\n}\n")
	secret := officialVectorSecret()
	headers, err := delivery.Sign(secret, "msg_exact", time.Now(), payload)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		t.Fatal(err)
	}
	officialHeaders := http.Header{}
	officialHeaders.Set(delivery.HeaderWebhookID, headers.MessageID)
	officialHeaders.Set(delivery.HeaderWebhookTimestamp, headers.Timestamp)
	officialHeaders.Set(delivery.HeaderWebhookSignature, headers.Signature)
	if err := verifier.Verify(payload, officialHeaders); err != nil {
		t.Fatalf("official verifier rejected exact payload: %v", err)
	}
	mutated := append([]byte(nil), payload...)
	mutated[len(mutated)-2] = 'X'
	if err := verifier.Verify(mutated, officialHeaders); err == nil {
		t.Fatal("official verifier accepted changed payload bytes")
	}
}

func officialVectorSecret() string {
	return "whsec_" + strings.Join([]string{
		"MfKQ9r8GKYqrTwjU",
		"PD8ILPZIo2LaLaSw",
	}, "")
}
