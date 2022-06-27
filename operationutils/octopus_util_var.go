package operationutils

import (
	"reflect"
)

//interface转换为字符串数组
func ArrayByInter(inter interface{}) []string {
	s := reflect.ValueOf(inter)
	le := make([]string, s.Len())
	for i := 0; i < s.Len(); i++ {
		le = append(le, s.Index(i).String())
	}
	return le
}
