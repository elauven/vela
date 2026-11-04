package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// Action handles a POST to a page (docs/APP.md, form actions). It returns
// a value that is ignored, or an error: app.Fail for a validation failure,
// app.Redirect, or any loader error (L-5).
type Action func(ctx context.Context, r *Request) (any, error)

// Actions are the actions of a page by name, which the mirrored
// actions.go declares with `func Actions() app.Actions`. "default" handles
// POST <page url>; another name handles POST <page url>?/name.
type Actions map[string]Action

// FailError is a validation failure of an action: the page renders again
// with Value as its form prop.
type FailError struct {
	Status int
	Value  any
}

func (e *FailError) Error() string { return fmt.Sprintf("app: action failed with %d", e.Status) }

// Fail reports a validation failure from an action: the response has the
// status (400 to 499; 400 otherwise), and the page gets value, converted
// through JSON, as its form prop. Without JavaScript the document renders
// again with it; with JavaScript only the form prop changes.
func Fail(status int, value any) error {
	if status < 400 || status > 499 {
		status = http.StatusBadRequest
	}
	return &FailError{Status: status, Value: value}
}

// maxFormMemory is the memory limit of a parsed multipart form; larger
// files go to temporary files (net/http).
const maxFormMemory = 32 << 20

// Form parses the body of an action's request and returns its form
// values: application/x-www-form-urlencoded or multipart/form-data. Files
// of a multipart form are in r.HTTP.MultipartForm.
func (r *Request) Form() (url.Values, error) {
	if r.HTTP == nil {
		return nil, errors.New("app: Form: no request")
	}
	if isMultipart(r.HTTP) {
		if err := r.HTTP.ParseMultipartForm(maxFormMemory); err != nil {
			return nil, fmt.Errorf("app: Form: %w", err)
		}
	} else if err := r.HTTP.ParseForm(); err != nil {
		return nil, fmt.Errorf("app: Form: %w", err)
	}
	return r.HTTP.PostForm, nil
}

func isMultipart(r *http.Request) bool {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return media == "multipart/form-data"
}

// Decode fills the struct v points to from the request body: JSON for
// application/json, otherwise the form values. A form value goes to the
// field whose `form` tag, or else `json` tag, or else lowercased name
// matches; strings, booleans ("on", "true", "1"), integers, floats and
// slices of them are supported. A missing value leaves the field as it is.
func (r *Request) Decode(v any) error {
	if r.HTTP == nil {
		return errors.New("app: Decode: no request")
	}
	media, _, _ := mime.ParseMediaType(r.HTTP.Header.Get("Content-Type"))
	if media == "application/json" {
		if err := json.NewDecoder(r.HTTP.Body).Decode(v); err != nil {
			return fmt.Errorf("app: Decode: %w", err)
		}
		return nil
	}
	values, err := r.Form()
	if err != nil {
		return err
	}
	target := reflect.ValueOf(v)
	if target.Kind() != reflect.Pointer || target.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("app: Decode needs a pointer to a struct, not %T", v)
	}
	return decodeForm(values, target.Elem())
}

func decodeForm(values url.Values, s reflect.Value) error {
	t := s.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name := fieldName(field)
		if name == "-" {
			continue
		}
		list, ok := values[name]
		if !ok {
			continue
		}
		if err := setField(s.Field(i), list); err != nil {
			return fmt.Errorf("app: Decode: field %s: %w", name, err)
		}
	}
	return nil
}

func fieldName(field reflect.StructField) string {
	for _, tag := range []string{"form", "json"} {
		if name, _, _ := strings.Cut(field.Tag.Get(tag), ","); name != "" {
			return name
		}
	}
	return strings.ToLower(field.Name)
}

func setField(f reflect.Value, list []string) error {
	if f.Kind() == reflect.Slice && f.Type().Elem().Kind() != reflect.Uint8 {
		out := reflect.MakeSlice(f.Type(), len(list), len(list))
		for i, s := range list {
			if err := setScalar(out.Index(i), s); err != nil {
				return err
			}
		}
		f.Set(out)
		return nil
	}
	if len(list) == 0 {
		return nil
	}
	return setScalar(f, list[0])
}

func setScalar(f reflect.Value, s string) error {
	switch f.Kind() {
	case reflect.String:
		f.SetString(s)
	case reflect.Bool:
		switch strings.ToLower(s) {
		case "on", "true", "1", "yes":
			f.SetBool(true)
		case "", "off", "false", "0", "no":
			f.SetBool(false)
		default:
			return fmt.Errorf("%q is not a boolean", s)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, f.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an integer", s)
		}
		f.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(strings.TrimSpace(s), 10, f.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an unsigned integer", s)
		}
		f.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(strings.TrimSpace(s), f.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", s)
		}
		f.SetFloat(n)
	default:
		return fmt.Errorf("unsupported type %s", f.Type())
	}
	return nil
}

// actionName is the action of a POST: "default", or name for a query that
// starts with /name (POST /users?/delete).
func actionName(rawQuery string) string {
	first, _, _ := strings.Cut(rawQuery, "&")
	if strings.HasPrefix(first, "/") {
		name, _ := url.QueryUnescape(first[1:])
		name, _, _ = strings.Cut(name, "=")
		if name != "" {
			return name
		}
	}
	return "default"
}

// withoutAction is the query without the action name: the URL of the page
// a successful action redirects to (post/redirect/get).
func withoutAction(rawQuery string) string {
	parts := strings.Split(rawQuery, "&")
	kept := parts[:0]
	for _, p := range parts {
		if p != "" && !strings.HasPrefix(p, "/") {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "&")
}
