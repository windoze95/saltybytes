package testutil

import (
	"fmt"
	"sync"
	"time"

	"github.com/windoze95/saltybytes-api/internal/models"
)

// MockOAuthRepo is an in-memory implementation of repository.OAuthRepo.
type MockOAuthRepo struct {
	mu      sync.Mutex
	Clients map[string]*models.OAuthClient
	Codes   map[string]*models.OAuthAuthCode
	Tokens  map[string]*models.OAuthToken
	nextID  uint
}

// NewMockOAuthRepo creates a new MockOAuthRepo with initialized maps.
func NewMockOAuthRepo() *MockOAuthRepo {
	return &MockOAuthRepo{
		Clients: make(map[string]*models.OAuthClient),
		Codes:   make(map[string]*models.OAuthAuthCode),
		Tokens:  make(map[string]*models.OAuthToken),
		nextID:  1,
	}
}

func (m *MockOAuthRepo) id() uint {
	id := m.nextID
	m.nextID++
	return id
}

func (m *MockOAuthRepo) CreateClient(client *models.OAuthClient) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	client.ID = m.id()
	client.CreatedAt = time.Now()
	m.Clients[client.ClientID] = client
	return nil
}

func (m *MockOAuthRepo) GetClientByClientID(clientID string) (*models.OAuthClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.Clients[clientID]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("client not found")
}

func (m *MockOAuthRepo) CreateAuthCode(code *models.OAuthAuthCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	code.ID = m.id()
	code.CreatedAt = time.Now()
	m.Codes[code.CodeHash] = code
	return nil
}

func (m *MockOAuthRepo) GetAuthCodeByHash(codeHash string) (*models.OAuthAuthCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.Codes[codeHash]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("code not found")
}

func (m *MockOAuthRepo) ConsumeAuthCode(codeID uint) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.Codes {
		if c.ID == codeID {
			if c.UsedAt != nil {
				return false, nil
			}
			now := time.Now()
			c.UsedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func (m *MockOAuthRepo) CreateToken(token *models.OAuthToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	token.ID = m.id()
	token.CreatedAt = time.Now()
	m.Tokens[token.TokenHash] = token
	return nil
}

func (m *MockOAuthRepo) GetTokenByHash(tokenHash string) (*models.OAuthToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.Tokens[tokenHash]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("token not found")
}

func (m *MockOAuthRepo) RevokeToken(tokenID uint) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.Tokens {
		if t.ID == tokenID {
			if t.RevokedAt != nil {
				return false, nil
			}
			now := time.Now()
			t.RevokedAt = &now
			return true, nil
		}
	}
	return false, nil
}

func (m *MockOAuthRepo) RevokeAllForUserClient(userID uint, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, t := range m.Tokens {
		if t.UserID == userID && t.ClientID == clientID && t.RevokedAt == nil {
			t.RevokedAt = &now
		}
	}
	return nil
}

func (m *MockOAuthRepo) DeleteExpired(cutoff time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.Codes {
		if c.ExpiresAt.Before(cutoff) {
			delete(m.Codes, k)
		}
	}
	for k, t := range m.Tokens {
		if t.ExpiresAt.Before(cutoff) {
			delete(m.Tokens, k)
		}
	}
	return nil
}
