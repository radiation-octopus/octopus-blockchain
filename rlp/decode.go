package rlp

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/rlp/rlpstruct"
	"io"
	"math/big"
	"reflect"
	"strings"
	"sync"
)

var (
	ErrExpectedString   = errors.New("rlp: expected String or Byte")
	ErrExpectedList     = errors.New("rlp: expected List")
	ErrCanonInt         = errors.New("rlp: non-canonical integer format")
	ErrCanonSize        = errors.New("rlp: non-canonical size information")
	ErrElemTooLarge     = errors.New("rlp: element is larger than containing list")
	ErrValueTooLarge    = errors.New("rlp: value size exceeds available input length")
	ErrMoreThanOneValue = errors.New("rlp: input contains more than one value")

	// 内部错误
	errNotInList     = errors.New("rlp: call of ListEnd outside of any list")
	errNotAtEOL      = errors.New("rlp: call of ListEnd not positioned at EOL")
	errUintOverflow  = errors.New("rlp: uint overflow")
	errNoPointer     = errors.New("rlp: interface given to Decode must be a pointer")
	errDecodeIntoNil = errors.New("rlp: pointer given to Decode must not be nil")

	streamPool = sync.Pool{
		New: func() interface{} { return new(Stream) },
	}
)

//内部ErrorsCoder由需要自定义RLP解码规则或需要解码到私有字段的类型实现。
//DecodeRLP方法应该从给定流中读取一个值。不禁止少读或多读，但这可能会令人困惑。
type Decoder interface {
	DecodeRLP(*Stream) error
}

// Decode解析来自r的RLP编码数据，并将结果存储在val指向的值中。Val必须是非nil指针。如果r没有实现ByteReader，Decode将自己进行缓冲。
//请注意，Decode并没有为所有读卡器设置输入限制，并且可能容易受到巨大值大小导致的恐慌的影响。如果需要输入限制，请使用NewStream（r，limit）。解码（val）
func Decode(r io.Reader, val interface{}) error {
	stream := streamPool.Get().(*Stream)
	defer streamPool.Put(stream)

	stream.Reset(r, 0)
	return stream.Decode(val)
}

// DecodeBytes将RLP数据从b解析为val。输入必须只包含一个值，并且不包含尾随数据。
func DecodeBytes(b []byte, val interface{}) error {
	r := bytes.NewReader(b)

	stream := streamPool.Get().(*Stream)
	defer streamPool.Put(stream)

	stream.Reset(r, uint64(len(b)))
	if err := stream.Decode(val); err != nil {
		return err
	}
	if r.Len() > 0 {
		return ErrMoreThanOneValue
	}
	return nil
}

type decodeError struct {
	msg string
	typ reflect.Type
	ctx []string
}

func (err *decodeError) Error() string {
	ctx := ""
	if len(err.ctx) > 0 {
		ctx = ", decoding into "
		for i := len(err.ctx) - 1; i >= 0; i-- {
			ctx += err.ctx[i]
		}
	}
	return fmt.Sprintf("rlp: %s for %v%s", err.msg, err.typ, ctx)
}

func wrapStreamError(err error, typ reflect.Type) error {
	switch err {
	case ErrCanonInt:
		return &decodeError{msg: "non-canonical integer (leading zero bytes)", typ: typ}
	case ErrCanonSize:
		return &decodeError{msg: "non-canonical size information", typ: typ}
	case ErrExpectedList:
		return &decodeError{msg: "expected input list", typ: typ}
	case ErrExpectedString:
		return &decodeError{msg: "expected input string or byte", typ: typ}
	case errUintOverflow:
		return &decodeError{msg: "input string too long", typ: typ}
	case errNotAtEOL:
		return &decodeError{msg: "input list has too many elements", typ: typ}
	}
	return err
}

func addErrorContext(err error, ctx string) error {
	if decErr, ok := err.(*decodeError); ok {
		decErr.ctx = append(decErr.ctx, ctx)
	}
	return err
}

func makeDecoder(typ reflect.Type, tags rlpstruct.Tags) (dec decoder, err error) {
	kind := typ.Kind()
	switch {
	case typ == rawValueType:
		return decodeRawValue, nil
	case typ.AssignableTo(reflect.PtrTo(bigInt)):
		return decodeBigInt, nil
	case typ.AssignableTo(bigInt):
		return decodeBigIntNoPtr, nil
	case kind == reflect.Ptr:
		return makePtrDecoder(typ, tags)
	case reflect.PtrTo(typ).Implements(decoderInterface):
		return decodeDecoder, nil
	case isUint(kind):
		return decodeUint, nil
	case kind == reflect.Bool:
		return decodeBool, nil
	case kind == reflect.String:
		return decodeString, nil
	case kind == reflect.Slice || kind == reflect.Array:
		return makeListDecoder(typ, tags)
	case kind == reflect.Struct:
		return makeStructDecoder(typ)
	case kind == reflect.Interface:
		return decodeInterface, nil
	default:
		return nil, fmt.Errorf("rlp: type %v is not RLP-serializable", typ)
	}
}

var ifsliceType = reflect.TypeOf([]interface{}{})

func decodeInterface(s *Stream, val reflect.Value) error {
	if val.Type().NumMethod() != 0 {
		return fmt.Errorf("rlp: type %v is not RLP-serializable", val.Type())
	}
	kind, _, err := s.Kind()
	if err != nil {
		return err
	}
	if kind == List {
		slice := reflect.New(ifsliceType).Elem()
		if err := decodeListSlice(s, slice, decodeInterface); err != nil {
			return err
		}
		val.Set(slice)
	} else {
		b, err := s.Bytes()
		if err != nil {
			return err
		}
		val.Set(reflect.ValueOf(b))
	}
	return nil
}

func makeStructDecoder(typ reflect.Type) (decoder, error) {
	fields, err := structFields(typ)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if f.info.decoderErr != nil {
			return nil, structFieldError{typ, f.index, f.info.decoderErr}
		}
	}
	dec := func(s *Stream, val reflect.Value) (err error) {
		if _, err := s.List(); err != nil {
			return wrapStreamError(err, typ)
		}
		for i, f := range fields {
			err := f.info.decoder(s, val.Field(f.index))
			if err == EOL {
				if f.optional {
					// 该字段是可选的，因此在到达最后一个字段之前到达列表末尾是可以接受的。所有剩余的未编码字段均为零。
					zeroFields(val, fields[i:])
					break
				}
				return &decodeError{msg: "too few elements", typ: typ}
			} else if err != nil {
				return addErrorContext(err, "."+typ.Field(f.index).Name)
			}
		}
		return wrapStreamError(s.ListEnd(), typ)
	}
	return dec, nil
}

func zeroFields(structval reflect.Value, fields []field) {
	for _, f := range fields {
		fv := structval.Field(f.index)
		fv.Set(reflect.Zero(fv.Type()))
	}
}

func decodeRawValue(s *Stream, val reflect.Value) error {
	r, err := s.Raw()
	if err != nil {
		return err
	}
	val.SetBytes(r)
	return nil
}

func decodeBigInt(s *Stream, val reflect.Value) error {
	i := val.Interface().(*big.Int)
	if i == nil {
		i = new(big.Int)
		val.Set(reflect.ValueOf(i))
	}

	err := s.decodeBigInt(i)
	if err != nil {
		return wrapStreamError(err, val.Type())
	}
	return nil
}

func decodeBigIntNoPtr(s *Stream, val reflect.Value) error {
	return decodeBigInt(s, val.Addr())
}

// makePtrDecoder创建解码为指针元素类型的解码器。
func makePtrDecoder(typ reflect.Type, tag rlpstruct.Tags) (decoder, error) {
	etype := typ.Elem()
	etypeinfo := theTC.infoWhileGenerating(etype, rlpstruct.Tags{})
	switch {
	case etypeinfo.decoderErr != nil:
		return nil, etypeinfo.decoderErr
	case !tag.NilOK:
		return makeSimplePtrDecoder(etype, etypeinfo), nil
	default:
		return makeNilPtrDecoder(etype, etypeinfo, tag), nil
	}
}

func makeSimplePtrDecoder(etype reflect.Type, etypeinfo *typeinfo) decoder {
	return func(s *Stream, val reflect.Value) (err error) {
		newval := val
		if val.IsNil() {
			newval = reflect.New(etype)
		}
		if err = etypeinfo.decoder(s, newval.Elem()); err == nil {
			val.Set(newval)
		}
		return err
	}
}

//MakeNilptOrderCoder创建一个解码器，将空值解码为nil。
//非空值被解码为元素类型的值，就像makePtrDecoder一样。此解码器用于结构标记为“nil”的指针类型的结构字段。
func makeNilPtrDecoder(etype reflect.Type, etypeinfo *typeinfo, ts rlpstruct.Tags) decoder {
	typ := reflect.PtrTo(etype)
	nilPtr := reflect.Zero(typ)

	// 确定导致指针为零的值类型。
	nilKind := typeNilKind(etype, ts)

	return func(s *Stream, val reflect.Value) (err error) {
		kind, size, err := s.Kind()
		if err != nil {
			val.Set(nilPtr)
			return wrapStreamError(err, typ)
		}
		// Handle empty values as a nil pointer.
		if kind != Byte && size == 0 {
			if kind != nilKind {
				return &decodeError{
					msg: fmt.Sprintf("wrong kind of empty value (got %v, want %v)", kind, nilKind),
					typ: typ,
				}
			}
			// rearm s.Kind. This is important because the input
			// position must advance to the next value even though
			// we don't read anything.
			s.kind = -1
			val.Set(nilPtr)
			return nil
		}
		newval := val
		if val.IsNil() {
			newval = reflect.New(etype)
		}
		if err = etypeinfo.decoder(s, newval.Elem()); err == nil {
			val.Set(newval)
		}
		return err
	}
}

func decodeByteArray(s *Stream, val reflect.Value) error {
	kind, size, err := s.Kind()
	if err != nil {
		return err
	}
	slice := byteArrayBytes(val, val.Len())
	switch kind {
	case Byte:
		if len(slice) == 0 {
			return &decodeError{msg: "input string too long", typ: val.Type()}
		} else if len(slice) > 1 {
			return &decodeError{msg: "input string too short", typ: val.Type()}
		}
		slice[0] = s.byteval
		s.kind = -1
	case String:
		if uint64(len(slice)) < size {
			return &decodeError{msg: "input string too long", typ: val.Type()}
		}
		if uint64(len(slice)) > size {
			return &decodeError{msg: "input string too short", typ: val.Type()}
		}
		if err := s.readFull(slice); err != nil {
			return err
		}
		// 拒绝应使用单字节编码的情况。
		if size == 1 && slice[0] < 128 {
			return wrapStreamError(ErrCanonSize, val.Type())
		}
	case List:
		return wrapStreamError(ErrExpectedString, val.Type())
	}
	return nil
}

func makeListDecoder(typ reflect.Type, tag rlpstruct.Tags) (decoder, error) {
	etype := typ.Elem()
	if etype.Kind() == reflect.Uint8 && !reflect.PtrTo(etype).Implements(decoderInterface) {
		if typ.Kind() == reflect.Array {
			return decodeByteArray, nil
		}
		return decodeByteSlice, nil
	}
	etypeinfo := theTC.infoWhileGenerating(etype, rlpstruct.Tags{})
	if etypeinfo.decoderErr != nil {
		return nil, etypeinfo.decoderErr
	}
	var dec decoder
	switch {
	case typ.Kind() == reflect.Array:
		dec = func(s *Stream, val reflect.Value) error {
			return decodeListArray(s, val, etypeinfo.decoder)
		}
	case tag.Tail:
		// 带有“tail”标记的片段可以作为结构的最后一个字段出现，并且应该吞并所有剩余的列表元素。
		//结构解码器已经调用了s.List，直接对元素进行解码。
		dec = func(s *Stream, val reflect.Value) error {
			return decodeSliceElems(s, val, etypeinfo.decoder)
		}
	default:
		dec = func(s *Stream, val reflect.Value) error {
			return decodeListSlice(s, val, etypeinfo.decoder)
		}
	}
	return dec, nil
}

func decodeListSlice(s *Stream, val reflect.Value, elemdec decoder) error {
	size, err := s.List()
	if err != nil {
		return wrapStreamError(err, val.Type())
	}
	if size == 0 {
		val.Set(reflect.MakeSlice(val.Type(), 0, 0))
		return s.ListEnd()
	}
	if err := decodeSliceElems(s, val, elemdec); err != nil {
		return err
	}
	return s.ListEnd()
}

func decodeSliceElems(s *Stream, val reflect.Value, elemdec decoder) error {
	i := 0
	for ; ; i++ {
		// 必要时增加切片
		if i >= val.Cap() {
			newcap := val.Cap() + val.Cap()/2
			if newcap < 4 {
				newcap = 4
			}
			newv := reflect.MakeSlice(val.Type(), val.Len(), newcap)
			reflect.Copy(newv, val)
			val.Set(newv)
		}
		if i >= val.Len() {
			val.SetLen(i + 1)
		}
		// 解码为元素
		if err := elemdec(s, val.Index(i)); err == EOL {
			break
		} else if err != nil {
			return addErrorContext(err, fmt.Sprint("[", i, "]"))
		}
	}
	if i < val.Len() {
		val.SetLen(i)
	}
	return nil
}

func decodeListArray(s *Stream, val reflect.Value, elemdec decoder) error {
	if _, err := s.List(); err != nil {
		return wrapStreamError(err, val.Type())
	}
	vlen := val.Len()
	i := 0
	for ; i < vlen; i++ {
		if err := elemdec(s, val.Index(i)); err == EOL {
			break
		} else if err != nil {
			return addErrorContext(err, fmt.Sprint("[", i, "]"))
		}
	}
	if i < vlen {
		return &decodeError{msg: "input list has too few elements", typ: val.Type()}
	}
	return wrapStreamError(s.ListEnd(), val.Type())
}

func decodeByteSlice(s *Stream, val reflect.Value) error {
	b, err := s.Bytes()
	if err != nil {
		return wrapStreamError(err, val.Type())
	}
	val.SetBytes(b)
	return nil
}

func decodeDecoder(s *Stream, val reflect.Value) error {
	return val.Addr().Interface().(Decoder).DecodeRLP(s)
}

func decodeBool(s *Stream, val reflect.Value) error {
	b, err := s.Bool()
	if err != nil {
		return wrapStreamError(err, val.Type())
	}
	val.SetBool(b)
	return nil
}

func decodeUint(s *Stream, val reflect.Value) error {
	typ := val.Type()
	num, err := s.uint(typ.Bits())
	if err != nil {
		return wrapStreamError(err, val.Type())
	}
	val.SetUint(num)
	return nil
}

func decodeString(s *Stream, val reflect.Value) error {
	b, err := s.Bytes()
	if err != nil {
		return wrapStreamError(err, val.Type())
	}
	val.SetString(string(b))
	return nil
}

// 流式处理期间到达当前列表的末尾时，将返回EOL。
var EOL = errors.New("rlp: end of list")

// 流可用于对输入流进行逐段解码。如果输入非常大，或者类型的解码规则取决于输入结构，则这非常有用。流不保留内部缓冲区。解码一个值后，输入读取器将位于下一个值的类型信息之前。
//当解码列表且输入位置达到列表的声明长度时，所有操作将返回错误EOL。必须使用ListEnd确认列表结束，才能继续读取封闭列表。
//流对于并发使用不安全。
type Stream struct {
	r ByteReader

	remaining uint64   // 要从r读取的剩余字节数
	size      uint64   // 未来价值大小
	kinderr   error    // 上次readKind出错
	stack     []uint64 // list大小
	uintbuf   [32]byte // 整数解码辅助缓冲器
	kind      Kind     // kind 未来的价值
	byteval   byte     // 类型标记中单个字节的值
	limited   bool     // 如果输入限制生效，则为true
}

// NewStream创建一个从r读取的新解码流。如果r实现ByteReader接口，则流不会引入任何缓冲。对于非顶级值，Stream为不符合封闭列表的值返回ErrElemTooLarge。
//流支持可选的输入限制。如果设置了限制，将对照剩余输入长度检查任何toplevel值的大小。
//遇到超过剩余输入长度的值的流操作将返回ErrValueTooLarge。可以通过为inputLimit传递非零值来设置限制。
//如果r是字节。读取器或字符串。读取器，除非提供明确的限制，否则输入限制设置为r的基础数据的长度。
func NewStream(r io.Reader, inputLimit uint64) *Stream {
	s := new(Stream)
	s.Reset(r, inputLimit)
	return s
}

//Raw读取包含RLP类型信息的原始编码值。
func (s *Stream) Raw() ([]byte, error) {
	kind, size, err := s.Kind()
	if err != nil {
		return nil, err
	}
	if kind == Byte {
		s.kind = -1 // 重整整备kind
		return []byte{s.byteval}, nil
	}
	//原始标题已被读取，不再可用。阅读内容并在其前面放置一个新标题。
	start := headsize(size)
	buf := make([]byte, uint64(start)+size)
	if err := s.readFull(buf[start:]); err != nil {
		return nil, err
	}
	if kind == String {
		puthead(buf, 0x80, 0xB7, size)
	} else {
		puthead(buf, 0xC0, 0xF7, size)
	}
	return buf, nil
}

// Kind返回输入流中下一个值的种类和大小。返回的大小是组成该值的字节数。对于kind==Byte，大小为零，因为该值包含在type标记中。
//第一次调用Kind将从输入读取器读取大小信息，并将其定位在值的实际字节的开头。对Kind的后续调用（直到值被解码）不会使输入读取器前进并返回缓存的信息。
func (s *Stream) Kind() (kind Kind, size uint64, err error) {
	if s.kind >= 0 {
		return s.kind, s.size, s.kinderr
	}

	// 检查列表末尾。需要在此处执行此操作，因为readKind会检查列表大小，并将返回错误的错误。
	inList, listLimit := s.listLimit()
	if inList && listLimit == 0 {
		return 0, 0, EOL
	}
	// 读取实际尺寸标记。
	s.kind, s.size, s.kinderr = s.readKind()
	if s.kinderr == nil {
		// 对照输入限制检查前面值的数据大小。这是因为许多解码器需要分配与值大小匹配的输入缓冲区。在这里检查它可以保护这些解码器免受声明非常大值大小的输入的影响。
		if inList && s.size > listLimit {
			s.kinderr = ErrElemTooLarge
		} else if s.limited && s.size > s.remaining {
			s.kinderr = ErrValueTooLarge
		}
	}
	return s.kind, s.size, s.kinderr
}

func (s *Stream) readKind() (kind Kind, size uint64, err error) {
	b, err := s.readByte()
	if err != nil {
		if len(s.stack) == 0 {
			// 在顶层，将误差调整为实际EOF。io。调用方使用EOF来确定何时停止解码。
			switch err {
			case io.ErrUnexpectedEOF:
				err = io.EOF
			case ErrValueTooLarge:
				err = io.EOF
			}
		}
		return 0, 0, err
	}
	s.byteval = 0
	switch {
	case b < 0x80:
		// 对于值在[0x00，0x7F]范围内的单个字节，该字节是其自己的RLP编码。
		s.byteval = b
		return Byte, 0, nil
	case b < 0xB8:
		// 否则，如果字符串的长度为0-55字节，则RLP编码由一个值为0x80的字节加上字符串的长度，再加上字符串。因此，第一个字节的范围为[0x80，0xB7]。
		return String, uint64(b - 0x80), nil
	case b < 0xC0:
		// 如果字符串长度超过55字节，则RLP编码由一个值为0xB7的字节加上二进制形式的字符串长度，然后是字符串长度，然后是字符串。
		//例如，长度为1024的字符串将被编码为0xB90400，后跟该字符串。因此，第一个字节的范围为
		//[0xB8，0xBF]。
		size, err = s.readUint(b - 0xB7)
		if err == nil && size < 56 {
			err = ErrCanonSize
		}
		return String, size, err
	case b < 0xF8:
		// 如果列表的总有效载荷（即其所有项目的组合长度）为0-55字节长，则RLP编码由一个值为0xC0的单个字节加上列表长度，然后串联项目的RLP编码组成。
		//因此，第一个字节的范围为[0xC0，0xF7]。
		return List, uint64(b - 0xC0), nil
	default:
		// 如果列表的总有效负载长度超过55字节，则RLP编码由一个值为0xF7的单个字节加上二进制形式的有效负载长度，然后是有效负载长度，然后是项目RLP编码的串联组成。
		//因此，第一个字节的范围为[0xF8，0xFF]。
		size, err = s.readUint(b - 0xF7)
		if err == nil && size < 56 {
			err = ErrCanonSize
		}
		return List, size, err
	}
}

// Decode对一个值进行解码，并将结果存储在val所指的值中.
func (s *Stream) Decode(val interface{}) error {
	if val == nil {
		return errDecodeIntoNil
	}
	rval := reflect.ValueOf(val)
	rtyp := rval.Type()
	if rtyp.Kind() != reflect.Ptr {
		return errNoPointer
	}
	if rval.IsNil() {
		return errDecodeIntoNil
	}
	decoder, err := cachedDecoder(rtyp.Elem())
	if err != nil {
		return err
	}

	err = decoder(s, rval.Elem())
	if decErr, ok := err.(*decodeError); ok && len(decErr.ctx) > 0 {
		// 将解码目标类型添加到错误中，以便配置具有更多含义。
		decErr.ctx = append(decErr.ctx, fmt.Sprint("(", rtyp.Elem(), ")"))
	}
	return err
}

// Reset丢弃有关当前解码上下文的任何信息，并开始从r读取。此方法旨在促进在许多解码操作中重用预分配流。
//若r也并没有实现ByterReader，则流将自己进行缓冲。
func (s *Stream) Reset(r io.Reader, inputLimit uint64) {
	if inputLimit > 0 {
		s.remaining = inputLimit
		s.limited = true
	} else {
		// Attempt to automatically discover
		// the limit when reading from a byte slice.
		switch br := r.(type) {
		case *bytes.Reader:
			s.remaining = uint64(br.Len())
			s.limited = true
		case *bytes.Buffer:
			s.remaining = uint64(br.Len())
			s.limited = true
		case *strings.Reader:
			s.remaining = uint64(br.Len())
			s.limited = true
		default:
			s.limited = false
		}
	}
	// 如果没有缓冲区，则使用缓冲区包装r。
	bufr, ok := r.(ByteReader)
	if !ok {
		bufr = bufio.NewReader(r)
	}
	s.r = bufr
	// 重置解码上下文。
	s.stack = s.stack[:0]
	s.size = 0
	s.kind = -1
	s.kinderr = nil
	s.byteval = 0
	s.uintbuf = [32]byte{}
}

func (s *Stream) readUint(size byte) (uint64, error) {
	switch size {
	case 0:
		s.kind = -1 // 重新整备 Kind
		return 0, nil
	case 1:
		b, err := s.readByte()
		return uint64(b), err
	default:
		buffer := s.uintbuf[:8]
		for i := range buffer {
			buffer[i] = 0
		}
		start := int(8 - size)
		if err := s.readFull(buffer[start:]); err != nil {
			return 0, err
		}
		if buffer[start] == 0 {
			// 注意：readUint还用于解码整数值。在这种情况下，需要将错误调整为ErrCanonInt。
			return 0, ErrCanonSize
		}
		return binary.BigEndian.Uint64(buffer[:]), nil
	}
}

// readFull从底层流读取buf。
func (s *Stream) readFull(buf []byte) (err error) {
	if err := s.willRead(uint64(len(buf))); err != nil {
		return err
	}
	var nn, n int
	for n < len(buf) && err == nil {
		nn, err = s.r.Read(buf[n:])
		n += nn
	}
	if err == io.EOF {
		if n < len(buf) {
			err = io.ErrUnexpectedEOF
		} else {
			// 即使读取成功，也允许读者给出EOF。在这种情况下，我们丢弃EOF，就像io一样。ReadFull（）可以。
			err = nil
		}
	}
	return err
}

// readByte从底层流中读取单个字节。
func (s *Stream) readByte() (byte, error) {
	if err := s.willRead(1); err != nil {
		return 0, err
	}
	b, err := s.r.ReadByte()
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return b, err
}

// 在从基础流进行任何读取之前调用willRead。它根据大小限制检查n，如果n没有溢出，则更新限制。
func (s *Stream) willRead(n uint64) error {
	s.kind = -1 // 重新整备 Kind

	if inList, limit := s.listLimit(); inList {
		if n > limit {
			return ErrElemTooLarge
		}
		s.stack[len(s.stack)-1] = limit - n
	}
	if s.limited {
		if n > s.remaining {
			return ErrValueTooLarge
		}
		s.remaining -= n
	}
	return nil
}

func (s *Stream) decodeBigInt(dst *big.Int) error {
	var buffer []byte
	kind, size, err := s.Kind()
	switch {
	case err != nil:
		return err
	case kind == List:
		return ErrExpectedString
	case kind == Byte:
		buffer = s.uintbuf[:1]
		buffer[0] = s.byteval
		s.kind = -1 // re arm类型
	case size == 0:
		// 避免零长度读取。
		s.kind = -1
	case size <= uint64(len(s.uintbuf)):
		// 对于小于s.uintbuf的整数，可以避免分配缓冲区。
		buffer = s.uintbuf[:size]
		if err := s.readFull(buffer); err != nil {
			return err
		}
		// 拒绝应使用单字节编码的输入。
		if size == 1 && buffer[0] < 128 {
			return ErrCanonSize
		}
	default:
		// 对于大整数，需要一个临时缓冲区。
		buffer = make([]byte, size)
		if err := s.readFull(buffer); err != nil {
			return err
		}
	}

	// 拒绝前导零字节.
	if len(buffer) > 0 && buffer[0] == 0 {
		return ErrCanonInt
	}
	// 设置整数字节。
	dst.SetBytes(buffer)
	return nil
}

//listLimit返回最内层列表中剩余的数据量。
func (s *Stream) listLimit() (inList bool, limit uint64) {
	if len(s.stack) == 0 {
		return false, 0
	}
	return true, s.stack[len(s.stack)-1]
}

func (s *Stream) uint(maxbits int) (uint64, error) {
	kind, size, err := s.Kind()
	if err != nil {
		return 0, err
	}
	switch kind {
	case Byte:
		if s.byteval == 0 {
			return 0, ErrCanonInt
		}
		s.kind = -1 // 重新装配 Kind
		return uint64(s.byteval), nil
	case String:
		if size > uint64(maxbits/8) {
			return 0, errUintOverflow
		}
		v, err := s.readUint(byte(size))
		switch {
		case err == ErrCanonSize:
			// 调整错误，因为我们现在没有读取大小。
			return 0, ErrCanonInt
		case err != nil:
			return 0, err
		case size > 0 && v < 128:
			return 0, ErrCanonSize
		default:
			return v, nil
		}
	default:
		return 0, ErrExpectedString
	}
}

// Bool读取最多1字节的RLP字符串，并以布尔值形式返回其内容。如果输入不包含RLP字符串，则返回的错误将是ErrExpectedString。
func (s *Stream) Bool() (bool, error) {
	num, err := s.uint(8)
	if err != nil {
		return false, err
	}
	switch num {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("rlp: invalid boolean value: %d", num)
	}
}

// 字节读取RLP字符串并将其内容作为字节片返回。如果输入不包含RLP字符串，则返回的错误将是ErrExpectedString。
func (s *Stream) Bytes() ([]byte, error) {
	kind, size, err := s.Kind()
	if err != nil {
		return nil, err
	}
	switch kind {
	case Byte:
		s.kind = -1 // 重新装配 Kind
		return []byte{s.byteval}, nil
	case String:
		b := make([]byte, size)
		if err = s.readFull(b); err != nil {
			return nil, err
		}
		if size == 1 && b[0] < 128 {
			return nil, ErrCanonSize
		}
		return b, nil
	default:
		return nil, ErrExpectedString
	}
}

// List开始解码RLP列表。如果输入不包含列表，则返回的错误将是ErrExpectedList。当到达列表末尾时，任何流操作都将返回EOL。
func (s *Stream) List() (size uint64, err error) {
	kind, size, err := s.Kind()
	if err != nil {
		return 0, err
	}
	if kind != List {
		return 0, ErrExpectedList
	}

	// 在将新大小推送到堆栈上之前，请从外部列表中删除内部列表的大小。
	//这可以确保在匹配ListEnd调用之后，剩余的外部列表大小将是正确的。
	if inList, limit := s.listLimit(); inList {
		s.stack[len(s.stack)-1] = limit - size
	}
	s.stack = append(s.stack, size)
	s.kind = -1
	s.size = 0
	return size, nil
}

// ListEnd返回到封闭列表。输入读取器必须位于列表的末尾。
func (s *Stream) ListEnd() error {
	// 确保当前列表中没有剩余数据。
	if inList, listLimit := s.listLimit(); !inList {
		return errNotInList
	} else if listLimit > 0 {
		return errNotAtEOL
	}
	s.stack = s.stack[:len(s.stack)-1] // pop
	s.kind = -1
	s.size = 0
	return nil
}
