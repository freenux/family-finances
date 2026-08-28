package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"family-finances/internal/domain"
)

// TestStatsTopAPIScope 缺陷 1：/api/stats/top 曾经把口径写死成 domain.ScopeAll，
// 用户在仪表盘选了「日常」，点柱子拉出来的大额榜单照样是那笔装修款。
// 现在必须解析 scope 查询参数并透传，缺省与 /api/stats 一致（daily）。
func TestStatsTopAPIScope(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantScope domain.Scope
	}{
		{"缺省落到 daily", "", domain.ScopeDaily},
		{"空串落到 daily", "&scope=", domain.ScopeDaily},
		{"非法值落到 daily", "&scope=garbage", domain.ScopeDaily},
		{"大小写不匹配也落到 daily", "&scope=ALL", domain.ScopeDaily},
		{"all 生效", "&scope=all", domain.ScopeAll},
		{"special 生效", "&scope=special", domain.ScopeSpecial},
		{"daily 显式生效", "&scope=daily", domain.ScopeDaily},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txRepo := newStubTxRepo()
			h := newSpecialTestHandler(txRepo)

			req := httptest.NewRequest(http.MethodGet,
				"/api/stats/top?period=2026-05&direction=expense&account=family"+tt.query, nil)
			rec := httptest.NewRecorder()
			h.StatsTopAPI(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200（body=%s）", rec.Code, rec.Body.String())
			}
			if len(txRepo.topScopes) != 1 || txRepo.topScopes[0] != tt.wantScope {
				t.Fatalf("TopTransactions 收到口径 %v; want [%s]", txRepo.topScopes, tt.wantScope)
			}
		})
	}
}

// TestStatsTopAPIStillValidatesPeriod 口径解析不能吞掉原有的 period 校验
func TestStatsTopAPIStillValidatesPeriod(t *testing.T) {
	txRepo := newStubTxRepo()
	h := newSpecialTestHandler(txRepo)
	rec := httptest.NewRecorder()
	h.StatsTopAPI(rec, httptest.NewRequest(http.MethodGet, "/api/stats/top?period=nope&scope=all", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
	if len(txRepo.topScopes) != 0 {
		t.Fatalf("period 非法却仍然查了库：%v", txRepo.topScopes)
	}
}

// TestStatsTopAPIStillHonorsLimit limit 仍然生效（口径改动别把它带偏）
func TestStatsTopAPIStillHonorsLimit(t *testing.T) {
	txRepo := newStubTxRepo()
	h := newSpecialTestHandler(txRepo)
	rec := httptest.NewRecorder()
	h.StatsTopAPI(rec, httptest.NewRequest(http.MethodGet,
		"/api/stats/top?period=2026-05&direction=expense&limit=25&scope=special", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v（body=%s）", err, rec.Body.String())
	}
	if len(txRepo.topScopes) != 1 || txRepo.topScopes[0] != domain.ScopeSpecial {
		t.Fatalf("TopTransactions 收到口径 %v; want [special]", txRepo.topScopes)
	}
}
