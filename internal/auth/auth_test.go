package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateNormalizesAndEnforcesBoundaries(t *testing.T) {
	email, nickname, err := Validate("  USER@Example.com ", "  昵称  ", strings.Repeat("p", 8), true)
	if err != nil || email != "user@example.com" || nickname != "昵称" {
		t.Fatalf("Validate() = (%q, %q, %v)", email, nickname, err)
	}
	_, _, err = Validate(strings.Repeat("a", 250)+"@x.io", strings.Repeat("名", 51), strings.Repeat("p", 129), true)
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Fields) != 3 {
		t.Fatalf("validation error = %#v", err)
	}
}

func TestPasswordHashUsesArgon2idAndVerifies(t *testing.T) {
	hash, err := HashPassword("password123", deterministicRandom)
	if err != nil || !strings.HasPrefix(hash, "$argon2id$v=19$") || strings.Contains(hash, "password123") {
		t.Fatalf("HashPassword() = (%q, %v)", hash, err)
	}
	ok, err := VerifyPassword("password123", hash)
	if err != nil || !ok {
		t.Fatalf("correct password = (%t, %v)", ok, err)
	}
	ok, err = VerifyPassword("incorrect", hash)
	if err != nil || ok {
		t.Fatalf("wrong password = (%t, %v)", ok, err)
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
	hash, _ := HashPassword("right-password", deterministicRandom)
	store.findErr = nil
	store.user = StoredUser{User: User{ID: uuid.New(), Email: "known@example.com"}, PasswordHash: hash}
	_, wrongErr := service.Login(context.Background(), "known@example.com", "wrong-password")
	if !errors.Is(missingErr, ErrInvalidCredentials) || !errors.Is(wrongErr, ErrInvalidCredentials) {
		t.Fatalf("errors = (%v, %v)", missingErr, wrongErr)
	}
}

func TestMissingAccountUsesValidDummyHash(t *testing.T) {
	service := NewService(&memoryStore{findErr: ErrUnauthenticated}, time.Now, deterministicRandom)
	ok, err := VerifyPassword("any-password", service.dummyPasswordHash)
	if err != nil || ok {
		t.Fatalf("dummy password verification = (%t, %v)", ok, err)
	}
}

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
