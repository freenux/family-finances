package web

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// ---- 统计页前端脚本的两条约定 ----
//
// 这两条都在 Go 侧测不到运行时行为，但它们各自对应一个真实缺陷，
// 所以直接对嵌进二进制的脚本源码做断言，改坏了立刻红。

// readStatic 读一份真正嵌进二进制的静态资源（StaticFS 根就是 static/）
func readStatic(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(StaticFS(), name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// readTemplate 读一份模板源码（模板 FS 未导出，直接读包目录下的同一份文件）
func readTemplate(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("template/" + name)
	if err != nil {
		t.Fatalf("read template/%s: %v", name, err)
	}
	return string(b)
}

// funcBody 从 src 里按大括号配对截出以 header 开头的那个函数体
func funcBody(t *testing.T, src, header string) string {
	t.Helper()
	i := strings.Index(src, header)
	if i < 0 {
		t.Fatalf("脚本里找不到 %q", header)
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		t.Fatalf("%q 后面没有函数体", header)
	}
	depth := 0
	for j := i + open; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i+open : j+1]
			}
		}
	}
	t.Fatalf("%q 的大括号没有配平", header)
	return ""
}

// TestStatsPageFetchesCarryScope 两个远端请求都必须带上当前口径。
//
// 缺陷：/api/stats 带了 scope，/api/stats/top 没带 —— 后端于是永远收到缺省 daily。
// 用户在仪表盘切到「全部/仅专项」再点柱子，下面的 Top 榜单还是那份日常流水，
// 看起来就像筛选没生效（后端这一半已经修好，前端不带参数等于白修）。
func TestStatsPageFetchesCarryScope(t *testing.T) {
	src := readStatic(t, "js/stats_page.js")

	tests := []struct {
		name     string
		header   string
		wantPath string
	}{
		{"主视图 /api/stats", "async fetchView()", "/api/stats?"},
		{"点柱子拉 Top 榜单 /api/stats/top", "async fetchFocusTop()", "/api/stats/top?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := funcBody(t, src, tt.header)
			if !strings.Contains(body, tt.wantPath) {
				t.Fatalf("%s 没有请求 %s:\n%s", tt.header, tt.wantPath, body)
			}
			if !strings.Contains(body, "scope: this.scope") {
				t.Fatalf("%s 的查询参数里没有 scope，后端会落到缺省 daily:\n%s", tt.header, body)
			}
		})
	}
}

// TestScopeLabelsDefinedOnce 「日常/全部/仅专项」这组文案只留一份。
// 原本 Go 侧有个零调用的 domain.Scope.Label()，模板里一份按钮文案，JS 里再一份 map，
// 三份改一处忘一处。现在按钮由 JS 的 SCOPES 渲染，scopeLabel() 也从它取。
func TestScopeLabelsDefinedOnce(t *testing.T) {
	js := readStatic(t, "js/stats_page.js")
	html := readTemplate(t, "pages/stats.html")

	for _, label := range []string{"日常", "全部", "仅专项"} {
		if !strings.Contains(js, "label: '"+label+"'") {
			t.Fatalf("stats_page.js 的 SCOPES 里缺少口径 %q", label)
		}
	}
	// 模板不再自己写按钮：三个 setScope 字面量都不该出现
	for _, key := range []string{"daily", "all", "special"} {
		if lit := "setScope('" + key + "')"; strings.Contains(html, lit) {
			t.Fatalf("stats.html 里还留着写死的 %s；按钮应由 x-for=\"s in scopes\" 渲染", lit)
		}
	}
	if !strings.Contains(html, `x-for="s in scopes"`) {
		t.Fatal(`stats.html 里没有 x-for="s in scopes"：口径按钮没有从 JS 的 SCOPES 渲染`)
	}
	// scopeLabel() 不能再自带一份 map
	body := funcBody(t, js, "scopeLabel()")
	for _, label := range []string{"日常", "全部", "仅专项"} {
		if strings.Contains(body, "'"+label+"'") {
			t.Fatalf("scopeLabel() 里又抄了一份文案 %q；应从 SCOPES 取", label)
		}
	}
}
