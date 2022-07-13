package node

// 生命周期包括可以在节点上启动和停止的服务的行为。
//生命周期管理委托给节点，但特定于服务的包负责使用“RegisterLifecycle”方法在节点上配置和注册服务。
type Lifecycle interface {
	// 在构建所有服务之后调用Start，并且网络层也被初始化以生成服务所需的任何goroutine。
	Start() error

	// Stop终止属于服务的所有goroutine，阻塞直到它们全部终止。
	Stop() error
}
