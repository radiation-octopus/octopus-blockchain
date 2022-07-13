package rlp

import (
	"io"
	"math/big"
	"reflect"
	"sync"
)

type encBuffer struct {
	str     []byte     // 字符串数据，包含除列表标题以外的所有内容
	lheads  []listhead // 所有列表标题
	lhsize  int        // 所有编码列表头的大小之和
	sizebuf [9]byte    // uint编码辅助缓冲区
}

func getEncBuffer() *encBuffer {
	buf := encBufferPool.Get().(*encBuffer)
	buf.reset()
	return buf
}

func (buf *encBuffer) reset() {
	buf.lhsize = 0
	buf.str = buf.str[:0]
	buf.lheads = buf.lheads[:0]
}

// makeBytes创建编码器输出。
func (w *encBuffer) makeBytes() []byte {
	out := make([]byte, w.size())
	w.copyTo(out)
	return out
}

func (w *encBuffer) copyTo(dst []byte) {
	strpos := 0
	pos := 0
	for _, head := range w.lheads {
		//在标头之前写入字符串数据
		n := copy(dst[pos:], w.str[strpos:head.offset])
		pos += n
		strpos += n
		// 写入标题
		enc := head.encode(dst[pos:])
		pos += len(enc)
	}
	// 复制最后一个列表标题后的字符串数据
	copy(dst[pos:], w.str[strpos:])
}

//写入实现io。Writer并将b直接附加到输出。
func (buf *encBuffer) Write(b []byte) (int, error) {
	buf.str = append(buf.str, b...)
	return len(b), nil
}

//writeTo将编码器输出写入w。
func (buf *encBuffer) writeTo(w io.Writer) (err error) {
	strpos := 0
	for _, head := range buf.lheads {
		// 在header之前写入字符串数据
		if head.offset-strpos > 0 {
			n, err := w.Write(buf.str[strpos:head.offset])
			strpos += n
			if err != nil {
				return err
			}
		}
		// 写入header
		enc := head.encode(buf.sizebuf[:])
		if _, err = w.Write(enc); err != nil {
			return err
		}
	}
	if strpos < len(buf.str) {
		// 在最后一个列表标题后写入字符串数据
		_, err = w.Write(buf.str[strpos:])
	}
	return err
}

//size返回编码数据的长度。
func (buf *encBuffer) size() int {
	return len(buf.str) + buf.lhsize
}

func (buf *encBuffer) encode(val interface{}) error {
	rval := reflect.ValueOf(val)
	writer, err := cachedWriter(rval.Type())
	if err != nil {
		return err
	}
	return writer(rval, buf)
}

// writeBool将b写入为整数0（false）或1（true）。
func (buf *encBuffer) writeBool(b bool) {
	if b {
		buf.str = append(buf.str, 0x01)
	} else {
		buf.str = append(buf.str, 0x80)
	}
}

func (buf *encBuffer) writeUint64(i uint64) {
	if i == 0 {
		buf.str = append(buf.str, 0x80)
	} else if i < 128 {
		// fits single byte
		buf.str = append(buf.str, byte(i))
	} else {
		s := putint(buf.sizebuf[1:], i)
		buf.sizebuf[0] = 0x80 + byte(s)
		buf.str = append(buf.str, buf.sizebuf[:s+1]...)
	}
}

// wordBytes是一个大文件中的字节数。word
const wordBytes = (32 << (uint64(^big.Word(0)) >> 63)) / 8

//writeBigInt将i作为整数写入。
func (w *encBuffer) writeBigInt(i *big.Int) {
	bitlen := i.BitLen()
	if bitlen <= 64 {
		w.writeUint64(i.Uint64())
		return
	}
	// 整数大于64位，从i.bits（）编码。最小字节长度是位长度向上舍入到8除以8的下一个倍数。
	length := ((bitlen + 7) & -8) >> 3
	w.encodeStringHeader(length)
	w.str = append(w.str, make([]byte, length)...)
	index := length
	buf := w.str[len(w.str)-length:]
	for _, d := range i.Bits() {
		for j := 0; j < wordBytes && index > 0; j++ {
			index--
			buf[index] = byte(d)
			d >>= 8
		}
	}
}

func (buf *encBuffer) encodeStringHeader(size int) {
	if size < 56 {
		buf.str = append(buf.str, 0x80+byte(size))
	} else {
		sizesize := putint(buf.sizebuf[1:], uint64(size))
		buf.sizebuf[0] = 0xB7 + byte(sizesize)
		buf.str = append(buf.str, buf.sizebuf[:sizesize+1]...)
	}
}

func (buf *encBuffer) writeBytes(b []byte) {
	if len(b) == 1 && b[0] <= 0x7F {
		// 适合单字节，无字符串标头
		buf.str = append(buf.str, b[0])
	} else {
		buf.encodeStringHeader(len(b))
		buf.str = append(buf.str, b...)
	}
}

func (buf *encBuffer) writeString(s string) {
	buf.writeBytes([]byte(s))
}

// 列表将新的列表标题添加到标题堆栈。它返回标头的索引。
//在对列表的内容进行编码后，使用此索引调用listEnd。
func (buf *encBuffer) list() int {
	buf.lheads = append(buf.lheads, listhead{offset: len(buf.str), size: buf.lhsize})
	return len(buf.lheads) - 1
}

func (buf *encBuffer) listEnd(index int) {
	lh := &buf.lheads[index]
	lh.size = buf.size() - lh.offset - lh.size
	if lh.size < 56 {
		buf.lhsize++ //编码到种类标记中的长度
	} else {
		buf.lhsize += 1 + intsize(uint64(lh.size))
	}
}

//全局encBuffer池。
var encBufferPool = sync.Pool{
	New: func() interface{} { return new(encBuffer) },
}

// EncoderBuffer是增量编码的缓冲区。
//零值未准备好使用。要获得可用的缓冲区，请使用NewEncoderBuffer或调用Reset创建它。
type EncoderBuffer struct {
	buf *encBuffer
	dst io.Writer

	ownBuffer bool
}

// NewEncoderBuffer创建编码器缓冲区。
func NewEncoderBuffer(dst io.Writer) EncoderBuffer {
	var w EncoderBuffer
	w.Reset(dst)
	return w
}

// AppendToBytes将编码字节追加到dst。
func (w *EncoderBuffer) AppendToBytes(dst []byte) []byte {
	size := w.buf.size()
	out := append(dst, make([]byte, size)...)
	w.buf.copyTo(out[len(dst):])
	return out
}

//重置将截断缓冲区并设置输出目标。
func (w *EncoderBuffer) Reset(dst io.Writer) {
	if w.buf != nil && !w.ownBuffer {
		panic("can't Reset derived EncoderBuffer")
	}

	// 如果目标写入程序有*encBuffer，请使用它。请注意，这里w.ownBuffer为false。
	if dst != nil {
		if outer := encBufferFromWriter(dst); outer != nil {
			*w = EncoderBuffer{outer, nil, false}
			return
		}
	}

	// 获取新的缓冲区。
	if w.buf == nil {
		w.buf = encBufferPool.Get().(*encBuffer)
		w.ownBuffer = true
	}
	w.buf.reset()
	w.dst = dst
}

//Flush将编码的RLP数据写入输出写入器。这只能调用一次。如果要在刷新后重新使用缓冲区，必须调用Reset。
func (w *EncoderBuffer) Flush() error {
	var err error
	if w.dst != nil {
		err = w.buf.writeTo(w.dst)
	}
	// Release the internal buffer.
	if w.ownBuffer {
		encBufferPool.Put(w.buf)
	}
	*w = EncoderBuffer{}
	return err
}

//ToBytes返回编码的字节。
func (w *EncoderBuffer) ToBytes() []byte {
	return w.buf.makeBytes()
}

//写入将b直接附加到编码器输出。
func (w EncoderBuffer) Write(b []byte) (int, error) {
	return w.buf.Write(b)
}

func encBufferFromWriter(w io.Writer) *encBuffer {
	switch w := w.(type) {
	case EncoderBuffer:
		return w.buf
	case *EncoderBuffer:
		return w.buf
	case *encBuffer:
		return w
	default:
		return nil
	}
}

// WriteBytes将b编码为RLP字符串。
func (w EncoderBuffer) WriteBytes(b []byte) {
	w.buf.writeBytes(b)
}

// 列表启动列表。它返回一个内部索引。对内容进行编码后，使用此索引调用EndList以完成列表。
func (w EncoderBuffer) List() int {
	return w.buf.list()
}

//ListEnd完成给定的列表。
func (w EncoderBuffer) ListEnd(index int) {
	w.buf.listEnd(index)
}

// encReader是io。编码器读取器返回的读取器。它在EOF时释放其encbuf。
type encReader struct {
	buf    *encBuffer // 我们正在读取的缓冲区。当我们在EOF时，这是零。
	lhpos  int        // 我们正在读取的列表标题索引
	strpos int        // 字符串缓冲区中的当前位置
	piece  []byte     // 下一篇要阅读的文章
}

func (r *encReader) Read(b []byte) (n int, err error) {
	for {
		if r.piece = r.next(); r.piece == nil {
			// 当第一次遇到编码缓冲区时，在EOF时将其放回池中。
			//后续调用仍然返回EOF作为错误，但缓冲区不再有效。
			if r.buf != nil {
				encBufferPool.Put(r.buf)
				r.buf = nil
			}
			return n, io.EOF
		}
		nn := copy(b[n:], r.piece)
		n += nn
		if nn < len(r.piece) {
			// 这件衣服不合身，下次见。
			r.piece = r.piece[nn:]
			return n, nil
		}
		r.piece = nil
	}
}

// next返回要读取的下一段数据。它在EOF时返回零。
func (r *encReader) next() []byte {
	switch {
	case r.buf == nil:
		return nil

	case r.piece != nil:
		// 仍有数据可供读取。
		return r.piece

	case r.lhpos < len(r.buf.lheads):
		// 我们在最后一个列表标题之前。
		head := r.buf.lheads[r.lhpos]
		sizebefore := head.offset - r.strpos
		if sizebefore > 0 {
			// 标题前的字符串数据。
			p := r.buf.str[r.strpos:head.offset]
			r.strpos += sizebefore
			return p
		}
		r.lhpos++
		return head.encode(r.buf.sizebuf[:])

	case r.strpos < len(r.buf.str):
		// 在所有列表标题之后的末尾显示字符串数据。
		p := r.buf.str[r.strpos:]
		r.strpos = len(r.buf.str)
		return p

	default:
		return nil
	}
}
