package server

import (
	"strings"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

// resolveModel 解析模型名协议。
//
// 前缀已从「必填的路由协议」退役为「可选的兼容输入」：
//
//	"cn:model" / "global:model" → ("cn"/"global", "model")：显式钉死单域
//	                              （旧客户端/旧脚本仍可照旧调用，输出侧不再出现前缀）
//	裸名                        → ("", model)：域未知，由 routeRealms 按模型目录自动路由
//
// 取第一个 ":"，前段恰为 "cn"/"global" 才视为显式域；其余（裸名、其他命名空间的
// 冒号名）一律当裸名。大小写敏感（前缀必须是精确的小写枚举）。
// bare 即出站/选号/账本使用的裸模型名。
//
// 导出为 ResolveModel（cmd/server 粘性闭包需要），包内简写 resolveModel。
func resolveModel(model string) (realm, bare string) {
	idx := strings.IndexByte(model, ':')
	if idx <= 0 {
		return "", model
	}
	prefix := model[:idx]
	if prefix != "cn" && prefix != "global" {
		return "", model
	}
	return prefix, model[idx+1:]
}

// ResolveModel 是 resolveModel 的导出面（跨包调用）。
func ResolveModel(model string) (realm, bare string) { return resolveModel(model) }

// realmAllowed 报告账号域是否落在候选序列内（realms 为空 = 不限制任何域）。
func realmAllowed(realm string, realms []string) bool {
	if len(realms) == 0 {
		return true
	}
	for _, r := range realms {
		if r == realm {
			return true
		}
	}
	return false
}

// routeRealms 返回本次请求的候选域序列（有序，cn 优先）：
//
//   - 显式 "cn:"/"global:" 前缀（旧协议兼容输入）→ 单域钉死；
//   - 裸名 → 上游目录里有这个模型的域（cn 在前）。两域都有时由 pickAcrossRealms
//     按 attempt 轮换起点，实现「先国内、再国际版」的自动切换；
//   - 目录未覆盖（尚未探测 / 探测失败 / 上游新模型）→ 池里**实际有账号**的域（cn 优先）。
//     纯 global 池 + 冷目录下裸名照样能路由（此前裸名恒 cn，纯 global 池必 503）；
//   - global.enabled=false（纯 CN 逃生门）→ 恒 ["cn"]。
//
// 恒返回非空序列（最差 ["cn"]），调用方无需处理空候选。
func (h *Handler) routeRealms(bare, pinned string) []string {
	if pinned != "" {
		return []string{pinned}
	}
	if !h.cfg.GlobalEnabled {
		return []string{"cn"}
	}
	if h.cfg.Upstream != nil {
		if realms := h.cfg.Upstream.ModelRealms(bare); len(realms) > 0 {
			return realms
		}
	}
	out := make([]string, 0, 2)
	for _, realm := range [...]string{"cn", "global"} {
		if total, _, _, _, _ := h.cfg.Pool.CountsDetailedForRealm(realm); total > 0 {
			out = append(out, realm)
		}
	}
	if len(out) == 0 {
		return []string{"cn"}
	}
	return out
}

// pickAcrossRealms 按候选域选号：attempt 序号决定起始域（realms[attempt%n]），起始域
// 此刻给不出账号时按序跨域兜底——即「先国内、再国际版」的自动路由。
// realms 为空 = 不过滤（等价 PickExcludingForModel）。
func (h *Handler) pickAcrossRealms(tried map[string]bool, model string, realms []string, attempt int) *auth.Auth {
	if len(realms) == 0 {
		return h.cfg.Pool.PickExcludingForRealm(tried, model, "")
	}
	n := len(realms)
	start := attempt % n
	if start < 0 {
		start = 0
	}
	ordered := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ordered = append(ordered, realms[(start+i)%n])
	}
	return h.cfg.Pool.PickExcludingForRealms(tried, model, ordered)
}
