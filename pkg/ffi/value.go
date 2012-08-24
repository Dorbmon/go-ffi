package ffi

import (
	"reflect"
	"runtime"
	"unsafe"
)

// methodName returns the name of the calling method,
// assumed to be two stack frames above.
func methodName() string {
	pc, _, _, _ := runtime.Caller(2)
	f := runtime.FuncForPC(pc)
	if f == nil {
		return "unknown method"
	}
	return f.Name()
}

// A ValueError occurs when a Value method is invoked on
// a Value that does not support it.  Such cases are documented
// in the description of each method.
type ValueError struct {
	Method string
	Kind   Kind
}

func (e *ValueError) Error() string {
	if e.Kind == 0 {
		return "ffi: call of " + e.Method + " on zero Value"
	}
	return "ffi: call of " + e.Method + " on " + e.Kind.String() + " Value"
}

// Value is the binary representation of an instance of type Type
type Value struct {
	// typ holds the type of the value represented by the Value
	typ Type

	// val holds the 1-word representation of the value.
	// If flag's flagIndir bit is set, then val is a pointer to the data.
	// Otherwise, val is a word holding the actual data.
	// When the data is smaller than a word, it begins at
	// the first byte (in the memory address sense) of val.
	// We use unsafe.Pointer so that the garbage collector
	// knows that val could be a pointer.
	val unsafe.Pointer

	// flag holds metadata about the value.
	// The lowest bits are flag bits:
	//	- flagIndir: val holds a pointer to the data
	//	- flagAddr: v.CanAddr is true (implies flagIndir)
	// The next five bits give the Kind of the value.
	// This repeats typ.Kind() except for method values.
	// The remaining 23+ bits give a method number for method values.
	// If flag.kind() != Func, code can assume that flagMethod is unset.
	// If typ.size > ptrSize, code can assume that flagIndir is set.
	//flag
}

type flag uintptr

const (
	flagRO flag = 1 << iota
	flagIndir
	flagAddr
	flagKindShift      = iota
	flagKindWidth      = 5 // there are 16 kinds
	flagKindMask  flag = 1<<flagKindWidth - 1
)

func (f flag) kind() Kind {
	return Kind((f >> flagKindShift) & flagKindMask)
}

// New returns a Value representing a pointer to a new zero value for
// the specified type.
func New(typ Type) Value {
	if typ == nil {
		panic("ffi: New(nil)")
	}
	buf := make([]byte, int(typ.Size()))
	ptr := unsafe.Pointer(&buf[0])
	v := Value{typ: typ, val: ptr}

	return v
}

// NewAt returns a Value representing a pointer to a value of the specified
// type, using p as that pointer.
func NewAt(typ Type, p unsafe.Pointer) Value {
	if typ == nil {
		panic("ffi: NewAt(nil)")
	}
	typ, err := NewPointerType(typ)
	if err != nil {
		return Value{}
	}
	v := Value{typ, p}
	return v
}

// mustBe panics if v's kind is not expected.
func (v Value) mustBe(expected Kind) {
	k := v.typ.Kind()
	if k != expected {
		panic("ffi: call of " + methodName() + " on " + k.String() + " Value")
	}
}

// Addr returns a pointer value representing the address of v.
// It panics if CanAddr() returns false.
// Addr is typically used to obtain a pointer to a struct field.
func (v Value) Addr() Value {
	typ := PtrTo(v.typ)
	if typ == nil {
		return Value{}
	}
	ptr := unsafe.Pointer(&v.val)
	return Value{typ, ptr}
}

// Buffer returns the underlying byte storage for this value.
func (v Value) Buffer() []byte {
	buf := make([]byte, 0)
	val := reflect.ValueOf(&buf)
	slice := (*reflect.SliceHeader)(unsafe.Pointer(val.Pointer()))
	slice.Len = int(v.typ.Size())
	slice.Data = uintptr(v.val)
	return buf
}

// Cap returns v's capacity.
// It panics if v's Kind is not Array.
func (v Value) Cap() int {
	v.mustBe(Array)
	return v.typ.Len()
}

// Elem returns the value that the pointer v points to.
// It panics if v's kind is not Ptr
func (v Value) Elem() Value {
	v.mustBe(Ptr)
	typ := v.typ.Elem()
	val := v.val
	val = *(*unsafe.Pointer)(val)
	return Value{typ: typ, val: val}
}

// Field returns the i'th field of the struct v.
// It panics if v's Kind is not Struct or i is out of range.
func (v Value) Field(i int) Value {
	v.mustBe(Struct)
	tt := v.typ.(cffi_struct)
	nfields := tt.NumField()
	if i < 0 || i >= nfields {
		panic("ffi: Field index out of range")
	}
	field := &tt.fields[i]
	typ := field.Type

	var val unsafe.Pointer
	// Indirect.  Just bump pointer.
	val = unsafe.Pointer(uintptr(v.val) + field.Offset)
	return Value{typ, val}
}

// FieldByIndex returns the nested field corresponding to index.
// It panics if v's Kind is not struct.
func (v Value) FieldByIndex(index []int) Value {
	v.mustBe(Struct)
	for i, x := range index {
		if i > 0 {
			if v.Kind() == Ptr && v.Elem().Kind() == Struct {
				v = v.Elem()
			}
		}
		v = v.Field(x)
	}
	return v
}

// FieldByName returns the struct field with the given name.
// It returns the zero Value if no field was found.
// It panics if v's Kind is not struct.
func (v Value) FieldByName(name string) Value {
	v.mustBe(Struct)
	for i := 0; i < v.typ.NumField(); i++ {
		if v.typ.Field(i).Name == name {
			return v.Field(i)
		}
	}
	return Value{}
	/*
		if f, ok := v.typ.FieldByName(name); ok {
			return v.FieldByIndex(f.Index)
		}
		return Value{}
	*/
}

// Float returns v's underlying value, as a float64.
// It panics if v's Kind is not Float or Double
func (v Value) Float() float64 {
	k := v.typ.Kind()
	switch k {
	case Float:
		return float64(*(*float32)(v.val))
	case Double:
		return *(*float64)(v.val)
	}
	panic(&ValueError{"ffi.Value.Float", k})
}

// Index returns v's i'th element.
// It panics if v's Kind is not Array or Slice or i is out of range.
func (v Value) Index(i int) Value {
	v.mustBe(Array)
	tt := v.typ.(cffi_array)
	if i < 0 || i > int(tt.Len()) {
		panic("ffi: array index out of range")
	}
	typ := tt.Elem()
	offset := uintptr(i) * typ.Size()

	var val unsafe.Pointer = unsafe.Pointer(uintptr(v.val) + offset)
	return Value{typ, val}
}

// Int returns v's underlying value, as an int64.
// It panics if v's Kind is not Int, Int8, Int16, Int32, or Int64.
func (v Value) Int() int64 {
	k := v.typ.Kind()
	var p unsafe.Pointer = v.val
	switch k {
	case Int:
		return int64(*(*int)(p))
	case Int8:
		return int64(*(*int8)(p))
	case Int16:
		return int64(*(*int16)(p))
	case Int32:
		return int64(*(*int32)(p))
	case Int64:
		return int64(*(*int64)(p))
	}
	panic(&ValueError{"ffi.Value.Int", k})
}

// IsNil returns true if v is a nil value.
// It panics if v's Kind is Ptr.
func (v Value) IsNil() bool {
	v.mustBe(Ptr)
	ptr := v.val
	ptr = *(*unsafe.Pointer)(ptr)
	return ptr == nil
}

// IsValid returns true if v represents a value.
// It returns false if v is the zero Value.
// If IsValid returns false, all other methods except String panic.
// Most functions and methods never return an invalid value.
// If one does, its documentation states the conditions explicitly.
func (v Value) IsValid() bool {
	return v.val != nil
}

// Kind returns v's Kind.
func (v Value) Kind() Kind {
	return v.typ.Kind()
}

// Len returns v's length.
// It panics if v's Kind is not Array
func (v Value) Len() int {
	v.mustBe(Array)
	tt := v.typ.(cffi_array)
	return int(tt.Len())
}

// NumField returns the number of fields in the struct v.
// It panics if v's Kind is not Struct.
func (v Value) NumField() int {
	v.mustBe(Struct)
	return v.typ.NumField()
}

// SetFloat sets v's underlying value to x.
// It panics if v's Kind is not Float or Double, or if CanSet() is false.
func (v Value) SetFloat(x float64) {
	switch k := v.typ.Kind(); k {
	default:
		panic(&ValueError{"ffi.Value.SetFloat", k})
	case Float:
		*(*float32)(v.val) = float32(x)
	case Double:
		*(*float64)(v.val) = x
	}
}

// SetInt sets v's underlying value to x.
// It panics if v's Kind is not Int, Int8, Int16, Int32, or Int64, or if CanSet() is false.
func (v Value) SetInt(x int64) {
	//v.mustBeAssignable()
	switch k := v.typ.Kind(); k {
	default:
		panic(&ValueError{"ffi.Value.SetInt", k})
	case Int:
		*(*int)(v.val) = int(x)
	case Int8:
		*(*int8)(v.val) = int8(x)
	case Int16:
		*(*int16)(v.val) = int16(x)
	case Int32:
		*(*int32)(v.val) = int32(x)
	case Int64:
		*(*int64)(v.val) = x
	}
}

// SetUint sets v's underlying value to x.
// It panics if v's Kind is not Int, Int8, Int16, Int32, or Int64, or if CanSet() is false.
func (v Value) SetUint(x uint64) {
	//v.mustBeAssignable()
	switch k := v.typ.Kind(); k {
	default:
		panic(&ValueError{"ffi.Value.SetUint", k})
	// case Uint:
	// 	*(*uint)(v.val) = uint(x)
	case Uint8:
		*(*uint8)(v.val) = uint8(x)
	case Uint16:
		*(*uint16)(v.val) = uint16(x)
	case Uint32:
		*(*uint32)(v.val) = uint32(x)
	case Uint64:
		*(*uint64)(v.val) = x
	}
}

// Type returns v's type
func (v Value) Type() Type {
	return v.typ
}

// Uint returns v's underlying value, as a uint64.
// It panics if v's Kind is not Uint, Uintptr, Uint8, Uint16, Uint32, or Uint64.
func (v Value) Uint() uint64 {
	k := v.typ.Kind()
	var p unsafe.Pointer = v.val
	switch k {
	// case Uint:
	// 	return uint64(*(*uint)(p))
	case Uint8:
		return uint64(*(*uint8)(p))
	case Uint16:
		return uint64(*(*uint16)(p))
	case Uint32:
		return uint64(*(*uint32)(p))
	case Uint64:
		return uint64(*(*uint64)(p))
		// case Uintptr:
		// 	return uint64(*(*uintptr)(p))
	}
	panic(&ValueError{"ffi.Value.Uint", k})
}

// UnsafeAddr returns a pointer to v's data.
// It is for advanced clients that also import the "unsafe" package.
func (v Value) UnsafeAddr() uintptr {
	if v.typ == nil {
		panic("ffi: call of ffi.Value.UnsafeAddr on an invalid Value")
	}
	// FIXME: use flagAddr ??
	return uintptr(v.val)
}

// Indirect returns the value that v points to.
// If v is a nil pointer, Indirect returns a zero Value.
// If v is not a pointer, Indirect returns v.
func Indirect(v Value) Value {
	if v.typ.Kind() != Ptr {
		return v
	}
	return v.Elem()
}

// EOF