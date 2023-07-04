package email

import (
	"bytes"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/email/smtp"
	"github.com/kasuganosora/community/email/types"
	"github.com/kasuganosora/community/internalfs"
	"github.com/kasuganosora/community/log"
	"html/template"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

var useDrive types.Drive

func init() {
	if config.C.SmtpServer != "" {
		useDrive = smtp.NewDrive()
	}
}

func SendText(to string, cc string, bcc string, subject string, textBody string, body string) (err error) {
	if useDrive == nil {
		return
	}

	mail := &types.Mail{
		To:       to,
		Cc:       cc,
		Subject:  subject,
		Body:     body,
		TextBody: textBody,
	}

	err = useDrive.SendMail(to, cc, mail)

	return
}

func SendHtmlTemplate(to string, cc string, bcc string, subject string, textBody string, templateName string, data map[string]any) (err error) {
	if useDrive == nil {
		return
	}

	templateFile, err := internalfs.Get().Open("mails/" + templateName + ".html")
	if err != nil {
		return
	}
	templateContent, err := io.ReadAll(templateFile)
	if err != nil {
		return
	}

	tpl, err := template.ParseGlob(string(templateContent))

	if err != nil {
		return
	}

	buf := bytes.NewBufferString("")
	err = tpl.ExecuteTemplate(buf, templateName, data)
	if err != nil {
		return
	}

	// 提取出里面的 cid://xxxx.xxx 的内容,然后获取成文件,然后替换
	content, attachments := extractAttachments(buf.String())

	mail := &types.Mail{
		To:          to,
		Cc:          cc,
		Subject:     subject,
		Body:        content,
		TextBody:    textBody,
		Attachments: attachments,
	}

	err = useDrive.SendMail(to, cc, mail)

	if err != nil {
		return
	}

	if bcc != "" {
		mail.To = bcc
		err = useDrive.SendMail(bcc, "", mail)
	}

	return
}

func SendHtmlTemplateAndAttachments(to string, cc string, bcc string, subject string, textBody string, templateName string, data map[string]any, appendAttachments []*types.MailAttachment) (err error) {
	if useDrive == nil {
		return
	}

	templateFile, err := internalfs.Get().Open("mails/" + templateName + ".html")
	if err != nil {
		return
	}
	templateContent, err := io.ReadAll(templateFile)
	if err != nil {
		return
	}

	tpl, err := template.ParseGlob(string(templateContent))

	if err != nil {
		return
	}

	buf := bytes.NewBufferString("")
	err = tpl.ExecuteTemplate(buf, templateName, data)
	if err != nil {
		return
	}

	// 提取出里面的 cid://xxxx.xxx 的内容,然后获取成文件,然后替换
	content, attachments := extractAttachments(buf.String())

	attachments = append(attachments, appendAttachments...)

	mail := &types.Mail{
		To:          to,
		Cc:          cc,
		Subject:     subject,
		Body:        content,
		TextBody:    textBody,
		Attachments: attachments,
	}

	err = useDrive.SendMail(to, cc, mail)

	if err != nil {
		return
	}

	if bcc != "" {
		mail.To = bcc
		err = useDrive.SendMail(bcc, "", mail)
	}

	return

}

func extractAttachments(content string) (ret string, attachments []*types.MailAttachment) {
	// 用正则表达式提取出 cid://abc/def.png 的内容
	// 例如 src="cid://abc/def.png" 这样的内容
	attachments = make([]*types.MailAttachment, 0)

	cidRegex := regexp.MustCompile(`src="cid://(.+?)"`)
	cidMatches := cidRegex.FindAllStringSubmatch(content, -1)

	for _, match := range cidMatches {
		ref := match[1]
		fp, err := internalfs.Get().Open("mails/" + ref)
		if err != nil {
			log.Warn("can not find attachment file: %s", ref)
			continue
		}

		attachment := &types.MailAttachment{
			File:      fp,
			Name:      filepath.Base(ref),
			ContentId: uuid.NewString(),
		}

		attachments = append(attachments, attachment)
		// 替换掉 cid://xxx/xxx.png 为 cid:xxx
		content = strings.ReplaceAll(content, match[0], `src="cid:`+attachment.ContentId+`"`)
	}

	ret = content
	return
}
