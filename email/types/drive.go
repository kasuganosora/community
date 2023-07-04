package types

type Drive interface {
	SendMail(to string, cc string, mailObject *Mail) (err error)
}
