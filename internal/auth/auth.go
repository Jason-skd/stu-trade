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

// SessionLifetime 是会话创建后的固定有效期。
const SessionLifetime = 7 * 24 * time.Hour

var (
	// ErrEmailTaken 表示规范化后的邮箱已注册。
	ErrEmailTaken = errors.New("email taken")
	// ErrInvalidCredentials 统一表示邮箱不存在或密码错误。
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrUnauthenticated 表示会话缺失、无效或已过期。
	ErrUnauthenticated = errors.New("unauthenticated")
)

// User 是认证域向调用方提供的当前用户身份。
type User struct {
	ID              uuid.UUID
	Email, Nickname string
}

// StoredUser 包含仅供认证存储使用的密码哈希。
type StoredUser struct {
	User
	PasswordHash string
}

// Session 是新建会话及其一次性明文令牌。
type Session struct {
	User      User
	Token     string
	ExpiresAt time.Time
}

// Store 定义认证服务所需的最小持久化能力。
type Store interface {
	CreateUserSession(context.Context, StoredUser, []byte, time.Time, time.Time) error
	FindUserByEmail(context.Context, string) (StoredUser, error)
	CreateSession(context.Context, uuid.UUID, []byte, time.Time, time.Time) error
	FindSessionUser(context.Context, []byte, time.Time) (User, error)
	DeleteSession(context.Context, []byte) error
}

// Clock 提供可替换的当前时间。
type Clock func() time.Time

// Random 填充密码盐和会话令牌所需的随机字节。
type Random func([]byte) (int, error)

// Service 实现账户和固定期限会话规则。
type Service struct {
	store             Store
	now               Clock
	random            Random
	dummyPasswordHash string
}

// NewService 创建认证服务；nil 时间或随机源使用安全默认值。
func NewService(store Store, now Clock, random Random) *Service {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Read
	}
	dummySalt := []byte("stu-trade-dummy!")
	dummyHash := argon2.IDKey([]byte("not-a-user-password"), dummySalt, 1, 64*1024, 4, 32)
	return &Service{
		store:             store,
		now:               now,
		random:            random,
		dummyPasswordHash: encodePasswordHash(dummySalt, dummyHash),
	}
}

// NormalizeEmail 去除首尾空白并统一为小写。
func NormalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

// NormalizeNickname 去除昵称首尾空白。
func NormalizeNickname(value string) string { return strings.TrimSpace(value) }

// FieldError 描述一个请求字段的稳定校验失败。
type FieldError struct{ Field, Code, Detail string }

// ValidationError 汇总请求中全部字段错误。
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string { return "validation failed" }

// Validate 规范化并校验注册或登录输入。
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

// Signup 在一个存储事务中创建用户和首个会话。
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

// Login 校验凭据并创建固定期限会话。
func (s *Service) Login(ctx context.Context, email, password string) (Session, error) {
	email, _, err := Validate(email, "", password, false)
	if err != nil {
		return Session{}, err
	}
	user, err := s.store.FindUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			_, _ = VerifyPassword(password, s.dummyPasswordHash)
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

// Current 返回有效令牌对应的当前用户。
func (s *Service) Current(ctx context.Context, token string) (User, error) {
	digest, ok := TokenDigest(token)
	if !ok {
		return User{}, ErrUnauthenticated
	}
	return s.store.FindSessionUser(ctx, digest, s.now().UTC())
}

// Logout 幂等删除可识别令牌对应的会话。
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

// TokenDigest 校验令牌编码并返回用于持久化的 SHA-256 摘要。
func TokenDigest(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

// HashPassword 使用随机盐生成 Argon2id PHC 字符串。
func HashPassword(password string, random Random) (string, error) {
	salt := make([]byte, 16)
	if n, err := random(salt); err != nil || n != len(salt) {
		if err == nil {
			err = errors.New("short random read")
		}
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return encodePasswordHash(salt, hash), nil
}

func encodePasswordHash(salt, hash []byte) string {
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=1,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
}

// VerifyPassword 校验密码与受支持的 Argon2id PHC 字符串。
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
