package finalizeoriginal

import "testing"

func TestOriginalPayloadRoundTrip(t *testing.T) {
	encoded, err := MarshalPayload(Payload{FileID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if encoded != `{"fileId":42}` {
		t.Fatalf("unexpected payload: %s", encoded)
	}
	decoded, err := UnmarshalPayload(encoded)
	if err != nil || decoded.FileID != 42 {
		t.Fatalf("round trip: %+v %v", decoded, err)
	}
}

func TestOriginalPayloadRejectsInvalidInput(t *testing.T) {
	for _, value := range []string{"", "null", "{}", `{"fileId":0}`, `{"fileId":-1}`, `{"fileId":"1"}`,
		`{"fileId":1,"url":"https://attacker.invalid"}`, `{"fileId":1} {"fileId":2}`, `{"fileId":1} garbage`} {
		if _, err := UnmarshalPayload(value); err == nil {
			t.Fatalf("accepted invalid payload %q", value)
		}
	}
	if _, err := MarshalPayload(Payload{}); err == nil {
		t.Fatal("marshalled invalid payload")
	}
}
