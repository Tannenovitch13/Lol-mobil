package relay

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	secret := NewSecret()
	a, err := DeriveKeys(secret)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := DeriveKeys(secret)
	if a.RoomID != b.RoomID || len(a.RoomID) != 32 || len(a.HostAuth) != 64 {
		t.Fatalf("bad derivation: %q %q", a.RoomID, a.HostAuth)
	}
	wire := a.Seal([]byte(`{"type":"ping"}`))
	plain, err := b.Open(wire)
	if err != nil || string(plain) != `{"type":"ping"}` {
		t.Fatalf("round trip failed: %v %q", err, plain)
	}
	big := bytes.Repeat([]byte(`{"championId":103,"name":"Ahri"},`), 500)
	plainBig, err := b.Open(a.Seal(big))
	if err != nil || !bytes.Equal(plainBig, big) {
		t.Fatalf("compressed round trip failed: %v", err)
	}
	if len(a.Seal(big)) > len(big)/4 {
		t.Fatalf("large message not compressed")
	}
	other, _ := DeriveKeys(NewSecret())
	if _, err := other.Open(wire); err == nil {
		t.Fatal("a different secret decrypted the message")
	}
}

// The web page (web/js/remote.js) and the Android app (Remote.kt) derive
// the same values; keep this vector in sync with their tests.
func TestKnownVector(t *testing.T) {
	k, err := DeriveKeys("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	if err != nil {
		t.Fatal(err)
	}
	if k.RoomID != "2f61607ffb59ccd5d83b7cf79412cb73" {
		t.Errorf("room id %s", k.RoomID)
	}
	if k.HostAuth != "3bc159045bfcb08401cb1801cccce3d93ada4ea1ecea79cd788148bab5732281" {
		t.Errorf("host auth %s", k.HostAuth)
	}
}
