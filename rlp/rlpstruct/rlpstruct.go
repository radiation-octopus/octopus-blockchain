package rlpstruct

import (
	"fmt"
	"reflect"
	"strings"
)

//标记表示结构标记。
type Tags struct {
	// rlp：“nil”控制空输入是否会导致nil指针。nilKind是字段允许的空值类型。
	NilKind NilKind
	NilOK   bool

	// rlp：“可选”允许输入列表中缺少字段。如果设置了此选项，则所有后续字段也必须是可选的。
	Optional bool

	// rlp：“tail”控制此字段是否包含其他列表元素。只能为最后一个字段设置，该字段必须为切片类型。
	Tail bool

	// rlp：“-”忽略字段。
	Ignored bool
}

// 针对无效的结构标记引发TagError。
type TagError struct {
	StructType string

	// 这些由此包设置。
	Field string
	Tag   string
	Err   string
}

func (e TagError) Error() string {
	field := "field " + e.Field
	if e.StructType != "" {
		field = e.StructType + "." + e.Field
	}
	return fmt.Sprintf("rlp: invalid struct tag %q for %s (%s)", e.Tag, field, e.Err)
}

//NilKind是代替nil指针编码的RLP值。
type NilKind uint8

const (
	NilKindString NilKind = 0x80
	NilKindList   NilKind = 0xC0
)

// 类型表示Go类型的属性。
type Type struct {
	Name      string
	Kind      reflect.Kind
	IsEncoder bool  // 类型是否实现rlp。编码器
	IsDecoder bool  // 类型是否实现rlp。解码器
	Elem      *Type // Ptr、Slice、Array的类值为非nil
}

// defaultNilValue确定指向t的nil指针是编码/解码为空字符串还是空列表。
func (t Type) DefaultNilValue() NilKind {
	k := t.Kind
	if isUint(k) || k == reflect.String || k == reflect.Bool || isByteArray(t) {
		return NilKindString
	}
	return NilKindList
}

//字段表示结构字段。
type Field struct {
	Name     string
	Index    int
	Exported bool
	Type     Type
	Tag      string
}

//ProcessFields过滤给定的结构字段，只返回编码/解码时应考虑的字段。
func ProcessFields(allFields []Field) ([]Field, []Tags, error) {
	lastPublic := lastPublicField(allFields)

	// 收集所有导出字段及其标记。
	var fields []Field
	var tags []Tags
	for _, field := range allFields {
		if !field.Exported {
			continue
		}
		ts, err := parseTag(field, lastPublic)
		if err != nil {
			return nil, nil, err
		}
		if ts.Ignored {
			continue
		}
		fields = append(fields, field)
		tags = append(tags, ts)
	}

	// 验证可选字段的一致性。如果存在任何可选字段，则其后面的所有字段也必须是可选的。注意：支持可选+尾部。
	var anyOptional bool
	var firstOptionalName string
	for i, ts := range tags {
		name := fields[i].Name
		if ts.Optional || ts.Tail {
			if !anyOptional {
				firstOptionalName = name
			}
			anyOptional = true
		} else {
			if anyOptional {
				msg := fmt.Sprintf("must be optional because preceding field %q is optional", firstOptionalName)
				return nil, nil, TagError{Field: name, Err: msg}
			}
		}
	}
	return fields, tags, nil
}

func parseTag(field Field, lastPublic int) (Tags, error) {
	name := field.Name
	tag := reflect.StructTag(field.Tag)
	var ts Tags
	for _, t := range strings.Split(tag.Get("rlp"), ",") {
		switch t = strings.TrimSpace(t); t {
		case "":
			// empty tag is allowed for some reason
		case "-":
			ts.Ignored = true
		case "nil", "nilString", "nilList":
			ts.NilOK = true
			if field.Type.Kind != reflect.Ptr {
				return ts, TagError{Field: name, Tag: t, Err: "field is not a pointer"}
			}
			switch t {
			case "nil":
				ts.NilKind = field.Type.Elem.DefaultNilValue()
			case "nilString":
				ts.NilKind = NilKindString
			case "nilList":
				ts.NilKind = NilKindList
			}
		case "optional":
			ts.Optional = true
			if ts.Tail {
				return ts, TagError{Field: name, Tag: t, Err: `also has "tail" tag`}
			}
		case "tail":
			ts.Tail = true
			if field.Index != lastPublic {
				return ts, TagError{Field: name, Tag: t, Err: "must be on last field"}
			}
			if ts.Optional {
				return ts, TagError{Field: name, Tag: t, Err: `also has "optional" tag`}
			}
			if field.Type.Kind != reflect.Slice {
				return ts, TagError{Field: name, Tag: t, Err: "field type is not slice"}
			}
		default:
			return ts, TagError{Field: name, Tag: t, Err: "unknown tag"}
		}
	}
	return ts, nil
}

func lastPublicField(fields []Field) int {
	last := 0
	for _, f := range fields {
		if f.Exported {
			last = f.Index
		}
	}
	return last
}

func isUint(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uintptr
}

func isByte(typ Type) bool {
	return typ.Kind == reflect.Uint8 && !typ.IsEncoder
}

func isByteArray(typ Type) bool {
	return (typ.Kind == reflect.Slice || typ.Kind == reflect.Array) && isByte(*typ.Elem)
}
