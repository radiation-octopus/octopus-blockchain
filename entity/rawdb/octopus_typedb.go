package rawdb

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus-blockchain/typedb/leveldb"
)

// freezerdb是一个数据库包装器，它支持冻结器数据检索。
type freezerdb struct {
	typedb.KeyValueStore
	typedb.AncientStore
}

// 关门。Closer，关闭快速键值存储和慢速古代表。
func (frdb *freezerdb) Close() error {
	var errs []error
	if err := frdb.AncientStore.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := frdb.KeyValueStore.Close(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) != 0 {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// NewDatabaseWithFriezer在给定的键值数据存储上创建一个高级数据库，通过一个冷冻库将不变的链段移动到冷藏库中。
func NewDatabaseWithFreezer(db typedb.KeyValueStore, freezer string, namespace string, readonly bool) (typedb.Database, error) {
	//创建空闲冻结器实例
	a := db.NewBatch()
	a.ValueSize()
	fmt.Println(a)
	frdb, err := newChainFreezer(freezer, namespace, readonly, freezerTableSize, FreezerNoSnappy)
	if err != nil {
		return nil, err
	}
	// 由于冷冻柜可以与用户的键值数据库分开存储，因此用户请求冷冻柜和数据库的无效组合的可能性相当高。确保我们不会因为提供冲突的数据而自食其果，从而导致两个数据存储都被破坏。
	// -如果冷冻库和键值存储都是空的（没有genesis），那么我们只是初始化了一个新的空冷冻库，所以一切正常。
	// -如果键值存储是空的，但冷冻柜不是空的，我们需要确保用户的genesis与冷冻柜匹配。这将在区块链中进行检查，因为我们这里没有genesis区块（此时我们也不应该在意，键值/冷冻组合是有效的）。
	// -如果键值存储区和冷冻库都不为空，则交叉验证genesis哈希以确保它们兼容。如果是，还要确保冷冻柜和随后的leveldb之间没有间隙。
	// -如果键值存储不是空的，但冷冻库是空的，那么我们可能只是升级到冷冻库版本，或者我们可能有一个小的链，还没有冻结任何内容。确保键值存储中没有丢失任何块，因为这意味着我们已经有了一个旧冰箱。如果genesis哈希为空，则我们有一个新的键值存储，因此此方法中无需验证任何内容。但是，如果genesis哈希不是nil，请将其与冷冻柜内容进行比较。
	if kvgenesis, _ := db.Get(headerHashKey(0)); len(kvgenesis) > 0 {
		if frozen, _ := frdb.Ancients(); frozen > 0 {
			// 如果冷冻柜已经包含了一些东西，请确保genesis块匹配，否则我们可能会跨链混合冷冻柜，并破坏冷冻柜和键值存储。
			frgenesis, err := frdb.Ancient(freezerHashTable, 0)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve genesis from ancient %v", err)
			} else if !bytes.Equal(kvgenesis, frgenesis) {
				return nil, fmt.Errorf("genesis mismatch: %#x (leveldb) != %#x (ancients)", kvgenesis, frgenesis)
			}
			// 键值存储和冻结属于同一网络。确保它们是连续的，否则我们可能会得到一个不起作用的冷冻柜。
			if kvhash, _ := db.Get(headerHashKey(frozen)); len(kvhash) == 0 {
				// 数据库中缺少冻结限制之后的后续标头。
				//拒绝启动是指数据库具有较新的标头。
				if *ReadHeaderNumber(db, ReadHeadHeaderHash(db)) > frozen-1 {
					return nil, fmt.Errorf("gap (#%d) in the chain between ancients and leveldb", frozen)
				}
				// 数据库只包含比冻结器旧的数据，如果状态已从现有冻结器中擦除并重新初始化，则会发生这种情况。
			}
			// 否则，键值存储将在冷冻室停止的位置继续，一切正常。我们可能有重复的块（在冻结器写入之后，但在键值存储删除之前崩溃，但这很好）。
		} else {
			// 如果冷冻柜是空的，请确保尚未从键值存储中移动任何内容，否则最终会丢失数据。
			//我们检查块#1以确定之前是否冻结了任何内容，但只处理genesis块的数据库。
			if ReadHeadHeaderHash(db) != entity.BytesToHash(kvgenesis) {
				// 键值存储包含比genesis块更多的数据，请确保我们尚未冻结任何数据。
				if kvblob, _ := db.Get(headerHashKey(1)); len(kvblob) == 0 {
					return nil, errors.New("ancient chain segments already extracted, please set --datadir.ancient to the correct path")
				}
				// 块#1仍在数据库中，我们可以初始化新的feezer
			}
			// 否则，头部仍然是起源，我们可以初始化一个新的冷冻柜。
		}
	}
	// 冷冻柜与键值数据库一致，允许两者结合
	if !frdb.readonly {
		frdb.wg.Add(1)
		go func() {
			frdb.freeze(db)
			frdb.wg.Done()
		}()
	}
	return &freezerdb{
		KeyValueStore: db,
		AncientStore:  frdb,
	}, nil
}

//NewLevelDBDatabaseWithFriezer创建一个持久的键值数据库，其中一个冷冻库将不可变的链段移动到冷藏库中。
func NewLevelDBDatabaseWithFreezer(file string, cache int, handles int, freezer string, namespace string, readonly bool) (typedb.Database, error) {
	kvdb, err := leveldb.New(file, cache, handles, namespace, readonly)
	if err != nil {
		return nil, err
	}
	frdb, err := NewDatabaseWithFreezer(kvdb, freezer, namespace, readonly)
	if err != nil {
		kvdb.Close()
		return nil, err
	}
	return frdb, nil
}
