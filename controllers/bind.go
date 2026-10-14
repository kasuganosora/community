package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// maxBindMemory bounds the in-memory part of a multipart body; larger file
// parts spill to disk as with gin's own binding.
const maxBindMemory = 8 << 20

// BindBody binds a Mastodon-style request body into obj, accepting the three
// encodings Mastodon accepts: JSON, application/x-www-form-urlencoded and
// multipart/form-data. Form keys use Rails conventions:
//
//	media_ids[]=1&media_ids[]=2      -> "media_ids": ["1","2"]
//	poll[options][]=a&poll[expires_in]=300 -> "poll": {"options":["a"],"expires_in":300}
//	sensitive=true                    -> "sensitive": true
//
// Form values are coerced to the JSON type of the target field (found via
// its json tag), then decoded with encoding/json so custom UnmarshalJSON
// methods still run, and finally validated with the `binding` tags.
// An empty body is not an error; obj keeps its defaults.
func BindBody(c *gin.Context, obj any) error {
	req := c.Request
	if req.Body == nil || req.Body == http.NoBody {
		return binding.Validator.ValidateStruct(obj)
	}
	mediaType, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	var values url.Values
	switch mediaType {
	case "application/x-www-form-urlencoded":
		if err := req.ParseForm(); err != nil {
			return err
		}
		values = req.PostForm
	case "multipart/form-data":
		if err := req.ParseMultipartForm(maxBindMemory); err != nil {
			return err
		}
		values = url.Values(req.MultipartForm.Value)
	default:
		if req.ContentLength == 0 {
			return binding.Validator.ValidateStruct(obj)
		}
		return c.ShouldBindJSON(obj)
	}
	if err := DecodeFormValues(values, obj); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(obj)
}

// DecodeFormValues decodes Rails-style form values into obj (see BindBody).
func DecodeFormValues(values url.Values, obj any) error {
	tree := map[string]any{}
	for key, vals := range values {
		path, isArray := splitFormKey(key)
		if len(path) == 0 {
			continue
		}
		var leaf any = vals[len(vals)-1]
		if isArray {
			// An empty "media_ids[]=" means an empty list, not [""].
			list := make([]any, 0, len(vals))
			for _, v := range vals {
				if v != "" {
					list = append(list, v)
				}
			}
			leaf = list
		}
		if err := setPath(tree, path, leaf); err != nil {
			return err
		}
	}
	t := reflect.TypeOf(obj)
	if t.Kind() != reflect.Pointer {
		return errors.New("BindBody needs a pointer")
	}
	coerced, err := coerce(tree, t.Elem())
	if err != nil {
		return err
	}
	data, err := json.Marshal(coerced)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, obj)
}

// splitFormKey turns "poll[options][]" into ["poll","options"], true.
func splitFormKey(key string) ([]string, bool) {
	isArray := strings.HasSuffix(key, "[]")
	key = strings.TrimSuffix(key, "[]")
	head, rest, _ := strings.Cut(key, "[")
	path := []string{head}
	for rest != "" {
		part, after, ok := strings.Cut(rest, "]")
		if !ok {
			return []string{key}, isArray
		}
		path = append(path, part)
		rest = strings.TrimPrefix(after, "[")
	}
	if path[0] == "" {
		return nil, false
	}
	return path, isArray
}

func setPath(tree map[string]any, path []string, leaf any) error {
	for _, p := range path[:len(path)-1] {
		next, ok := tree[p].(map[string]any)
		if !ok {
			if _, exists := tree[p]; exists {
				return fmt.Errorf("conflicting form keys for %q", p)
			}
			next = map[string]any{}
			tree[p] = next
		}
		tree = next
	}
	tree[path[len(path)-1]] = leaf
	return nil
}

// coerce converts form strings to the JSON shape of type t.
func coerce(v any, t reflect.Type) (any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return v, nil
		}
		out := map[string]any{}
		for k, val := range m {
			out[k] = val
		}
		if err := coerceFields(out, t); err != nil {
			return nil, err
		}
		return out, nil
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return v, nil
		}
		// Rails index maps (fields_attributes[0][name]) also fit a slice.
		out := map[string]any{}
		for k, val := range m {
			c, err := coerce(val, t.Elem())
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		var list []any
		switch x := v.(type) {
		case []any:
			list = x
		case map[string]any: // indexed: a[0]=x&a[1]=y
			for i := 0; ; i++ {
				item, ok := x[strconv.Itoa(i)]
				if !ok {
					break
				}
				list = append(list, item)
			}
		case string:
			if x == "" {
				list = []any{}
			} else {
				list = []any{x}
			}
		default:
			return v, nil
		}
		out := make([]any, len(list))
		for i, item := range list {
			c, err := coerce(item, t.Elem())
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	}
	s, ok := v.(string)
	if !ok {
		if list, isList := v.([]any); isList && len(list) > 0 {
			return coerce(list[len(list)-1], t)
		}
		return v, nil
	}
	switch t.Kind() {
	case reflect.Bool:
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "1", "true", "on", "yes", "t":
			return true, nil
		case "", "0", "false", "off", "no", "f":
			return false, nil
		}
		return nil, fmt.Errorf("invalid boolean %q", s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return nil, fmt.Errorf("invalid number %q", s)
		}
		return json.Number(strings.TrimSpace(s)), nil
	case reflect.Interface:
		return s, nil
	}
	return s, nil
}

func coerceFields(m map[string]any, t reflect.Type) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Tag.Get("json") == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if err := coerceFields(m, ft); err != nil {
					return err
				}
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if f.Tag.Get("form") == "-" {
			// Not settable from a form (e.g. taken from the URL path).
			if name != "" {
				delete(m, name)
			}
			continue
		}
		if name == "" {
			name, _, _ = strings.Cut(f.Tag.Get("form"), ",")
		}
		if name == "" || name == "-" {
			name = f.Name
		}
		val, ok := m[name]
		if !ok {
			continue
		}
		c, err := coerce(val, f.Type)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		m[name] = c
	}
	return nil
}
