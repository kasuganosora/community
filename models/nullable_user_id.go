package models

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"

	"gorm.io/gorm/schema"
)

// Account.UserId retains its public uint64 API: zero means no local login.
// SQL NULL is required for remote identities while retaining the users FK.
type nullableUserIDSerializer struct{}

func init() { schema.RegisterSerializer("nullable_user_id", nullableUserIDSerializer{}) }

func (nullableUserIDSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, value interface{}) error {
	var id uint64
	if value != nil {
		var raw string
		switch v := value.(type) {
		case []byte:
			raw = string(v)
		default:
			raw = fmt.Sprint(v)
		}
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid account user ID: %w", err)
		}
		id = parsed
	}
	return field.Set(ctx, dst, id)
}

func (nullableUserIDSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, value interface{}) (interface{}, error) {
	id, ok := value.(uint64)
	if !ok {
		return nil, fmt.Errorf("invalid account user ID type %T", value)
	}
	if id == 0 {
		return nil, nil
	}
	if id > math.MaxInt64 {
		return strconv.FormatUint(id, 10), nil
	}
	return int64(id), nil
}
