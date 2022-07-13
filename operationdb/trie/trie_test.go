package trie

import (
	"fmt"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/rlp"
	"github.com/radiation-octopus/octopus-blockchain/typedb/memorydb"
	"github.com/radiation-octopus/octopus/utils"
	"math/big"
	"testing"
)

func TestMissingNodeDisk(t *testing.T)    { testMissingNode(t, false) }
func TestMissingNodeMemonly(t *testing.T) { testMissingNode(t, true) }

func testMissingNode(t *testing.T, memonly bool) {
	//构建内存数据库
	diskdb := memorydb.New()
	//构建默克树数据库
	triedb := NewTrieDatabase(diskdb)

	//构建树结构体
	trie := NewEmpty(triedb)
	strie := &SecureTrie{
		trie: *trie,
	}
	//账户
	addr := "0x6B1d1FD04cDEFa7316E52F2bC29014caB34bCe5E"
	data := &entity.StateAccount{
		Balance: big.NewInt(1000),
	}
	updateAccount(strie, addr, data)
	updateString(trie, "120000", "qwerqwerqwerqwerqwerqwerqwerqwer")
	updateString(trie, "123456", "asdfasdfasdfasdfasdfasdfasdfasdf")

	root, _, _ := strie.Commit(nil)
	if !memonly {
		triedb.Commit(root, true, nil)
	}

	trie, _ = NewTrie(entity.Hash{}, root, triedb)

	enc, err := strie.TryGet([]byte(addr))
	d := new(entity.StateAccount)
	if err := rlp.DecodeBytes(enc, d); err != nil {
		log.Error("Failed to decode operation object", "addr", addr, "err", err)
	}
	fmt.Println(d.Balance)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	_, err = trie.TryGet([]byte("120099"))
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	_, err = trie.TryGet([]byte("123456"))
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	err = trie.TryUpdate([]byte("120099"), []byte("zxcvzxcvzxcvzxcvzxcvzxcvzxcvzxcv"))
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	err = trie.TryDelete([]byte("123456"))
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	hash := entity.BytesToHash(utils.Hex2Bytes("0xe1d943cc8f061a0c0b98162830b970395ac9315654824bf21b73b891365262f9"))
	if memonly {
		delete(triedb.dirties, hash)
	} else {
		diskdb.Delete(hash[:])
	}

	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	_, err = trie.TryGet([]byte("120000"))
	if _, ok := err.(*MissingNodeError); !ok {
		t.Errorf("Wrong error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	_, err = trie.TryGet([]byte("120099"))
	if _, ok := err.(*MissingNodeError); !ok {
		t.Errorf("Wrong error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	_, err = trie.TryGet([]byte("123456"))
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	err = trie.TryUpdate([]byte("120099"), []byte("zxcv"))
	if _, ok := err.(*MissingNodeError); !ok {
		t.Errorf("Wrong error: %v", err)
	}
	trie, _ = NewTrie(entity.Hash{}, root, triedb)
	err = trie.TryDelete([]byte("123456"))
	if _, ok := err.(*MissingNodeError); !ok {
		t.Errorf("Wrong error: %v", err)
	}
}

func updateString(trie *Trie, k, v string) {
	trie.Update([]byte(k), []byte(v))
}
func updateAccount(t *SecureTrie, k string, account *entity.StateAccount) {
	data, err := rlp.EncodeToBytes(account)
	if err != nil {
		log.Error(err)
	}
	t.trie.Update(t.hashKey([]byte(k)), data)
}
