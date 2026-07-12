package domain

import "time"

// Digest cadence / channel 取值
const (
	DigestWeekly    = "weekly"
	DigestQuarterly = "quarterly"

	DigestChannelEmail   = "email"
	DigestChannelWebhook = "webhook"
)

// DigestSettings 摘要推送设置（digest_settings 单行表）
type DigestSettings struct {
	ID         string
	Enabled    bool
	Cadence    string    // weekly | quarterly
	Channel    string    // email | webhook
	Target     string    // 收件地址或 webhook URL
	Modules    []string  // 启用的模块（expense_compare / top_categories / pending_count / llm_note）
	LastSentAt time.Time // 零值 = 从未发送
}

// DefaultDigestSettings 缺省值
func DefaultDigestSettings() DigestSettings {
	return DigestSettings{
		ID:      "default",
		Cadence: DigestWeekly,
		Channel: DigestChannelEmail,
		Modules: []string{"expense_compare", "top_categories", "pending_count", "llm_note"},
	}
}
