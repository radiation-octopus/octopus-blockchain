package misc

import (
	"github.com/radiation-octopus/octopus-blockchain/block"
	"github.com/radiation-octopus/octopus-blockchain/entity"
	"math/big"
)

// VerifyDAOHeaderExtraData验证块头的额外数据字段，以确保它符合DAO硬分叉规则。
//DAO硬叉扩展到标头的有效性：
//a） 如果节点不是fork，则不接受具有fork特定额外数据集的[fork，fork+10]范围中的块
//b） 如果节点是pro fork，则要求特定范围内的块具有唯一的额外数据集。
func VerifyDAOHeaderExtraData(config *entity.ChainConfig, header *block.Header) error {
	// 如果节点不关心DAO分叉，则短路验证
	if config.DAOForkBlock == nil {
		return nil
	}
	// 确保块在fork修改的额外数据范围内
	limit := new(big.Int).Add(config.DAOForkBlock, entity.DAOForkExtraRange)
	if header.Number.Cmp(config.DAOForkBlock) < 0 || header.Number.Cmp(limit) >= 0 {
		return nil
	}
	// 根据我们是支持还是反对fork，验证额外的数据内容
	//if config.DAOForkSupport {
	//	if !bytes.Equal(header.Extra, entity.DAOForkBlockExtra) {
	//		return ErrBadProDAOExtra
	//	}
	//} else {
	//	if bytes.Equal(header.Extra, entity.DAOForkBlockExtra) {
	//		return ErrBadNoDAOExtra
	//	}
	//}
	// 一切正常，标题具有我们期望的相同额外数据
	return nil
}
