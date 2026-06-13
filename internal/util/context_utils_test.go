package util

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/models"
)

func newTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func TestGetUserFromContext_Present(t *testing.T) {
	c := newTestGinContext()
	want := &models.User{Username: "chef"}
	c.Set("user", want)

	got, err := GetUserFromContext(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("GetUserFromContext returned %p, want the same pointer %p", got, want)
	}
}

func TestGetUserFromContext_Absent(t *testing.T) {
	c := newTestGinContext()

	got, err := GetUserFromContext(c)
	if err == nil {
		t.Fatal("expected error when no user is set, got nil")
	}
	if got != nil {
		t.Errorf("expected nil user, got %+v", got)
	}
	if err.Error() != "no user information" {
		t.Errorf("error = %q, want 'no user information'", err.Error())
	}
}

func TestGetUserFromContext_WrongType(t *testing.T) {
	c := newTestGinContext()
	// A value user (not *models.User) must be rejected.
	c.Set("user", models.User{Username: "chef"})

	got, err := GetUserFromContext(c)
	if err == nil {
		t.Fatal("expected error for wrong-typed user value, got nil")
	}
	if got != nil {
		t.Errorf("expected nil user, got %+v", got)
	}
	if err.Error() != "user information is of the wrong type" {
		t.Errorf("error = %q, want 'user information is of the wrong type'", err.Error())
	}
}

func TestGetUserIDFromContext_Present(t *testing.T) {
	c := newTestGinContext()
	c.Set("user_id", uint(42))

	got, err := GetUserIDFromContext(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("GetUserIDFromContext = %d, want 42", got)
	}
}

func TestGetUserIDFromContext_Absent(t *testing.T) {
	c := newTestGinContext()

	got, err := GetUserIDFromContext(c)
	if err == nil {
		t.Fatal("expected error when no user_id is set, got nil")
	}
	if got != 0 {
		t.Errorf("expected zero user ID, got %d", got)
	}
	if err.Error() != "no user ID information" {
		t.Errorf("error = %q, want 'no user ID information'", err.Error())
	}
}

func TestGetUserIDFromContext_WrongType(t *testing.T) {
	c := newTestGinContext()
	// An int (not uint) must be rejected.
	c.Set("user_id", 42)

	got, err := GetUserIDFromContext(c)
	if err == nil {
		t.Fatal("expected error for wrong-typed user_id, got nil")
	}
	if got != 0 {
		t.Errorf("expected zero user ID, got %d", got)
	}
	if err.Error() != "user ID information is of the wrong type" {
		t.Errorf("error = %q, want 'user ID information is of the wrong type'", err.Error())
	}
}
