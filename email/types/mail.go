package types

import (
	"io/fs"
)

type Mail struct {
	To          string
	Cc          string
	Subject     string
	Attachments []*MailAttachment
	Body        string
	TextBody    string
}

type MailAttachment struct {
	File      fs.File
	Name      string
	ContentId string
}
