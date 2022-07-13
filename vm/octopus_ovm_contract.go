package vm

import (
	"crypto/sha256"
	"errors"
	"github.com/holiman/uint256"
	"github.com/radiation-octopus/octopus-blockchain/crypto"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"github.com/radiation-octopus/octopus-blockchain/operationutils"
	"golang.org/x/crypto/ripemd160"
	"math/big"
)

type ContractRef interface {
	Address() entity.Address
}

// SHA256作为本机契约实现。
type sha256hash struct{}

// RequiredGas返回执行预编译合同所需的气体。
// 这种方法不需要任何溢出检查，因为任何重要的输入尺寸气体成本都很高，无法支付。
func (c *sha256hash) RequiredGas(input []byte) uint64 {
	return uint64(len(input)+31)/32*entity.Sha256PerWordGas + entity.Sha256BaseGas
}
func (c *sha256hash) Run(input []byte) ([]byte, error) {
	h := sha256.Sum256(input)
	return h[:], nil
}

// RIPEMD160作为本机合同实现。
type ripemd160hash struct{}

// RequiredGas返回执行预编译合同所需的气体。
//这种方法不需要任何溢出检查，因为任何重要的输入尺寸气体成本都很高，无法支付。
func (c *ripemd160hash) RequiredGas(input []byte) uint64 {
	return uint64(len(input)+31)/32*entity.Ripemd160PerWordGas + entity.Ripemd160BaseGas
}
func (c *ripemd160hash) Run(input []byte) ([]byte, error) {
	ripemd := ripemd160.New()
	ripemd.Write(input)
	return operationutils.LeftPadBytes(ripemd.Sum(nil), 32), nil
}

// 作为本机约定实现的数据拷贝。
type dataCopy struct{}

// RequiredGas返回执行预编译合同所需的气体。
//这种方法不需要任何溢出检查，因为任何重要的输入尺寸气体成本都很高，无法支付。
func (c *dataCopy) RequiredGas(input []byte) uint64 {
	return uint64(len(input)+31)/32*entity.IdentityPerWordGas + entity.IdentityBaseGas
}
func (c *dataCopy) Run(in []byte) ([]byte, error) {
	return in, nil
}

//合同表示状态数据库中的以太坊合同。它包含合同代码，调用参数。合同执行ContractRef
type Contract struct {
	// CallerAddress是初始化此合同的呼叫者的结果
	CallerAddress entity.Address
	caller        ContractRef
	self          ContractRef

	jumpdests map[entity.Hash]bitvec //JUMPDEST分析的汇总结果。
	analysis  bitvec                 // JUMPDEST分析的本地缓存结果

	Code     []byte
	CodeHash entity.Hash
	CodeAddr *entity.Address
	Input    []byte

	Gas   uint64
	value *big.Int
}

type AccountRef entity.Address

func (ar AccountRef) Address() entity.Address { return (entity.Address)(ar) }

func (c *Contract) UseGas(gas uint64) (ok bool) {
	if c.Gas < gas {
		return false
	}
	c.Gas -= gas
	return true
}

type PrecompiledContract interface {
	RequiredGas(input []byte) uint64  // RequiredPrice calculates the contract gas use
	Run(input []byte) ([]byte, error) // Run runs the precompiled contract
}

type ecrecover struct{}

func (c *ecrecover) RequiredGas(input []byte) uint64 {
	return entity.EcrecoverGas
}
func (c *ecrecover) Run(input []byte) ([]byte, error) {
	const ecRecoverInputLength = 128

	input = operationutils.RightPadBytes(input, ecRecoverInputLength)
	// "input" is (hash, v, r, s), each 32 bytes
	// but for ecrecover we want (r, s, v)

	r := new(big.Int).SetBytes(input[64:96])
	s := new(big.Int).SetBytes(input[96:128])
	v := input[63] - 27

	//更紧密的sig s值输入homestead仅适用于tx sig
	if !allZero(input[32:63]) || !crypto.ValidateSignatureValues(v, r, s, false) {
		return nil, nil
	}
	// 我们必须确保不修改“输入”，因此需要在新的分配上放置“v”和签名
	sig := make([]byte, 65)
	copy(sig, input[64:128])
	sig[64] = v
	// 对于libsecp256k1，v需要在末尾
	pubKey, err := crypto.Ecrecover(input[:32], sig)
	// 确保公钥有效
	if err != nil {
		return nil, nil
	}

	// 比特币遗产
	return operationutils.LeftPadBytes(crypto.Keccak256(pubKey[1:])[12:], 32), nil
}

var PrecompiledContractsHomestead = map[entity.Address]PrecompiledContract{
	entity.BytesToAddress([]byte{1}): &ecrecover{},
	entity.BytesToAddress([]byte{2}): &sha256hash{},
	entity.BytesToAddress([]byte{3}): &ripemd160hash{},
	entity.BytesToAddress([]byte{4}): &dataCopy{},
}

var (
	PrecompiledAddressesBerlin    []entity.Address
	PrecompiledAddressesIstanbul  []entity.Address
	PrecompiledAddressesByzantium []entity.Address
	PrecompiledAddressesHomestead []entity.Address
)

// ActivePrecompiles returns the precompiles enabled with the current configuration.
func ActivePrecompiles(rules entity.Rules) []entity.Address {
	switch {
	case rules.IsBerlin:
		return PrecompiledAddressesBerlin
	case rules.IsIstanbul:
		return PrecompiledAddressesIstanbul
	case rules.IsByzantium:
		return PrecompiledAddressesByzantium
	default:
		return PrecompiledAddressesHomestead
	}
}

//RunPrecompiledContract运行并评估预编译协定的输出。
//它回来了
//-返回的字节，
//-剩余气体，
//-发生的任何错误
func RunPrecompiledContract(p PrecompiledContract, input []byte, suppliedGas uint64) (ret []byte, remainingGas uint64, err error) {
	gasCost := p.RequiredGas(input)
	if suppliedGas < gasCost {
		return nil, 0, errors.New("gas用完")
	}
	suppliedGas -= gasCost
	output, err := p.Run(input)
	return output, suppliedGas, err
}

// 调用者返回合同的调用者。当契约是委托调用时，调用方将递归调用调用方，包括调用方的调用方的委托调用。
func (c *Contract) Caller() entity.Address {
	return c.CallerAddress
}

func (c *Contract) validJumpdest(dest *uint256.Int) bool {
	udest, overflow := dest.Uint64WithOverflow()
	// PC不能超过len（代码），当然也不能超过63位。在这种情况下，不用麻烦检查JUMPDEST。
	if overflow || udest >= uint64(len(c.Code)) {
		return false
	}
	// 目的地只允许跳转
	if OpCode(c.Code[udest]) != JUMPDEST {
		return false
	}
	return c.isCode(udest)
}

func (c *Contract) Address() entity.Address {
	return c.self.Address()
}

// 如果提供的PC位置是实际的操作码，而不是PUSH操作后的数据段，则isCode返回true。
func (c *Contract) isCode(udest uint64) bool {
	// w们已经有分析了吗？
	if c.analysis != nil {
		return c.analysis.codeSegment(udest)
	}
	// 我们已经有合同了吗？如果我们有一个散列，这意味着它是一个“常规”合同。
	//对于常规契约（不是临时initcode），我们将分析存储在映射中
	if c.CodeHash != (entity.Hash{}) {
		// 父上下文有分析吗？
		analysis, exist := c.jumpdests[c.CodeHash]
		if !exist {
			// 进行分析并保存在父上下文中，我们不需要将其存储在c.analysis中
			analysis = codeBitmap(c.Code)
			c.jumpdests[c.CodeHash] = analysis
		}
		// 同时将其保存在当前合同中，以便更快地访问
		c.analysis = analysis
		return analysis.codeSegment(udest)
	}
	// 我们没有代码散列，很可能是一段尚未处于状态trie的initcode。在这种情况下，我们进行分析，并将其保存在本地
	//，因此我们不必为执行中的每个跳转指令重新计算它。然而，我们不将其保存在父上下文中
	if c.analysis == nil {
		c.analysis = codeBitmap(c.Code)
	}
	return c.analysis.codeSegment(udest)
}

//返回ovm新合同环境
func NewContract(caller ContractRef, object ContractRef, value *big.Int, gas uint64) *Contract {
	c := &Contract{CallerAddress: caller.Address(), caller: caller, self: object}

	//if parent, ok := caller.(*Contract); ok {
	//	//
	//	c.jumpdests = parent.jumpdests
	//} else {
	//	c.jumpdests = make(map[blockchain.Hash]bitvec)
	//}

	c.Gas = gas

	c.value = value

	return c
}

// AsDelegate将协定设置为委托调用并返回当前协定（用于链接调用）
func (c *Contract) AsDelegate() *Contract {
	// 注意：呼叫方必须始终是合同方。打电话的人不应该是合同以外的人。
	parent := c.caller.(*Contract)
	c.CallerAddress = parent.CallerAddress
	c.value = parent.value

	return c
}

func (c *Contract) SetCallCode(addr *entity.Address, hash entity.Hash, code []byte) {
	c.Code = code
	c.CodeHash = hash
	c.CodeAddr = addr
}

// GetOp returns the n'th element in the contract's byte array
func (c *Contract) GetOp(n uint64) OpCode {
	if n < uint64(len(c.Code)) {
		return OpCode(c.Code[n])
	}

	return STOP
}
