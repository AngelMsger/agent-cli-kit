package config

import (
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A struct field binds to a key with a `config:"<key>"` tag. Untagged struct
// fields are searched for tags too, so a nested AuthConfig can carry
// `config:"auth.scheme"`. Tagged fields are strings, ints, bools or
// time.Duration values.

var durationType = reflect.TypeOf(time.Duration(0))

type binding struct {
	key   string
	index []int
}

var bindings sync.Map // reflect.Type → []binding

func bindingsOf(t reflect.Type) []binding {
	if b, ok := bindings.Load(t); ok {
		return b.([]binding)
	}
	var out []binding
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			index := append(append([]int(nil), prefix...), i)
			key, tagged := sf.Tag.Lookup("config")
			switch {
			case tagged && key != "" && key != "-":
				out = append(out, binding{key: key, index: index})
			case !tagged && sf.Type.Kind() == reflect.Struct && sf.Type != durationType:
				walk(sf.Type, index)
			}
		}
	}
	walk(t, nil)
	bindings.Store(t, out)
	return out
}

func structValue(v any) reflect.Value {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		panic("config: want a struct or a pointer to one, got " + rv.Kind().String())
	}
	return rv
}

// Encode returns the tagged fields of src that hold a value: non-empty
// strings, positive numbers and durations, and true. A duration is written as
// time.Duration.String does ("30s").
func Encode(src any) Values {
	rv := structValue(src)
	out := Values{}
	for _, b := range bindingsOf(rv.Type()) {
		if s := format(rv.FieldByIndex(b.index)); s != "" {
			out[b.key] = s
		}
	}
	return out
}

// Decode sets every tagged field of dst, a pointer to a struct, from v. A
// number, duration or boolean that is missing or does not parse takes its
// field's Default, or zero without one. Booleans accept 1/true/yes/on and
// 0/false/no/off in any case.
func Decode(v Values, dst any) {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		panic("config: Decode needs a pointer to a struct")
	}
	rv = rv.Elem()
	for _, b := range bindingsOf(rv.Type()) {
		def, _ := field(b.key)
		parse(rv.FieldByIndex(b.index), v[b.key], def.Default)
	}
}

func format(fv reflect.Value) string {
	switch {
	case fv.Kind() == reflect.String:
		return fv.String()
	case fv.Kind() == reflect.Bool:
		if fv.Bool() {
			return "true"
		}
		return ""
	case fv.Type() == durationType:
		if d := time.Duration(fv.Int()); d > 0 {
			return d.String()
		}
		return ""
	case fv.CanInt():
		if n := fv.Int(); n > 0 {
			return strconv.FormatInt(n, 10)
		}
		return ""
	}
	panic("config: unsupported field type " + fv.Type().String())
}

func parse(fv reflect.Value, raw, def string) {
	switch {
	case fv.Kind() == reflect.String:
		fv.SetString(raw)
	case fv.Kind() == reflect.Bool:
		fv.SetBool(boolOr(raw, boolOr(def, false)))
	case fv.Type() == durationType:
		fv.SetInt(int64(durationOr(raw, durationOr(def, 0))))
	case fv.CanInt():
		fv.SetInt(int64(atoiOr(raw, atoiOr(def, 0))))
	default:
		panic("config: unsupported field type " + fv.Type().String())
	}
}

func boolOr(s string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return fallback
}

func durationOr(s string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return fallback
}

// get and set read and write one tagged field of a struct value.
func get(rv reflect.Value, key string) string {
	for _, b := range bindingsOf(rv.Type()) {
		if b.key == key {
			return format(rv.FieldByIndex(b.index))
		}
	}
	return ""
}

func set(rv reflect.Value, key, value string) {
	for _, b := range bindingsOf(rv.Type()) {
		if b.key == key {
			def, _ := field(key)
			parse(rv.FieldByIndex(b.index), value, def.Default)
			return
		}
	}
}

func has(t reflect.Type, key string) bool {
	for _, b := range bindingsOf(t) {
		if b.key == key {
			return true
		}
	}
	return false
}
