package handler

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
)

// ExportPage GET /export —— 三张卡说明 + 下载按钮（纯静态展示，下载走独立端点）
func (h *Handler) ExportPage(w http.ResponseWriter, r *http.Request) {
	vm := pageBase{Title: "数据导出", Nav: "export"}
	h.renderPage(w, http.StatusOK, "export", vm)
}

var exportCSVHeader = []string{
	"id", "occurred_at", "source", "account", "direction", "amount_fen", "amount_yuan",
	"category_id", "category_name", "description", "counterparty", "note", "status",
}

// ExportTransactionsCSV GET /export/transactions.csv —— 全量流水，UTF-8 带 BOM（Excel 兼容）
func (h *Handler) ExportTransactionsCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := h.exportUC.TransactionRows(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="transactions.csv"`)
	// UTF-8 BOM，保证 Excel 打开不乱码
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		h.log.Error("export csv write bom", "err", err)
		return
	}

	cw := csv.NewWriter(w)
	if err := cw.Write(exportCSVHeader); err != nil {
		h.log.Error("export csv write header", "err", err)
		return
	}
	for _, row := range rows {
		record := []string{
			row.ID,
			row.OccurredAt.Format("2006-01-02 15:04:05"),
			row.Source,
			row.Account,
			row.Direction,
			strconv.FormatInt(row.AmountFen, 10),
			row.AmountYuan,
			row.CategoryID,
			row.CategoryName,
			row.Description,
			row.Counterparty,
			row.Note,
			row.Status,
		}
		if err := cw.Write(record); err != nil {
			h.log.Error("export csv write row", "err", err)
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		h.log.Error("export csv flush", "err", err)
	}
}

// ExportFullJSON GET /export/full.json —— 全量数据，json.Encoder 流式写出
func (h *Handler) ExportFullJSON(w http.ResponseWriter, r *http.Request) {
	full, err := h.exportUC.BuildFull(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="full-export.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(full); err != nil {
		h.log.Error("export full json encode", "err", err)
	}
}
