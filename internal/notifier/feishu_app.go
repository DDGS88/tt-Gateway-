package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// feishuAppBaseURL 飞书开放平台 API 根地址
const feishuAppBaseURL = "https://open.feishu.cn"

// FeishuAppConfig 飞书自建应用配置（app 模式）
type FeishuAppConfig struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	ChatID    string `json:"chat_id"`
}

// FeishuAppNotifier 飞书自建应用通知器
// 通过 App ID / App Secret 获取 tenant_access_token，
// 调用 im/v1/messages 以 chat_id 为接收方发送互动卡片。
type FeishuAppNotifier struct {
	client *http.Client
	logger *zap.Logger

	mu        sync.Mutex
	token     string
	tokenApp  string    // 当前 token 对应的 App ID
	expireAt  time.Time // token 过期时间（已预留提前量）
}

func NewFeishuAppNotifier(logger *zap.Logger) *FeishuAppNotifier {
	return &FeishuAppNotifier{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// getTenantAccessToken 获取 tenant_access_token，带内存缓存（提前 5 分钟刷新）
func (n *FeishuAppNotifier) getTenantAccessToken(cfg FeishuAppConfig) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.token != "" && n.tokenApp == cfg.AppID && time.Now().Before(n.expireAt) {
		return n.token, nil
	}

	reqBody, err := json.Marshal(map[string]string{
		"app_id":     cfg.AppID,
		"app_secret": cfg.AppSecret,
	})
	if err != nil {
		return "", fmt.Errorf("marshal token request: %w", err)
	}

	resp, err := n.client.Post(feishuAppBaseURL+"/open-apis/auth/v3/tenant_access_token/internal",
		"application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("request tenant_access_token: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var tokenResp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w, body: %s", err, string(respBody))
	}
	if tokenResp.Code != 0 || tokenResp.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu token api error code=%d msg=%s", tokenResp.Code, tokenResp.Msg)
	}

	n.token = tokenResp.TenantAccessToken
	n.tokenApp = cfg.AppID
	n.expireAt = time.Now().Add(time.Duration(tokenResp.Expire-300) * time.Second)
	return n.token, nil
}

// buildAlertCardContent 构建告警卡片 markdown 正文（与 webhook 卡片样式一致）
func buildAlertCardContent(msg AlertMessage) string {
	content := fmt.Sprintf(
		"**规则名称**: %s\n**规则类型**: %s\n**匹配次数**: %d / %d\n**时间窗口**: %s ~ %s\n**匹配条件**: %s\n**规则描述**: %s",
		msg.RuleName,
		msg.RuleType,
		msg.MatchCount,
		msg.Threshold,
		msg.WindowStart.Format("2006-01-02 15:04:05"),
		msg.WindowEnd.Format("2006-01-02 15:04:05"),
		msg.Condition,
		msg.Description,
	)

	if len(msg.Samples) > 0 {
		content += "\n\n**匹配样本**:"
		for i, s := range msg.Samples {
			streamLabel := "no-stream"
			if s.IsStream {
				streamLabel = "stream"
			}
			content += fmt.Sprintf("\n> **#%d** | 渠道: `%s` | 模型: `%s` | 类型: `%s`", i+1, s.ChannelName, s.ModelName, streamLabel)
			if s.Detail != "" {
				content += fmt.Sprintf("\n> %s", s.Detail)
				continue
			}
			content += fmt.Sprintf("\n> Content: %s", s.MatchedValue)
			content += fmt.Sprintf("\n> RequestID: `%s`", s.RequestID)
		}
	}
	return content
}

// buildAlertCard 构建互动卡片，@用户固定放在卡片内部最底部
func buildAlertCard(msg AlertMessage) map[string]interface{} {
	card := map[string]interface{}{
		"config": map[string]interface{}{"wide_screen_mode": true},
		"header": map[string]interface{}{
			"title":    map[string]interface{}{"tag": "plain_text", "content": "AI Gateway Alert"},
			"template": "red",
		},
		"elements": []map[string]interface{}{
			{"tag": "markdown", "content": buildAlertCardContent(msg)},
		},
	}

	// 卡片内 @ 用户（open_id），追加为最底部的独立元素
	if len(msg.AtUsers) > 0 {
		atContent := ""
		for _, u := range msg.AtUsers {
			atContent += fmt.Sprintf(`<at id=%s></at>`, u)
		}
		elements := card["elements"].([]map[string]interface{})
		elements = append(elements, map[string]interface{}{
			"tag":     "markdown",
			"content": atContent,
		})
		card["elements"] = elements
	}
	return card
}

// SendCard 通过自建应用向指定群（chat_id）发送告警卡片
func (n *FeishuAppNotifier) SendCard(cfg FeishuAppConfig, msg AlertMessage) error {
	if cfg.AppID == "" || cfg.AppSecret == "" || cfg.ChatID == "" {
		return fmt.Errorf("feishu app config incomplete: app_id/app_secret/chat_id required")
	}

	token, err := n.getTenantAccessToken(cfg)
	if err != nil {
		return err
	}

	cardJSON, err := json.Marshal(buildAlertCard(msg))
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}

	reqBody, err := json.Marshal(map[string]interface{}{
		"receive_id": cfg.ChatID,
		"msg_type":   "interactive",
		"content":    string(cardJSON),
	})
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost,
		feishuAppBaseURL+"/open-apis/im/v1/messages?receive_id_type=chat_id",
		bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := n.client.Do(req)
	if err != nil {
		n.logger.Error("feishu app send failed", zap.Error(err))
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var sendResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(respBody, &sendResp)
	if resp.StatusCode != http.StatusOK || sendResp.Code != 0 {
		n.logger.Error("feishu app non-success response",
			zap.Int("status", resp.StatusCode),
			zap.Int("code", sendResp.Code),
			zap.String("msg", sendResp.Msg),
			zap.String("body", string(respBody)))
		return fmt.Errorf("feishu send api error status=%d code=%d msg=%s", resp.StatusCode, sendResp.Code, sendResp.Msg)
	}

	n.logger.Info("feishu app alert sent",
		zap.String("rule_name", msg.RuleName),
		zap.Int("match_count", msg.MatchCount),
		zap.Int("at_users", len(msg.AtUsers)))
	return nil
}
