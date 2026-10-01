package mcp

import (
	"testing"
)

func TestRegistrationDefinitionStrictNormalization(t *testing.T) {
	minimal := []byte(`{"name":"exact-name","transport":"stdio","command":"/bin/backend"}`)
	input, canonical, err := DecodeRegistrationJSON(minimal, false)
	if err != nil {
		t.Fatal(err)
	}
	if input.SessionMode != "stateless" || input.Args == nil || input.Env == nil {
		t.Fatal("defaults were not normalized")
	}
	_, equivalent, err := DecodeRegistrationJSON([]byte(`{"command":"/bin/backend","transport":"stdio","name":"exact-name","env":null,"args":null,"session_mode":"","description":""}`), false)
	if err != nil || canonical != equivalent {
		t.Fatalf("equivalent defaults mismatch: %v", err)
	}
	invalid := []string{
		`{"name":"x","name":"y","transport":"stdio","command":"/bin/a"}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","env":{"a":"1","a":"2"}}`,
		`{"name":"x","transport":"stdio","command":"relative"}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","url":""}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","args":[1]}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","env":{"a":null}}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","description":null}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","session_mode":false}`,
		`{"name":"x","transport":"stdio","command":"/bin/a"} {}`,
		`{"name":"x__bad","transport":"stdio","command":"/bin/a"}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","description":"\ud800"}`,
		`{"name":"x","transport":"stdio","command":"/bin/a","description":"\udc00"}`,
	}
	for _, data := range invalid {
		if _, _, err := DecodeRegistrationJSON([]byte(data), false); err == nil {
			t.Errorf("accepted invalid definition %s", data)
		}
	}
	_, first, err := DecodeRegistrationJSON([]byte(`{"name":"x","transport":"stdio","command":"/bin/a","args":["a","b"],"env":{"z":"1","a":"2"}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := DecodeRegistrationJSON([]byte(`{"name":"x","transport":"stdio","command":"/bin/a","args":["a","b"],"env":{"a":"2","z":"1"}}`), false)
	if err != nil || first != second {
		t.Fatal("map ordering changed definition")
	}
	_, third, err := DecodeRegistrationJSON([]byte(`{"name":"x","transport":"stdio","command":"/bin/a","args":["b","a"],"env":{"a":"2","z":"1"}}`), false)
	if err != nil || first == third {
		t.Fatal("argument order lost")
	}
	if _, _, err := DecodeRegistrationJSON([]byte(`{"registration":`+string(minimal)+`}`), true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeRegistrationJSON([]byte(`{"registration":`+string(minimal)+`,"extra":1}`), true); err == nil {
		t.Fatal("resolve accepted extra field")
	}
}
