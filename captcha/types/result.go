package types

// VerificationResult 验证结果
// 所有供应商共用的验证结果结构体
type VerificationResult struct {
	// Success 是否验证通过
	Success bool

	// Score 分数（0.0 - 1.0，用于行为分析型验证码如 reCAPTCHA v3）
	Score float64

	// Action 动作标识（如 reCAPTCHA v3 的 action）
	Action string

	// ChallengeTS 挑战时间戳
	ChallengeTS string

	// Hostname 验证的主机名
	Hostname string

	// ErrorCodes 错误代码列表
	ErrorCodes []string

	// Provider 供应商名称
	Provider string
}
