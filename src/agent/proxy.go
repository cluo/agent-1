package main

import (
	"fmt"
	"os"
	"time"

	log "github.com/gonet2/libs/nsq-logger"
	"github.com/peterbourgon/g2s"
)

import (
	"client_handler"
	"misc/packet"
	. "types"
	"utils"
)

const (
	STATSD_PREFIX       = "API.NR"
	ENV_STATSD          = "STATSD_HOST"
	DEFAULT_STATSD_HOST = "127.0.0.1:8125"
)

var (
	_statter g2s.Statter
)

func init() {
	addr := DEFAULT_STATSD_HOST
	if env := os.Getenv(ENV_STATSD); env != "" {
		addr = env
	}

	s, err := g2s.Dial("udp", addr)
	if err != nil {
		log.Critical(err)
		os.Exit(-1)
	}
	_statter = s
}

// client protocol handle proxy
func proxy_user_request(sess *Session, p []byte) []byte {
	start := time.Now()
	defer utils.PrintPanicStack(sess, p)
	// 解密
	if sess.Flag&SESS_ENCRYPT != 0 {
		sess.Decoder.XORKeyStream(p, p)
	}

	// 封装为reader
	reader := packet.Reader(p)

	// 读客户端数据包序列号(1,2,3...)
	// 可避免重放攻击-REPLAY-ATTACK
	seq_id, err := reader.ReadU32()
	if err != nil {
		log.Error("read client timestamp failed:", err)
		sess.Flag |= SESS_KICKED_OUT
		return nil
	}

	// 数据包序列号验证
	if seq_id != sess.PacketCount {
		log.Errorf("illegal packet sequence id:%v should be:%v size:%v", seq_id, sess.PacketCount, len(p)-6)
		sess.Flag |= SESS_KICKED_OUT
		return nil
	}

	// 读协议号
	b, err := reader.ReadS16()
	if err != nil {
		log.Error("read protocol number failed.")
		sess.Flag |= SESS_KICKED_OUT
		return nil
	}

	// 根据协议号断做服务划分
	var ret []byte
	if b > MAX_PROTO_NUM {
		if err := forward(sess, p[4:]); err != nil {
			log.Errorf("service id:%v execute failed, error:%v", b, err)
			sess.Flag |= SESS_KICKED_OUT
			return nil
		}
	} else {
		if h := client_handler.Handlers[b]; h != nil {
			ret = h(sess, reader)
		} else {
			log.Errorf("service id:%v not bind", b)
			sess.Flag |= SESS_KICKED_OUT
			return nil
		}
	}

	// 统计处理时间
	elasped := time.Now().Sub(start)
	if b != 0 { // 排除心跳包日志
		log.Trace("[REQ]", client_handler.RCode[b])
		_statter.Timing(1.0, fmt.Sprintf("%v%v", STATSD_PREFIX, client_handler.RCode[b]), elasped)
	}
	return ret
}
