package smtp

import (
	"bytes"
	"github.com/jhillyerd/enmime"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/email/types"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"io"
	"io/fs"
	"mime"
	"net/smtp"
	"path/filepath"
	"strings"
)

type Drive struct {
	server string
	auth   smtp.Auth
}

func NewDrive() *Drive {
	if config.C.SmtpServer == "" {
		return nil
	}

	port := config.C.SmtpPort
	if port == "" {
		port = "25"
	}

	server := config.C.SmtpServer + ":" + port
	auth := smtp.PlainAuth("", config.C.SmtpUsername, config.C.SmtpPassword, config.C.SmtpServer)

	return &Drive{
		server: server,
		auth:   auth,
	}
}

func (d Drive) Send(to string, cc string, body []byte) (err error) {
	if d.server == "" {
		return
	}

	toAddrs := strings.Split(to, ";")
	ccAddrs := strings.Split(cc, ";")

	addrs := make([]string, 0, len(toAddrs)+len(ccAddrs))
	if len(toAddrs) > 0 {
		addrs = append(addrs, toAddrs...)
	}

	if len(ccAddrs) > 0 {
		addrs = append(addrs, ccAddrs...)
	}

	addrs = utils.FilterDuplicateAndEmptyString(addrs)

	if len(addrs) == 0 {
		return
	}

	err = smtp.SendMail(d.server, d.auth, config.C.SmtpUsername, addrs, body)

	return
}

func (d Drive) SendMail(to string, cc string, mailObject *types.Mail) (err error) {
	defer func() {
		for _, att := range mailObject.Attachments {
			_ = att.File.Close()
		}
	}()

	msg := enmime.Builder().
		From("", config.C.SmtpUsername)

	toAddrs := utils.FilterDuplicateAndEmptyString(strings.Split(to, ";"))

	for _, addr := range toAddrs {
		msg = msg.To("", addr)
	}

	ccAddrs := utils.FilterDuplicateAndEmptyString(strings.Split(cc, ";"))

	for _, addr := range ccAddrs {
		msg = msg.To("", addr)
	}

	msg = msg.Subject(mailObject.Subject)

	if mailObject.TextBody != "" {
		msg = msg.Text([]byte(mailObject.TextBody))
	}

	if mailObject.Body != "" {
		msg = msg.HTML([]byte(mailObject.Body))
	}

	for _, att := range mailObject.Attachments {
		var fileStat fs.FileInfo
		fileStat, err = att.File.Stat()
		if err != nil {
			return err
		}

		contentType := mime.TypeByExtension(filepath.Ext(fileStat.Name()))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		var fileData []byte
		fileData, err = io.ReadAll(att.File)
		if err != nil {
			return err
		}

		if att.Name == "" {
			att.Name = fileStat.Name()
		}

		if att.ContentId != "" {
			msg = msg.AddInline(fileData, contentType, att.Name, att.ContentId)
		} else {
			msg = msg.AddAttachment(fileData, contentType, att.Name)
		}
	}

	buf := &bytes.Buffer{}

	root, err := msg.Build()
	if err != nil {
		err = errors.WithMessagef(err, "build mail failed")
		return err
	}

	err = root.Encode(buf)
	if err != nil {
		err = errors.WithMessagef(err, "encode mail failed")
		return err
	}

	err = d.Send(to, cc, buf.Bytes())

	if err != nil {
		err = errors.WithMessagef(err, "send mail failed")
	}

	return
}
