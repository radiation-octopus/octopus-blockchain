package leveldb

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/typedb"
	"github.com/radiation-octopus/octopus/utils"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/filter"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	//degradationWarnInterval指定如果leveldb数据库无法跟上请求的写入，则应多久打印一次警告。
	degradationWarnInterval = time.Minute

	// minCache是分配给leveldb读写缓存的最小内存量，以MB为单位，一分为二。
	minCache = 16

	// minHandles是要分配给打开的数据库文件的最小文件句柄数。
	minHandles = 16

	// metricsGatheringInterval指定检索leveldb数据库压缩、io和暂停统计数据以向用户报告的间隔。
	metricsGatheringInterval = 3 * time.Second
)

//数据库是一个持久的键值存储。除了基本的数据存储功能外，它还支持按二进制字母顺序对键空间进行批写入和迭代。
type Database struct {
	fn string      // 用于报告的文件名
	db *leveldb.DB // LevelDB实例

	//compTimeMeter      metrics.Meter // 用于测量数据库压缩总时间的仪表
	//compReadMeter      metrics.Meter // 用于测量压实过程中读取的数据的仪表
	//compWriteMeter     metrics.Meter // 用于测量压实过程中写入的数据的仪表
	//writeDelayNMeter   metrics.Meter // 用于测量数据库压缩导致的写入延迟数的仪表
	//writeDelayMeter    metrics.Meter // 用于测量数据库压缩导致的写入延迟持续时间的仪表
	//diskSizeGauge      metrics.Gauge // 用于跟踪数据库中所有级别大小的仪表
	//diskReadMeter      metrics.Meter // 测量有效读取数据量的仪表
	//diskWriteMeter     metrics.Meter // 测量写入数据有效量的仪表
	//memCompGauge       metrics.Gauge // 用于跟踪内存压缩次数的仪表
	//level0CompGauge    metrics.Gauge // 用于跟踪0级工作台压实次数的量规
	//nonlevel0CompGauge metrics.Gauge // 用于跟踪非0级工作台压实次数的量规
	//seekCompGauge      metrics.Gauge // 用于跟踪由read opt导致的表格压缩次数的仪表

	quitLock sync.Mutex      // 互斥保护退出通道访问
	quitChan chan chan error // 在关闭数据库之前退出通道以停止度量集合

	//log log.Logger // 跟踪数据库路径的配置记录器
}

func (db *Database) Compact(start []byte, limit []byte) error {
	panic("implement me")
}

func (db *Database) Stat(property string) (string, error) {
	panic("implement me")
}

//New返回一个包装的LevelDB对象。命名空间是度量报告用于呈现内部统计信息的前缀。
func New(file string, cache int, handles int, namespace string, readonly bool) (*Database, error) {
	return NewCustom(file, namespace, func(options *opt.Options) {

		// 确保我们有一些最小的缓存和文件保证
		if cache < minCache {
			cache = minCache
		}
		if handles < minHandles {
			handles = minHandles
		}
		// 设置默认选项
		options.OpenFilesCacheCapacity = handles
		options.BlockCacheCapacity = cache / 2 * opt.MiB
		options.WriteBuffer = cache / 4 * opt.MiB // 其中两个在内部使用
		if readonly {
			options.ReadOnly = true
		}
	})
}

//NewCustom返回一个包装的LevelDB对象。命名空间是度量报告用于呈现内部统计信息的前缀。
//自定义函数允许调用者修改leveldb选项
func NewCustom(file string, namespace string, customize func(options *opt.Options)) (*Database, error) {
	options := configureOptions(customize)
	//logger := log.New("database", file)
	usedCache := options.GetBlockCacheCapacity() + options.GetWriteBuffer()*2
	logCtx := []interface{}{"cache", utils.StorageSize(usedCache), "handles", options.GetOpenFilesCacheCapacity()}
	if options.ReadOnly {
		logCtx = append(logCtx, "readonly", "true")
	}
	//logger.Info("Allocated cache and file handles", logCtx...)

	// 打开数据库并恢复任何潜在的损坏
	db, err := leveldb.OpenFile(file, options)
	if _, corrupted := err.(*errors.ErrCorrupted); corrupted {
		db, err = leveldb.RecoverFile(file, nil)
	}
	if err != nil {
		return nil, err
	}
	//使用所有已注册的度量组装包装器
	ldb := &Database{
		fn: file,
		db: db,
		//log: logger,
		quitChan: make(chan chan error),
	}
	//ldb.compTimeMeter = metrics.NewRegisteredMeter(namespace+"compact/time", nil)
	//ldb.compReadMeter = metrics.NewRegisteredMeter(namespace+"compact/input", nil)
	//ldb.compWriteMeter = metrics.NewRegisteredMeter(namespace+"compact/output", nil)
	//ldb.diskSizeGauge = metrics.NewRegisteredGauge(namespace+"disk/size", nil)
	//ldb.diskReadMeter = metrics.NewRegisteredMeter(namespace+"disk/read", nil)
	//ldb.diskWriteMeter = metrics.NewRegisteredMeter(namespace+"disk/write", nil)
	//ldb.writeDelayMeter = metrics.NewRegisteredMeter(namespace+"compact/writedelay/duration", nil)
	//ldb.writeDelayNMeter = metrics.NewRegisteredMeter(namespace+"compact/writedelay/counter", nil)
	//ldb.memCompGauge = metrics.NewRegisteredGauge(namespace+"compact/memory", nil)
	//ldb.level0CompGauge = metrics.NewRegisteredGauge(namespace+"compact/level0", nil)
	//ldb.nonlevel0CompGauge = metrics.NewRegisteredGauge(namespace+"compact/nonlevel0", nil)
	//ldb.seekCompGauge = metrics.NewRegisteredGauge(namespace+"compact/seek", nil)

	//启动指标收集并返回
	//go ldb.meter(metricsGatheringInterval)
	return ldb, nil
}

//configureOptions设置一些默认选项，然后运行提供的setter。
func configureOptions(customizeFn func(*opt.Options)) *opt.Options {
	// 设置默认选项
	options := &opt.Options{
		Filter: filter.NewBloomFilter(10),
		//DisableSeeksCompaction: true,
	}
	// 允许调用者对选项进行自定义修改
	if customizeFn != nil {
		customizeFn(options)
	}
	return options
}

// Close停止度量集合，将所有挂起的数据刷新到磁盘，并关闭对底层键值存储的所有io访问。
func (db *Database) Close() error {
	db.quitLock.Lock()
	defer db.quitLock.Unlock()

	if db.quitChan != nil {
		errc := make(chan error)
		db.quitChan <- errc
		if err := <-errc; err != nil {
			log.Error("Metrics collection failed", "err", err)
		}
		db.quitChan = nil
	}
	return db.db.Close()
}

func (dbd *Database) IsHas(key []byte) (bool, error) {
	return dbd.db.Has(key, nil)
}

// Get检索给定的键（如果它存在于键值存储中）。
func (db *Database) Get(key []byte) ([]byte, error) {
	dat, err := db.db.Get(key, nil)
	if err != nil {
		return nil, err
	}
	return dat, nil
}

// Put将给定值插入键值存储区。
func (db *Database) Put(key []byte, value []byte) error {
	return db.db.Put(key, value, nil)
}

// Delete从键值存储中删除键。
func (db *Database) Delete(key []byte) error {
	return db.db.Delete(key, nil)
}

//NewBatch创建一个只写键值存储，该存储缓冲对其主机数据库的更改，直到调用最终写入。
func (db *Database) NewBatch() typedb.Batch {
	return &batch{
		db: db.db,
		b:  new(leveldb.Batch),
	}
}

// NewBatchWithSize使用预先分配的缓冲区创建一个只写数据库批处理。
func (db *Database) NewBatchWithSize(size int) typedb.Batch {
	return &batch{
		db: db.db,
		b:  leveldb.MakeBatch(size),
	}
}

// NewIterator在具有特定键前缀的数据库内容子集上创建一个二进制字母迭代器，从特定的初始键开始（如果不存在，则在其之后）。
func (db *Database) NewIterator(prefix []byte, start []byte) typedb.Iterator {
	return db.db.NewIterator(bytesPrefixRange(prefix, start), nil)
}

// meter periodically retrieves internal leveldb counters and reports them to
// the metrics subsystem.
//
// This is how a LevelDB stats table looks like (currently):
//   Compactions
//    Level |   Tables   |    Size(MB)   |    Time(sec)  |    Read(MB)   |   Write(MB)
//   -------+------------+---------------+---------------+---------------+---------------
//      0   |          0 |       0.00000 |       1.27969 |       0.00000 |      12.31098
//      1   |         85 |     109.27913 |      28.09293 |     213.92493 |     214.26294
//      2   |        523 |    1000.37159 |       7.26059 |      66.86342 |      66.77884
//      3   |        570 |    1113.18458 |       0.00000 |       0.00000 |       0.00000
//
// This is how the write delay look like (currently):
// DelayN:5 Delay:406.604657ms Paused: false
//
// This is how the iostats look like (currently):
// Read(MB):3895.04860 Write(MB):3654.64712
func (db *Database) meter(refresh time.Duration) {
	// Create the counters to store current and previous compaction values
	compactions := make([][]float64, 2)
	for i := 0; i < 2; i++ {
		compactions[i] = make([]float64, 4)
	}
	// Create storage for iostats.
	var iostats [2]float64

	// Create storage and warning log tracer for write delay.
	var (
		delaystats      [2]int64
		lastWritePaused time.Time
	)

	var (
		errc chan error
		merr error
	)

	timer := time.NewTimer(refresh)
	defer timer.Stop()

	// Iterate ad infinitum and collect the stats
	for i := 1; errc == nil && merr == nil; i++ {
		// Retrieve the database stats
		stats, err := db.db.GetProperty("leveldb.stats")
		if err != nil {
			//db.log.Error("Failed to read database stats", "err", err)
			merr = err
			continue
		}
		// Find the compaction table, skip the header
		lines := strings.Split(stats, "\n")
		for len(lines) > 0 && strings.TrimSpace(lines[0]) != "Compactions" {
			lines = lines[1:]
		}
		if len(lines) <= 3 {
			//db.log.Error("Compaction leveldbTable not found")
			merr = errors.New("compaction leveldbTable not found")
			continue
		}
		lines = lines[3:]

		// Iterate over all the leveldbTable rows, and accumulate the entries
		for j := 0; j < len(compactions[i%2]); j++ {
			compactions[i%2][j] = 0
		}
		for _, line := range lines {
			parts := strings.Split(line, "|")
			if len(parts) != 6 {
				break
			}
			for idx, counter := range parts[2:] {
				value, err := strconv.ParseFloat(strings.TrimSpace(counter), 64)
				if err != nil {
					//db.log.Error("Compaction entry parsing failed", "err", err)
					merr = err
					continue
				}
				compactions[i%2][idx] += value
			}
		}
		// Update all the requested meters
		//if db.diskSizeGauge != nil {
		//	db.diskSizeGauge.Update(int64(compactions[i%2][0] * 1024 * 1024))
		//}
		//if db.compTimeMeter != nil {
		//	db.compTimeMeter.Mark(int64((compactions[i%2][1] - compactions[(i-1)%2][1]) * 1000 * 1000 * 1000))
		//}
		//if db.compReadMeter != nil {
		//	db.compReadMeter.Mark(int64((compactions[i%2][2] - compactions[(i-1)%2][2]) * 1024 * 1024))
		//}
		//if db.compWriteMeter != nil {
		//	db.compWriteMeter.Mark(int64((compactions[i%2][3] - compactions[(i-1)%2][3]) * 1024 * 1024))
		//}
		// Retrieve the write delay statistic
		writedelay, err := db.db.GetProperty("leveldb.writedelay")
		if err != nil {
			//db.log.Error("Failed to read database write delay statistic", "err", err)
			merr = err
			continue
		}
		var (
			delayN        int64
			delayDuration string
			duration      time.Duration
			paused        bool
		)
		if n, err := fmt.Sscanf(writedelay, "DelayN:%d Delay:%s Paused:%t", &delayN, &delayDuration, &paused); n != 3 || err != nil {
			//db.log.Error("Write delay statistic not found")
			merr = err
			continue
		}
		duration, err = time.ParseDuration(delayDuration)
		if err != nil {
			//db.log.Error("Failed to parse delay duration", "err", err)
			merr = err
			continue
		}
		//if db.writeDelayNMeter != nil {
		//	db.writeDelayNMeter.Mark(delayN - delaystats[0])
		//}
		//if db.writeDelayMeter != nil {
		//	db.writeDelayMeter.Mark(duration.Nanoseconds() - delaystats[1])
		//}
		// If a warning that db is performing compaction has been displayed, any subsequent
		// warnings will be withheld for one minute not to overwhelm the user.
		if paused && delayN-delaystats[0] == 0 && duration.Nanoseconds()-delaystats[1] == 0 &&
			time.Now().After(lastWritePaused.Add(degradationWarnInterval)) {
			//db.log.Warn("Database compacting, degraded performance")
			lastWritePaused = time.Now()
		}
		delaystats[0], delaystats[1] = delayN, duration.Nanoseconds()

		// Retrieve the database iostats.
		ioStats, err := db.db.GetProperty("leveldb.iostats")
		if err != nil {
			//db.log.Error("Failed to read database iostats", "err", err)
			merr = err
			continue
		}
		var nRead, nWrite float64
		parts := strings.Split(ioStats, " ")
		if len(parts) < 2 {
			//db.log.Error("Bad syntax of ioStats", "ioStats", ioStats)
			merr = fmt.Errorf("bad syntax of ioStats %s", ioStats)
			continue
		}
		if n, err := fmt.Sscanf(parts[0], "Read(MB):%f", &nRead); n != 1 || err != nil {
			//db.log.Error("Bad syntax of read entry", "entry", parts[0])
			merr = err
			continue
		}
		if n, err := fmt.Sscanf(parts[1], "Write(MB):%f", &nWrite); n != 1 || err != nil {
			//db.log.Error("Bad syntax of write entry", "entry", parts[1])
			merr = err
			continue
		}
		//if db.diskReadMeter != nil {
		//	db.diskReadMeter.Mark(int64((nRead - iostats[0]) * 1024 * 1024))
		//}
		//if db.diskWriteMeter != nil {
		//	db.diskWriteMeter.Mark(int64((nWrite - iostats[1]) * 1024 * 1024))
		//}
		iostats[0], iostats[1] = nRead, nWrite

		compCount, err := db.db.GetProperty("leveldb.compcount")
		if err != nil {
			//db.log.Error("Failed to read database iostats", "err", err)
			merr = err
			continue
		}

		var (
			memComp       uint32
			level0Comp    uint32
			nonLevel0Comp uint32
			seekComp      uint32
		)
		if n, err := fmt.Sscanf(compCount, "MemComp:%d Level0Comp:%d NonLevel0Comp:%d SeekComp:%d", &memComp, &level0Comp, &nonLevel0Comp, &seekComp); n != 4 || err != nil {
			//db.log.Error("Compaction count statistic not found")
			merr = err
			continue
		}
		//db.memCompGauge.Update(int64(memComp))
		//db.level0CompGauge.Update(int64(level0Comp))
		//db.nonlevel0CompGauge.Update(int64(nonLevel0Comp))
		//db.seekCompGauge.Update(int64(seekComp))

		// Sleep a bit, then repeat the stats collection
		select {
		case errc = <-db.quitChan:
			// Quit requesting, stop hammering the database
		case <-timer.C:
			timer.Reset(refresh)
			// Timeout, gather a new set of stats
		}
	}

	if errc == nil {
		errc = <-db.quitChan
	}
	errc <- merr
}

// batch是一个只写的leveldb批处理，在调用write时将更改提交到其主机数据库。批处理不能同时使用。
type batch struct {
	db   *leveldb.DB
	b    *leveldb.Batch
	size int
}

//Put将给定值插入批中，以便稍后提交。
func (b *batch) Put(key []byte, value []byte) error {
	b.b.Put(key, value)
	b.size += len(key) + len(value)
	return nil
}

//Delete将删除密钥插入批处理中，以便稍后提交。
func (b *batch) Delete(key []byte) error {
	b.b.Delete(key)
	b.size += len(key)
	return nil
}

//ValueSize检索排队等待写入的数据量。
func (b *batch) ValueSize() int {
	return b.size
}

//写入将所有累积数据刷新到磁盘。
func (b *batch) Write() error {
	return b.db.Write(b.b, nil)
}

//重置重置批以供重用。
func (b *batch) Reset() {
	b.b.Reset()
	b.size = 0
}

//Replay重播批处理内容。
func (b *batch) Replay(w typedb.KeyValueWriter) error {
	return b.b.Replay(&replayer{writer: w})
}

// replayer是一个小包装器，用于实现正确的replay方法。
type replayer struct {
	writer  typedb.KeyValueWriter
	failure error
}

// Put将给定值插入键值数据存储。
func (r *replayer) Put(key, value []byte) {
	// 如果重播已失败，请停止执行ops
	if r.failure != nil {
		return
	}
	r.failure = r.writer.Put(key, value)
}

// Delete从键值数据存储中删除键。
func (r *replayer) Delete(key []byte) {
	// 如果重播已失败，请停止执行ops
	if r.failure != nil {
		return
	}
	r.failure = r.writer.Delete(key)
}

//bytesPrefixRange返回满足以下条件的键范围
//-给定前缀，以及
//-给定的寻道位置
func bytesPrefixRange(prefix, start []byte) *util.Range {
	r := util.BytesPrefix(prefix)
	r.Start = append(r.Start, start...)
	return r
}
