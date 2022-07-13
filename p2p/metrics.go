package p2p

import (
	"github.com/radiation-octopus/octopus-blockchain/metrics"
	"net"
)

const (
	// IngressTerName 是每个数据包入站指标的前缀。
	ingressMeterName = "p2p/ingress"

	// egressMeterName 是每个数据包出站度量的前缀。
	egressMeterName = "p2p/egress"

	// HandleHistName 是每个数据包服务时间直方图的前缀。
	HandleHistName = "p2p/handle"
)

// meteredConn是一种网络包装。测量入站和出站网络流量的连接。
type meteredConn struct {
	net.Conn
}

// newMeteredConn创建一个新的按流量计费的连接，碰撞入口或出口连接表，
//并增加按流量计费的对等计数。如果禁用了度量系统，函数将返回原始连接。
func newMeteredConn(conn net.Conn, ingress bool, addr *net.TCPAddr) net.Conn {
	//禁用指标时短路
	if !metrics.Enabled {
		return conn
	}
	// 碰撞连接计数器并包装连接
	//if ingress {
	//	ingressConnectMeter.Mark(1)
	//} else {
	//	egressConnectMeter.Mark(1)
	//}
	//activePeerGauge.Inc(1)
	return &meteredConn{Conn: conn}
}
