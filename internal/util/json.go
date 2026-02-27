package util

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

// SerializeToJSONString serializes the given struct to a JSON string.
func SerializeToJSONString(v interface{}) (string, error) {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

// SerializeToJSONStringWithBuffer serializes the given struct to a JSON string using a buffer.
func SerializeToJSONStringWithBuffer(v interface{}) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	if err := encoder.Encode(v); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

// DeserializeFromJSONString deserializes the given JSON string to the given struct.
func DeserializeFromJSONString(jsonString string, v interface{}) error {
	// Check if v is a pointer
	if reflect.ValueOf(v).Kind() != reflect.Ptr {
		return errors.New("input must be a pointer")
	}
	return json.Unmarshal([]byte(jsonString), v)
}
