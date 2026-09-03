package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"family-finances/internal/adapter/bill"
	"family-finances/internal/domain"
	"family-finances/internal/usecase"
)

// CSV 导入：上传预览（暂存临时目录）→ 映射 → 走 ImportBill 同一链路。
// 隐私铁律：原始文件解析后即删；预览时顺手清理 1 小时前的陈旧暂存。

const csvTempKeepDuration = time.Hour

func csvTempDir() string {
	return filepath.Join(os.TempDir(), "family-finances-csv")
}

var csvTokenPattern = regexp.MustCompile(`^[a-f0-9-]{36}$`)

func csvTempPath(token string) (string, error) {
	if !csvTokenPattern.MatchString(token) {
		return "", fmt.Errorf("非法的文件 token")
	}
	return filepath.Join(csvTempDir(), token+".csv"), nil
}

type csvImportVM struct {
	pageBase
	TemplatesJSON string
}

// CSVImportPage GET /imports/csv —— 映射导入页
func (h *Handler) CSVImportPage(w http.ResponseWriter, r *http.Request) {
	templates, err := h.templateRepo.ListTemplates(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	type tplJSON struct {
		Name    string          `json:"name"`
		Mapping json.RawMessage `json:"mapping"`
	}
	out := make([]tplJSON, 0, len(templates))
	for _, t := range templates {
		out = append(out, tplJSON{Name: t.Name, Mapping: json.RawMessage(t.Mapping)})
	}
	b, _ := json.Marshal(out)
	vm := csvImportVM{
		pageBase:      pageBase{Title: "CSV 导入", Nav: "imports", Flash: h.flash.pop(w, r)},
		TemplatesJSON: string(b),
	}
	h.renderPage(w, http.StatusOK, "csv_import", vm)
}

// CSVPreview POST /api/imports/csv/preview —— 上传暂存 + 返回前 8 行原始值
func (h *Handler) CSVPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "上传内容无效或超过 32MB 限制", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "请选择 CSV 文件", http.StatusBadRequest)
		return
	}
	defer file.Close()

	dir := csvTempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		h.serverError(w, err)
		return
	}
	cleanupStaleCSVTemp(dir)

	token := newID()
	path, err := csvTempPath(token)
	if err != nil {
		h.serverError(w, err)
		return
	}
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		_ = os.Remove(path)
		h.serverError(w, err)
		return
	}
	dst.Close()

	f, err := os.Open(path)
	if err != nil {
		h.serverError(w, err)
		return
	}
	records, parseErr := bill.ReadCSVRecords(f)
	f.Close()
	if parseErr != nil {
		_ = os.Remove(path)
		http.Error(w, parseErr.Error(), http.StatusBadRequest)
		return
	}
	preview := records
	if len(preview) > 8 {
		preview = preview[:8]
	}
	writeJSON(w, map[string]any{
		"token":      token,
		"rows":       preview,
		"total_rows": len(records),
	})
}

func cleanupStaleCSVTemp(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-csvTempKeepDuration)
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

type csvImportReq struct {
	Token        string          `json:"token"`
	Account      string          `json:"account"`
	Member       string          `json:"member"`
	TemplateName string          `json:"template_name"`
	SaveTemplate bool            `json:"save_template"`
	Mapping      bill.CSVMapping `json:"mapping"`
}

// CSVImportSubmit POST /imports/csv —— mapping + token → ImportBill 链路；文件用后即删
func (h *Handler) CSVImportSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req csvImportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	acc := domain.Account(req.Account)
	if !acc.IsStorageAccount() {
		http.Error(w, "请选择账户归属（男主/女主）", http.StatusBadRequest)
		return
	}
	if err := req.Mapping.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.TemplateName = strings.TrimSpace(req.TemplateName)
	path, err := csvTempPath(req.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "上传的文件已过期，请重新上传", http.StatusBadRequest)
		return
	}
	defer os.Remove(path) // 隐私铁律：解析后即删（无论成败）
	defer f.Close()

	parser := bill.NewGenericCSVParser(req.Mapping, req.TemplateName)
	res, err := h.importBill.ExecuteWithParser(r.Context(), usecase.ImportBillInput{
		Source:   parser.Source(),
		Account:  acc,
		Member:   strings.TrimSpace(req.Member),
		Filename: req.Token + ".csv",
		Reader:   f,
	}, parser)
	if err != nil {
		h.log.Error("csv import", "err", err)
		http.Error(w, "导入失败："+err.Error(), http.StatusBadRequest)
		return
	}

	if req.SaveTemplate && req.TemplateName != "" {
		mappingJSON, _ := json.Marshal(req.Mapping)
		if err := h.templateRepo.SaveTemplate(r.Context(), &domain.ImportTemplate{
			ID: newID(), Name: req.TemplateName, Mapping: string(mappingJSON), CreatedAt: time.Now(),
		}); err != nil {
			h.log.Warn("save csv template", "err", err)
		}
	}

	msg := fmt.Sprintf("CSV 导入完成（%s）：新增 %d 条（其中不计收支 %d 条），跳过重复 %d 条，未导入 %d 条，待核对 %d 条。",
		acc.Label(), res.InsertedRows, res.TransferRows, res.SkippedDuplicates, res.SkippedInvalid, res.PendingCategory)
	h.flash.set(w, msg)
	writeJSON(w, map[string]any{"redirect": importRedirectURL(acc, res)})
}
