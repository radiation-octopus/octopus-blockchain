// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package discover

import (
	"crypto/ecdsa"
	"net"

	"github.com/radiation-octopus/octopus-blockchain/entity/mclock"
	"github.com/radiation-octopus/octopus-blockchain/log"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enode"
	"github.com/radiation-octopus/octopus-blockchain/p2p/enr"
	"github.com/radiation-octopus/octopus-blockchain/p2p/netutil"
)

// UDPConn is a network connection on which discovery can operate.
type UDPConn interface {
	ReadFromUDP(b []byte) (n int, addr *net.UDPAddr, err error)
	WriteToUDP(b []byte, addr *net.UDPAddr) (n int, err error)
	Close() error
	LocalAddr() net.Addr
}

// Config holds settings for the discovery listener.
type Config struct {
	// 需要这些设置并配置UDP侦听器：
	PrivateKey *ecdsa.PrivateKey

	// These settings are optional:
	NetRestrict  *netutil.Netlist   // 允许的IP网络列表
	Bootnodes    []*enode.Node      // 引导节点列表
	Unhandled    chan<- ReadPacket  // 未处理的数据包在此通道上发送
	Log          log.Logger         // 如果设置了，则记录消息到此处
	ValidSchemes enr.IdentityScheme // 允许的标识方案
	Clock        mclock.Clock
}

func (cfg Config) withDefaults() Config {
	if cfg.Log == nil {
		cfg.Log = log.Root()
	}
	if cfg.ValidSchemes == nil {
		cfg.ValidSchemes = enode.ValidSchemes
	}
	if cfg.Clock == nil {
		cfg.Clock = mclock.System{}
	}
	return cfg
}

// ListenUDP starts listening for discovery packets on the given UDP socket.
func ListenUDP(c UDPConn, ln *enode.LocalNode, cfg Config) (*UDPv4, error) {
	return ListenV4(c, ln, cfg)
}

// ReadPacket is a packet that couldn't be handled. Those packets are sent to the unhandled
// channel if configured.
type ReadPacket struct {
	Data []byte
	Addr *net.UDPAddr
}

func min(x, y int) int {
	if x > y {
		return y
	}
	return x
}
