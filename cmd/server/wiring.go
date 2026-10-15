package main

import (
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/server"
)

// realmAwareAvailableForModel 构造会话粘性路由按模型可用口径的 realm 感知闭包。
//
// 粘性分配只记 uid、不记域：裸模型名（新协议）返回**两域并集**（cn 在前），会话可
// 绑定任一域的账号，域合法性由 handler 按本次请求的候选域校验（跨域自动路由）；
// 仍带 "cn:"/"global:" 前缀的旧调用（兼容输入）返回该域单域集合，与旧语义一致。
//
// 显式域前缀存在的意义：老客户端/运维脚本可能仍按旧协议钉死单域，本闭包必须与其
// 对齐，否则粘性会把 global 号分配给 cn 前缀请求（跨域泄漏）。
//
// 返回列表可能对快过期账号重复同一 UID，作为虚拟实例权重；会话哈希分配无需感知
// 权重细节，已有绑定的快路径仍直接返回原账号，不做迁移。
func realmAwareAvailableForModel(p *pool.Pool) func(model string) []string {
	return func(model string) []string {
		realm, bare := server.ResolveModel(model)
		if realm != "" {
			return p.WeightedAvailableUIDsForModelRealm(bare, realm)
		}
		// 裸名：两域并集（cn 在前，权重语义与单域一致）。切片各自独立分配，
		// append 不会串改 cn 那一份。
		cn := p.WeightedAvailableUIDsForModelRealm(bare, "cn")
		gl := p.WeightedAvailableUIDsForModelRealm(bare, "global")
		return append(cn, gl...)
	}
}
