package blockchain

//区块链启动配置cfg结构体
type BlockChainStart struct {
	//BindingTxLookupLimit		uint64    	`autoInjectCfg:"octopus.blockchain.binding.txLookupLimit"`
	//BindingMethod 			string    	`autoInjectCfg:"octopus.blockchain.binding.method"`
	//BindingStruct  			string 		`autoInjectCfg:"octopus.blockchain.binding.struct"`
}

func (bc *BlockChainStart) Start() {
	//TxLookupLimit = bc.BindingTxLookupLimit
	//BindingMethod = bc.BindingMethod
	//BindingStruct = bc.BindingStruct
	Start()
}
