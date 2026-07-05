package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/middleware"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

var sixDigits = regexp.MustCompile(`\b\d{6}\b`)

func verificationOnConfig() *config.Config {
	return &config.Config{EnvVars: config.EnvVars{
		JwtSecretKey:             "test-jwt-secret-key",
		EmailVerificationEnabled: true,
		EmailFrom:                "SaltyBytes <no-reply@saltybytes.ai>",
	}}
}

// newVerificationTestStack wires a real UserHandler + EmailVerificationHandler
// against in-memory mocks with verification enabled.
func newVerificationTestStack() (*UserHandler, *EmailVerificationHandler, *testutil.MockUserRepo, *testutil.MockEmailSender) {
	cfg := verificationOnConfig()
	users := testutil.NewMockUserRepo()
	codes := testutil.NewMockEmailVerificationRepo()
	sender := &testutil.MockEmailSender{}

	userService := service.NewUserService(cfg, users)
	verification := service.NewEmailVerificationService(cfg, users, codes, sender)

	userHandler := NewUserHandler(userService)
	userHandler.EmailVerification = verification
	return userHandler, NewEmailVerificationHandler(verification), users, sender
}

func TestSignup_SendsVerificationCode_WhenEnabled(t *testing.T) {
	userHandler, _, users, sender := newVerificationTestStack()

	r := gin.New()
	r.POST("/users", userHandler.CreateUser)

	w := postJSON(r, "/users", `{"username": "newchef", "email": "newchef@example.com", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("signup status = %d, body: %s", w.Code, w.Body.String())
	}

	sent := sender.LastSent()
	if sent == nil {
		t.Fatal("signup should have sent a verification email")
	}
	if sent.To != "newchef@example.com" {
		t.Errorf("sent to %q", sent.To)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	user, _ := resp["user"].(map[string]interface{})
	if user == nil {
		t.Fatal("response missing user")
	}
	if verified, _ := user["email_verified"].(bool); verified {
		t.Error("new signups must report email_verified=false while the feature is on")
	}

	// The account exists and is unverified in the repo.
	stored, _ := users.GetUserAuthByUsername("newchef")
	if stored.EmailVerified() {
		t.Error("stored user should be unverified")
	}
}

func TestConfirmVerification_Roundtrip(t *testing.T) {
	userHandler, evHandler, users, sender := newVerificationTestStack()

	r := gin.New()
	r.POST("/users", userHandler.CreateUser)

	w := postJSON(r, "/users", `{"username": "newchef", "email": "newchef@example.com", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("signup status = %d, body: %s", w.Code, w.Body.String())
	}
	code := sixDigits.FindString(sender.LastSent().Text)
	user, _ := users.GetUserAuthByUsername("newchef")

	rc := gin.New()
	rc.POST("/confirm", setUser(user), evHandler.ConfirmVerification)

	// Wrong code first.
	wrong := "000000"
	if wrong == code {
		wrong = "000001"
	}
	w = postJSON(rc, "/confirm", `{"code": "`+wrong+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong code status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "code_invalid" {
		t.Errorf("error_code = %v, want code_invalid", resp["error_code"])
	}

	// Then the right one.
	w = postJSON(rc, "/confirm", `{"code": "`+code+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body: %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	userResp, _ := resp["user"].(map[string]interface{})
	if verified, _ := userResp["email_verified"].(bool); !verified {
		t.Error("confirm response must report email_verified=true")
	}
}

func TestRequestVerification_CooldownReturns429(t *testing.T) {
	userHandler, evHandler, users, _ := newVerificationTestStack()

	r := gin.New()
	r.POST("/users", userHandler.CreateUser)
	postJSON(r, "/users", `{"username": "newchef", "email": "newchef@example.com", "password": "Password1!"}`)
	user, _ := users.GetUserAuthByUsername("newchef")

	rr := gin.New()
	rr.POST("/resend", setUser(user), evHandler.RequestVerification)

	// Signup already sent one; an immediate resend is inside the cooldown.
	w := postJSON(rr, "/resend", `{}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429. body: %s", w.Code, w.Body.String())
	}
}

func TestRequireVerifiedEmail_GatesUnverifiedUsers(t *testing.T) {
	unverified := testutil.TestUser()
	unverified.EmailVerifiedAt = nil

	r := gin.New()
	r.POST("/gated", setUser(unverified), middleware.RequireVerifiedEmail(true), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := postJSON(r, "/gated", `{}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403. body: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "email_unverified" {
		t.Errorf("error_code = %v, want email_unverified", resp["error_code"])
	}
}

func TestRequireVerifiedEmail_PassesVerifiedAndDisabled(t *testing.T) {
	verified := testutil.TestUser()
	now := time.Now()
	verified.EmailVerifiedAt = &now

	// Verified user through an enabled gate.
	r := gin.New()
	r.POST("/gated", setUser(verified), middleware.RequireVerifiedEmail(true), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	if w := postJSON(r, "/gated", `{}`); w.Code != http.StatusOK {
		t.Fatalf("verified user: status = %d, want 200", w.Code)
	}

	// Unverified user through a DISABLED gate (feature dark) must pass.
	unverified := testutil.TestUser()
	unverified.EmailVerifiedAt = nil
	rd := gin.New()
	rd.POST("/gated", setUser(unverified), middleware.RequireVerifiedEmail(false), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	if w := postJSON(rd, "/gated", `{}`); w.Code != http.StatusOK {
		t.Fatalf("disabled gate: status = %d, want 200", w.Code)
	}
}

func TestSignup_MailFailureDoesNotFailSignup(t *testing.T) {
	userHandler, _, _, sender := newVerificationTestStack()
	sender.SendErr = errTestMailDown

	r := gin.New()
	r.POST("/users", userHandler.CreateUser)

	w := postJSON(r, "/users", `{"username": "newchef", "email": "newchef@example.com", "password": "Password1!"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("signup must succeed even when the mail send fails; status = %d, body: %s", w.Code, w.Body.String())
	}
}

var errTestMailDown = &mailDownError{}

type mailDownError struct{}

func (e *mailDownError) Error() string { return "smtp on fire" }
