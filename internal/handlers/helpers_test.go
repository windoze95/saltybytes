package handlers

import "testing"

func TestParseUintParam_Valid(t *testing.T) {
	got, err := parseUintParam("123")
	if err != nil {
		t.Fatalf("parseUintParam('123') error: %v", err)
	}
	if got != 123 {
		t.Errorf("parseUintParam('123') = %d, want 123", got)
	}
}

func TestParseUintParam_Zero(t *testing.T) {
	got, err := parseUintParam("0")
	if err != nil {
		t.Fatalf("parseUintParam('0') error: %v", err)
	}
	if got != 0 {
		t.Errorf("parseUintParam('0') = %d, want 0", got)
	}
}

func TestParseUintParam_Negative(t *testing.T) {
	_, err := parseUintParam("-1")
	if err == nil {
		t.Error("parseUintParam('-1') should return error")
	}
}

func TestParseUintParam_NonNumeric(t *testing.T) {
	_, err := parseUintParam("abc")
	if err == nil {
		t.Error("parseUintParam('abc') should return error")
	}
}

func TestParseUintParam_LargeNumber(t *testing.T) {
	got, err := parseUintParam("999999999")
	if err != nil {
		t.Fatalf("parseUintParam('999999999') error: %v", err)
	}
	if got != 999999999 {
		t.Errorf("parseUintParam('999999999') = %d, want 999999999", got)
	}
}

func TestParseUintParam_Empty(t *testing.T) {
	_, err := parseUintParam("")
	if err == nil {
		t.Error("parseUintParam('') should return error")
	}
}

func TestParseUintParam_Float(t *testing.T) {
	_, err := parseUintParam("3.14")
	if err == nil {
		t.Error("parseUintParam('3.14') should return error")
	}
}
