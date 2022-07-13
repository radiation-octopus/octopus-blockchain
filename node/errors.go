package node

import (
	"errors"
	"fmt"
	"reflect"
	"syscall"
)

var (
	ErrDatadirUsed    = errors.New("datadir already used by another process")
	ErrNodeStopped    = errors.New("node not started")
	ErrNodeRunning    = errors.New("node already running")
	ErrServiceUnknown = errors.New("unknown service")

	datadirInUseErrnos = map[uint]bool{11: true, 32: true, 35: true}
)

// 如果节点无法停止其任何已注册服务或自身，则返回StopError。
type StopError struct {
	Server   error
	Services map[reflect.Type]error
}

// Error生成停止错误的文本表示。
func (e *StopError) Error() string {
	return fmt.Sprintf("server: %v, services: %v", e.Server, e.Services)
}

func convertFileLockError(err error) error {
	if errno, ok := err.(syscall.Errno); ok && datadirInUseErrnos[uint(errno)] {
		return ErrDatadirUsed
	}
	return err
}
