package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// === 验证认证规则 ===

func TestValidateNormalizesAndEnforcesBoundaries(t *testing.T) {
	email, nickname, err := validate("  USER@Example.com ", "  昵称  ", strings.Repeat("p", 8), true)
	if err != nil || email != "user@example.com" || nickname != "昵称" {
		t.Fatalf("validate() = (%q, %q, %v)", email, nickname, err)
	}
	_, _, err = validate(strings.Repeat("a", 250)+"@x.io", strings.Repeat("名", 51), strings.Repeat("p", 129), true)
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Fields) != 3 {
		t.Fatalf("validation error = %#v", err)
	}
}

func TestPasswordHashUsesArgon2idAndVerifies(t *testing.T) {
	hash, err := hashPassword("password123", deterministicRandom)
	if err != nil || !strings.HasPrefix(hash, "$argon2id$v=19$") || strings.Contains(hash, "password123") {
		t.Fatalf("hashPassword() = (%q, %v)", hash, err)
	}
	ok, err := verifyPassword("password123", hash)
	if err != nil || !ok {
		t.Fatalf("correct password = (%t, %v)", ok, err)
	}
	ok, err = verifyPassword("incorrect", hash)
	if err != nil || ok {
		t.Fatalf("wrong password = (%t, %v)", ok, err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{name: "missing prefix", hash: "argon2id$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA"},
		{name: "wrong algorithm", hash: "$argon2i$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA"},
		{name: "wrong parameters", hash: "$argon2id$v=19$m=32768,t=2,p=1$c2FsdA$aGFzaA"},
		{name: "invalid salt encoding", hash: "$argon2id$v=19$m=65536,t=1,p=4$***$YWJj"},
		{name: "empty salt", hash: "$argon2id$v=19$m=65536,t=1,p=4$$YWJj"},
		{name: "short salt", hash: "$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$YWJj"},
		{name: "long salt", hash: encodePasswordHash(make([]byte, passwordSaltLength+1), make([]byte, passwordHashLength))},
		{name: "invalid digest encoding", hash: "$argon2id$v=19$m=65536,t=1,p=4$MTIzNDU2Nzg5MDEyMzQ1Ng$***"},
		{name: "empty digest", hash: "$argon2id$v=19$m=65536,t=1,p=4$MTIzNDU2Nzg5MDEyMzQ1Ng$"},
		{name: "short digest", hash: "$argon2id$v=19$m=65536,t=1,p=4$MTIzNDU2Nzg5MDEyMzQ1Ng$YWJj"},
		{name: "long digest", hash: encodePasswordHash(make([]byte, passwordSaltLength), make([]byte, passwordHashLength+1))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := verifyPassword("password123", tt.hash)
			if !errors.Is(err, errInvalidPasswordHash) || ok {
				t.Fatalf("verifyPassword() = (%t, %v), want (false, error)", ok, err)
			}
		})
	}
}

func TestSignupCreatesSevenDayDigestedSession(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{}
	session, err := NewService(store, func() time.Time { return now }, deterministicRandom).Signup(context.Background(), "person@example.com", "Person", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if !session.ExpiresAt.Equal(now.Add(SessionLifetime)) || !store.expiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("expiry = %v", session.ExpiresAt)
	}
	if len(store.digest) != 32 || strings.Contains(string(store.digest), session.Token) {
		t.Fatalf("stored digest = %x", store.digest)
	}
}

func TestLoginUnifiesCredentialFailures(t *testing.T) {
	store := &memoryStore{findErr: ErrUnauthenticated}
	service := NewService(store, time.Now, deterministicRandom)
	_, missingErr := service.Login(context.Background(), "missing@example.com", "password123")
	hash, _ := hashPassword("right-password", deterministicRandom)
	store.findErr = nil
	store.user = StoredUser{User: User{ID: uuid.New(), Email: "known@example.com"}, PasswordHash: hash}
	_, wrongErr := service.Login(context.Background(), "known@example.com", "wrong-password")
	if !errors.Is(missingErr, ErrInvalidCredentials) || !errors.Is(wrongErr, ErrInvalidCredentials) {
		t.Fatalf("errors = (%v, %v)", missingErr, wrongErr)
	}
}

func TestMissingAccountUsesValidDummyHash(t *testing.T) {
	service := NewService(&memoryStore{findErr: ErrUnauthenticated}, time.Now, deterministicRandom)
	ok, err := verifyPassword("any-password", service.dummyPasswordHash)
	if err != nil || ok {
		t.Fatalf("dummy password verification = (%t, %v)", ok, err)
	}
}

// === 提供认证测试替身 ===

func deterministicRandom(value []byte) (int, error) {
	for i := range value {
		value[i] = byte(i + 1)
	}
	return len(value), nil
}

type memoryStore struct {
	user      StoredUser
	findErr   error
	digest    []byte
	expiresAt time.Time
}

func (s *memoryStore) CreateUserSession(_ context.Context, user StoredUser, digest []byte, expiresAt, _ time.Time) error {
	s.user = user
	s.digest = append([]byte(nil), digest...)
	s.expiresAt = expiresAt
	return nil
}
func (s *memoryStore) FindUserByEmail(context.Context, string) (StoredUser, error) {
	return s.user, s.findErr
}
func (s *memoryStore) CreateSession(context.Context, uuid.UUID, []byte, time.Time, time.Time) error {
	return nil
}
func (s *memoryStore) FindSessionUser(context.Context, []byte, time.Time) (User, error) {
	return s.user.User, s.findErr
}
func (s *memoryStore) DeleteSession(context.Context, []byte) error { return nil }
