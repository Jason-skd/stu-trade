// Package auth 实现宿主账户和固定期限会话规则。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

const SessionLifetime = 7 * 24 * time.Hour

var (
	ErrEmailTaken         = errors.New("email taken")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("unauthenticated")
)

type User struct {
	ID              uuid.UUID
	Email, Nickname string
}
type StoredUser struct {
	User
	PasswordHash string
}
type Session struct {
	User      User
	Token     string
	ExpiresAt time.Time
}

type Store interface {
	CreateUserSession(context.Context, StoredUser, []byte, time.Time, time.Time) error
	FindUserByEmail(context.Context, string) (StoredUser, error)
	CreateSession(context.Context, uuid.UUID, []byte, time.Time, time.Time) error
	FindSessionUser(context.Context, []byte, time.Time) (User, error)
	DeleteSession(context.Context, []byte) error
}

type Clock func() time.Time
type Random func([]byte) (int, error)

type Service struct {
	store  Store
	now    Clock
	random Random
}

func NewService(store Store, now Clock, random Random) *Service {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Read
	}
	return &Service{store: store, now: now, random: random}
}

func NormalizeEmail(value string) string    { return strings.ToLower(strings.TrimSpace(value)) }
func NormalizeNickname(value string) string { return strings.TrimSpace(value) }

type FieldError struct{ Field, Code, Detail string }
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string { return "validation failed" }

func Validate(email, nickname, password string, includeNickname bool) (string, string, error) {
	email, nickname = NormalizeEmail(email), NormalizeNickname(nickname)
	var fields []FieldError
	if len(email) == 0 || len(email) > 254 || !validEmail(email) {
		fields = append(fields, FieldError{"email", "invalid", "must be a valid email address of at most 254 characters"})
	}
	if includeNickname && (len([]rune(nickname)) < 1 || len([]rune(nickname)) > 50) {
		fields = append(fields, FieldError{"nickname", "invalid_length", "must contain between 1 and 50 characters"})
	}
	if len([]rune(password)) < 8 || len([]rune(password)) > 128 {
		fields = append(fields, FieldError{"password", "invalid_length", "must contain between 8 and 128 characters"})
	}
	if len(fields) > 0 {
		return email, nickname, &ValidationError{Fields: fields}
	}
	return email, nickname, nil
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func (s *Service) Signup(ctx context.Context, email, nickname, password string) (Session, error) {
	email, nickname, err := Validate(email, nickname, password, true)
	if err != nil {
		return Session{}, err
	}
	hash, err := HashPassword(password, s.random)
	if err != nil {
		return Session{}, fmt.Errorf("hash password: %w", err)
	}
	token, digest, err := s.newToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	user := User{ID: uuid.New(), Email: email, Nickname: nickname}
	if err := s.store.CreateUserSession(ctx, StoredUser{User: user, PasswordHash: hash}, digest, now.Add(SessionLifetime), now); err != nil {
		return Session{}, err
	}
	return Session{User: user, Token: token, ExpiresAt: now.Add(SessionLifetime)}, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	email, _, err := Validate(email, "", password, false)
	if err != nil {
		return Session{}, err
	}
	user, err := s.store.FindUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return Session{}, ErrInvalidCredentials
		}
		return Session{}, err
	}
	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return Session{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return Session{}, ErrInvalidCredentials
	}
	token, digest, err := s.newToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	if err := s.store.CreateSession(ctx, user.ID, digest, now.Add(SessionLifetime), now); err != nil {
		return Session{}, err
	}
	return Session{User: user.User, Token: token, ExpiresAt: now.Add(SessionLifetime)}, nil
}

func (s *Service) Current(ctx context.Context, token string) (User, error) {
	digest, ok := TokenDigest(token)
	if !ok {
		return User{}, ErrUnauthenticated
	}
	return s.store.FindSessionUser(ctx, digest, s.now().UTC())
}
func (s *Service) Logout(ctx context.Context, token string) error {
	digest, ok := TokenDigest(token)
	if !ok {
		return nil
	}
	return s.store.DeleteSession(ctx, digest)
}
func (s *Service) newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if n, err := s.random(b); err != nil || n != len(b) {
		if err == nil {
			err = errors.New("short random read")
		}
		return "", nil, fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	digest, _ := TokenDigest(token)
	return token, digest, nil
}
func TokenDigest(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

func HashPassword(password string, random Random) (string, error) {
	salt := make([]byte, 16)
	if n, err := random(salt); err != nil || n != len(salt) {
		if err == nil {
			err = errors.New("short random read")
		}
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=1,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=1,p=4" {
		return false, errors.New("invalid password hash")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
