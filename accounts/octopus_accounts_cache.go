package accounts

import (
	"bufio"
	"encoding/json"
	"fmt"
	mapset "github.com/deckarep/golang-set"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus/utils"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//缓存重新加载之间的最短时间。如果平台不支持更改通知，则此限制适用。
//如果密钥库目录还不存在，它也适用，代码最多会尝试创建一个观察者。
const minReloadInterval = 2 * time.Second

// accountCache是密钥库中所有帐户的实时索引。
type accountCache struct {
	keydir   string
	watcher  *watcher
	mu       sync.Mutex
	all      accountsByURL
	byAddr   map[entity.Address][]Account
	throttle *time.Timer
	notify   chan struct{}
	fileC    fileCache
}

func (ac *accountCache) add(newAccount Account) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	i := sort.Search(len(ac.all), func(i int) bool { return ac.all[i].URL.Cmp(newAccount.URL) >= 0 })
	if i < len(ac.all) && ac.all[i] == newAccount {
		return
	}
	//newAccount不在缓存中。
	ac.all = append(ac.all, Account{})
	copy(ac.all[i+1:], ac.all[i:])
	ac.all[i] = newAccount
	ac.byAddr[newAccount.Address] = append(ac.byAddr[newAccount.Address], newAccount)
}

func (ac *accountCache) hasAddress(addr entity.Address) bool {
	ac.maybeReload()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return len(ac.byAddr[addr]) > 0
}

func (ac *accountCache) accounts() []Account {
	ac.maybeReload()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	cpy := make([]Account, len(ac.all))
	copy(cpy, ac.all)
	return cpy
}

func (ac *accountCache) maybeReload() {
	ac.mu.Lock()

	if ac.watcher.running {
		ac.mu.Unlock()
		return //监视程序正在运行，并将保持缓存最新。
	}
	if ac.throttle == nil {
		ac.throttle = time.NewTimer(0)
	} else {
		select {
		case <-ac.throttle.C:
		default:
			ac.mu.Unlock()
			return // 最近重新加载了缓存。
		}
	}
	// 没有监视程序运行，请启动它。
	ac.watcher.start()
	ac.throttle.Reset(minReloadInterval)
	ac.mu.Unlock()
	ac.scanAccounts()
}

// ScanCounts检查文件系统上是否发生了任何更改，并相应地更新帐户缓存
func (ac *accountCache) scanAccounts() error {
	// 扫描整个文件夹元数据以查看文件更改
	creates, deletes, updates, err := ac.fileC.scan(ac.keydir)
	if err != nil {
		log.Debug("Failed to reload keystore contents", "err", err)
		return err
	}
	if creates.Cardinality() == 0 && deletes.Cardinality() == 0 && updates.Cardinality() == 0 {
		return nil
	}
	// 创建助手方法以扫描关键文件的内容
	var (
		buf = new(bufio.Reader)
		key struct {
			Address string `json:"address"`
		}
	)
	readAccount := func(path string) *Account {
		fd, err := os.Open(path)
		if err != nil {
			log.Info("Failed to open keystore file", "path", path, "err", err)
			return nil
		}
		defer fd.Close()
		buf.Reset(fd)
		// 解析地址。
		key.Address = ""
		err = json.NewDecoder(buf).Decode(&key)
		addr := entity.HexToAddress(utils.GetInToStr(key.Address))
		switch {
		case err != nil:
			log.Debug("Failed to decode keystore key", "path", path, "err", err)
		case addr == entity.Address{}:
			log.Debug("Failed to decode keystore key", "path", path, "err", "missing or zero address")
		default:
			return &Account{
				Address: addr,
				URL:     URL{Scheme: KeyStoreScheme, Path: path},
			}
		}
		return nil
	}
	//处理所有文件差异
	start := time.Now()

	for _, p := range creates.ToSlice() {
		if a := readAccount(p.(string)); a != nil {
			ac.add(*a)
		}
	}
	for _, p := range deletes.ToSlice() {
		ac.deleteByFile(p.(string))
	}
	for _, p := range updates.ToSlice() {
		path := p.(string)
		ac.deleteByFile(path)
		if a := readAccount(path); a != nil {
			ac.add(*a)
		}
	}
	end := time.Now()

	select {
	case ac.notify <- struct{}{}:
	default:
	}
	log.Info("Handled keystore changes", "time", end.Sub(start))
	return nil
}

// deleteByFile删除给定路径引用的帐户。
func (ac *accountCache) deleteByFile(path string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	i := sort.Search(len(ac.all), func(i int) bool { return ac.all[i].URL.Path >= path })

	if i < len(ac.all) && ac.all[i].URL.Path == path {
		removed := ac.all[i]
		ac.all = append(ac.all[:i], ac.all[i+1:]...)
		if ba := removeAccount(ac.byAddr[removed.Address], removed); len(ba) == 0 {
			delete(ac.byAddr, removed.Address)
		} else {
			ac.byAddr[removed.Address] = ba
		}
	}
}

//如果存在唯一匹配项，find将返回地址的缓存帐户。准确的匹配规则由账户文档进行解释。账户呼叫者必须持有ac.mu。
func (ac *accountCache) find(a Account) (Account, error) {
	// 如果可能的话，将搜索限制为针对候选人。
	matches := ac.all
	if (a.Address != entity.Address{}) {
		matches = ac.byAddr[a.Address]
	}
	if a.URL.Path != "" {
		// 	如果只指定了basename，请填写路径。
		if !strings.ContainsRune(a.URL.Path, filepath.Separator) {
			a.URL.Path = filepath.Join(ac.keydir, a.URL.Path)
		}
		for i := range matches {
			if matches[i].URL == a.URL {
				return matches[i], nil
			}
		}
		if (a.Address == entity.Address{}) {
			return Account{}, ErrNoMatch
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Account{}, ErrNoMatch
	default:
		err := &AmbiguousAddrError{Addr: a.Address, Matches: make([]Account, len(matches))}
		copy(err.Matches, matches)
		sort.Sort(accountsByURL(err.Matches))
		return Account{}, err
	}
}

func (ac *accountCache) close() {
	ac.mu.Lock()
	ac.watcher.close()
	if ac.throttle != nil {
		ac.throttle.Stop()
	}
	if ac.notify != nil {
		close(ac.notify)
		ac.notify = nil
	}
	ac.mu.Unlock()
}

func removeAccount(slice []Account, elem Account) []Account {
	for i := range slice {
		if slice[i] == elem {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func newAccountCache(keydir string) (*accountCache, chan struct{}) {
	ac := &accountCache{
		keydir: keydir,
		byAddr: make(map[entity.Address][]Account),
		notify: make(chan struct{}, 1),
		fileC:  fileCache{all: mapset.NewThreadUnsafeSet()},
	}
	ac.watcher = newWatcher(ac)
	return ac, ac.notify
}

type accountsByURL []Account

func (s accountsByURL) Len() int           { return len(s) }
func (s accountsByURL) Less(i, j int) bool { return s[i].URL.Cmp(s[j].URL) < 0 }
func (s accountsByURL) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// fileCache是在扫描密钥库期间看到的文件的缓存。
type fileCache struct {
	all     mapset.Set // 密钥库文件夹中的所有文件集
	lastMod time.Time  // 上次修改文件时的实例
	mu      sync.Mutex
}

//scan对给定目录执行新扫描，与已缓存的文件名进行比较，并返回文件集：创建、删除、更新。
func (fc *fileCache) scan(keyDir string) (mapset.Set, mapset.Set, mapset.Set, error) {
	//t0 := time.Now()

	// 列出keystore文件夹中的所有失败
	//files, err := os.Open(keyDir)
	//log.Debug(files)
	//if err != nil {
	//	return nil, nil, nil, err
	//}
	//t1 := time.Now()

	fc.mu.Lock()
	defer fc.mu.Unlock()

	// 迭代所有文件并收集其元数据
	all := mapset.NewThreadUnsafeSet()
	mods := mapset.NewThreadUnsafeSet()

	var newLastMod time.Time
	//for _, fi := range files {
	//	path := filepath.Join(keyDir)
	//	// 跳过文件夹中的任何非关键文件
	//	if nonKeyFile(fi) {
	//		log.Info("Ignoring file on account scan", "path", path)
	//		continue
	//	}
	//	// Gather the set of all and fresly modified files
	//	all.Add(path)
	//
	//	info, err := fi.Info()
	//	if err != nil {
	//		return nil, nil, nil, err
	//	}
	//	modified := info.ModTime()
	//	if modified.After(fc.lastMod) {
	//		mods.Add(path)
	//	}
	//	if modified.After(newLastMod) {
	//		newLastMod = modified
	//	}
	//}
	//t2 := time.Now()

	// 更新跟踪的文件并返回三组
	deletes := fc.all.Difference(all)   // Deletes = previous - current
	creates := all.Difference(fc.all)   // Creates = current - previous
	updates := mods.Difference(creates) // Updates = modified - creates

	fc.all, fc.lastMod = all, newLastMod
	//t3 := time.Now()

	// 报告扫描统计数据并返回
	//log.Debug("FS scan times", "list", t1.Sub(t0), "set", t2.Sub(t1), "diff", t3.Sub(t2))
	return creates, deletes, updates, nil
}

//尝试解锁存在多个文件的地址时，将返回AmbiguousAddError。
type AmbiguousAddrError struct {
	Addr    entity.Address
	Matches []Account
}

func (err *AmbiguousAddrError) Error() string {
	files := ""
	for i, a := range err.Matches {
		files += a.URL.Path
		if i < len(err.Matches)-1 {
			files += ", "
		}
	}
	return fmt.Sprintf("multiple keys match address (%s)", files)
}

type watcher struct{ running bool }

func newWatcher(*accountCache) *watcher { return new(watcher) }
func (*watcher) start()                 {}
func (*watcher) close()                 {}
