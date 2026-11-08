// Package securerand 使用 crypto/rand 为 math/rand/v2 的标准库算法提供随机源。
package securerand

import (
	"crypto/rand"
	"encoding/binary"
	randv2 "math/rand/v2"
)

type source struct{}

func (source) Uint64() uint64 {
	var b [8]byte
	// crypto/rand.Read 在随机源失败时终止程序，不降级到其他随机源。
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return binary.LittleEndian.Uint64(b[:])
}

// New 返回局部使用的 Rand；调用方不应跨 goroutine 共享它。
func New() *randv2.Rand {
	return randv2.New(source{})
}
