package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// resetDynamicModelsCache 清空 CN 目录缓存并在用例结束后再清一次（包级缓存跨用例共享）。
func resetDynamicModelsCache(t *testing.T) {
	t.Helper()
	clear := func() {
		dynamicModelsCache.Lock()
		dynamicModelsCache.ids = nil
		dynamicModelsCache.fetched = time.Time{}
		dynamicModelsCache.lastFail = time.Time{}
		dynamicModelsCache.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// TestResolveModelProtocol 模型名协议：显式域前缀仍被识别为「钉死单域」（旧客户端兼容输入），
// 裸名返回空域 = 交给目录自动路由；其他命名空间的冒号名/大小写变体不得被误剥。
func TestResolveModelProtocol(t *testing.T) {
	cases := []struct {
		in       string
		want     string
		wantBare string
	}{
		{"glm-5.2", "", "glm-5.2"},
		{"cn:glm-5.2", "cn", "glm-5.2"},
		{"global:gpt-6-sol", "global", "gpt-6-sol"},
		{"openai:gpt-4", "", "openai:gpt-4"}, // 非 cn/global 命名空间不剥
		{"CN:glm-5.2", "", "CN:glm-5.2"},     // 大小写敏感：前缀必须精确小写
		{":leading", "", ":leading"},         // 冒号在首位不算前缀
		{"", "", ""},
	}
	for _, c := range cases {
		realm, bare := resolveModel(c.in)
		if realm != c.want || bare != c.wantBare {
			t.Errorf("resolveModel(%q)=(%q,%q) want (%q,%q)", c.in, realm, bare, c.want, c.wantBare)
		}
	}
}

// TestRouteRealmsByAccountDirectory 候选域解析：
//   - 目录未覆盖（冷缓存/探测失败）→ 回落「池里有账号的域」，纯 global 池 + 裸名不再恒 503；
//   - 显式前缀 → 钉死单域；
//   - global 逃生门关闭 → 恒 cn。
func TestRouteRealmsByAccountDirectory(t *testing.T) {
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })

	onlyGlobal := testPoolWith(&auth.Auth{UID: "g1", Domain: "www.workbuddy.ai", AccessToken: "at", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: onlyGlobal, Upstream: upstream.New(), GlobalEnabled: true})
	if got := h.routeRealms("whatever-model", ""); len(got) != 1 || got[0] != "global" {
		t.Errorf("pure-global pool routeRealms=%v want [global]", got)
	}

	mixed := testPoolWith(
		&auth.Auth{UID: "cn1", AccessToken: "at", ExpiresAt: 9999999999},
		&auth.Auth{UID: "g1", Domain: "www.workbuddy.ai", AccessToken: "at", ExpiresAt: 9999999999},
	)
	hMixed := NewHandler(Config{Pool: mixed, Upstream: upstream.New(), GlobalEnabled: true})
	if got := hMixed.routeRealms("whatever-model", ""); len(got) != 2 || got[0] != "cn" || got[1] != "global" {
		t.Errorf("mixed pool routeRealms=%v want [cn global]", got)
	}
	if got := hMixed.routeRealms("whatever-model", "global"); len(got) != 1 || got[0] != "global" {
		t.Errorf("pinned realm routeRealms=%v want [global]", got)
	}

	hOff := NewHandler(Config{Pool: mixed, Upstream: upstream.New(), GlobalEnabled: false})
	if got := hOff.routeRealms("whatever-model", ""); len(got) != 1 || got[0] != "cn" {
		t.Errorf("global disabled routeRealms=%v want [cn]", got)
	}
}

// TestChatBareModelRoutesToGlobalOnlyPool 裸模型名 + 纯国际版账号池：自动路由到 global 账号
// 正常出票（前缀时代裸名恒 cn，纯 global 池必然 no_healthy_account）。
func TestChatBareModelRoutesToGlobalOnlyPool(t *testing.T) {
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })

	calls := 0
	up := newFakeUpstream(t, func(string) (int, string, bool) {
		calls++
		return 200, sseOK, true
	})
	up.GlobalEnabled = true
	p := testPoolWith(&auth.Auth{UID: "g1", Domain: "www.workbuddy.ai", AccessToken: "at", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up, GlobalEnabled: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-6-sol","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if calls != 1 {
		t.Errorf("upstream calls=%d want 1", calls)
	}
}

// TestModelsDedupAcrossRealms 同名模型两域都有 → /v1/models 只出一条裸名，realms 数组
// 同时标出两个域（「模型名字就是名字 + 自动路由」的核心契约）。
func TestModelsDedupAcrossRealms(t *testing.T) {
	auth.SetGlobalEnabled(true)
	t.Cleanup(func() { auth.SetGlobalEnabled(true) })
	resetDynamicModelsCache(t)

	catalog := `{"code":0,"data":{"models":[` +
		`{"id":"hy3","maxInputTokens":192000,"maxOutputTokens":64000},` +
		`{"id":"gpt-6-sol","maxInputTokens":1000000,"maxOutputTokens":393000}],` +
		`"agents":[{"name":"cli","models":["hy3","gpt-6-sol"]}]}}`
	up := newFakeUpstream(t, func(string) (int, string, bool) { return 200, catalog, false })
	up.GlobalEnabled = true

	p := testPoolWith(
		&auth.Auth{UID: "cn1", AccessToken: "at-cn", ExpiresAt: 9999999999},
		&auth.Auth{UID: "g1", Domain: "www.workbuddy.ai", AccessToken: "at-gl", ExpiresAt: 9999999999},
	)
	h := NewHandler(Config{Pool: p, Upstream: up, GlobalEnabled: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("want 2 deduped models, got %d: %v", len(resp.Data), resp.Data)
	}
	for _, m := range resp.Data {
		id, _ := m["id"].(string)
		if strings.HasPrefix(id, "cn:") || strings.HasPrefix(id, "global:") {
			t.Errorf("model id must be bare, got %q", id)
		}
		rs, _ := m["realms"].([]any)
		if len(rs) != 2 || rs[0] != "cn" || rs[1] != "global" {
			t.Errorf("%s realms=%v want [cn global]", id, m["realms"])
		}
	}
}
