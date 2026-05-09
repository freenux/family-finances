# Family Finances Tools

## 微信流水 xlsx 分类

使用 `uv` 运行脚本：

```bash
uv run python -m wepay_classifier.classify_wepay \
  微信支付账单流水文件.xlsx \
  -o outputs/wepay-classified.csv \
  --print-summary
```

支持多个微信导出的 `.xlsx` 输入文件，脚本会合并输出：

```bash
uv run python -m wepay_classifier.classify_wepay \
  2026-01.xlsx 2026-02.xlsx 2026-03.xlsx \
  -o outputs/wepay-q1-classified.csv
```

输出文件：

- 明细 CSV：保留微信流水原字段，并在最后追加 `分类`
- 汇总 CSV：默认输出为 `<明细文件名>_summary.csv`

分类规则在 `wepay_classifier/category_rules.py`，优先人工维护这个文件。规则没有匹配到时，脚本会尝试调用 OpenAI 兼容的大模型接口；如果没有配置 API key，则归为 `未分类`。

环境变量：

```bash
export OPENAI_API_KEY="sk-..."
export LLM_MODEL="gpt-4.1-mini"
export LLM_BASE_URL="https://api.openai.com/v1"
```

如果只想使用本地规则，不调用大模型：

```bash
uv run python -m wepay_classifier.classify_wepay \
  微信支付账单流水文件.xlsx \
  -o outputs/wepay-classified.csv \
  --no-llm
```

