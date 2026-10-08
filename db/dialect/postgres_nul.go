package dialect

import (
	"reflect"
	"strings"

	"gorm.io/gorm"
)

// Initialize sets up the upstream dialector and registers the callbacks
// PostgreSQL needs on top of it.
func (d PostgresDialector) Initialize(db *gorm.DB) error {
	if err := d.Dialector.Initialize(db); err != nil {
		return err
	}
	if err := db.Callback().Create().Before("gorm:create").Register("community:strip_nul", stripNUL); err != nil {
		return err
	}

	return db.Callback().Update().Before("gorm:update").Register("community:strip_nul", stripNUL)
}

// stripNUL removes NUL bytes from string values about to be written.
// PostgreSQL text columns cannot store U+0000 ("invalid byte sequence for
// encoding UTF8: 0x00"), while MySQL stores it verbatim; dropping it keeps
// user input such as status text from failing with a server error.
func stripNUL(db *gorm.DB) {
	if db.Error != nil || db.Statement == nil {
		return
	}
	stmt := db.Statement
	if dest, ok := stmt.Dest.(map[string]any); ok {
		for k, v := range dest {
			if s, ok := v.(string); ok && strings.ContainsRune(s, 0) {
				dest[k] = strings.ReplaceAll(s, "\x00", "")
			}
		}
	}
	if stmt.Schema == nil || !stmt.ReflectValue.IsValid() {
		return
	}

	var fix func(rv reflect.Value)
	fix = func(rv reflect.Value) {
		switch rv.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !rv.IsNil() {
				fix(rv.Elem())
			}
		case reflect.Slice, reflect.Array:
			for i := range rv.Len() {
				fix(rv.Index(i))
			}
		case reflect.Struct:
			if rv.Type() != stmt.Schema.ModelType {
				return
			}
			for _, field := range stmt.Schema.Fields {
				if field.FieldType.Kind() != reflect.String || field.Serializer != nil {
					continue
				}
				v, isZero := field.ValueOf(stmt.Context, rv)
				s, ok := v.(string)
				if isZero || !ok || !strings.ContainsRune(s, 0) {
					continue
				}
				_ = field.Set(stmt.Context, rv, strings.ReplaceAll(s, "\x00", ""))
			}
		}
	}
	fix(stmt.ReflectValue)
}
