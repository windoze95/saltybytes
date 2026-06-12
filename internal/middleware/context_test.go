package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/service"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"github.com/windoze95/saltybytes-api/internal/util"
)

// setUserID is a test middleware that simulates VerifyTokenMiddleware having
// placed the user_id in the context.
func setUserID(userID uint) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Next()
	}
}

func setupAttachUserRouter(repo *testutil.MockUserRepo, pre ...gin.HandlerFunc) *gin.Engine {
	svc := service.NewUserService(&config.Config{}, repo)

	r := gin.New()
	for _, mw := range pre {
		r.Use(mw)
	}
	r.Use(AttachUserToContext(svc))
	r.GET("/test", func(c *gin.Context) {
		user, err := util.GetUserFromContext(c)
		if err != nil || user == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "user missing from context"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"username": user.Username})
	})
	return r
}

func TestAttachUserToContext_Success(t *testing.T) {
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	r := setupAttachUserRouter(repo, setUserID(user.ID))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestAttachUserToContext_MissingUserID_401(t *testing.T) {
	repo := testutil.NewMockUserRepo()

	// No setUserID middleware: simulates the middleware being reached without
	// VerifyTokenMiddleware having set a user_id.
	r := setupAttachUserRouter(repo)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d. body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestAttachUserToContext_UserLookupFails_401(t *testing.T) {
	repo := testutil.NewMockUserRepo() // empty: lookup will fail

	r := setupAttachUserRouter(repo, setUserID(42))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (deleted user with valid token must not 500). body: %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestAttachUserToContext_ComposedWithVerifyToken(t *testing.T) {
	// Full protected-route composition: VerifyTokenMiddleware extracts the
	// user_id from a valid JWT, then AttachUserToContext loads the user.
	repo := testutil.NewMockUserRepo()
	user := testutil.TestUser()
	repo.Users[user.ID] = user

	cfg := &config.Config{EnvVars: config.EnvVars{JwtSecretKey: testSecret}}
	svc := service.NewUserService(cfg, repo)

	var attached *models.User
	r := gin.New()
	r.Use(VerifyTokenMiddleware(cfg))
	r.Use(AttachUserToContext(svc))
	r.GET("/test", func(c *gin.Context) {
		attached, _ = util.GetUserFromContext(c)
		c.JSON(http.StatusOK, gin.H{})
	})

	token := makeTestToken(user.ID, "access", time.Now().Add(15*time.Minute), testSecret)
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d. body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if attached == nil || attached.ID != user.ID {
		t.Errorf("attached user = %+v, want user %d", attached, user.ID)
	}
}
