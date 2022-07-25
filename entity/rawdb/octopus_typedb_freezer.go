package rawdb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/golang/snappy"
	"github.com/prometheus/tsdb/fileutil"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/metrics"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/utils"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var (
	//如果冷冻柜以只读模式打开，则返回ERREADONLY。所有的突变都是不允许的。
	errReadOnly = errors.New("read only")

	// 如果用户试图从冷冻柜未跟踪的表中读取，则返回errUnknownTable。
	errUnknownTable = errors.New("unknown table")

	// 如果用户试图向冷冻柜中注入无序的二进制Blob，则返回errOutOrderInsertion。
	errOutOrderInsertion = errors.New("the append operation is out-order")

	// 如果用户指定的古代目录是符号链接，则返回errSymlinkDatadir。
	errSymlinkDatadir = errors.New("symbolic link datadir is not supported")

	// 如果操作在冷冻柜表关闭后尝试读取或写入冷冻柜表，则返回errClosed。
	errClosed = errors.New("closed")

	// 如果请求的项目不包含在冷冻柜表中，则返回errOutOfBounds。
	errOutOfBounds = errors.New("out of bounds")
)

const (
	// FreezerCheckInterval是检查键值数据库中是否存在可能允许将新块冻结到不可变存储中的链进程的频率。
	freezerRecheckInterval = time.Minute

	// freezerBatchLimit是在执行fsync并将其从键值存储中删除之前，一批中要冻结的最大块数。
	freezerBatchLimit = 30000

	// freezerTableSize定义冻结器数据文件的最大大小。
	freezerTableSize = 2 * 1000 * 1000 * 1000

	// 这是单个冷冻台批次内存中缓冲的最大数据量。
	freezerBatchBufferLimit = 2 * 1024 * 1024
)

//冷冻库是一个内存映射的仅附加数据库，用于将不可变的有序数据存储到平面文件中：-仅附加的特性确保磁盘写入最小化。
//-内存映射确保我们可以最大限度地利用系统内存进行缓存，而无需为go ethereum保留内存。这还将减少Geth的内存需求，从而减少GC开销。
type Freezer struct {
	// 警告：“冻结”和“尾部”字段是按原子方式访问的。在32位平台上，只有64位对齐的字段可以是原子字段。结构保证如此对齐
	frozen uint64 // 已冻结的块数
	tail   uint64 // 冷冻柜中第一个存储项目的编号

	datadir string // 古商店根目录路径

	// 此锁同步写入程序和截断操作，以及“原子”（批处理）读取操作。
	writeLock  sync.RWMutex
	writeBatch *freezerBatch

	readonly     bool
	tables       map[string]*freezerTable // 用于存储所有内容的数据表
	instanceLock fileutil.Releaser        // 防止双重打开的文件系统锁定
	closeOnce    sync.Once
}

// NewFreezer创建一个冻结器实例，用于根据给定的参数维护不可变的有序数据。
//“tables”参数定义数据表。如果映射项的值为true，则会对表禁用snappy压缩。
func NewFreezer(datadir string, namespace string, readonly bool, maxTableSize uint32, tables map[string]bool) (*Freezer, error) {
	// 创建初始冻结器对象
	var (
		readMeter  = metrics.NewRegisteredMeter(namespace+"ancient/read", nil)
		writeMeter = metrics.NewRegisteredMeter(namespace+"ancient/write", nil)
		sizeGauge  = metrics.NewRegisteredGauge(namespace+"ancient/size", nil)
	)
	// 确保datadir不是符号链接（如果存在）。
	if info, err := os.Lstat(datadir); !os.IsNotExist(err) {
		if info.Mode()&os.ModeSymlink != 0 {
			log.Warn("Symbolic link ancient database is not supported", "path", datadir)
			return nil, errSymlinkDatadir
		}
	}
	// Leveldb使用LOCK作为filelock文件名。为了防止名称冲突，我们使用FLOCK作为锁名称。
	lock, _, err := fileutil.Flock(filepath.Join(datadir, "FLOCK"))
	if err != nil {
		return nil, err
	}
	// 打开所有支持的数据表
	freezer := &Freezer{
		readonly:     readonly,
		tables:       make(map[string]*freezerTable),
		instanceLock: lock,
		datadir:      datadir,
	}

	// 创建表。
	for name, disableSnappy := range tables {
		table, err := newTable(datadir, name, readMeter, writeMeter, sizeGauge, maxTableSize, disableSnappy, readonly)
		if err != nil {
			for _, table := range freezer.tables {
				table.Close()
			}
			lock.Release()
			return nil, err
		}
		freezer.tables[name] = table
	}

	if freezer.readonly {
		// 在只读模式下，仅验证，不截断。验证还设置了“冻结器”。已冻结“”。
		err = freezer.validate()
	} else {
		// 将所有表截断为公共长度。
		err = freezer.repair()
	}
	if err != nil {
		for _, table := range freezer.tables {
			table.Close()
		}
		lock.Release()
		return nil, err
	}

	// 创建写入批处理.
	freezer.writeBatch = newFreezerBatch(freezer)

	log.Info("Opened ancient database", "database", datadir, "readonly", readonly)
	return freezer, nil
}

// validate检查每个表的长度是否相同。在只读模式下使用，而不是“修复”。
func (f *Freezer) validate() error {
	if len(f.tables) == 0 {
		return nil
	}
	var (
		length uint64
		name   string
	)
	// Hack获取任意表的长度
	for kind, table := range f.tables {
		length = atomic.LoadUint64(&table.items)
		name = kind
		break
	}
	// 现在检查每个桌子的长度
	for kind, table := range f.tables {
		items := atomic.LoadUint64(&table.items)
		if length != items {
			return fmt.Errorf("freezer tables %s and %s have differing lengths: %d != %d", kind, name, items, length)
		}
	}
	atomic.StoreUint64(&f.frozen, length)
	return nil
}

//修复会将所有数据表截断为相同的长度。
func (f *Freezer) repair() error {
	var (
		head = uint64(math.MaxUint64)
		tail = uint64(0)
	)
	for _, table := range f.tables {
		items := atomic.LoadUint64(&table.items)
		if head > items {
			head = items
		}
		hidden := atomic.LoadUint64(&table.itemHidden)
		if hidden > tail {
			tail = hidden
		}
	}
	for _, table := range f.tables {
		if err := table.truncateHead(head); err != nil {
			return err
		}
		if err := table.truncateTail(tail); err != nil {
			return err
		}
	}
	atomic.StoreUint64(&f.frozen, head)
	atomic.StoreUint64(&f.tail, tail)
	return nil
}

//古代从只附加不可变文件中检索古代二进制blob。
func (f *Freezer) Ancient(kind string, number uint64) ([]byte, error) {
	if table := f.tables[kind]; table != nil {
		return table.Retrieve(number)
	}
	return nil, errUnknownTable
}

//古人返回冻结物品的长度。
func (f *Freezer) Ancients() (uint64, error) {
	return atomic.LoadUint64(&f.frozen), nil
}

// ModifyAncients运行给定的写入操作。
func (f *Freezer) ModifyAncients(fn func(typedb.AncientWriteOp) error) (writeSize int64, err error) {
	if f.readonly {
		return 0, errReadOnly
	}
	f.writeLock.Lock()
	defer f.writeLock.Unlock()

	// 如果出现错误，请将所有表格回滚到起始位置。
	prevItem := atomic.LoadUint64(&f.frozen)
	defer func() {
		if err != nil {
			// The write operation has failed. Go back to the previous item position.
			for name, table := range f.tables {
				err := table.truncateHead(prevItem)
				if err != nil {
					log.Error("Freezer table roll-back failed", "table", name, "index", prevItem, "err", err)
				}
			}
		}
	}()

	f.writeBatch.reset()
	if err := fn(f.writeBatch); err != nil {
		return 0, err
	}
	item, writeSize, err := f.writeBatch.commit()
	if err != nil {
		return 0, err
	}
	atomic.StoreUint64(&f.frozen, item)
	return writeSize, nil
}

//同步将所有数据表刷新到磁盘。
func (f *Freezer) Sync() error {
	var errs []error
	for _, table := range f.tables {
		if err := table.Sync(); err != nil {
			errs = append(errs, err)
		}
	}
	if errs != nil {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// Close终止链冻结器，取消映射所有数据文件。
func (f *Freezer) Close() error {
	f.writeLock.Lock()
	defer f.writeLock.Unlock()

	var errs []error
	f.closeOnce.Do(func() {
		for _, table := range f.tables {
			if err := table.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := f.instanceLock.Release(); err != nil {
			errs = append(errs, err)
		}
	})
	if errs != nil {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// HasAgent返回一个指示器，指示冷冻柜中是否存在指定的古代数据。
func (f *Freezer) HasAncient(kind string, number uint64) (bool, error) {
	if table := f.tables[kind]; table != nil {
		return table.has(number), nil
	}
	return false, nil
}

// Ancentrange从索引“start”开始按顺序检索多个项目。它会回来的
//-最多“最大”项，
//-至少1项（即使超过maxByteSize），但在其他情况下
//返回尽可能多的符合maxByteSize的项目。
func (f *Freezer) AncientRange(kind string, start, count, maxBytes uint64) ([][]byte, error) {
	if table := f.tables[kind]; table != nil {
		return table.RetrieveItems(start, count, maxBytes)
	}
	return nil, errUnknownTable
}

// Tail返回冷冻柜中第一个存储的项目数。
func (f *Freezer) Tail() (uint64, error) {
	return atomic.LoadUint64(&f.tail), nil
}

// AncientSize返回指定类别的古代大小。
func (f *Freezer) AncientSize(kind string) (uint64, error) {
	// 这需要写锁来避免表字段上的数据争用。速度在这里并不重要，AncientSize用于调试。
	f.writeLock.RLock()
	defer f.writeLock.RUnlock()

	if table := f.tables[kind]; table != nil {
		return table.size()
	}
	return 0, errUnknownTable
}

// ReadAncients运行给定的读取操作，同时确保在底层冻结器上不会发生写入操作。
func (f *Freezer) ReadAncients(fn func(typedb.AncientReaderOp) error) (err error) {
	f.writeLock.RLock()
	defer f.writeLock.RUnlock()

	return fn(f)
}

// TruncateHead丢弃任何超过所提供阈值的最近数据。
func (f *Freezer) TruncateHead(items uint64) error {
	if f.readonly {
		return errReadOnly
	}
	f.writeLock.Lock()
	defer f.writeLock.Unlock()

	if atomic.LoadUint64(&f.frozen) <= items {
		return nil
	}
	for _, table := range f.tables {
		if err := table.truncateHead(items); err != nil {
			return err
		}
	}
	atomic.StoreUint64(&f.frozen, items)
	return nil
}

// TruncateTail丢弃任何低于提供的阈值数的最新数据。
func (f *Freezer) TruncateTail(tail uint64) error {
	if f.readonly {
		return errReadOnly
	}
	f.writeLock.Lock()
	defer f.writeLock.Unlock()

	if atomic.LoadUint64(&f.tail) >= tail {
		return nil
	}
	for _, table := range f.tables {
		if err := table.truncateTail(tail); err != nil {
			return err
		}
	}
	atomic.StoreUint64(&f.tail, tail)
	return nil
}

// AncientDatadir返回古代存储的根目录路径。
func (f *Freezer) AncientDatadir() (string, error) {
	return f.datadir, nil
}

// convertLegacyFn以旧格式获取原始冷冻库条目，并以新格式返回它。
type convertLegacyFn = func([]byte) ([]byte, error)

// MigrateTable按顺序处理给定表中的条目，如果它们是旧格式的，则将它们转换为新格式。
func (f *Freezer) MigrateTable(kind string, convert convertLegacyFn) error {
	if f.readonly {
		return errReadOnly
	}
	f.writeLock.Lock()
	defer f.writeLock.Unlock()

	table, ok := f.tables[kind]
	if !ok {
		return errUnknownTable
	}
	// forEach按顺序串行迭代表中的每个条目，以该项为参数调用'fn'。如果“fn”返回错误，则迭代将停止，并将返回该错误。
	forEach := func(t *freezerTable, offset uint64, fn func(uint64, []byte) error) error {
		var (
			items     = atomic.LoadUint64(&t.items)
			batchSize = uint64(1024)
			maxBytes  = uint64(1024 * 1024)
		)
		for i := offset; i < items; {
			if i+batchSize > items {
				batchSize = items - i
			}
			data, err := t.RetrieveItems(i, batchSize, maxBytes)
			if err != nil {
				return err
			}
			for j, item := range data {
				if err := fn(i+uint64(j), item); err != nil {
					return err
				}
			}
			i += uint64(len(data))
		}
		return nil
	}
	// 该过程假设尾部没有删除，需要进行修改以说明这一点。
	if table.itemOffset > 0 || table.itemHidden > 0 {
		return fmt.Errorf("migration not supported for tail-deleted freezers")
	}
	ancientsPath := filepath.Dir(table.index.Name())
	// 为迁移的表设置新的目录，我们将在最后将其内容移到ancients目录。
	migrationPath := filepath.Join(ancientsPath, "migration")
	newTable, err := NewFreezerTable(migrationPath, kind, table.noCompression, false)
	if err != nil {
		return err
	}
	var (
		batch  = newTable.newBatch()
		out    []byte
		start  = time.Now()
		logged = time.Now()
		offset = newTable.items
	)
	if offset > 0 {
		log.Info("found previous migration attempt", "migrated", offset)
	}
	// 遍历条目并转换它们
	if err := forEach(table, offset, func(i uint64, blob []byte) error {
		if i%10000 == 0 && time.Since(logged) > 16*time.Second {
			log.Info("Processing legacy elements", "count", i, "elapsed", entity.PrettyDuration(time.Since(start)))
			logged = time.Now()
		}
		out, err = convert(blob)
		if err != nil {
			return err
		}
		if err := batch.AppendRaw(i, out); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	if err := batch.commit(); err != nil {
		return err
	}
	log.Info("Replacing old table files with migrated ones", "elapsed", entity.PrettyDuration(time.Since(start)))
	// 释放和删除旧表文件。注意，这不会删除索引文件。
	table.releaseFilesAfter(0, true)

	if err := newTable.Close(); err != nil {
		return err
	}
	//files, err := os.Open(migrationPath)
	//if err != nil {
	//	return err
	//}
	//// 将移植的文件移动到ancients目录。
	//for _, f := range files {
	//	// 这将替代旧的索引文件作为副作用。
	//	if err := os.Rename(filepath.Join(migrationPath, f.Name()), filepath.Join(ancientsPath, f.Name())); err != nil {
	//		return err
	//	}
	//}
	// 现在删除空目录。
	if err := os.Remove(migrationPath); err != nil {
		return err
	}

	return nil
}

// 链式冷冻柜是冷冻柜的包装，具有额外的链式冷冻功能。后台线程将继续将旧链段从键值数据库移动到平面文件，以节省实时数据库上的空间。
type chainFreezer struct {
	// 警告：“阈值”字段是按原子方式访问的。在32位平台上，只有64位对齐的字段可以是原子字段。结构保证如此对齐
	threshold uint64 // 最近不冻结的块数（除测试外，params.FullImmutabilityThreshold）

	*Freezer
	quit    chan struct{}
	wg      sync.WaitGroup
	trigger chan chan struct{} // 手动闭锁冻结触发器，测试确定性
}

//Close关闭链冻结器实例并终止后台线程。
func (f *chainFreezer) Close() error {
	err := f.Freezer.Close()
	select {
	case <-f.quit:
	default:
		close(f.quit)
	}
	f.wg.Wait()
	return err
}

// newChainFreezer为古代链数据初始化冻结器。
func newChainFreezer(datadir string, namespace string, readonly bool, maxTableSize uint32, tables map[string]bool) (*chainFreezer, error) {
	freezer, err := NewFreezer(datadir, namespace, readonly, maxTableSize, tables)
	if err != nil {
		return nil, err
	}
	return &chainFreezer{
		Freezer:   freezer,
		threshold: entity.FullImmutabilityThreshold,
		quit:      make(chan struct{}),
		trigger:   make(chan chan struct{}),
	}, nil
}

// 冻结是一个后台线程，它定期检查区块链的任何导入进度，并将古代数据从fast数据库移动到冻结器中。故意将此功能与块导入断开，以避免在块传播过程中产生额外的数据混乱延迟。
func (f *chainFreezer) freeze(db typedb.KeyValueStore) {
	nfdb := &nofreezedb{KeyValueStore: db}

	var (
		backoff   bool
		triggered chan struct{} // Used in tests
	)
	for {
		select {
		case <-f.quit:
			log.Info("Freezer shutting down")
			return
		default:
		}
		if backoff {
			// 如果我们在手动触发，通知它
			if triggered != nil {
				triggered <- struct{}{}
				triggered = nil
			}
			select {
			case <-time.NewTimer(freezerRecheckInterval).C:
				backoff = false
			case triggered = <-f.trigger:
				backoff = false
			case <-f.quit:
				return
			}
		}
		// 检索冻结阈值。
		hash := ReadHeadBlockHash(nfdb)
		if hash == (entity.Hash{}) {
			log.Debug("Current full block hash unavailable") // new chain, empty database
			backoff = true
			continue
		}
		number := ReadHeaderNumber(nfdb, hash)
		threshold := atomic.LoadUint64(&f.threshold)
		frozen := atomic.LoadUint64(&f.frozen)
		switch {
		case number == nil:
			log.Error("Current full block number unavailable", "hash", hash)
			backoff = true
			continue

		case *number < threshold:
			log.Debug("Current full block not old enough", "number", *number, "hash", hash, "delay", threshold)
			backoff = true
			continue

		case *number-threshold <= frozen:
			log.Debug("Ancient blocks frozen already", "number", *number, "hash", hash, "frozen", frozen)
			backoff = true
			continue
		}
		head := ReadHeader(nfdb, hash, *number)
		if head == nil {
			log.Error("Current full block unavailable", "number", *number, "hash", hash)
			backoff = true
			continue
		}

		// 看来我们已经准备好冻结数据，可以成批处理
		var (
			start    = time.Now()
			first, _ = f.Ancients()
			limit    = *number - threshold
		)
		if limit-first > freezerBatchLimit {
			limit = first + freezerBatchLimit
		}
		ancients, err := f.freezeRange(nfdb, first, limit)
		if err != nil {
			log.Error("Error in block freeze operation", "err", err)
			backoff = true
			continue
		}

		// 批次块已冻结，在从leveldb中擦拭之前冲洗
		if err := f.Sync(); err != nil {
			log.Info("Failed to flush frozen tables", "err", err)
		}

		// 清除活动数据库中的所有数据
		batch := db.NewBatch()
		for i := 0; i < len(ancients); i++ {
			// 始终将genesis块保留在活动数据库中
			if first+uint64(i) != 0 {
				DeleteBlockWithoutNumber(batch, ancients[i], first+uint64(i))
				DeleteCanonicalHash(batch, first+uint64(i))
			}
		}
		if err := batch.Write(); err != nil {
			log.Info("Failed to delete frozen canonical blocks", "err", err)
		}
		batch.Reset()

		// 同时擦净侧链并追踪悬挂的侧链
		var dangling []entity.Hash
		frozen = atomic.LoadUint64(&f.frozen) // 冻结范围内需要重新加载
		for number := first; number < frozen; number++ {
			// 始终将genesis块保留在活动数据库中
			if number != 0 {
				dangling = ReadAllHashes(db, number)
				for _, hash := range dangling {
					log.Info("Deleting side chain", "number", number, "hash", hash)
					DeleteBlock(batch, hash, number)
				}
			}
		}
		if err := batch.Write(); err != nil {
			log.Info("Failed to delete frozen side blocks", "err", err)
		}
		batch.Reset()

		// 迈向未来，删除并悬挂侧链
		if frozen > 0 {
			tip := frozen
			for len(dangling) > 0 {
				drop := make(map[entity.Hash]struct{})
				for _, hash := range dangling {
					log.Debug("Dangling parent from Freezer", "number", tip-1, "hash", hash)
					drop[hash] = struct{}{}
				}
				children := ReadAllHashes(db, tip)
				for i := 0; i < len(children); i++ {
					// 把孩子挖出来，确保它悬着
					child := ReadHeader(nfdb, children[i], tip)
					if child == nil {
						log.Error("Missing dangling header", "number", tip, "hash", children[i])
						continue
					}
					if _, ok := drop[child.ParentHash]; !ok {
						children = append(children[:i], children[i+1:]...)
						i--
						continue
					}
					// 删除与子级关联的所有块数据
					log.Debug("Deleting dangling block", "number", tip, "hash", children[i], "parent", child.ParentHash)
					DeleteBlock(batch, children[i], tip)
				}
				dangling = children
				tip++
			}
			if err := batch.Write(); err != nil {
				log.Info("Failed to delete dangling side blocks", "err", err)
			}
		}

		// 记录对用户友好的内容
		context := []interface{}{
			"blocks", frozen - first, "elapsed", entity.PrettyDuration(time.Since(start)), "number", frozen - 1,
		}
		if n := len(ancients); n > 0 {
			context = append(context, []interface{}{"hash", ancients[n-1]}...)
		}
		log.Info("Deep froze chain segment", context...)

		// 避免因微小写入而导致数据库崩溃
		if frozen-first < freezerBatchLimit {
			backoff = true
		}
	}
}

func (f *chainFreezer) freezeRange(nfdb *nofreezedb, number, limit uint64) (hashes []entity.Hash, err error) {
	hashes = make([]entity.Hash, 0, limit-number)

	_, err = f.ModifyAncients(func(op typedb.AncientWriteOp) error {
		for ; number <= limit; number++ {
			//检索规范块的所有组件。
			hash := ReadCanonicalHash(nfdb, number)
			if hash == (entity.Hash{}) {
				return fmt.Errorf("canonical hash missing, can't freeze block %d", number)
			}
			header := ReadHeaderRLP(nfdb, hash, number)
			if len(header) == 0 {
				return fmt.Errorf("block header missing, can't freeze block %d", number)
			}
			body := ReadBodyRLP(nfdb, hash, number)
			if len(body) == 0 {
				return fmt.Errorf("block body missing, can't freeze block %d", number)
			}
			receipts := ReadReceiptsRLP(nfdb, hash, number)
			if len(receipts) == 0 {
				return fmt.Errorf("block receipts missing, can't freeze block %d", number)
			}
			td := ReadTdRLP(nfdb, hash, number)
			if len(td) == 0 {
				return fmt.Errorf("total difficulty missing, can't freeze block %d", number)
			}

			// 写入批处理。
			if err := op.AppendRaw(freezerHashTable, number, hash[:]); err != nil {
				return fmt.Errorf("can't write hash to Freezer: %v", err)
			}
			if err := op.AppendRaw(freezerHeaderTable, number, header); err != nil {
				return fmt.Errorf("can't write header to Freezer: %v", err)
			}
			if err := op.AppendRaw(freezerBodiesTable, number, body); err != nil {
				return fmt.Errorf("can't write body to Freezer: %v", err)
			}
			if err := op.AppendRaw(freezerReceiptTable, number, receipts); err != nil {
				return fmt.Errorf("can't write receipts to Freezer: %v", err)
			}
			if err := op.AppendRaw(freezerDifficultyTable, number, td); err != nil {
				return fmt.Errorf("can't write td to Freezer: %v", err)
			}

			hashes = append(hashes, hash)
		}
		return nil
	})

	return hashes, err
}

// DeleteBlock删除与哈希关联的所有块数据。
func DeleteBlock(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	DeleteReceipts(db, hash, number)
	DeleteHeader(db, hash, number)
	DeleteBody(db, hash, number)
	DeleteTd(db, hash, number)
}

// DeleteBlockWithoutNumber删除与哈希关联的所有块数据，但哈希到数字的映射除外。
func DeleteBlockWithoutNumber(db typedb.KeyValueWriter, hash entity.Hash, number uint64) {
	DeleteReceipts(db, hash, number)
	deleteHeaderWithoutNumber(db, hash, number)
	DeleteBody(db, hash, number)
	DeleteTd(db, hash, number)
}

//冷冻包是一个冷冻柜上多个项目的写入操作。
type freezerBatch struct {
	tables map[string]*freezerTableBatch
}

func (batch *freezerBatch) Append(kind string, number uint64, item interface{}) error {
	return batch.tables[kind].Append(number, item)
}

func (batch *freezerBatch) AppendRaw(kind string, number uint64, item []byte) error {
	return batch.tables[kind].AppendRaw(number, item)
}

// 重置初始化批次。
func (batch *freezerBatch) reset() {
	for _, tb := range batch.tables {
		tb.reset()
	}
}

// 在写入操作结束时调用commit，并将所有剩余数据写入表。
func (batch *freezerBatch) commit() (item uint64, writeSize int64, err error) {
	// 检查所有批次的计数是否一致。
	item = uint64(math.MaxUint64)
	for name, tb := range batch.tables {
		if item < math.MaxUint64 && tb.curItem != item {
			return 0, 0, fmt.Errorf("table %s is at item %d, want %d", name, tb.curItem, item)
		}
		item = tb.curItem
	}

	// 提交所有表批处理。
	for _, tb := range batch.tables {
		if err := tb.commit(); err != nil {
			return 0, 0, err
		}
		writeSize += tb.totalBytes
	}
	return item, writeSize, nil
}

//冷冻台批次是冷冻台的批次。
type freezerTableBatch struct {
	t *freezerTable

	sb          *snappyBuffer
	encBuffer   writeBuffer
	dataBuffer  []byte
	indexBuffer []byte
	curItem     uint64 // 下一次追加的预期索引
	totalBytes  int64  // 统计重置后写入的字节数
}

func newFreezerBatch(f *Freezer) *freezerBatch {
	batch := &freezerBatch{tables: make(map[string]*freezerTableBatch, len(f.tables))}
	for kind, table := range f.tables {
		batch.tables[kind] = table.newBatch()
	}
	return batch
}

// 重置清除批以供重用。
func (batch *freezerTableBatch) reset() {
	batch.dataBuffer = batch.dataBuffer[:0]
	batch.indexBuffer = batch.indexBuffer[:0]
	batch.curItem = atomic.LoadUint64(&batch.t.items)
	batch.totalBytes = 0
}

//提交将批处理的项目写入备份冻结表。
func (batch *freezerTableBatch) commit() error {
	// 写入数据。
	_, err := batch.t.head.Write(batch.dataBuffer)
	if err != nil {
		return err
	}
	dataSize := int64(len(batch.dataBuffer))
	batch.dataBuffer = batch.dataBuffer[:0]

	// 写入索引。
	_, err = batch.t.index.Write(batch.indexBuffer)
	if err != nil {
		return err
	}
	//indexSize := int64(len(batch.indexBuffer))
	batch.indexBuffer = batch.indexBuffer[:0]

	// 更新表的headBytes。
	batch.t.headBytes += dataSize
	atomic.StoreUint64(&batch.t.items, batch.curItem)

	// 更新指标。
	//batch.t.sizeGauge.Inc(dataSize + indexSize)
	//batch.t.writeMeter.Mark(dataSize + indexSize)
	return nil
}

// Append rlp在冷冻柜表格的末尾编码并添加数据。
//项目编号是确保数据正确性的预防性参数，但表将拒绝现有数据。
func (batch *freezerTableBatch) Append(item uint64, data interface{}) error {
	if item != batch.curItem {
		return fmt.Errorf("%w: have %d want %d", errOutOrderInsertion, item, batch.curItem)
	}

	// 对项目进行编码。
	batch.encBuffer.Reset()
	if err := rlp.Encode(&batch.encBuffer, data); err != nil {
		return err
	}
	encItem := batch.encBuffer.data
	if batch.sb != nil {
		encItem = batch.sb.compress(encItem)
	}
	return batch.appendItem(encItem)
}

// AppendRaw在冷冻台的末端注入一个二进制blob。项目编号是确保数据正确性的预防性参数，但表将拒绝现有数据。
func (batch *freezerTableBatch) AppendRaw(item uint64, blob []byte) error {
	if item != batch.curItem {
		return fmt.Errorf("%w: have %d want %d", errOutOrderInsertion, item, batch.curItem)
	}

	encItem := blob
	if batch.sb != nil {
		encItem = batch.sb.compress(blob)
	}
	return batch.appendItem(encItem)
}

func (batch *freezerTableBatch) appendItem(data []byte) error {
	// 检查项目是否适合当前数据文件。
	itemSize := int64(len(data))
	itemOffset := batch.t.headBytes + int64(len(batch.dataBuffer))
	if itemOffset+itemSize > int64(batch.t.maxFileSize) {
		// 它不合适，请先转到下一个文件。
		if err := batch.commit(); err != nil {
			return err
		}
		if err := batch.t.advanceHead(); err != nil {
			return err
		}
		itemOffset = 0
	}

	// 将数据放入缓冲区。
	batch.dataBuffer = append(batch.dataBuffer, data...)
	batch.totalBytes += itemSize

	// 将索引项放入缓冲区。
	entry := indexEntry{filenum: batch.t.headId, offset: uint32(itemOffset + itemSize)}
	batch.indexBuffer = entry.append(batch.indexBuffer)
	batch.curItem++

	return batch.maybeCommit()
}

//如果缓冲区足够满，则可能会写入缓冲数据。
func (batch *freezerTableBatch) maybeCommit() error {
	if len(batch.dataBuffer) > freezerBatchBufferLimit {
		return batch.commit()
	}
	return nil
}

//snappyBuffer以块格式写入snappy，并且可以重用。调用WriteTo时会重置。
type snappyBuffer struct {
	dst []byte
}

//压缩snappy压缩数据。
func (s *snappyBuffer) compress(data []byte) []byte {
	//snappy库不关心缓冲区的容量，只检查长度。
	//如果长度太小，它将分配一个全新的缓冲区。
	//为了避免这种情况，我们在这里检查所需的大小，并增大缓冲区的大小以利用全部容量。
	if n := snappy.MaxEncodedLen(len(data)); len(s.dst) < n {
		if cap(s.dst) < n {
			s.dst = make([]byte, n)
		}
		s.dst = s.dst[:n]
	}

	s.dst = snappy.Encode(s.dst, data)
	return s.dst
}

//writeBuffer实现io。字节片的写入程序。
type writeBuffer struct {
	data []byte
}

func (wb *writeBuffer) Write(data []byte) (int, error) {
	wb.data = append(wb.data, data...)
	return len(data), nil
}

func (wb *writeBuffer) Reset() {
	wb.data = wb.data[:0]
}

// freezerTable表示冷冻柜内的单链数据表（例如块）。它由一个数据文件（snappy编码的任意数据Blob）和一个indexEntry文件（未压缩到数据文件中的64位索引）组成。
type freezerTable struct {
	// 警告：“items”字段是按原子方式访问的。在32位平台上，只有64位对齐的字段可以是原子字段。结构保证如此对齐
	items      uint64 // 表中存储的项目数（包括从尾部删除的项目）
	itemOffset uint64 // 从表中删除的项目数

	// itemHidden是标记为已删除的项目数。
	//尾部删除仅在文件级别受支持，这意味着实际删除将延迟，直到整个数据文件标记为已删除。
	//在此之前，这些项目将被隐藏，以防止再次访问。该值不得低于itemOffset。
	itemHidden uint64

	noCompression bool // 如果为true，则禁用snappy压缩。注意：不具有追溯效力
	readonly      bool
	maxFileSize   uint32 // 数据文件的最大文件大小
	name          string
	path          string

	head   *os.File            // 表数据头的文件描述符
	index  *os.File            // 表的indexEntry文件的文件描述符
	meta   *os.File            // 表元数据的文件描述符
	files  map[uint32]*os.File // 打开文件
	headId uint32              // 当前活动头文件的编号
	tailId uint32              // 最早文件的编号

	headBytes  int64         // 写入头文件的字节数
	readMeter  metrics.Meter // 测量有效读取数据量的仪表
	writeMeter metrics.Meter // 测量写入数据有效量的仪表
	sizeGauge  metrics.Gauge // 用于跟踪所有冷冻台组合尺寸的仪表

	logger log.Logger   // 嵌入数据库路径和表名的记录器
	lock   sync.RWMutex // 保护数据文件描述符的互斥体
}

const indexEntrySize = 6

//newTable打开一个冻结表，如果数据和索引文件不存在，则创建它们。这两个文件都被截断为最短的公共长度，以确保它们不会失去同步。
func newTable(path string, name string, readMeter metrics.Meter, writeMeter metrics.Meter, sizeGauge metrics.Gauge, maxFilesize uint32, noCompression, readonly bool) (*freezerTable, error) {
	// 确保包含目录存在并打开indexEntry文件
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	var idxName string
	if noCompression {
		idxName = fmt.Sprintf("%s.ridx", name) // 原始索引文件
	} else {
		idxName = fmt.Sprintf("%s.cidx", name) // 压缩索引文件
	}
	var (
		err   error
		index *os.File
		meta  *os.File
	)
	if readonly {
		// 如果表不存在，则将失败
		index, err = openFreezerFileForReadOnly(filepath.Join(path, idxName))
		if err != nil {
			return nil, err
		}
		// 在rw模式下。这是暂时的解决方案，应该在实际使用尾部删除时进行更改。
		//这种攻击的原因是为每个冷冻柜表添加了额外的元文件，以支持尾部删除，但对于大多数遗留节点，该文件丢失。
		//此检查将突然中断许多与数据库相关的命令。因此，元数据文件总是为突变而打开，除了初始化之外，不会写入任何其他内容。
		meta, err = openFreezerFileForAppend(filepath.Join(path, fmt.Sprintf("%s.meta", name)))
		if err != nil {
			return nil, err
		}
	} else {
		index, err = openFreezerFileForAppend(filepath.Join(path, idxName))
		if err != nil {
			return nil, err
		}
		meta, err = openFreezerFileForAppend(filepath.Join(path, fmt.Sprintf("%s.meta", name)))
		if err != nil {
			return nil, err
		}
	}
	// 创建表并修复过去的任何不一致
	tab := &freezerTable{
		index:         index,
		meta:          meta,
		files:         make(map[uint32]*os.File),
		readMeter:     readMeter,
		writeMeter:    writeMeter,
		sizeGauge:     sizeGauge,
		name:          name,
		path:          path,
		logger:        log.New("database", path, "table", name),
		noCompression: noCompression,
		readonly:      readonly,
		maxFileSize:   maxFilesize,
	}
	if err := tab.repair(); err != nil {
		tab.Close()
		return nil, err
	}
	//初始化起始大小计数器
	size, err := tab.sizeNolock()
	if err != nil {
		tab.Close()
		return nil, err
	}
	tab.sizeGauge.Inc(int64(size))
	return tab, nil
}

// newBatch为冷冻台创建新批次。
func (t *freezerTable) newBatch() *freezerTableBatch {
	batch := &freezerTableBatch{t: t}
	if !t.noCompression {
		batch.sb = new(snappyBuffer)
	}
	batch.reset()
	return batch
}

//修复会交叉检查标头和索引文件，并在可能发生崩溃/数据丢失后截断它们以使它们彼此同步。
func (t *freezerTable) repair() error {
	// 创建临时偏移缓冲区以初始化文件，并将indexEntry读取到
	buffer := make([]byte, indexEntrySize)

	// 如果我们刚刚创建了文件，请使用0 indexEntry初始化索引
	stat, err := t.index.Stat()
	if err != nil {
		return err
	}
	if stat.Size() == 0 {
		if _, err := t.index.Write(buffer); err != nil {
			return err
		}
	}
	// 确保索引是indexEntrySize字节的倍数
	if overflow := stat.Size() % indexEntrySize; overflow != 0 {
		truncateFreezerFile(t.index, stat.Size()-overflow) // 新文件无法触发此路径
	}
	// 检索文件大小并准备截断
	if stat, err = t.index.Stat(); err != nil {
		return err
	}
	offsetsSize := stat.Size()

	// 打开头文件
	var (
		firstIndex  indexEntry
		lastIndex   indexEntry
		contentSize int64
		contentExp  int64
	)
	// 读取索引0，确定最早的文件以及要使用的项目偏移量
	t.index.ReadAt(buffer, 0)
	firstIndex.unmarshalBinary(buffer)

	//为尾部字段分配第一个存储的索引。
	//移除的项目总数用uint32表示，
	//这在理论上还不够，但在实践中已经足够了。
	t.tailId = firstIndex.filenum
	t.itemOffset = uint64(firstIndex.offset)

	// 从文件加载元数据
	meta, err := loadMetadata(t.meta, t.itemOffset)
	if err != nil {
		return err
	}
	t.itemHidden = meta.VirtualTail

	// 读取最后一个索引，如果冷冻柜为空，则使用默认值
	if offsetsSize == indexEntrySize {
		lastIndex = indexEntry{filenum: t.tailId, offset: 0}
	} else {
		t.index.ReadAt(buffer, offsetsSize-indexEntrySize)
		lastIndex.unmarshalBinary(buffer)
	}
	if t.readonly {
		t.head, err = t.openFile(lastIndex.filenum, openFreezerFileForReadOnly)
	} else {
		t.head, err = t.openFile(lastIndex.filenum, openFreezerFileForAppend)
	}
	if err != nil {
		return err
	}
	if stat, err = t.head.Stat(); err != nil {
		return err
	}
	contentSize = stat.Size()

	// 继续截断两个文件，直到它们同步
	contentExp = int64(lastIndex.offset)
	for contentExp != contentSize {
		// 将头文件截断为最后一个偏移指针
		if contentExp < contentSize {
			log.Warn("Truncating dangling head", "indexed", utils.StorageSize(contentExp), "stored", utils.StorageSize(contentSize))
			if err := truncateFreezerFile(t.head, contentExp); err != nil {
				return err
			}
			contentSize = contentExp
		}
		// 将索引截断为头文件内的点
		if contentExp > contentSize {
			log.Warn("Truncating dangling indexes", "indexed", utils.StorageSize(contentExp), "stored", utils.StorageSize(contentSize))
			if err := truncateFreezerFile(t.index, offsetsSize-indexEntrySize); err != nil {
				return err
			}
			offsetsSize -= indexEntrySize

			// 读取新的头部索引，如果冷冻柜已空，则使用默认值。
			var newLastIndex indexEntry
			if offsetsSize == indexEntrySize {
				newLastIndex = indexEntry{filenum: t.tailId, offset: 0}
			} else {
				t.index.ReadAt(buffer, offsetsSize-indexEntrySize)
				newLastIndex.unmarshalBinary(buffer)
			}
			// 我们可能又回到了以前的头像文件中
			if newLastIndex.filenum != lastIndex.filenum {
				// 释放先前打开的文件
				t.releaseFile(lastIndex.filenum)
				if t.head, err = t.openFile(newLastIndex.filenum, openFreezerFileForAppend); err != nil {
					return err
				}
				if stat, err = t.head.Stat(); err != nil {
					// 数据文件丢失。。。
					return err
				}
				contentSize = stat.Size()
			}
			lastIndex = newLastIndex
			contentExp = int64(lastIndex.offset)
		}
	}
	//对于windows上的只读文件，Sync（）失败。
	if !t.readonly {
		// 确保已将所有修复更改写入磁盘
		if err := t.index.Sync(); err != nil {
			return err
		}
		if err := t.head.Sync(); err != nil {
			return err
		}
		if err := t.meta.Sync(); err != nil {
			return err
		}
	}
	// 更新项目和字节计数器并返回
	t.items = t.itemOffset + uint64(offsetsSize/indexEntrySize-1) // last indexEntry指向数据文件的结尾
	t.headBytes = contentSize
	t.headId = lastIndex.filenum

	// 删除头部删除留下的文件
	t.releaseFilesAfter(t.headId, true)

	// 删除尾部删除留下的文件
	t.releaseFilesBefore(t.tailId, true)

	// 关闭打开的文件并预打开所有文件
	if err := t.preopen(); err != nil {
		return err
	}
	log.Debug("Chain freezer table opened", "items", t.items, "size", utils.StorageSize(t.headBytes))
	return nil
}

//preopen打开冷冻柜需要的所有文件。这个方法应该从init上下文中调用，因为它假设它不必麻烦锁定。执行preopen的基本原理是不必从Retrieve中执行，因此不需要在Retrieve中获得写锁。
func (t *freezerTable) preopen() (err error) {
	// 修复可能已打开（某些）文件
	t.releaseFilesAfter(0, false)

	// 仅在RDONLY中打开除头部以外的所有
	for i := t.tailId; i < t.headId; i++ {
		if _, err = t.openFile(i, openFreezerFileForReadOnly); err != nil {
			return err
		}
	}
	if t.readonly {
		t.head, err = t.openFile(t.headId, openFreezerFileForReadOnly)
	} else {
		// 读/写时打开磁头
		t.head, err = t.openFile(t.headId, openFreezerFileForAppend)
	}
	return err
}

// 当当前头文件超出文件限制时，应调用advanceHead，并且必须打开一个新文件。调用此方法之前，此方法的调用方必须持有写锁。
func (t *freezerTable) advanceHead() error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// 我们以截断模式打开下一个文件——如果这个文件已经存在，我们需要从头开始。
	nextID := t.headId + 1
	newHead, err := t.openFile(nextID, openFreezerFileTruncated)
	if err != nil {
		return err
	}

	// 关闭旧文件，然后以RDONLY模式重新打开。
	t.releaseFile(t.headId)
	t.openFile(t.headId, openFreezerFileForReadOnly)

	// 交换当前磁头。
	t.head = newHead
	t.headBytes = 0
	t.headId = nextID
	return nil
}

// sizeNolock返回冻结器表中的总数据大小，而不首先获取互斥锁。
func (t *freezerTable) sizeNolock() (uint64, error) {
	stat, err := t.index.Stat()
	if err != nil {
		return 0, err
	}
	total := uint64(t.maxFileSize)*uint64(t.headId-t.tailId) + uint64(t.headBytes) + uint64(stat.Size())
	return total, nil
}

// 同步将所有挂起的数据从内存推送到磁盘。这是一项昂贵的手术，所以请小心使用。
func (t *freezerTable) Sync() error {
	if err := t.index.Sync(); err != nil {
		return err
	}
	if err := t.meta.Sync(); err != nil {
		return err
	}
	return t.head.Sync()
}

// 关闭关闭所有打开的文件。
func (t *freezerTable) Close() error {
	t.lock.Lock()
	defer t.lock.Unlock()

	var errs []error
	if err := t.index.Close(); err != nil {
		errs = append(errs, err)
	}
	t.index = nil

	if err := t.meta.Close(); err != nil {
		errs = append(errs, err)
	}
	t.meta = nil

	for _, f := range t.files {
		if err := f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	t.head = nil

	if errs != nil {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// openFile假定写锁由调用方持有
func (t *freezerTable) openFile(num uint32, opener func(string) (*os.File, error)) (f *os.File, err error) {
	var exist bool
	if f, exist = t.files[num]; !exist {
		var name string
		if t.noCompression {
			name = fmt.Sprintf("%s.%04d.rdat", t.name, num)
		} else {
			name = fmt.Sprintf("%s.%04d.cdat", t.name, num)
		}
		f, err = opener(filepath.Join(t.path, name))
		if err != nil {
			return nil, err
		}
		t.files[num] = f
	}
	return f, err
}

// truncateHead丢弃任何超过所提供阈值的最近数据。
func (t *freezerTable) truncateHead(items uint64) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// 确保给定的截断目标落在正确的范围内
	existing := atomic.LoadUint64(&t.items)
	if existing <= items {
		return nil
	}
	if items < atomic.LoadUint64(&t.itemHidden) {
		return errors.New("truncation below tail")
	}
	// 我们需要截断、保存旧的大小以进行度量跟踪
	//oldSize, err := t.sizeNolock()
	//if err != nil {
	//	return err
	//}
	// 某些内容不同步，请截断表的偏移索引
	//if existing > items+1 {
	//	log = t.logger.Warn // 仅在删除多个项目时发出响亮警告
	//}
	log.Warn("Truncating freezer table", "items", existing, "limit", items)

	// 首先截断索引文件，在计算新冷冻台长度时也会考虑尾部位置。
	length := items - atomic.LoadUint64(&t.itemOffset)
	if err := truncateFreezerFile(t.index, int64(length+1)*indexEntrySize); err != nil {
		return err
	}
	// 计算数据文件的新预期大小并将其截断
	var expected indexEntry
	if length == 0 {
		expected = indexEntry{filenum: t.tailId, offset: 0}
	} else {
		buffer := make([]byte, indexEntrySize)
		if _, err := t.index.ReadAt(buffer, int64(length*indexEntrySize)); err != nil {
			return err
		}
		expected.unmarshalBinary(buffer)
	}
	// 我们可能需要截断回旧文件
	if expected.filenum != t.headId {
		// 如果已打开进行读取，请强制重新打开进行写入
		t.releaseFile(expected.filenum)
		newHead, err := t.openFile(expected.filenum, openFreezerFileForAppend)
		if err != nil {
			return err
		}
		// 释放当前磁头之后的所有文件（包括前一个磁头和可能已打开以供读取的所有文件）
		t.releaseFilesAfter(expected.filenum, true)
		// 历史性的倒退
		t.head = newHead
		t.headId = expected.filenum
	}
	if err := truncateFreezerFile(t.head, int64(expected.offset)); err != nil {
		return err
	}
	// 所有数据文件被截断，设置内部计数器并返回
	t.headBytes = int64(expected.offset)
	atomic.StoreUint64(&t.items, items)

	// 检索新大小并更新总大小计数器
	//newSize, err := t.sizeNolock()
	//if err != nil {
	//	return err
	//}
	//t.sizeGauge.Dec(int64(oldSize - newSize))
	return nil
}

// truncateTail丢弃所提供阈值之前的任何最近数据。
func (t *freezerTable) truncateTail(items uint64) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// 确保给定的截断目标落在正确的范围内
	if atomic.LoadUint64(&t.itemHidden) >= items {
		return nil
	}
	if atomic.LoadUint64(&t.items) < items {
		return errors.New("truncation above head")
	}
	// 按给定的新尾部位置加载新尾部索引
	var (
		newTailId uint32
		buffer    = make([]byte, indexEntrySize)
	)
	if atomic.LoadUint64(&t.items) == items {
		newTailId = t.headId
	} else {
		offset := items - atomic.LoadUint64(&t.itemOffset)
		if _, err := t.index.ReadAt(buffer, int64((offset+1)*indexEntrySize)); err != nil {
			return err
		}
		var newTail indexEntry
		newTail.unmarshalBinary(buffer)
		newTailId = newTail.filenum
	}
	// 更新虚拟尾部标记并在表中隐藏这些条目。
	atomic.StoreUint64(&t.itemHidden, items)
	if err := writeMetadata(t.meta, newMetadata(items)); err != nil {
		return err
	}
	// 隐藏项仍位于当前尾部文件中，无法删除任何数据文件。
	if t.tailId == newTailId {
		return nil
	}
	// 隐藏项位于不正确的范围内，返回错误。
	if t.tailId > newTailId {
		return fmt.Errorf("invalid index, tail-file %d, item-file %d", t.tailId, newTailId)
	}
	// 隐藏项超过当前尾部文件，请删除相关数据文件。我们需要截断、保存旧的大小以进行度量跟踪。
	_, err := t.sizeNolock()
	if err != nil {
		return err
	}
	// 计算可以从文件中删除的项目数。
	var (
		newDeleted = items
		deleted    = atomic.LoadUint64(&t.itemOffset)
	)
	for current := items - 1; current >= deleted; current -= 1 {
		if _, err := t.index.ReadAt(buffer, int64((current-deleted+1)*indexEntrySize)); err != nil {
			return err
		}
		var pre indexEntry
		pre.unmarshalBinary(buffer)
		if pre.filenum != newTailId {
			break
		}
		newDeleted = current
	}
	// 在操作索引文件之前，先提交元数据文件的更改。
	if err := t.meta.Sync(); err != nil {
		return err
	}
	// 截断索引文件中已删除的索引项。
	err = copyFrom(t.index.Name(), t.index.Name(), indexEntrySize*(newDeleted-deleted+1), func(f *os.File) error {
		tailIndex := indexEntry{
			filenum: newTailId,
			offset:  uint32(newDeleted),
		}
		_, err := f.Write(tailIndex.append(nil))
		return err
	})
	if err != nil {
		return err
	}
	// 重新打开修改后的索引文件以加载更改
	if err := t.index.Close(); err != nil {
		return err
	}
	t.index, err = openFreezerFileForAppend(t.index.Name())
	if err != nil {
		return err
	}
	// 在当前尾部之前释放所有文件
	t.tailId = newTailId
	atomic.StoreUint64(&t.itemOffset, newDeleted)
	t.releaseFilesBefore(t.tailId, true)

	// Retrieve the new size and update the total size counter
	//newSize, err := t.sizeNolock()
	//if err != nil {
	//	return err
	//}
	//t.sizeGauge.Dec(int64(oldSize - newSize))
	return nil
}

// releaseFile关闭文件，并将其从打开的文件缓存中删除。假设调用方持有写锁
func (t *freezerTable) releaseFile(num uint32) {
	if f, exist := t.files[num]; exist {
		delete(t.files, num)
		f.Close()
	}
}

//releaseFilesAfter关闭所有打开的文件，并可以选择删除这些文件
func (t *freezerTable) releaseFilesAfter(num uint32, remove bool) {
	for fnum, f := range t.files {
		if fnum > num {
			delete(t.files, fnum)
			f.Close()
			if remove {
				os.Remove(f.Name())
			}
		}
	}
}

//releaseFilesBefore以较小的数字关闭所有打开的文件，还可以选择删除这些文件
func (t *freezerTable) releaseFilesBefore(num uint32, remove bool) {
	for fnum, f := range t.files {
		if fnum < num {
			delete(t.files, fnum)
			f.Close()
			if remove {
				os.Remove(f.Name())
			}
		}
	}
}

// Retrieve查找具有给定数字的项的数据偏移量，并从数据文件中检索原始二进制blob。
func (t *freezerTable) Retrieve(item uint64) ([]byte, error) {
	items, err := t.RetrieveItems(item, 1, 0)
	if err != nil {
		return nil, err
	}
	return items[0], nil
}

//RetrieveItems按顺序返回多个项，从索引“start”开始。
//它最多将返回“max”项，但将提前中止以尊重“maxBytes”参数。
//但是，如果“maxBytes”小于一个项目的大小，它将返回一个元素，可能会使maxBytes溢出。
func (t *freezerTable) RetrieveItems(start, count, maxBytes uint64) ([][]byte, error) {
	// 首先，我们读取可能被压缩的“原始”数据。
	diskData, sizes, err := t.retrieveItems(start, count, maxBytes)
	if err != nil {
		return nil, err
	}
	var (
		output     = make([][]byte, 0, count)
		offset     int // 读数偏移量
		outputSize int // 未压缩数据的大小
	)
	// 现在将数据切片并解压缩。
	for i, diskSize := range sizes {
		item := diskData[offset : offset+diskSize]
		offset += diskSize
		decompressedSize := diskSize
		if !t.noCompression {
			decompressedSize, _ = snappy.DecodedLen(item)
		}
		if i > 0 && uint64(outputSize+decompressedSize) > maxBytes {
			break
		}
		if !t.noCompression {
			data, err := snappy.Decode(nil, item)
			if err != nil {
				return nil, err
			}
			output = append(output, data)
		} else {
			output = append(output, item)
		}
		outputSize += decompressedSize
	}
	return output, nil
}

// size返回冷冻柜表中的总数据大小。
func (t *freezerTable) size() (uint64, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	return t.sizeNolock()
}

// 返回一个指示器，指示冷冻柜表中是否仍可以访问指定的数字数据。
func (t *freezerTable) has(number uint64) bool {
	return atomic.LoadUint64(&t.items) > number && atomic.LoadUint64(&t.itemHidden) <= number
}

//retrieveItems从表中最多读取“count”个项目。它至少读取一个项，但避免读取超过maxBytes字节的内容。它返回（可能压缩的）数据和大小。
func (t *freezerTable) retrieveItems(start, count, maxBytes uint64) ([]byte, []int, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	//确保表格和项目可访问
	if t.index == nil || t.head == nil {
		return nil, nil, errClosed
	}
	var (
		items  = atomic.LoadUint64(&t.items)      // 项目总数（总目+1）
		hidden = atomic.LoadUint64(&t.itemHidden) // 隐藏项的数量
	)
	// 确保开头是写的，而不是从尾部删除的，并且调用者确实想要一些东西
	if items <= start || hidden > start || count == 0 {
		return nil, nil, errOutOfBounds
	}
	if start+count > items {
		count = items - start
	}
	var (
		output     = make([]byte, maxBytes) // 要读取数据的缓冲区
		outputSize int                      // 已使用的缓冲区大小
	)
	// readData是从磁盘读取单个数据项的辅助方法。
	readData := func(fileId, start uint32, length int) error {
		// 如果使用了较小的限制，并且元素较大，则在读取第一个（也是唯一一个）项时可能需要重新锁定读取缓冲区。
		if len(output) < length {
			output = make([]byte, length)
		}
		dataFile, exist := t.files[fileId]
		if !exist {
			return fmt.Errorf("missing data file %d", fileId)
		}
		if _, err := dataFile.ReadAt(output[outputSize:outputSize+length], int64(start)); err != nil {
			return err
		}
		outputSize += length
		return nil
	}
	// 一次性读取所有索引
	indices, err := t.getIndices(start, count)
	if err != nil {
		return nil, nil, err
	}
	var (
		sizes      []int               // 每个元素的大小
		totalSize  = 0                 // 迄今为止读取的所有数据的总大小
		readStart  = indices[0].offset // 在文件中开始读取的位置
		unreadSize = 0                 // 尚未读取数据的大小
	)

	for i, firstIndex := range indices[:len(indices)-1] {
		secondIndex := indices[i+1]
		// 确定项目的大小。
		offset1, offset2, _ := firstIndex.bounds(secondIndex)
		size := int(offset2 - offset1)
		// 跨越文件边界
		if secondIndex.filenum != firstIndex.filenum {
			// 如果第一个文件中有未读数据，则需要立即进行读取。
			if unreadSize > 0 {
				if err := readData(firstIndex.filenum, readStart, unreadSize); err != nil {
					return nil, nil, err
				}
				unreadSize = 0
			}
			readStart = 0
		}
		if i > 0 && uint64(totalSize+size) > maxBytes {
			// 由于超出字节限制，即将中断。我们不读取最后一项，但现在需要进行延迟读取。
			if unreadSize > 0 {
				if err := readData(secondIndex.filenum, readStart, unreadSize); err != nil {
					return nil, nil, err
				}
			}
			break
		}
		// 将读取延迟到以后
		unreadSize += size
		totalSize += size
		sizes = append(sizes, size)
		if i == len(indices)-2 || uint64(totalSize) > maxBytes {
			// 最后一项，需要立即阅读
			if err := readData(secondIndex.filenum, readStart, unreadSize); err != nil {
				return nil, nil, err
			}
			break
		}
	}
	return output[:outputSize], sizes, nil
}

//GetIndexes返回给定from项的索引项，包括“count”项。N、 B：N个项目的实际返回索引数将始终为N+1（除非返回错误）。
//OBS：此方法假设调用方已经验证（和/或修剪）范围，以便项目在边界内。
//如果此方法用于读取越界，则将返回错误。
func (t *freezerTable) getIndices(from, count uint64) ([]*indexEntry, error) {
	// 应用表格偏移
	from = from - t.itemOffset
	// 要读取N个项目，我们需要N+1个索引。
	buffer := make([]byte, (count+1)*indexEntrySize)
	if _, err := t.index.ReadAt(buffer, int64(from*indexEntrySize)); err != nil {
		return nil, err
	}
	var (
		indices []*indexEntry
		offset  int
	)
	for i := from; i <= from+count; i++ {
		index := new(indexEntry)
		index.unmarshalBinary(buffer[offset:])
		offset += indexEntrySize
		indices = append(indices, index)
	}
	if from == 0 {
		// 特殊情况下，如果我们正在读取冷冻柜中的第一个项目。
		//我们假设第一项总是从零开始（关于删除，我们只支持按文件删除，因此假设成立）。
		//这意味着对于删除情况，我们可以使用第一项元数据来携带有关“全局”偏移量的信息
		indices[0].offset = 0
		indices[0].filenum = indices[1].filenum
	}
	return indices, nil
}

// NewFreezerTable将给定路径作为冻结表打开。
func NewFreezerTable(path, name string, disableSnappy, readonly bool) (*freezerTable, error) {
	return newTable(path, name, metrics.NilMeter{}, metrics.NilMeter{}, metrics.NilGauge{}, freezerTableSize, disableSnappy, readonly)
}

const freezerVersion = 1 // 冷冻柜表元数据的初始版本标记

// freezerTableMeta包装冷冻柜表的所有元数据。
type freezerTableMeta struct {
	// Version是冷冻柜表的版本控制描述符。
	Version uint16

	// VirtualTail指示已标记为已删除的项目数。它的值等于从表中删除的项目数加上隐藏在表中的项目数，因此它决不能低于“实际尾部”。
	VirtualTail uint64
}

//newMetadata使用给定的虚拟尾部初始化元数据对象。
func newMetadata(tail uint64) *freezerTableMeta {
	return &freezerTableMeta{
		Version:     freezerVersion,
		VirtualTail: tail,
	}
}

// writeMetadata将冷冻柜表的元数据写入给定的元数据文件。
func writeMetadata(file *os.File, meta *freezerTableMeta) error {
	_, err := file.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}
	return rlp.Encode(file, meta)
}

// readMetadata从给定的元数据文件中读取冷冻柜表的元数据。
func readMetadata(file *os.File) (*freezerTableMeta, error) {
	_, err := file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, err
	}
	var meta freezerTableMeta
	if err := rlp.Decode(file, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// loadMetadata从给定的元数据文件加载元数据。使用给定的“实际尾部”（如果为空）初始化元数据文件。
func loadMetadata(file *os.File, tail uint64) (*freezerTableMeta, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	// 如果元数据文件不存在，则将具有给定实际尾部的元数据写入元数据文件。这里有两种可能的情况：
	//-冷冻台是空的
	//-在这两种情况下，冻结表都是遗留的，将meta写入到文件中，实际尾部作为虚拟尾部。
	if stat.Size() == 0 {
		m := newMetadata(tail)
		if err := writeMetadata(file, m); err != nil {
			return nil, err
		}
		return m, nil
	}
	m, err := readMetadata(file)
	if err != nil {
		return nil, err
	}
	// 如果虚拟尾部甚至低于给定的实际尾部，请使用该尾部更新虚拟尾部。理论上这根本不应该发生，在这里打印一条警告。
	if m.VirtualTail < tail {
		log.Warn("Updated virtual tail", "have", m.VirtualTail, "now", tail)
		m.VirtualTail = tail
		if err := writeMetadata(file, m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// indexEntry包含数据所在文件的编号/id，以及文件内到数据末尾的偏移量。在序列化形式中，filenum存储为uint16。
type indexEntry struct {
	filenum uint32 // 存储为uint16（2字节）
	offset  uint32 // 存储为uint32（4字节）
}

// unmarshalBinary将二进制b反序列化到rawIndex条目中。
func (i *indexEntry) unmarshalBinary(b []byte) {
	i.filenum = uint32(binary.BigEndian.Uint16(b[:2]))
	i.offset = binary.BigEndian.Uint32(b[2:6])
}

// append将编码的条目添加到b的末尾。
func (i *indexEntry) append(b []byte) []byte {
	offset := len(b)
	out := append(b, make([]byte, indexEntrySize)...)
	binary.BigEndian.PutUint16(out[offset:], uint16(i.filenum))
	binary.BigEndian.PutUint32(out[offset+2:], i.offset)
	return out
}

//bounds返回起始偏移量和结束偏移量，以及由两个索引项标记的数据项的读取位置的文件号。假设这两个条目是连续的。
func (i *indexEntry) bounds(end *indexEntry) (startOffset, endOffset, fileId uint32) {
	if i.filenum != end.filenum {
		// 如果一段数据“穿过”了一个数据文件，那么它实际上是在第二个数据文件上的一段。我们为第二个文件返回零索引entry作为start
		return 0, end.offset, end.filenum
	}
	return i.offset, end.offset, end.filenum
}

//--utils

// copyFrom将数据从偏移量“offset”处的“srcPath”复制到“destPath”中。如果“destPath”不存在，则创建它，否则将覆盖它。在执行复制之前，可以注册一个回调来操作dest文件。destPath==srcPath完全有效。
func copyFrom(srcPath, destPath string, offset uint64, before func(f *os.File) error) error {
	// 在我们想要结束的同一目录中创建一个临时文件
	f, err := os.Create(filepath.Dir(destPath))
	if err != nil {
		return err
	}
	fname := f.Name()

	// 清理遗留文件
	defer func() {
		if f != nil {
			f.Close()
		}
		os.Remove(fname)
	}()
	// 在从src复制内容之前，如果给定函数不是nil，则应用该函数。
	if before != nil {
		if err := before(f); err != nil {
			return err
		}
	}
	// 打开源文件
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	if _, err = src.Seek(int64(offset), 0); err != nil {
		src.Close()
		return err
	}
	// io。Copy在内部使用32K缓冲区。
	_, err = io.Copy(f, src)
	if err != nil {
		src.Close()
		return err
	}
	// 将临时文件重命名为指定的dest名称。src可能与dest相同，因此需要在执行最终移动之前关闭。
	src.Close()

	if err := f.Close(); err != nil {
		return err
	}
	f = nil

	if err := os.Rename(fname, destPath); err != nil {
		return err
	}
	return nil
}

// openFreezerFileForAppend打开一个冻结表文件并查找到底
func openFreezerFileForAppend(filename string) (*os.File, error) {
	// 打开不带O\U APPEND标志的文件，因为在不同操作系统上执行截断操作时，该文件的行为不同
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	// 为追加搜索结束
	if _, err = file.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	return file, nil
}

// openFreezerFileForReadOnly为只读访问打开冻结器表文件
func openFreezerFileForReadOnly(filename string) (*os.File, error) {
	return os.OpenFile(filename, os.O_RDONLY, 0644)
}

//openFreezerFileTruncated打开冷冻柜表，确保其被截断
func openFreezerFileTruncated(filename string) (*os.File, error) {
	return os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
}

//truncateFreezerFile调整冷冻柜表格文件的大小并查找到底
func truncateFreezerFile(file *os.File, size int64) error {
	if err := file.Truncate(size); err != nil {
		return err
	}
	// 为追加搜索结束
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	return nil
}
