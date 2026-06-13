package util

import (
	"strings"
	"testing"
)

type jsonSample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestSerializeToJSONString_Struct(t *testing.T) {
	got, err := SerializeToJSONString(jsonSample{Name: "salt", Count: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"name":"salt","count":3}`
	if got != want {
		t.Errorf("SerializeToJSONString = %q, want %q", got, want)
	}
}

func TestSerializeToJSONString_UnsupportedType(t *testing.T) {
	if _, err := SerializeToJSONString(make(chan int)); err == nil {
		t.Error("expected error serializing a channel, got nil")
	}
}

func TestSerializeToJSONStringWithBuffer_AppendsNewline(t *testing.T) {
	got, err := SerializeToJSONStringWithBuffer(jsonSample{Name: "pepper", Count: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// json.Encoder always terminates each value with a newline.
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("expected trailing newline from encoder, got %q", got)
	}
	want := `{"name":"pepper","count":1}`
	if strings.TrimSpace(got) != want {
		t.Errorf("SerializeToJSONStringWithBuffer = %q, want %q (plus newline)", got, want)
	}
}

func TestSerializeToJSONStringWithBuffer_UnsupportedType(t *testing.T) {
	if _, err := SerializeToJSONStringWithBuffer(func() {}); err == nil {
		t.Error("expected error serializing a func, got nil")
	}
}

func TestDeserializeFromJSONString_RoundTrip(t *testing.T) {
	in := jsonSample{Name: "cumin", Count: 7}
	s, err := SerializeToJSONString(in)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	var out jsonSample
	if err := DeserializeFromJSONString(s, &out); err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	if out != in {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
}

func TestDeserializeFromJSONString_NonPointerRejected(t *testing.T) {
	tests := []struct {
		name   string
		target interface{}
	}{
		{"struct value", jsonSample{}},
		{"string value", "not a pointer"},
		{"int value", 42},
		{"nil interface", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := DeserializeFromJSONString(`{"name":"x"}`, tc.target)
			if err == nil {
				t.Fatal("expected error for non-pointer target, got nil")
			}
			if err.Error() != "input must be a pointer" {
				t.Errorf("error = %q, want 'input must be a pointer'", err.Error())
			}
		})
	}
}

func TestDeserializeFromJSONString_InvalidJSON(t *testing.T) {
	var out jsonSample
	if err := DeserializeFromJSONString(`{"name": broken`, &out); err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}
