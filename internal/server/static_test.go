package server

import (
	"encoding/json"
	"testing"
)

func TestTrim(t *testing.T) {
	raw := json.RawMessage(`[{"id":8112,"name":"Électrocution","iconPath":"/x.png","longDesc":"very long...","tooltip":"..."}]`)
	out, err := trim(raw, staticFields["/lol-perks/v1/perks"])
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `[{"iconPath":"/x.png","id":8112,"name":"Électrocution"}]` {
		t.Errorf("trimmed: %s", out)
	}
}
