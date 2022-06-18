package rlp

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/rlp/rlpstruct"
	"reflect"
	"sync"
	"sync/atomic"
)

//typeinfo是类型缓存中的一个条目。
type typeinfo struct {
	decoder    decoder
	decoderErr error // makeDecoder出错
	writer     writer
	writerErr  error // makeWriter出错
}

func (i *typeinfo) generate(typ reflect.Type, tags rlpstruct.Tags) {
	i.decoder, i.decoderErr = makeDecoder(typ, tags)
	i.writer, i.writerErr = makeWriter(typ, tags)
}

// typekey是typeCache中类型的键。它包括struct标记，因为它们可能生成不同的解码器。
type typekey struct {
	reflect.Type
	rlpstruct.Tags
}

type decoder func(*Stream, reflect.Value) error

type writer func(reflect.Value, *encBuffer) error

func cachedWriter(typ reflect.Type) (writer, error) {
	info := theTC.info(typ)
	return info.writer, info.writerErr
}

var theTC = newTypeCache()

type typeCache struct {
	cur atomic.Value

	//此锁同步写入程序。
	mu   sync.Mutex
	next map[typekey]*typeinfo
}

func (c *typeCache) info(typ reflect.Type) *typeinfo {
	key := typekey{Type: typ}
	if info := c.cur.Load().(map[typekey]*typeinfo)[key]; info != nil {
		return info
	}

	//不在缓存中，需要生成此类型的信息。
	return c.generate(typ, rlpstruct.Tags{})
}

func (c *typeCache) generate(typ reflect.Type, tags rlpstruct.Tags) *typeinfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	cur := c.cur.Load().(map[typekey]*typeinfo)
	if info := cur[typekey{typ, tags}]; info != nil {
		return info
	}

	// 将cur复制到下一个。
	c.next = make(map[typekey]*typeinfo, len(cur)+1)
	for k, v := range cur {
		c.next[k] = v
	}

	// 生成
	info := c.infoWhileGenerating(typ, tags)

	// next -> cur
	c.cur.Store(c.next)
	c.next = nil
	return info
}

func (c *typeCache) infoWhileGenerating(typ reflect.Type, tags rlpstruct.Tags) *typeinfo {
	key := typekey{typ, tags}
	if info := c.next[key]; info != nil {
		return info
	}
	// 在生成之前，将虚拟值放入缓存。如果生成器尝试查找自身，它将获得伪值，并且不会递归调用自身。
	info := new(typeinfo)
	c.next[key] = info
	info.generate(typ, tags)
	return info
}

type field struct {
	index    int
	info     *typeinfo
	optional bool
}

// firstOptionalField返回带有“optional”标记的第一个字段的索引。
func firstOptionalField(fields []field) int {
	for i, f := range fields {
		if f.optional {
			return i
		}
	}
	return len(fields)
}

type structFieldError struct {
	typ   reflect.Type
	field int
	err   error
}

func (e structFieldError) Error() string {
	return fmt.Sprintf("%v (struct field %v.%s)", e.err, e.typ, e.typ.Field(e.field).Name)
}

//structFields解析结构类型中所有公共字段的typeinfo。
func structFields(typ reflect.Type) (fields []field, err error) {
	// 将字段转换为rlpstruct。领域
	var allStructFields []rlpstruct.Field
	for i := 0; i < typ.NumField(); i++ {
		rf := typ.Field(i)
		allStructFields = append(allStructFields, rlpstruct.Field{
			Name:     rf.Name,
			Index:    i,
			Exported: rf.PkgPath == "",
			Tag:      string(rf.Tag),
			Type:     *rtypeToStructType(rf.Type, nil),
		})
	}

	// 筛选/验证字段。
	structFields, structTags, err := rlpstruct.ProcessFields(allStructFields)
	if err != nil {
		if tagErr, ok := err.(rlpstruct.TagError); ok {
			tagErr.StructType = typ.String()
			return nil, tagErr
		}
		return nil, err
	}

	// Resolve typeinfo.
	for i, sf := range structFields {
		typ := typ.Field(sf.Index).Type
		tags := structTags[i]
		info := theTC.infoWhileGenerating(typ, tags)
		fields = append(fields, field{sf.Index, info, tags.Optional})
	}
	return fields, nil
}

func newTypeCache() *typeCache {
	c := new(typeCache)
	c.cur.Store(make(map[typekey]*typeinfo))
	return c
}

//typeNilKind为指向“typ”的nil指针提供RLP值种类。
func typeNilKind(typ reflect.Type, tags rlpstruct.Tags) Kind {
	styp := rtypeToStructType(typ, nil)

	var nk rlpstruct.NilKind
	if tags.NilOK {
		nk = tags.NilKind
	} else {
		nk = styp.DefaultNilValue()
	}
	switch nk {
	case rlpstruct.NilKindString:
		return String
	case rlpstruct.NilKindList:
		return List
	default:
		panic("invalid nil kind value")
	}
}

//rtypeToStructType将类型转换为rlpstruct。类型
func rtypeToStructType(typ reflect.Type, rec map[reflect.Type]*rlpstruct.Type) *rlpstruct.Type {
	k := typ.Kind()
	if k == reflect.Invalid {
		panic("invalid kind")
	}

	if prev := rec[typ]; prev != nil {
		return prev // short-circuit for recursive types
	}
	if rec == nil {
		rec = make(map[reflect.Type]*rlpstruct.Type)
	}

	t := &rlpstruct.Type{
		Name:      typ.String(),
		Kind:      k,
		IsEncoder: typ.Implements(encoderInterface),
		IsDecoder: typ.Implements(decoderInterface),
	}
	rec[typ] = t
	if k == reflect.Array || k == reflect.Slice || k == reflect.Ptr {
		t.Elem = rtypeToStructType(typ.Elem(), rec)
	}
	return t
}

func isUint(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uintptr
}

func isByte(typ reflect.Type) bool {
	return typ.Kind() == reflect.Uint8 && !typ.Implements(encoderInterface)
}
