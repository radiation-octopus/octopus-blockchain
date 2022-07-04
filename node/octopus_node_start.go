package node

type NodeStart struct {
	Node *Node `autoInjectLang:"node.Node"`
}

func (ns *NodeStart) Start() {
	ns.Node.start()
}
