package config

import "reflect"

// fillNilSlices replaces every nil slice in v (a pointer to a struct) with an empty one, so the API
// always sends [] rather than null and clients can iterate without checking.
func fillNilSlices(v any) {
	fill(reflect.ValueOf(v))
}

func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			fill(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				fill(f)
			}
		}
	case reflect.Slice:
		if v.IsNil() {
			if v.CanSet() {
				v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			fill(v.Index(i))
		}
	}
}
