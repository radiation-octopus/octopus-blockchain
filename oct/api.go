package oct

// OctopusAPI provides an API to access Octopus full node-related information.
type OctopusAPI struct {
	o *Octopus
}

// NewOctopusAPI creates a new Octopus protocol API for full nodes.
func NewOctopusAPI(o *Octopus) *OctopusAPI {
	return &OctopusAPI{o}
}

//// Etherbase is the address that mining rewards will be send to.
//func (api *OctopusAPI) Etherbase() (entity.Address, error) {
//	return api.o.Etherbase()
//}
//
//// Coinbase is the address that mining rewards will be send to (alias for Etherbase).
//func (api *OctopusAPI) Coinbase() (entity.Address, error) {
//	return api.Etherbase()
//}
//
//// Hashrate returns the POW hashrate.
////func (api *OctopusAPI) Hashrate() hexutil.Uint64 {
////	return hexutil.Uint64(api.o.Miner().Hashrate())
////}
//
//// Mining returns an indication if this node is currently mining.
//func (api *OctopusAPI) Mining() bool {
//	return api.o.IsMining()
//}
//
// MinerAPI provides an API to control the miner.
type MinerAPI struct {
	o *Octopus
}

//
// NewMinerAPI create a new MinerAPI instance.
func NewMinerAPI(o *Octopus) *MinerAPI {
	return &MinerAPI{o}
}

//
//// Start starts the miner with the given number of threads. If threads is nil,
//// the number of workers started is equal to the number of logical CPUs that are
//// usable by this process. If mining is already running, this method adjust the
//// number of threads allowed to use and updates the minimum price required by the
//// transaction pool.
//func (api *MinerAPI) Start(threads *int) error {
//	if threads == nil {
//		return api.o.StartMining(runtime.NumCPU())
//	}
//	return api.o.StartMining(*threads)
//}
//
//// Stop terminates the miner, both at the consensus engine level as well as at
//// the block creation level.
//func (api *MinerAPI) Stop() {
//	api.o.StopMining()
//}
//
//// SetExtra sets the extra data string that is included when this miner mines a block.
//func (api *MinerAPI) SetExtra(extra string) (bool, error) {
//	if err := api.o.Miner().SetExtra([]byte(extra)); err != nil {
//		return false, err
//	}
//	return true, nil
//}
//
//// SetGasPrice sets the minimum accepted gas price for the miner.
//func (api *MinerAPI) SetGasPrice(gasPrice hexutil.Big) bool {
//	api.o.lock.Lock()
//	api.o.gasPrice = (*big.Int)(&gasPrice)
//	api.o.lock.Unlock()
//
//	api.o.txPool.SetGasPrice((*big.Int)(&gasPrice))
//	return true
//}
//
//// SetGasLimit sets the gaslimit to target towards during mining.
//func (api *MinerAPI) SetGasLimit(gasLimit hexutil.Uint64) bool {
//	api.o.Miner().SetGasCeil(uint64(gasLimit))
//	return true
//}
//
//// SetEtherbase sets the etherbase of the miner.
//func (api *MinerAPI) SetEtherbase(etherbase entity.Address) bool {
//	api.o.SetEtherbase(etherbase)
//	return true
//}
//
//// SetRecommitInterval updates the interval for miner sealing work recommitting.
//func (api *MinerAPI) SetRecommitInterval(interval int) {
//	api.o.Miner().SetRecommitInterval(time.Duration(interval) * time.Millisecond)
//}
//
// AdminAPI is the collection of Octopus full node related APIs for node
// administration.
type AdminAPI struct {
	eth *Octopus
}

// NewAdminAPI creates a new instance of AdminAPI.
func NewAdminAPI(oct *Octopus) *AdminAPI {
	return &AdminAPI{eth: oct}
}

//
//// ExportChain exports the current blockchain into a local file,
//// or a range of blocks if first and last are non-nil.
//func (api *AdminAPI) ExportChain(file string, first *uint64, last *uint64) (bool, error) {
//	if first == nil && last != nil {
//		return false, errors.New("last cannot be specified without first")
//	}
//	if first != nil && last == nil {
//		head := api.eth.BlockChain().CurrentHeader().Number.Uint64()
//		last = &head
//	}
//	if _, err := os.Stat(file); err == nil {
//		// File already exists. Allowing overwrite could be a DoS vector,
//		// since the 'file' may point to arbitrary paths on the drive
//		return false, errors.New("location would overwrite an existing file")
//	}
//	// Make sure we can create the file to export into
//	out, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.ModePerm)
//	if err != nil {
//		return false, err
//	}
//	defer out.Close()
//
//	var writer io.Writer = out
//	if strings.HasSuffix(file, ".gz") {
//		writer = gzip.NewWriter(writer)
//		defer writer.(*gzip.Writer).Close()
//	}
//
//	// Export the blockchain
//	if first != nil {
//		if err := api.eth.BlockChain().ExportN(writer, *first, *last); err != nil {
//			return false, err
//		}
//	} else if err := api.eth.BlockChain().Export(writer); err != nil {
//		return false, err
//	}
//	return true, nil
//}
//
//func hasAllBlocks(chain *blockchain.BlockChain, bs []*block.Block) bool {
//	for _, b := range bs {
//		if !chain.HasBlock(b.Hash(), b.NumberU64()) {
//			return false
//		}
//	}
//
//	return true
//}
//
//// ImportChain imports a blockchain from a local file.
//func (api *AdminAPI) ImportChain(file string) (bool, error) {
//	// Make sure the can access the file to import
//	in, err := os.Open(file)
//	if err != nil {
//		return false, err
//	}
//	defer in.Close()
//
//	var reader io.Reader = in
//	if strings.HasSuffix(file, ".gz") {
//		if reader, err = gzip.NewReader(reader); err != nil {
//			return false, err
//		}
//	}
//
//	// Run actual the import in pre-configured batches
//	stream := rlp.NewStream(reader, 0)
//
//	blocks, index := make([]*block.Block, 0, 2500), 0
//	for batch := 0; ; batch++ {
//		// Load a batch of blocks from the input file
//		for len(blocks) < cap(blocks) {
//			block := new(block.Block)
//			if err := stream.Decode(block); err == io.EOF {
//				break
//			} else if err != nil {
//				return false, fmt.Errorf("block %d: failed to parse: %v", index, err)
//			}
//			blocks = append(blocks, block)
//			index++
//		}
//		if len(blocks) == 0 {
//			break
//		}
//
//		if hasAllBlocks(api.eth.BlockChain(), blocks) {
//			blocks = blocks[:0]
//			continue
//		}
//		// Import the batch and reset the buffer
//		if _, err := api.eth.BlockChain().InsertChain(blocks); err != nil {
//			return false, fmt.Errorf("batch %d: failed to insert: %v", batch, err)
//		}
//		blocks = blocks[:0]
//	}
//	return true, nil
//}
//
// DebugAPI is the collection of Octopus full node APIs for debugging the
// protocol.
type DebugAPI struct {
	eth *Octopus
}

// NewDebugAPI creates a new DebugAPI instance.
func NewDebugAPI(eth *Octopus) *DebugAPI {
	return &DebugAPI{eth: eth}
}

//
//// DumpBlock retrieves the entire state of the database at a given block.
//func (api *DebugAPI) DumpBlock(blockNr rpc.BlockNumber) (state.Dump, error) {
//	opts := &state.DumpConfig{
//		OnlyWithAddresses: true,
//		Max:               AccountRangeMaxResults, // Sanity limit over RPC
//	}
//	if blockNr == rpc.PendingBlockNumber {
//		// If we're dumping the pending state, we need to request
//		// both the pending block as well as the pending state from
//		// the miner and operate on those
//		_, stateDb := api.eth.miner.Pending()
//		return stateDb.RawDump(opts), nil
//	}
//	var block *block.Block
//	if blockNr == rpc.LatestBlockNumber {
//		block = api.eth.blockchain.CurrentBlock()
//	} else if blockNr == rpc.FinalizedBlockNumber {
//		block = api.eth.blockchain.CurrentFinalizedBlock()
//	} else {
//		block = api.eth.blockchain.GetBlockByNumber(uint64(blockNr))
//	}
//	if block == nil {
//		return state.Dump{}, fmt.Errorf("block #%d not found", blockNr)
//	}
//	stateDb, err := api.eth.BlockChain().StateAt(block.Root())
//	if err != nil {
//		return state.Dump{}, err
//	}
//	return stateDb.RawDump(opts), nil
//}
//
//// Preimage is a debug API function that returns the preimage for a sha3 hash, if known.
//func (api *DebugAPI) Preimage(ctx context.Context, hash entity.Hash) (hexutil.Bytes, error) {
//	if preimage := rawdb.ReadPreimage(api.eth.ChainDb(), hash); preimage != nil {
//		return preimage, nil
//	}
//	return nil, errors.New("unknown preimage")
//}
//
//// BadBlockArgs represents the entries in the list returned when bad blocks are queried.
//type BadBlockArgs struct {
//	Hash  entity.Hash            `json:"hash"`
//	Block map[string]interface{} `json:"block"`
//	RLP   string                 `json:"rlp"`
//}
//
//// GetBadBlocks returns a list of the last 'bad blocks' that the client has seen on the network
//// and returns them as a JSON list of block hashes.
//func (api *DebugAPI) GetBadBlocks(ctx context.Context) ([]*BadBlockArgs, error) {
//	var (
//		err     error
//		blocks  = rawdb.ReadAllBadBlocks(api.eth.chainDb)
//		results = make([]*BadBlockArgs, 0, len(blocks))
//	)
//	for _, block := range blocks {
//		var (
//			blockRlp  string
//			blockJSON map[string]interface{}
//		)
//		if rlpBytes, err := rlp.EncodeToBytes(block); err != nil {
//			blockRlp = err.Error() // Hacky, but hey, it works
//		} else {
//			blockRlp = fmt.Sprintf("%#x", rlpBytes)
//		}
//		if blockJSON, err = ethapi.RPCMarshalBlock(block, true, true, api.eth.APIBackend.ChainConfig()); err != nil {
//			blockJSON = map[string]interface{}{"error": err.Error()}
//		}
//		results = append(results, &BadBlockArgs{
//			Hash:  block.Hash(),
//			RLP:   blockRlp,
//			Block: blockJSON,
//		})
//	}
//	return results, nil
//}
//
//// AccountRangeMaxResults is the maximum number of results to be returned per call
//const AccountRangeMaxResults = 256
//
//// AccountRange enumerates all accounts in the given block and start point in paging request
//func (api *DebugAPI) AccountRange(blockNrOrHash rpc.BlockNumberOrHash, start hexutil.Bytes, maxResults int, nocode, nostorage, incompletes bool) (state.IteratorDump, error) {
//	var stateDb *state.StateDB
//	var err error
//
//	if number, ok := blockNrOrHash.Number(); ok {
//		if number == rpc.PendingBlockNumber {
//			// If we're dumping the pending state, we need to request
//			// both the pending block as well as the pending state from
//			// the miner and operate on those
//			_, stateDb = api.eth.miner.Pending()
//		} else {
//			var block *block.Block
//			if number == rpc.LatestBlockNumber {
//				block = api.eth.blockchain.CurrentBlock()
//			} else if number == rpc.FinalizedBlockNumber {
//				block = api.eth.blockchain.CurrentFinalizedBlock()
//			} else {
//				block = api.eth.blockchain.GetBlockByNumber(uint64(number))
//			}
//			if block == nil {
//				return state.IteratorDump{}, fmt.Errorf("block #%d not found", number)
//			}
//			stateDb, err = api.eth.BlockChain().StateAt(block.Root())
//			if err != nil {
//				return state.IteratorDump{}, err
//			}
//		}
//	} else if hash, ok := blockNrOrHash.Hash(); ok {
//		block := api.eth.blockchain.GetBlockByHash(hash)
//		if block == nil {
//			return state.IteratorDump{}, fmt.Errorf("block %s not found", hash.Hex())
//		}
//		stateDb, err = api.eth.BlockChain().StateAt(block.Root())
//		if err != nil {
//			return state.IteratorDump{}, err
//		}
//	} else {
//		return state.IteratorDump{}, errors.New("either block number or block hash must be specified")
//	}
//
//	opts := &state.DumpConfig{
//		SkipCode:          nocode,
//		SkipStorage:       nostorage,
//		OnlyWithAddresses: !incompletes,
//		Start:             start,
//		Max:               uint64(maxResults),
//	}
//	if maxResults > AccountRangeMaxResults || maxResults <= 0 {
//		opts.Max = AccountRangeMaxResults
//	}
//	return stateDb.IteratorDump(opts), nil
//}
//
//// StorageRangeResult is the result of a debug_storageRangeAt API call.
//type StorageRangeResult struct {
//	Storage storageMap   `json:"storage"`
//	NextKey *entity.Hash `json:"nextKey"` // nil if Storage includes the last key in the trie.
//}
//
//type storageMap map[entity.Hash]storageEntry
//
//type storageEntry struct {
//	Key   *entity.Hash `json:"key"`
//	Value entity.Hash  `json:"value"`
//}
//
//// StorageRangeAt returns the storage at the given block height and transaction index.
//func (api *DebugAPI) StorageRangeAt(blockHash entity.Hash, txIndex int, contractAddress entity.Address, keyStart hexutil.Bytes, maxResult int) (StorageRangeResult, error) {
//	// Retrieve the block
//	block := api.eth.blockchain.GetBlockByHash(blockHash)
//	if block == nil {
//		return StorageRangeResult{}, fmt.Errorf("block %#x not found", blockHash)
//	}
//	_, _, statedb, err := api.eth.stateAtTransaction(block, txIndex, 0)
//	if err != nil {
//		return StorageRangeResult{}, err
//	}
//	st := statedb.StorageTrie(contractAddress)
//	if st == nil {
//		return StorageRangeResult{}, fmt.Errorf("account %x doesn't exist", contractAddress)
//	}
//	return storageRangeAt(st, keyStart, maxResult)
//}
//
//func storageRangeAt(st state.Trie, start []byte, maxResult int) (StorageRangeResult, error) {
//	it := trie.NewIterator(st.NodeIterator(start))
//	result := StorageRangeResult{Storage: storageMap{}}
//	for i := 0; i < maxResult && it.Next(); i++ {
//		_, content, _, err := rlp.Split(it.Value)
//		if err != nil {
//			return StorageRangeResult{}, err
//		}
//		e := storageEntry{Value: entity.BytesToHash(content)}
//		if preimage := st.GetKey(it.Key); preimage != nil {
//			preimage := entity.BytesToHash(preimage)
//			e.Key = &preimage
//		}
//		result.Storage[entity.BytesToHash(it.Key)] = e
//	}
//	// Add the 'next key' so clients can continue downloading.
//	if it.Next() {
//		next := entity.BytesToHash(it.Key)
//		result.NextKey = &next
//	}
//	return result, nil
//}
//
//// GetModifiedAccountsByNumber returns all accounts that have changed between the
//// two blocks specified. A change is defined as a difference in nonce, balance,
//// code hash, or storage hash.
////
//// With one parameter, returns the list of accounts modified in the specified block.
//func (api *DebugAPI) GetModifiedAccountsByNumber(startNum uint64, endNum *uint64) ([]entity.Address, error) {
//	var startBlock, endBlock *block.Block
//
//	startBlock = api.eth.blockchain.GetBlockByNumber(startNum)
//	if startBlock == nil {
//		return nil, fmt.Errorf("start block %x not found", startNum)
//	}
//
//	if endNum == nil {
//		endBlock = startBlock
//		startBlock = api.eth.blockchain.GetBlockByHash(startBlock.ParentHash())
//		if startBlock == nil {
//			return nil, fmt.Errorf("block %x has no parent", endBlock.Number())
//		}
//	} else {
//		endBlock = api.eth.blockchain.GetBlockByNumber(*endNum)
//		if endBlock == nil {
//			return nil, fmt.Errorf("end block %d not found", *endNum)
//		}
//	}
//	return api.getModifiedAccounts(startBlock, endBlock)
//}
//
//// GetModifiedAccountsByHash returns all accounts that have changed between the
//// two blocks specified. A change is defined as a difference in nonce, balance,
//// code hash, or storage hash.
////
//// With one parameter, returns the list of accounts modified in the specified block.
//func (api *DebugAPI) GetModifiedAccountsByHash(startHash entity.Hash, endHash *entity.Hash) ([]entity.Address, error) {
//	var startBlock, endBlock *block.Block
//	startBlock = api.eth.blockchain.GetBlockByHash(startHash)
//	if startBlock == nil {
//		return nil, fmt.Errorf("start block %x not found", startHash)
//	}
//
//	if endHash == nil {
//		endBlock = startBlock
//		startBlock = api.eth.blockchain.GetBlockByHash(startBlock.ParentHash())
//		if startBlock == nil {
//			return nil, fmt.Errorf("block %x has no parent", endBlock.Number())
//		}
//	} else {
//		endBlock = api.eth.blockchain.GetBlockByHash(*endHash)
//		if endBlock == nil {
//			return nil, fmt.Errorf("end block %x not found", *endHash)
//		}
//	}
//	return api.getModifiedAccounts(startBlock, endBlock)
//}
//
//func (api *DebugAPI) getModifiedAccounts(startBlock, endBlock *block.Block) ([]entity.Address, error) {
//	if startBlock.Number().Uint64() >= endBlock.Number().Uint64() {
//		return nil, fmt.Errorf("start block height (%d) must be less than end block height (%d)", startBlock.Number().Uint64(), endBlock.Number().Uint64())
//	}
//	triedb := api.eth.BlockChain().StateCache().TrieDB()
//
//	oldTrie, err := trie.NewSecure(entity.Hash{}, startBlock.Root(), triedb)
//	if err != nil {
//		return nil, err
//	}
//	newTrie, err := trie.NewSecure(entity.Hash{}, endBlock.Root(), triedb)
//	if err != nil {
//		return nil, err
//	}
//	diff, _ := trie.NewDifferenceIterator(oldTrie.NodeIterator([]byte{}), newTrie.NodeIterator([]byte{}))
//	iter := trie.NewIterator(diff)
//
//	var dirty []entity.Address
//	for iter.Next() {
//		key := newTrie.GetKey(iter.Key)
//		if key == nil {
//			return nil, fmt.Errorf("no preimage found for hash %x", iter.Key)
//		}
//		dirty = append(dirty, entity.BytesToAddress(key))
//	}
//	return dirty, nil
//}
//
//// GetAccessibleState returns the first number where the node has accessible
//// state on disk. Note this being the post-state of that block and the pre-state
//// of the next block.
//// The (from, to) parameters are the sequence of blocks to search, which can go
//// either forwards or backwards
//func (api *DebugAPI) GetAccessibleState(from, to rpc.BlockNumber) (uint64, error) {
//	db := api.eth.ChainDb()
//	var pivot uint64
//	if p := rawdb.ReadLastPivotNumber(db); p != nil {
//		pivot = *p
//		log.Info("Found fast-sync pivot marker", "number", pivot)
//	}
//	var resolveNum = func(num rpc.BlockNumber) (uint64, error) {
//		// We don't have state for pending (-2), so treat it as latest
//		if num.Int64() < 0 {
//			block := api.eth.blockchain.CurrentBlock()
//			if block == nil {
//				return 0, fmt.Errorf("current block missing")
//			}
//			return block.NumberU64(), nil
//		}
//		return uint64(num.Int64()), nil
//	}
//	var (
//		start   uint64
//		end     uint64
//		delta   = int64(1)
//		lastLog time.Time
//		err     error
//	)
//	if start, err = resolveNum(from); err != nil {
//		return 0, err
//	}
//	if end, err = resolveNum(to); err != nil {
//		return 0, err
//	}
//	if start == end {
//		return 0, fmt.Errorf("from and to needs to be different")
//	}
//	if start > end {
//		delta = -1
//	}
//	for i := int64(start); i != int64(end); i += delta {
//		if time.Since(lastLog) > 8*time.Second {
//			log.Info("Finding roots", "from", start, "to", end, "at", i)
//			lastLog = time.Now()
//		}
//		if i < int64(pivot) {
//			continue
//		}
//		h := api.eth.BlockChain().GetHeaderByNumber(uint64(i))
//		if h == nil {
//			return 0, fmt.Errorf("missing header %d", i)
//		}
//		if ok, _ := api.eth.ChainDb().Has(h.Root[:]); ok {
//			return uint64(i), nil
//		}
//	}
//	return 0, fmt.Errorf("No state found")
//}
