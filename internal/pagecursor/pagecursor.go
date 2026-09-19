// Package pagecursor 编解码按创建时间与 UUID 排序的分页游标。
package pagecursor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

// === 定义游标表示 ===

type value struct {
	// === runtime 字段 ===
	CreatedAt string `json:"createdAt"`
	ID        string `json:"id"`
}

// === 编解码游标 ===

// Encode 返回只对服务端有语义的稳定游标。
func Encode(createdAt time.Time, id uuid.UUID) string {
	encoded, _ := json.Marshal(value{CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), ID: id.String()})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// Decode 严格解析由 Encode 产生的游标。
func Decode(encoded string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	var cursor value
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return time.Time{}, uuid.Nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return time.Time{}, uuid.Nil, errors.New("cursor must contain one value")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil {
		return time.Time{}, uuid.Nil, errors.New("invalid cursor id")
	}
	return createdAt.UTC(), id, nil
}
