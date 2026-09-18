package listing

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"

	engine "github.com/Jason-skd/stu-trade-engine"
	"github.com/google/uuid"
)

// === 规范化书籍标识 ===

func normalizeISBN(value string) (string, error) {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' {
			return -1
		}
		return unicode.ToUpper(r)
	}, value)
	switch len(compact) {
	case 10:
		if !validISBN10(compact) {
			return "", errors.New("invalid ISBN-10")
		}
		return isbn13From10(compact), nil
	case 13:
		if !validISBN13(compact) {
			return "", errors.New("invalid ISBN-13")
		}
		return compact, nil
	default:
		return "", errors.New("ISBN must contain 10 or 13 characters")
	}
}

func validISBN10(value string) bool {
	sum := 0
	for i, r := range value {
		var digit int
		switch {
		case r >= '0' && r <= '9':
			digit = int(r - '0')
		case i == 9 && r == 'X':
			digit = 10
		default:
			return false
		}
		sum += (10 - i) * digit
	}
	return sum%11 == 0
}

func validISBN13(value string) bool {
	if len(value) != 13 || (!strings.HasPrefix(value, "978") && !strings.HasPrefix(value, "979")) {
		return false
	}
	sum := 0
	for i := 0; i < 12; i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
		weight := 1
		if i%2 == 1 {
			weight = 3
		}
		sum += int(value[i]-'0') * weight
	}
	if value[12] < '0' || value[12] > '9' {
		return false
	}
	return (10-sum%10)%10 == int(value[12]-'0')
}

func isbn13From10(value string) string {
	prefix := "978" + value[:9]
	sum := 0
	for i := range prefix {
		weight := 1
		if i%2 == 1 {
			weight = 3
		}
		sum += int(prefix[i]-'0') * weight
	}
	return prefix + string(rune('0'+(10-sum%10)%10))
}

// === 校验挂单创建输入 ===

func validateCreate(input CreateInput) (CreateInput, error) {
	var fields []FieldError
	isbn, err := normalizeISBN(input.Book.ISBN13)
	if err != nil {
		fields = append(fields, FieldError{"book.isbn", "invalid", "must be a valid ISBN-10 or ISBN-13"})
	}
	input.Book.ISBN13 = isbn
	input.Book.Title = strings.TrimSpace(input.Book.Title)
	input.Book.Author = strings.TrimSpace(input.Book.Author)
	if input.Book.Title == "" {
		fields = append(fields, FieldError{"book.title", "required", "must not be empty"})
	}
	if input.Book.Author == "" {
		fields = append(fields, FieldError{"book.author", "required", "must not be empty"})
	}
	if input.Book.CoverImageURL != nil {
		value := strings.TrimSpace(*input.Book.CoverImageURL)
		parsed, parseErr := url.ParseRequestURI(value)
		if value == "" || parseErr != nil || !parsed.IsAbs() {
			fields = append(fields, FieldError{"book.coverImageUrl", "invalid", "must be an absolute URI"})
		} else {
			input.Book.CoverImageURL = &value
		}
	}
	if !input.Condition.valid() {
		fields = append(fields, FieldError{"condition", "invalid", "must be a supported book condition"})
	}
	if input.Note != nil {
		value := strings.TrimSpace(*input.Note)
		if value == "" {
			input.Note = nil
		} else {
			input.Note = &value
		}
	}
	if input.PriceCents <= 0 {
		fields = append(fields, FieldError{"price.amount", "invalid", "must be a positive integer amount in fen"})
	}
	if input.Currency != "CNY" {
		fields = append(fields, FieldError{"price.currency", "invalid", "must be CNY"})
	}
	if input.Side != engine.SideBuy && input.Side != engine.SideSell {
		fields = append(fields, FieldError{"side", "invalid", "must be buy or sell"})
	}
	if len(fields) != 0 {
		return CreateInput{}, &ValidationError{Fields: fields}
	}
	return input, nil
}

// === 校验挂单查询输入 ===

func validateList(input ListInput) (CandidateFilter, int, error) {
	filter := CandidateFilter{Side: input.Side, OwnerID: input.OwnerID, PageSize: defaultLimit + 1}
	var fields []FieldError
	if input.Side != engine.SideBuy && input.Side != engine.SideSell {
		fields = append(fields, FieldError{"side", "invalid", "must be buy or sell"})
	}
	if input.Condition != nil {
		if !input.Condition.valid() {
			fields = append(fields, FieldError{"condition", "invalid", "must be a supported book condition"})
		} else {
			filter.Condition = input.Condition
		}
	}
	if input.Query != nil {
		query := strings.TrimSpace(*input.Query)
		if query == "" {
			fields = append(fields, FieldError{"q", "required", "must not be empty"})
		} else if isbn, err := normalizeISBN(query); err == nil {
			filter.ISBN13 = &isbn
		} else if looksLikeISBN(query) {
			fields = append(fields, FieldError{"q", "invalid", "must contain a valid ISBN when searching by ISBN"})
		} else {
			query = escapeLike(query)
			filter.TitleQuery = &query
		}
	}
	limit := defaultLimit
	if input.Limit != nil {
		limit = *input.Limit
		if limit < 1 || limit > 50 {
			fields = append(fields, FieldError{"limit", "out_of_range", "must be between 1 and 50"})
		}
	}
	filter.PageSize = limit + 1
	if input.Cursor != nil {
		createdAt, id, err := decodeCursor(*input.Cursor)
		if err != nil {
			fields = append(fields, FieldError{"cursor", "invalid", "must be a continuation cursor returned by this API"})
		} else {
			filter.BeforeCreatedAt, filter.BeforeID = &createdAt, &id
		}
	}
	if len(fields) != 0 {
		return CandidateFilter{}, 0, &ValidationError{Fields: fields}
	}
	return filter, limit, nil
}

func looksLikeISBN(value string) bool {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' {
			return -1
		}
		return unicode.ToUpper(r)
	}, value)
	if len(compact) != 10 && len(compact) != 13 {
		return false
	}
	for i, r := range compact {
		if r >= '0' && r <= '9' {
			continue
		}
		if len(compact) == 10 && i == 9 && r == 'X' {
			continue
		}
		return false
	}
	return true
}

// === 编解码分页游标 ===

type cursorValue struct {
	// === runtime 字段 ===
	CreatedAt string `json:"createdAt"`
	ID        string `json:"id"`
}

func encodeCursor(createdAt time.Time, id uuid.UUID) string {
	value, _ := json.Marshal(cursorValue{CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), ID: id.String()})
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeCursor(value string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	var cursor cursorValue
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

// === 转义标题查询 ===

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
