package upstream

import "testing"

// TestModelRealmsIndex 跨域目录索引（裸模型名自动路由的判定源）：
// 按域记录最近一次成功下发的模型 id，两域都有 → cn 在前；只有一域 → 单域；
// 整体替换保证下架模型立刻从路由判定里消失。
func TestModelRealmsIndex(t *testing.T) {
	c := New()
	if got := c.ModelRealms("glm-5.2"); got != nil {
		t.Fatalf("冷缓存应返回 nil（调用方回落有账号的域）, got %v", got)
	}
	c.storeCatalogIDs("cn", []string{"glm-5.2", "hy3", ""})
	c.storeCatalogIDs("global", []string{"hy3", "gpt-6-sol"})

	if got := c.ModelRealms("glm-5.2"); len(got) != 1 || got[0] != "cn" {
		t.Errorf("glm-5.2 realms=%v want [cn]", got)
	}
	if got := c.ModelRealms("hy3"); len(got) != 2 || got[0] != "cn" || got[1] != "global" {
		t.Errorf("hy3 realms=%v want [cn global]", got)
	}
	if got := c.ModelRealms("gpt-6-sol"); len(got) != 1 || got[0] != "global" {
		t.Errorf("gpt-6-sol realms=%v want [global]", got)
	}
	if got := c.ModelRealms("nope"); got != nil {
		t.Errorf("未知模型 realms=%v want nil", got)
	}
	if got := c.ModelRealms(""); got != nil {
		t.Errorf("空模型名 realms=%v want nil", got)
	}

	// 整体替换：CN 目录刷新后 glm-5.2 不再下发 → 必须从路由候选里消失。
	c.storeCatalogIDs("cn", []string{"hy3"})
	if got := c.ModelRealms("glm-5.2"); got != nil {
		t.Errorf("下架后 realms=%v want nil", got)
	}
	if got := c.ModelRealms("hy3"); len(got) != 2 {
		t.Errorf("hy3 仍应两域可见, got %v", got)
	}
}
