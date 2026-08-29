package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDomainHasNoSQL domain 是纯领域模型层：不引用其他包，也不该出现任何持久化细节。
// 口径过滤的 SQL 片段曾经长在 Scope 上（还要 repo 把表别名当参数传下来），
// 已经挪到 internal/infrastructure/sqlite。这个测试把这条分层规则钉住，
// 免得下次"顺手在 domain 里拼一小段 SQL"又滑回去。
func TestDomainHasNoSQL(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("没扫到 domain 包的源码，测试本身失效了")
	}

	// 大小写敏感：SQL 关键字按惯例大写，中文注释里的普通词不会误伤
	banned := []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "WHERE ", "GROUP BY", "IS NOT NULL", "IS NULL"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, kw := range banned {
			if strings.Contains(string(src), kw) {
				t.Fatalf("%s 里出现了 SQL 片段 %q —— domain 不该知道数据怎么存；"+
					"把它挪到 internal/infrastructure/sqlite", f, kw)
			}
		}
	}
}
