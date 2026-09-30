package matrix

import (
	"html"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	xhtml "golang.org/x/net/html"
	"gorm.io/gorm"
)

// MessageBridge 消息桥接服务
// 负责消息在 Matrix 和本地系统之间的双向转换和同步.
type MessageBridge struct {
	client     *ASClient
	userMapper *UserMapper
	roomMapper *RoomMapper
}

// NewMessageBridge 创建消息桥接服务.
func NewMessageBridge(client *ASClient, userMapper *UserMapper, roomMapper *RoomMapper) *MessageBridge {
	return &MessageBridge{
		client:     client,
		userMapper: userMapper,
		roomMapper: roomMapper,
	}
}

// HandleMatrixMessage 处理来自 Matrix 的消息.
func (m *MessageBridge) HandleMatrixMessage(event *Event) error {
	// 解析消息内容
	content := event.ToRoomMessageContent()
	if content == nil {
		return nil
	}

	// 只处理文本类消息
	if content.MsgType != MsgTypeText && content.MsgType != MsgTypeEmote && content.MsgType != MsgTypeNotice {
		return nil
	}

	// 检查是否已处理过此消息
	if m.isEventProcessed(event.EventID) {
		return nil
	}

	// 获取发送者对应的本地账号
	senderAccount, err := m.userMapper.GetLocalAccountFromMatrixUser(event.Sender)
	if err != nil {
		return errors.Wrap(err, "failed to get local account for sender")
	}

	// 获取房间对应的本地会话
	matrixRoom, err := m.roomMapper.GetOrCreateConversationByRoom(event.RoomID, []string{event.Sender})
	if err != nil {
		return errors.Wrap(err, "failed to get conversation for room")
	}

	// 转换消息格式
	statusContent := m.convertMatrixMessageToStatus(content)

	// 创建本地 Status
	status, err := m.createLocalStatus(matrixRoom.ConversationId, senderAccount.Id, statusContent, event.EventID)
	if err != nil {
		return errors.Wrap(err, "failed to create local status")
	}

	// 记录消息映射
	if err := m.createMessageMapping(status.Id, event); err != nil {
		log.Error("Failed to create message mapping: %v", err)
		// 非致命错误，继续
	}

	// 更新房间最后同步信息
	if err := m.roomMapper.UpdateLastSync(event.RoomID, event.EventID); err != nil {
		log.Error("Failed to update room sync info: %v", err)
	}

	return nil
}

// SendLocalMessageToMatrix 将本地消息发送到 Matrix.
func (m *MessageBridge) SendLocalMessageToMatrix(convID uint64, senderAccountID uint64, content string) (string, error) {
	// 获取发送者对应的 Matrix 用户
	matrixUserID, err := m.userMapper.GetMatrixUserID(senderAccountID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get matrix user for sender")
	}

	// 获取会话对应的 Matrix 房间
	matrixRoom, err := m.roomMapper.GetOrCreateRoomByConversation(convID, []string{matrixUserID})
	if err != nil {
		return "", errors.Wrap(err, "failed to get matrix room for conversation")
	}

	// 转换消息格式
	matrixContent := m.convertStatusToMatrixMessage(content)

	// 发送到 Matrix
	resp, err := m.client.SendRoomMessage(matrixRoom.MatrixRoomID, matrixUserID, matrixContent)
	if err != nil {
		return "", errors.Wrap(err, "failed to send message to matrix")
	}

	return resp.EventID, nil
}

// convertMatrixMessageToStatus 将 Matrix 消息转换为本地 Status 格式.
func (m *MessageBridge) convertMatrixMessageToStatus(content *RoomMessageContent) string {
	body := content.Body

	// 处理格式化文本
	if content.FormattedBody != "" && content.Format == "org.matrix.custom.html" {
		// 使用 HTML 解析器正确去除标签
		body = m.stripHTMLTags(content.FormattedBody)
	}

	// 处理 emote 类型消息
	if content.MsgType == MsgTypeEmote {
		body = "* " + body
	}

	return body
}

// convertStatusToMatrixMessage 将本地 Status 转换为 Matrix 消息格式.
func (m *MessageBridge) convertStatusToMatrixMessage(content string) *RoomMessageContent {
	// 清理内容
	body := strings.TrimSpace(content)

	return &RoomMessageContent{
		MsgType:       MsgTypeText,
		Body:          body,
		Format:        "org.matrix.custom.html",
		FormattedBody: m.textToHTML(body),
	}
}

// createLocalStatus 创建本地 Status.
func (m *MessageBridge) createLocalStatus(convID uint64, accountID uint64, content string, eventID string) (*models.Status, error) { //nolint:unparam // param reserved for future use
	// 创建 Status 记录
	status := &models.Status{
		AccountId:      accountID,
		Visibility:     models.StatusVisibilityDirect,
		ConversationId: &convID,
	}

	// 获取会话，添加参与者
	var conv models.Conversation
	err := db.GetDB().First(&conv, convID).Error
	if err != nil {
		return nil, err
	}

	tx := db.GetDB().Begin()

	// 创建 Status
	err = tx.Create(status).Error
	if err != nil {
		tx.Rollback()

		return nil, err
	}

	// 创建 StatusContent
	statusContent := &models.StatusContent{
		StatusId:    status.Id,
		Content:     content,
		SpoilerText: "",
	}
	err = tx.Create(statusContent).Error
	if err != nil {
		tx.Rollback()

		return nil, err
	}

	// 更新会话的状态列表
	conv.AppendStatus(status.Id)

	err = tx.Save(&conv).Error
	if err != nil {
		tx.Rollback()

		return nil, err
	}

	err = tx.Commit().Error
	if err != nil {
		return nil, err
	}

	return status, nil
}

// createMessageMapping 创建消息映射记录.
func (m *MessageBridge) createMessageMapping(statusID uint64, event *Event) error {
	mapping := &models.MatrixMessage{
		StatusId:       statusID,
		MatrixEventID:  event.EventID,
		MatrixRoomID:   event.RoomID,
		MatrixSenderID: event.Sender,
		Direction:      "from_matrix",
	}

	return db.GetDB().Create(mapping).Error
}

// isEventProcessed 检查事件是否已处理.
func (m *MessageBridge) isEventProcessed(eventID string) bool {
	var count int64
	err := db.GetDB().Model(&models.MatrixMessage{}).
		Where("matrix_event_id = ?", eventID).
		Count(&count).Error
	if err != nil {
		return false
	}

	return count > 0
}

// stripHTMLTags 使用 HTML 解析器正确去除 HTML 标签并提取文本内容.
func (m *MessageBridge) stripHTMLTags(htmlContent string) string {
	doc, err := xhtml.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// 解析失败时回退到解码 HTML 实体
		return html.UnescapeString(htmlContent)
	}

	var (
		buf         strings.Builder
		extractText func(*xhtml.Node)
	)

	extractText = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			buf.WriteString(n.Data)
		}

		if n.Type == xhtml.ElementNode && n.Data == "br" {
			buf.WriteByte('\n')
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractText(c)
		}

		if n.Type == xhtml.ElementNode && htmlBlockTag(n.Data) {
			buf.WriteByte('\n')
		}
	}
	extractText(doc)

	return strings.TrimSpace(buf.String())
}

func htmlBlockTag(tag string) bool {
	switch tag {
	case "p", "div", "li", "tr", "blockquote", "pre", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}

// textToHTML 将纯文本转换为简单的 HTML.
func (m *MessageBridge) textToHTML(text string) string {
	// 转义 HTML 特殊字符
	text = html.EscapeString(text)
	// 将换行符转换为 <br>
	text = strings.ReplaceAll(text, "\n", "<br>")

	return text
}

// GetStatusByEventID 通过 Matrix 事件 ID 获取本地 Status.
func (m *MessageBridge) GetStatusByEventID(eventID string) (*models.Status, error) {
	var mapping models.MatrixMessage
	err := db.GetDB().Where("matrix_event_id = ?", eventID).First(&mapping).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil //nolint:nilnil
		}

		return nil, err
	}

	var status models.Status
	err = db.GetDB().First(&status, mapping.StatusId).Error
	if err != nil {
		return nil, err
	}

	return &status, nil
}

// GetEventIDByStatusID 通过本地 Status ID 获取 Matrix 事件 ID.
func (m *MessageBridge) GetEventIDByStatusID(statusID uint64) (string, error) {
	var mapping models.MatrixMessage
	err := db.GetDB().Where("status_id = ?", statusID).First(&mapping).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}

		return "", err
	}

	return mapping.MatrixEventID, nil
}
