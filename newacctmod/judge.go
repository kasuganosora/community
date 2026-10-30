package newacctmod

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/thinkbot/llm"
	"gorm.io/gorm"
)

const FeatureModeration = "new_account_mod"

type llmVerdict struct {
	Verdict    string  `json:"verdict"` // pass|flag|suspend
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

func gatherContext(conn *gorm.DB, accountID, statusID uint64, x int) (current string, prior []string, err error) {
	var st models.Status
	if err = conn.Preload("Content").First(&st, statusID).Error; err != nil {
		return "", nil, err
	}
	current = st.Content.EditableText()
	var rows []models.Status
	err = conn.Preload("Content").
		Where("account_id = ? AND id < ? AND deleted_at IS NULL AND (reblog_id IS NULL OR reblog_id = 0)", accountID, statusID).
		Order("id DESC").Limit(x).Find(&rows).Error
	if err != nil {
		return "", nil, err
	}
	for i := range rows {
		prior = append(prior, rows[i].Content.EditableText())
	}
	return current, prior, nil
}

func judgeLLM(ctx context.Context, current string, prior []string) (llmVerdict, error) {
	p, ok := llmsvc.ServiceProvider(llmsvc.ServiceNewAccountMod)
	if !ok {
		p, ok = llmsvc.ServiceProvider(llmsvc.ServiceTranslation)
	}
	if !ok {
		return llmVerdict{}, llmsvc.ErrNotConfigured
	}
	var b strings.Builder
	b.WriteString("You are a Mastodon instance moderator assistant.\n")
	b.WriteString("Site policy:\n")
	b.WriteString(PolicyText())
	b.WriteString("\n\nDecide for this local new account post.\n")
	b.WriteString("Reply with ONLY compact JSON: {\"verdict\":\"pass|flag|suspend\",\"confidence\":0-1,\"reason\":\"short\"}.\n")
	b.WriteString("Use suspend only for clear malicious advertising / spam farms. Use flag when unsure. Use pass for normal posts.\n\n")
	b.WriteString("Current post:\n")
	b.WriteString(current)
	b.WriteString("\n\nRecent prior posts (newest first):\n")
	if len(prior) == 0 {
		b.WriteString("(none)\n")
	}
	for i, ptxt := range prior {
		fmt.Fprintf(&b, "[%d] %s\n", i+1, ptxt)
	}
	temp := 0.0
	res, err := llmsvc.Generate(ctx, p, FeatureModeration, llm.GenerateParams{
		Messages:    []llm.Message{llm.UserMessage(b.String())},
		Temperature: &temp,
	})
	if err != nil {
		return llmVerdict{}, err
	}
	raw := strings.TrimSpace(res.Text)
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	}
	var v llmVerdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return llmVerdict{Verdict: "flag", Confidence: 0.3, Reason: "unparseable model output"}, nil
	}
	v.Verdict = strings.ToLower(strings.TrimSpace(v.Verdict))
	switch v.Verdict {
	case "pass", "flag", "suspend":
	default:
		v.Verdict = "flag"
		v.Reason = "unknown verdict from model"
	}
	return v, nil
}
