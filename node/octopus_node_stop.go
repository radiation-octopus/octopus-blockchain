package node

type NodeStop struct {
}

func (ns *NodeStop) Stop() {
	Stop()
}
