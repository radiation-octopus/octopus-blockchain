package entity

import "time"

// PrettyDuration是一个时间的打印版本。持续时间值，用于从格式化的文本表示中去除不必要的精度。
type PrettyDuration time.Duration
