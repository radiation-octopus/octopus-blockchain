package rlp

import (
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/rlp/rlpstruct"
	"io"
	"math/big"
	"reflect"
)

var (
	// 通用编码值。
	//这些在实现EncodeRLP时很有用。
	EmptyString = []byte{0x80}
	EmptyList   = []byte{0xC0}
)

var ErrNegativeBigInt = errors.New("rlp: cannot encode negative big.Int")

type listhead struct {
	offset int //字符串数据中此标头的索引
	size   int // 编码数据的总大小（包括列表标题）
}

//encode将磁头写入给定的缓冲区，缓冲区的长度必须至少为9字节。它返回编码的字节。
func (head *listhead) encode(buf []byte) []byte {
	return buf[:puthead(buf, 0xC0, 0xF7, uint64(head.size))]
}

// 种类表示RLP流中包含的值的种类。
type Kind int8

const (
	Byte Kind = iota
	String
	List
)

// ByterReader必须由流的任何输入读取器实现。由bufio等实施。读取器和字节。读者
type ByteReader interface {
	io.Reader
	io.ByteReader
}

// Encode将val的RLP编码写入w。请注意，Encode在某些情况下可能会执行许多小的写入。考虑将w设为缓冲。
//请参阅编码规则的包级文档。
func Encode(w io.Writer, val interface{}) error {
	// 优化：当EncodeRLP调用时重用*encBuffer。
	if buf := encBufferFromWriter(w); buf != nil {
		return buf.encode(val)
	}

	buf := getEncBuffer()
	defer encBufferPool.Put(buf)
	if err := buf.encode(val); err != nil {
		return err
	}
	return buf.writeTo(w)
}

// EncodeToBytes返回val的RLP编码。
func EncodeToBytes(val interface{}) ([]byte, error) {
	buf := getEncBuffer()
	defer encBufferPool.Put(buf)

	if err := buf.encode(val); err != nil {
		return nil, err
	}
	return buf.makeBytes(), nil
}

// 编码器读取器返回一个读取器，从中可以读取val的RLP编码。
//返回的大小是编码数据的总大小。有关编码规则，请参阅Encode的文档。
func EncodeToReader(val interface{}) (size int, r io.Reader, err error) {
	buf := getEncBuffer()
	if err := buf.encode(val); err != nil {
		encBufferPool.Put(buf)
		return 0, nil, err
	}
	// 注意：无法将读取器放回此处的池中，因为它由encReader持有。
	//当它完全用完时，读者会把它放回原处。
	return buf.size(), &encReader{buf: buf}, nil
}

// puthead将列表或字符串头写入buf。buf的长度必须至少为9字节。
func puthead(buf []byte, smalltag, largetag byte, size uint64) int {
	if size < 56 {
		buf[0] = smalltag + byte(size)
		return 1
	}
	sizesize := putint(buf[1:], size)
	buf[0] = largetag + byte(sizesize)
	return sizesize + 1
}

//putint以大端字节顺序将i写入b的开头，使用表示i所需的最少字节数。
func putint(b []byte, i uint64) (size int) {
	switch {
	case i < (1 << 8):
		b[0] = byte(i)
		return 1
	case i < (1 << 16):
		b[0] = byte(i >> 8)
		b[1] = byte(i)
		return 2
	case i < (1 << 24):
		b[0] = byte(i >> 16)
		b[1] = byte(i >> 8)
		b[2] = byte(i)
		return 3
	case i < (1 << 32):
		b[0] = byte(i >> 24)
		b[1] = byte(i >> 16)
		b[2] = byte(i >> 8)
		b[3] = byte(i)
		return 4
	case i < (1 << 40):
		b[0] = byte(i >> 32)
		b[1] = byte(i >> 24)
		b[2] = byte(i >> 16)
		b[3] = byte(i >> 8)
		b[4] = byte(i)
		return 5
	case i < (1 << 48):
		b[0] = byte(i >> 40)
		b[1] = byte(i >> 32)
		b[2] = byte(i >> 24)
		b[3] = byte(i >> 16)
		b[4] = byte(i >> 8)
		b[5] = byte(i)
		return 6
	case i < (1 << 56):
		b[0] = byte(i >> 48)
		b[1] = byte(i >> 40)
		b[2] = byte(i >> 32)
		b[3] = byte(i >> 24)
		b[4] = byte(i >> 16)
		b[5] = byte(i >> 8)
		b[6] = byte(i)
		return 7
	default:
		b[0] = byte(i >> 56)
		b[1] = byte(i >> 48)
		b[2] = byte(i >> 40)
		b[3] = byte(i >> 32)
		b[4] = byte(i >> 24)
		b[5] = byte(i >> 16)
		b[6] = byte(i >> 8)
		b[7] = byte(i)
		return 8
	}
}

var (
	decoderInterface = reflect.TypeOf(new(Decoder)).Elem()
	bigInt           = reflect.TypeOf(big.Int{})
)

// 编码器由需要自定义编码规则或想要编码私有字段的类型实现。
type Encoder interface {
	// EncodeRLP应该将其接收器的RLP编码写入w。
	//如果实现是指针方法，则也可以调用nil指针。实现应该生成有效的RLP。
	//写入的数据目前尚未验证，但将来的版本可能会验证。
	//建议只写入一个值，但也允许写入多个值或根本不写入值。
	EncodeRLP(io.Writer) error
}

var encoderInterface = reflect.TypeOf(new(Encoder)).Elem()

// makeWriter为给定类型创建一个writer函数。
func makeWriter(typ reflect.Type, ts rlpstruct.Tags) (writer, error) {
	kind := typ.Kind()
	switch {
	case typ == rawValueType:
		return writeRawValue, nil
	case typ.AssignableTo(reflect.PtrTo(bigInt)):
		return writeBigIntPtr, nil
	case typ.AssignableTo(bigInt):
		return writeBigIntNoPtr, nil
	case kind == reflect.Ptr:
		return makePtrWriter(typ, ts)
	case reflect.PtrTo(typ).Implements(encoderInterface):
		return makeEncoderWriter(typ), nil
	case isUint(kind):
		return writeUint, nil
	case kind == reflect.Bool:
		return writeBool, nil
	case kind == reflect.String:
		return writeString, nil
	case kind == reflect.Slice && isByte(typ.Elem()):
		return writeBytes, nil
	case kind == reflect.Array && isByte(typ.Elem()):
		return makeByteArrayWriter(typ), nil
	case kind == reflect.Slice || kind == reflect.Array:
		return makeSliceWriter(typ, ts)
	case kind == reflect.Struct:
		return makeStructWriter(typ)
	case kind == reflect.Interface:
		return writeInterface, nil
	default:
		return nil, fmt.Errorf("rlp: type %v is not RLP-serializable", typ)
	}
}

func writeRawValue(val reflect.Value, w *encBuffer) error {
	w.str = append(w.str, val.Bytes()...)
	return nil
}

func writeUint(val reflect.Value, w *encBuffer) error {
	w.writeUint64(val.Uint())
	return nil
}

func writeBool(val reflect.Value, w *encBuffer) error {
	w.writeBool(val.Bool())
	return nil
}

func writeBigIntPtr(val reflect.Value, w *encBuffer) error {
	ptr := val.Interface().(*big.Int)
	if ptr == nil {
		w.str = append(w.str, 0x80)
		return nil
	}
	if ptr.Sign() == -1 {
		return ErrNegativeBigInt
	}
	w.writeBigInt(ptr)
	return nil
}

func writeBigIntNoPtr(val reflect.Value, w *encBuffer) error {
	i := val.Interface().(big.Int)
	if i.Sign() == -1 {
		return ErrNegativeBigInt
	}
	w.writeBigInt(&i)
	return nil
}

func makePtrWriter(typ reflect.Type, ts rlpstruct.Tags) (writer, error) {
	nilEncoding := byte(0xC0)
	if typeNilKind(typ.Elem(), ts) == String {
		nilEncoding = 0x80
	}

	etypeinfo := theTC.infoWhileGenerating(typ.Elem(), rlpstruct.Tags{})
	if etypeinfo.writerErr != nil {
		return nil, etypeinfo.writerErr
	}

	writer := func(val reflect.Value, w *encBuffer) error {
		if ev := val.Elem(); ev.IsValid() {
			return etypeinfo.writer(ev, w)
		}
		w.str = append(w.str, nilEncoding)
		return nil
	}
	return writer, nil
}

func makeEncoderWriter(typ reflect.Type) writer {
	if typ.Implements(encoderInterface) {
		return func(val reflect.Value, w *encBuffer) error {
			return val.Interface().(Encoder).EncodeRLP(w)
		}
	}
	w := func(val reflect.Value, w *encBuffer) error {
		if !val.CanAddr() {
			// package json simply doesn't call MarshalJSON for this case, but encodes the
			// value as if it didn't implement the interface. We don't want to handle it that
			// way.
			return fmt.Errorf("rlp: unadressable value of type %v, EncodeRLP is pointer method", val.Type())
		}
		return val.Addr().Interface().(Encoder).EncodeRLP(w)
	}
	return w
}

func writeString(val reflect.Value, w *encBuffer) error {
	s := val.String()
	if len(s) == 1 && s[0] <= 0x7f {
		// 适合单字节，无字符串标头
		w.str = append(w.str, s[0])
	} else {
		w.encodeStringHeader(len(s))
		w.str = append(w.str, s...)
	}
	return nil
}

func writeBytes(val reflect.Value, w *encBuffer) error {
	w.writeBytes(val.Bytes())
	return nil
}

func makeByteArrayWriter(typ reflect.Type) writer {
	switch typ.Len() {
	case 0:
		return writeLengthZeroByteArray
	case 1:
		return writeLengthOneByteArray
	default:
		length := typ.Len()
		return func(val reflect.Value, w *encBuffer) error {
			if !val.CanAddr() {
				// 获取val的字节片需要它是可寻址的。通过复制使其可寻址。
				copy := reflect.New(val.Type()).Elem()
				copy.Set(val)
				val = copy
			}
			slice := byteArrayBytes(val, length)
			w.encodeStringHeader(len(slice))
			w.str = append(w.str, slice...)
			return nil
		}
	}
}

func writeLengthZeroByteArray(val reflect.Value, w *encBuffer) error {
	w.str = append(w.str, 0x80)
	return nil
}

func writeLengthOneByteArray(val reflect.Value, w *encBuffer) error {
	b := byte(val.Index(0).Uint())
	if b <= 0x7f {
		w.str = append(w.str, b)
	} else {
		w.str = append(w.str, 0x81, b)
	}
	return nil
}

func makeSliceWriter(typ reflect.Type, ts rlpstruct.Tags) (writer, error) {
	etypeinfo := theTC.infoWhileGenerating(typ.Elem(), rlpstruct.Tags{})
	if etypeinfo.writerErr != nil {
		return nil, etypeinfo.writerErr
	}

	var wfn writer
	if ts.Tail {
		// 这是用于结构尾部切片。w、 没有为它们调用列表。
		wfn = func(val reflect.Value, w *encBuffer) error {
			vlen := val.Len()
			for i := 0; i < vlen; i++ {
				if err := etypeinfo.writer(val.Index(i), w); err != nil {
					return err
				}
			}
			return nil
		}
	} else {
		// 这适用于常规切片和数组。
		wfn = func(val reflect.Value, w *encBuffer) error {
			vlen := val.Len()
			if vlen == 0 {
				w.str = append(w.str, 0xC0)
				return nil
			}
			listOffset := w.list()
			for i := 0; i < vlen; i++ {
				if err := etypeinfo.writer(val.Index(i), w); err != nil {
					return err
				}
			}
			w.listEnd(listOffset)
			return nil
		}
	}
	return wfn, nil
}

func makeStructWriter(typ reflect.Type) (writer, error) {
	fields, err := structFields(typ)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if f.info.writerErr != nil {
			return nil, structFieldError{typ, f.index, f.info.writerErr}
		}
	}

	var writer writer
	firstOptionalField := firstOptionalField(fields)
	if firstOptionalField == len(fields) {
		// 这是不带任何可选字段的结构的writer函数。
		writer = func(val reflect.Value, w *encBuffer) error {
			lh := w.list()
			for _, f := range fields {
				if err := f.info.writer(val.Field(f.index), w); err != nil {
					return err
				}
			}
			w.listEnd(lh)
			return nil
		}
	} else {
		// 如果有任何“可选”字段，编写器需要执行其他检查以确定输出列表长度。
		writer = func(val reflect.Value, w *encBuffer) error {
			lastField := len(fields) - 1
			for ; lastField >= firstOptionalField; lastField-- {
				if !val.Field(fields[lastField].index).IsZero() {
					break
				}
			}
			lh := w.list()
			for i := 0; i <= lastField; i++ {
				if err := fields[i].info.writer(val.Field(fields[i].index), w); err != nil {
					return err
				}
			}
			w.listEnd(lh)
			return nil
		}
	}
	return writer, nil
}

func writeInterface(val reflect.Value, w *encBuffer) error {
	if val.IsNil() {
		//写入空列表。这与之前的RLP编码器一致，因此应该避免任何问题。
		w.str = append(w.str, 0xC0)
		return nil
	}
	eval := val.Elem()
	writer, err := cachedWriter(eval.Type())
	if err != nil {
		return err
	}
	return writer(eval, w)
}

// headsize返回给定大小值的列表或字符串标题的大小。
func headsize(size uint64) int {
	if size < 56 {
		return 1
	}
	return 1 + intsize(size)
}

// intsize计算存储i所需的最小字节数。
func intsize(i uint64) (size int) {
	for size = 1; ; size++ {
		if i >>= 8; i == 0 {
			return size
		}
	}
}
