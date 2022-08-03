package octell

import (
	"errors"
	"fmt"
	"github.com/edsrzf/mmap-go"
	"github.com/hashicorp/golang-lru/simplelru"
	"github.com/radiation-octopus/octopus-blockchain/consensus"
	"github.com/radiation-octopus/octopus-blockchain/consensus/beacon"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"github.com/radiation-octopus/octopus-blockchain/rpc"
	"github.com/radiation-octopus/octopus/director"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var ErrInvalidDumpMagic = errors.New("invalid dump magic")

var (
	// two256是一个大整数，表示2^256
	two256 = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0))

	// sharedOctell是可以在多个用户之间共享的完整实例。
	sharedOctell *Octell

	// algorithmRevision是用于文件命名的数据结构版本。
	algorithmRevision = 23

	//dumpMagic是一个数据集转储头，用于检查数据转储的健全性。
	dumpMagic = []uint32{0xbaddcafe, 0xfee1dead}
)

// isLittleEndian返回本地系统是以低位字节顺序运行还是以高位字节顺序运行。
func isLittleEndian() bool {
	n := uint32(0x01020304)
	return *(*byte)(unsafe.Pointer(&n)) == 0x04
}

//memoryMap尝试对uint32s的文件进行内存映射以进行只读访问。
func memoryMap(path string, lock bool) (*os.File, mmap.MMap, []uint32, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0644)
	if err != nil {
		return nil, nil, nil, err
	}
	mem, buffer, err := memoryMapFile(file, false)
	if err != nil {
		file.Close()
		return nil, nil, nil, err
	}
	for i, magic := range dumpMagic {
		if buffer[i] != magic {
			mem.Unmap()
			file.Close()
			return nil, nil, nil, ErrInvalidDumpMagic
		}
	}
	if lock {
		if err := mem.Lock(); err != nil {
			mem.Unmap()
			file.Close()
			return nil, nil, nil, err
		}
	}
	return file, mem, buffer[len(dumpMagic):], err
}

// memoryMapFile尝试对已打开的文件描述符进行内存映射。
func memoryMapFile(file *os.File, write bool) (mmap.MMap, []uint32, error) {
	// 尝试内存映射文件
	flag := mmap.RDONLY
	if write {
		flag = mmap.RDWR
	}
	mem, err := mmap.Map(file, flag, 0)
	if err != nil {
		return nil, nil, err
	}
	// 该文件现在已被内存映射。创建文件的[]uint32视图。
	var view []uint32
	header := (*reflect.SliceHeader)(unsafe.Pointer(&view))
	header.Data = (*reflect.SliceHeader)(unsafe.Pointer(&mem)).Data
	header.Cap = len(mem) / 4
	header.Len = header.Cap
	return mem, view, nil
}

// MemoryPandGenerate尝试对UInt32的临时文件进行内存映射以进行写访问，用生成器中的数据填充该文件，然后将其移动到请求的最终路径中。
func memoryMapAndGenerate(path string, size uint64, lock bool, generator func(buffer []uint32)) (*os.File, mmap.MMap, []uint32, error) {
	// 确保数据文件夹存在
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, nil, err
	}
	// 创建一个巨大的临时空文件以填充数据
	temp := path + "." + strconv.Itoa(rand.Int())

	dump, err := os.Create(temp)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = ensureSize(dump, int64(len(dumpMagic))*4+int64(size)); err != nil {
		dump.Close()
		os.Remove(temp)
		return nil, nil, nil, err
	}
	// 内存映射要写入的文件并用生成器填充它
	mem, buffer, err := memoryMapFile(dump, true)
	if err != nil {
		dump.Close()
		os.Remove(temp)
		return nil, nil, nil, err
	}
	copy(buffer, dumpMagic)

	data := buffer[len(dumpMagic):]
	generator(data)

	if err := mem.Unmap(); err != nil {
		return nil, nil, nil, err
	}
	if err := dump.Close(); err != nil {
		return nil, nil, nil, err
	}
	if err := os.Rename(temp, path); err != nil {
		return nil, nil, nil, err
	}
	return memoryMap(path, lock)
}

// ensureSize将文件扩展到给定大小。这是为了防止以后出现运行时错误，如果基础文件扩展到磁盘容量之外，即使表面上它已经扩展，但由于稀疏，实际上并没有占用磁盘上声明的全部大小。
func ensureSize(f *os.File, size int64) error {
	// 在不支持fallocate的系统上，我们只是截断它。更可靠的替代方法是使用posix\U fallocate，或者显式地用零填充文件。
	return f.Truncate(size)
}

// 模式定义octell引擎进行的PoW验证的类型和数量。
type Mode uint

// lru按缓存或数据集的上次使用时间跟踪它们，最多保留N个。
type lru struct {
	what string
	new  func(epoch uint64) interface{}
	mu   sync.Mutex
	// 项目保存在LRU缓存中，但有一种特殊情况：我们始终将一个项目（最高可见历元）+1作为“未来项目”。
	cache      *simplelru.LRU
	future     uint64
	futureItem interface{}
}

// newlru为验证缓存或挖掘数据集创建一个新的最近使用最少的缓存。
func newlru(what string, maxItems int, new func(epoch uint64) interface{}) *lru {
	if maxItems <= 0 {
		maxItems = 1
	}
	cache, _ := simplelru.NewLRU(maxItems, func(key, value interface{}) {
		log.Info("Evicted octell "+what, "epoch", key)
	})
	return &lru{what: what, new: new, cache: cache}
}

// get检索或创建给定时期的项。第一个返回值总是非nil。如果lru认为某个项目在不久的将来会有用，则第二个返回值为非零。
func (lru *lru) get(epoch uint64) (item, future interface{}) {
	lru.mu.Lock()
	defer lru.mu.Unlock()

	// 获取或创建所请求时期的项目。
	item, ok := lru.cache.Get(epoch)
	if !ok {
		if lru.future > 0 && lru.future == epoch {
			item = lru.futureItem
		} else {
			log.Info("Requiring new octell "+lru.what, "epoch", epoch)
			item = lru.new(epoch)
		}
		lru.cache.Add(epoch, item)
	}
	// 如果时期大于以前看到的时期，请更新“未来项目”。
	if epoch < maxEpoch-1 && lru.future < epoch+1 {
		log.Info("Requiring new future octell "+lru.what, "epoch", epoch+1)
		future = lru.new(epoch + 1)
		lru.future = epoch + 1
		lru.futureItem = future
	}
	return item, future
}

// cache用一些元数据包装octell缓存，以便于并发使用。
type cache struct {
	epoch uint64    // 与此缓存相关的历元
	dump  *os.File  // 内存映射缓存的文件描述符
	mmap  mmap.MMap // 释放前将内存自身映射到取消映射
	cache []uint32  // 实际缓存数据内容（可能是内存映射）
	once  sync.Once // 确保缓存只生成一次
}

// newCache创建一个新的octell验证缓存，并将其作为普通Go接口返回，以便在LRU缓存中使用。
func newCache(epoch uint64) interface{} {
	return &cache{epoch: epoch}
}

// generate确保在使用之前生成缓存内容。
func (c *cache) generate(dir string, limit int, lock bool, test bool) {
	c.once.Do(func() {
		size := cacheSize(c.epoch*epochLength + 1)
		seed := seedHash(c.epoch*epochLength + 1)
		if test {
			size = 1024
		}
		// 如果我们没有在磁盘上存储任何内容，请生成并返回。
		if dir == "" {
			c.cache = make([]uint32, size/4)
			generateCache(c.cache, c.epoch, seed)
			return
		}
		// 需要磁盘存储，这会很有趣
		var endian string
		if !isLittleEndian() {
			endian = ".be"
		}
		path := filepath.Join(dir, fmt.Sprintf("cache-R%d-%x%s", algorithmRevision, seed[:8], endian))
		log.Info("epoch", c.epoch)

		// 我们即将对文件进行mmap，确保在缓存未使用时清除映射。
		runtime.SetFinalizer(c, (*cache).finalizer)

		// 尝试从磁盘加载文件并将其映射到内存
		var err error
		c.dump, c.mmap, c.cache, err = memoryMap(path, lock)
		if err == nil {
			log.Debug("Loaded old octell cache from disk")
			return
		}
		log.Debug("Failed to load old octell cache", "err", err)

		// 没有以前的缓存可用，请创建新的缓存文件以填充
		c.dump, c.mmap, c.cache, err = memoryMapAndGenerate(path, size, lock, func(buffer []uint32) { generateCache(buffer, c.epoch, seed) })
		if err != nil {
			log.Error("Failed to generate mapped octell cache", "err", err)

			c.cache = make([]uint32, size/4)
			generateCache(c.cache, c.epoch, seed)
		}
		// 迭代所有以前的实例并删除旧实例
		for ep := int(c.epoch) - limit; ep >= 0; ep-- {
			seed := seedHash(uint64(ep)*epochLength + 1)
			path := filepath.Join(dir, fmt.Sprintf("cache-R%d-%x%s", algorithmRevision, seed[:8], endian))
			os.Remove(path)
		}
	})
}

// 终结器取消映射内存并关闭文件。
func (c *cache) finalizer() {
	if c.mmap != nil {
		c.mmap.Unmap()
		c.dump.Close()
		c.mmap, c.dump = nil, nil
	}
}

// dataset使用一些元数据包装octell数据集，以便于并发使用。
type dataset struct {
	epoch   uint64    // 与此缓存相关的历元
	dump    *os.File  // 内存映射缓存的文件描述符
	mmap    mmap.MMap // 释放前将内存自身映射到取消映射
	dataset []uint32  // 实际缓存数据内容
	once    sync.Once // 确保缓存只生成一次
	done    uint32    // 确定生成状态的原子标志
}

//newDataset创建一个新的octell挖掘数据集，并将其作为普通Go接口返回，以便在LRU缓存中使用。
func newDataset(epoch uint64) interface{} {
	return &dataset{epoch: epoch}
}

// generated返回此特定数据集是否已完成生成（可能根本没有启动）。
//这对于远程工作者来说很有用，因为他们默认使用验证缓存，而不是阻塞DAG生成。
func (d *dataset) generated() bool {
	return atomic.LoadUint32(&d.done) == 1
}

// generate确保在使用前生成数据集内容。
func (d *dataset) generate(dir string, limit int, lock bool, test bool) {
	d.once.Do(func() {
		// 标记完成后生成的数据集。这是远程
		defer atomic.StoreUint32(&d.done, 1)

		csize := cacheSize(d.epoch*epochLength + 1)
		dsize := datasetSize(d.epoch*epochLength + 1)
		seed := seedHash(d.epoch*epochLength + 1)
		if test {
			csize = 1024
			dsize = 32 * 1024
		}
		// 如果我们没有在磁盘上存储任何内容，请生成并返回
		if dir == "" {
			cache := make([]uint32, csize/4)
			generateCache(cache, d.epoch, seed)

			d.dataset = make([]uint32, dsize/4)
			generateDataset(d.dataset, d.epoch, cache)

			return
		}
		// 需要磁盘存储，这会很有趣。。。
		var endian string
		if !isLittleEndian() {
			endian = ".be"
		}
		path := filepath.Join(dir, fmt.Sprintf("full-R%d-%x%s", algorithmRevision, seed[:8], endian))
		log.Info("epoch", d.epoch)

		// 我们即将对文件进行mmap，确保在缓存未使用时清除映射。
		runtime.SetFinalizer(d, (*dataset).finalizer)

		// 尝试从磁盘加载文件并将其映射到内存
		var err error
		d.dump, d.mmap, d.dataset, err = memoryMap(path, lock)
		if err == nil {
			log.Debug("Loaded old octell dataset from disk")
			return
		}
		log.Debug("Failed to load old octell dataset", "err", err)

		// 没有以前的数据集可用，请创建新的数据集文件以填充
		cache := make([]uint32, csize/4)
		generateCache(cache, d.epoch, seed)

		d.dump, d.mmap, d.dataset, err = memoryMapAndGenerate(path, dsize, lock, func(buffer []uint32) { generateDataset(buffer, d.epoch, cache) })
		if err != nil {
			log.Error("Failed to generate mapped octell dataset", "err", err)

			d.dataset = make([]uint32, dsize/4)
			generateDataset(d.dataset, d.epoch, cache)
		}
		// 迭代所有以前的实例并删除旧实例
		for ep := int(d.epoch) - limit; ep >= 0; ep-- {
			seed := seedHash(uint64(ep)*epochLength + 1)
			path := filepath.Join(dir, fmt.Sprintf("full-R%d-%x%s", algorithmRevision, seed[:8], endian))
			os.Remove(path)
		}
	})
}

// 终结器关闭所有文件处理程序并打开内存映射。
func (d *dataset) finalizer() {
	if d.mmap != nil {
		d.mmap.Unmap()
		d.dump.Close()
		d.mmap, d.dump = nil, nil
	}
}

const (
	ModeNormal Mode = iota
	ModeShared
	ModeTest
	ModeFake
	ModeFullFake
)

// Config是octell的配置参数。
type Config struct {
	CacheDir         string
	CachesInMem      int
	CachesOnDisk     int
	CachesLockMmap   bool
	DatasetDir       string
	DatasetsInMem    int
	DatasetsOnDisk   int
	DatasetsLockMmap bool
	PowMode          Mode

	// 设置后，远程密封器发送的通知将是块头JSON对象，而不是工作包数组。
	NotifyFull bool

	//Log log.Logger `toml:"-"`
}

// Octell是一个基于实现Octell算法的工作证明的共识引擎。
type Octell struct {
	Config *Config //`autoInjectLang:"octell.Config"` //启动项配置

	caches   *lru // 内存缓存，以避免频繁重新生成
	datasets *lru // 内存中的数据集，以避免过于频繁地重新生成

	// 工作相关字段
	rand    *rand.Rand    // nonce的正确种子随机源
	threads int           // 要在if挖掘上挖掘的线程数
	update  chan struct{} // 更新挖掘参数的通知通道
	//hashrate metrics.Meter // 跟踪平均哈希率的计数器
	remote *remoteSealer

	// 下面的字段是用于测试的挂钩
	shared    *Octell       // 避免缓存再生的共享PoW验证器
	fakeFail  uint64        // 即使在假模式下也无法通过PoW检查的块编号
	fakeDelay time.Duration // 从verify返回前的睡眠时间延迟

	lock      sync.Mutex // 确保内存缓存和挖掘字段的线程安全
	closeOnce sync.Once  // 确保出口通道不会关闭两次。
}

// API实现共识。引擎，返回面向用户的RPC API。
func (o *Octell) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	// 为了确保向后兼容性，我们向eth和ethash名称空间公开了ethash rpcapis。
	return []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   &API{o},
		},
		{
			Namespace: "ethash",
			Version:   "1.0",
			Service:   &API{o},
		},
	}
}

//关闭关闭退出通道以通知所有后端线程退出。
func (o *Octell) Close() error {
	return o.StopRemoteSealer()
}

// StopRemoteSealer停止远程密封
func (o *Octell) StopRemoteSealer() error {
	o.closeOnce.Do(func() {
		//如果未分配出口通道，则短路。
		if o.remote == nil {
			return
		}
		close(o.remote.requestExit)
		<-o.remote.exitCh
	})
	return nil
}

func CreateOctell(chainConfig *entity.ChainConfig, config *Config) consensus.Engine {
	noverify := director.ReadCfg("octopus", "miner", "binding", "config", "noverify").(bool)
	notify := director.ReadCfg("octopus", "miner", "binding", "config", "notify")
	// 如果需要权限证明，请进行设置
	var engine consensus.Engine
	if chainConfig.Engine == "Clique" { //POA
		//engine = clique.New(entity.chainConfig.Clique, db)
	} else { //POW
		switch config.PowMode {
		case ModeFake:
			log.Warn("Octell used in fake mode")
		case ModeTest:
			log.Warn("Octell used in test mode")
		case ModeShared:
			log.Warn("Octell used in shared mode")
		}
		engine = New(&Config{
			PowMode: config.PowMode,
			//CacheDir:         stack.ResolvePath(o.config.CacheDir),
			CachesInMem:      config.CachesInMem,
			CachesOnDisk:     config.CachesOnDisk,
			CachesLockMmap:   config.CachesLockMmap,
			DatasetDir:       config.DatasetDir,
			DatasetsInMem:    config.DatasetsInMem,
			DatasetsOnDisk:   config.DatasetsOnDisk,
			DatasetsLockMmap: config.DatasetsLockMmap,
			NotifyFull:       config.NotifyFull,
		}, operationutils.ArrayByInter(notify), noverify)
		engine.(*Octell).SetThreads(-1) // 禁用CPU工作
	}
	return beacon.New(engine)
}

// New创建一个全尺寸的octell PoW方案，并启动用于远程挖掘的后台线程，还可以选择通知一批远程服务新的工作包。
func New(config *Config, notify []string, noverify bool) *Octell {
	//if config.Log == nil {
	//	config.Log = log.Root()
	//}
	if config.CachesInMem <= 0 {
		log.Warn("One octell cache must always be in memory", "requested", config.CachesInMem)
		config.CachesInMem = 1
	}
	if config.CacheDir != "" && config.CachesOnDisk > 0 {
		log.Info("Disk storage enabled for octell caches", "dir", config.CacheDir, "count", config.CachesOnDisk)
	}
	if config.DatasetDir != "" && config.DatasetsOnDisk > 0 {
		log.Info("Disk storage enabled for octell DAGs", "dir", config.DatasetDir, "count", config.DatasetsOnDisk)
	}
	octell := &Octell{
		Config:   config,
		caches:   newlru("cache", config.CachesInMem, newCache),
		datasets: newlru("dataset", config.DatasetsInMem, newDataset),
		update:   make(chan struct{}),
		//hashrate: metrics.NewMeterForced(),
	}
	if config.PowMode == ModeShared {
		octell.shared = sharedOctell
	}
	octell.remote = startRemoteSealer(octell, notify, noverify)
	return octell
}

//cache尝试检索指定块号的验证缓存，方法是首先检查内存中的缓存列表，然后检查存储在磁盘上的缓存，如果找不到缓存，最后生成一个。
func (octell *Octell) cache(block uint64) *cache {
	epoch := block / epochLength
	currentI, futureI := octell.caches.get(epoch)
	current := currentI.(*cache)

	// 等待生成完成。
	current.generate(octell.Config.CacheDir, octell.Config.CachesOnDisk, octell.Config.CachesLockMmap, octell.Config.PowMode == ModeTest)

	// 如果我们需要一个新的未来缓存，现在是重新生成它的好时机。
	if futureI != nil {
		future := futureI.(*cache)
		go future.generate(octell.Config.CacheDir, octell.Config.CachesOnDisk, octell.Config.CachesLockMmap, octell.Config.PowMode == ModeTest)
	}
	return current
}

// SeedHash是用于生成验证缓存和挖掘数据集的种子。
func SeedHash(block uint64) []byte {
	return seedHash(block)
}

// SetThreads更新当前启用的挖掘线程数。
//调用此方法不会开始挖掘，只会设置线程数。
//如果指定为零，矿工将使用机器的所有芯。允许将线程数设置为零以下，并将导致矿工闲置，而不进行任何工作。
func (octell *Octell) SetThreads(threads int) {
	octell.lock.Lock()
	defer octell.lock.Unlock()

	// 如果我们运行的是共享PoW，请将线程数设置为该PoW
	if octell.shared != nil {
		octell.shared.SetThreads(threads)
		return
	}
	// 更新螺纹并敲打任何运行密封件，以拉入任何更改
	octell.threads = threads
	select {
	case octell.update <- struct{}{}:
	default:
	}
}
