package operationdb

//blockheader标识
var (
	headerMark     = "0101" //区块header前缀
	bodyMark       = "0102" //区块body前缀
	tdMark         = "0103" //td前缀
	receiptsMark   = "0104" //收据前缀
	headHeaderMark = "0105" //当前规范标头的哈希
	txMark         = "0106" //交易编号
	headBlockMark  = "0107" //当前规范区块的哈希
)
