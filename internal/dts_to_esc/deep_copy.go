package dts_to_esc

import "reflect"

// deepCopy returns a copy of v that shares no pointer, slice, map or interface
// value with it, so writing through the copy leaves v unchanged. It is meant
// for dts_parser trees, which hold no cycles.
//
// A struct's unexported fields are copied by value along with the struct, and
// only its exported fields are copied recursively. That is a full copy for the
// dts_parser nodes, whose unexported fields are docs, flags and spans.
func deepCopy[T any](v T) T {
	return deepCopyValue(reflect.ValueOf(&v).Elem()).Interface().(T)
}

func deepCopyValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopyValue(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopyValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), deepCopyValue(iter.Value()))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				out.Field(i).Set(deepCopyValue(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}
