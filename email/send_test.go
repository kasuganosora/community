package email

import (
	"errors"
	"strings"
	"testing"

	"github.com/kasuganosora/community/email/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingDrive is a test double for types.Drive that records every SendMail
// invocation so the test can assert on recipients, subject, body and call count
// without talking to a real SMTP server.
//
// errQueue is consumed in order: the n-th call returns errQueue[n] (or nil when
// the queue is exhausted). This lets a test simulate "first send succeeds, the
// BCC send fails" etc.
type recordingDrive struct {
	calls    []*types.Mail
	errQueue []error
}

// nextErr pops and returns the next queued error, defaulting to nil.
func (d *recordingDrive) nextErr() error {
	if len(d.errQueue) == 0 {
		return nil
	}

	err := d.errQueue[0]
	d.errQueue = d.errQueue[1:]

	return err
}

// SendMail implements types.Drive.
func (d *recordingDrive) SendMail(to string, cc string, mailObject *types.Mail) error {
	// Snapshot the relevant fields so later mutation of mailObject (e.g. the
	// real SendHtmlTemplate code reassigns mail.To for the BCC leg) cannot
	// retroactively alter what we recorded.
	d.calls = append(d.calls, &types.Mail{
		To:          to,
		Cc:          cc,
		Subject:     mailObject.Subject,
		Body:        mailObject.Body,
		TextBody:    mailObject.TextBody,
		Attachments: mailObject.Attachments,
	})

	return d.nextErr()
}

// withDrive swaps the package-level useDrive for the given drive for the
// duration of the test, restoring the previous value via t.Cleanup. Tests must
// NOT run in parallel while this is active (they mutate package state).
func withDrive(t *testing.T, drive types.Drive) {
	t.Helper()

	prev := useDrive
	useDrive = drive
	t.Cleanup(func() { useDrive = prev })
}

// -----------------------------------------------------------------------------
// isValidTemplateName
// -----------------------------------------------------------------------------

func TestIsValidTemplateName_AllowedBaseNames(t *testing.T) {
	for _, name := range allowedTemplatePrefixes {
		t.Run(name, func(t *testing.T) {
			assert.True(t, isValidTemplateName(name), "exact base name %q must be allowed", name)
		})
	}
}

func TestIsValidTemplateName_LanguageSuffix(t *testing.T) {
	// A base name followed by "_<lang>" is allowed (e.g. verify_email_en).
	cases := []string{
		"email_confirmation_en",
		"email_confirmation_zh",
		"recover_password_verify_email_en",
		"verify_email_en",
		"verify_email_fr",
		"welcome_mail_ja",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			assert.True(t, isValidTemplateName(name), "language-suffixed name %q must be allowed", name)
		})
	}
}

func TestIsValidTemplateName_Rejected(t *testing.T) {
	cases := []string{
		"",                       // empty
		"random",                 // unrelated
		"verify_email",           // valid base but here only to contrast with the next case
		"verify_email.html",      // extension smuggling
		"../etc/passwd",          // path traversal
		"verify_emailX",          // prefix without separator
		"verifyemail",            // missing underscore
		"xverify_email",          // leading garbage
		"welcome_mail",           // valid base
		"welcome_mailmore",       // prefix without separator -> rejected
		"verify_email/../../etc", // traversal with valid prefix
	}
	// expected: all cases except the two valid-base lines below are rejected.
	expected := map[string]bool{
		"verify_email": true,
		"welcome_mail": true,
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			got := isValidTemplateName(name)
			want := expected[name]
			assert.Equal(t, want, got, "isValidTemplateName(%q)", name)
		})
	}
}

func TestAllowedTemplatePrefixes_ContainsExpectedSet(t *testing.T) {
	// Guard against accidental removal of an allowed template.
	assert.Contains(t, allowedTemplatePrefixes, "email_confirmation")
	assert.Contains(t, allowedTemplatePrefixes, "recover_password_verify_email")
	assert.Contains(t, allowedTemplatePrefixes, "verify_email")
	assert.Contains(t, allowedTemplatePrefixes, "welcome_mail")
	assert.Len(t, allowedTemplatePrefixes, 4)
}

// -----------------------------------------------------------------------------
// extractAttachments
// -----------------------------------------------------------------------------

func TestExtractAttachments_NoCid(t *testing.T) {
	input := `<html><body><p>hello</p><img src="https://example.com/a.png"/></body></html>`
	content, attachments := extractAttachments(input)

	assert.Equal(t, input, content, "content without cid:// must be returned unchanged")
	assert.Empty(t, attachments)
}

func TestExtractAttachments_EmptyInput(t *testing.T) {
	content, attachments := extractAttachments("")
	assert.Empty(t, content)
	assert.Empty(t, attachments)
}

func TestExtractAttachments_CidButFileMissing(t *testing.T) {
	// The referenced file does not exist in the embedded mails/ fs, so the
	// function should log a warning and keep the content as-is (no attachment
	// produced, no crash).
	input := `<img src="cid://does/not-exist.png"/>`
	content, attachments := extractAttachments(input)

	assert.Equal(t, input, content, "content must be unchanged when the cid file is absent")
	assert.Empty(t, attachments, "no attachment should be created for a missing file")
}

func TestExtractAttachments_MultipleCidsAllMissing(t *testing.T) {
	input := `<img src="cid://a/x.png"/><img src="cid://b/y.png"/>`
	content, attachments := extractAttachments(input)

	assert.Equal(t, input, content)
	assert.Empty(t, attachments)
}

// -----------------------------------------------------------------------------
// SendText
// -----------------------------------------------------------------------------

func TestSendText_NilDriveIsNoOp(t *testing.T) {
	// Default state: no SMTP server -> useDrive is nil -> SendText returns nil
	// without sending anything.
	err := SendText("to@example.com", "cc@example.com", "bcc@example.com",
		"subject", "text body", "<b>html</b>")
	assert.NoError(t, err)
}

func TestSendText_WithoutBcc_SendsOnceToMainRecipient(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendText("alice@example.com", "carol@example.com", "",
		"Hello", "plain text", "<p>html</p>")

	require.NoError(t, err)
	require.Len(t, drive.calls, 1)

	c := drive.calls[0]
	assert.Equal(t, "alice@example.com", c.To)
	assert.Equal(t, "carol@example.com", c.Cc)
	assert.Equal(t, "Hello", c.Subject)
	assert.Equal(t, "plain text", c.TextBody)
	assert.Equal(t, "<p>html</p>", c.Body)
}

func TestSendText_WithBcc_SendsTwice(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendText("alice@example.com", "carol@example.com", "secret@example.com",
		"Hello", "plain", "<p>html</p>")

	require.NoError(t, err)
	require.Len(t, drive.calls, 2, "BCC must trigger a second SendMail call")

	// First call: original recipients.
	assert.Equal(t, "alice@example.com", drive.calls[0].To)
	assert.Equal(t, "carol@example.com", drive.calls[0].Cc)
	assert.Equal(t, "Hello", drive.calls[0].Subject)

	// Second call: the BCC recipient, with an empty cc, same subject/body.
	assert.Equal(t, "secret@example.com", drive.calls[1].To)
	assert.Empty(t, drive.calls[1].Cc)
	assert.Equal(t, "Hello", drive.calls[1].Subject)
	assert.Equal(t, "plain", drive.calls[1].TextBody)
	assert.Equal(t, "<p>html</p>", drive.calls[1].Body)
}

func TestSendText_DriveErrorPropagates(t *testing.T) {
	sentinel := errors.New("smtp boom")
	drive := &recordingDrive{errQueue: []error{sentinel}}
	withDrive(t, drive)

	err := SendText("alice@example.com", "", "", "s", "t", "b")
	assert.ErrorIs(t, err, sentinel)
	assert.Len(t, drive.calls, 1)
}

func TestSendText_BccNotSentWhenMainSendFails(t *testing.T) {
	sentinel := errors.New("smtp boom")
	drive := &recordingDrive{errQueue: []error{sentinel}}
	withDrive(t, drive)

	err := SendText("alice@example.com", "", "bcc@example.com", "s", "t", "b")
	assert.ErrorIs(t, err, sentinel)
	// Main send failed -> no BCC attempt.
	assert.Len(t, drive.calls, 1)
}

func TestSendText_BccErrorPropagates(t *testing.T) {
	sentinel := errors.New("bcc boom")
	drive := &recordingDrive{errQueue: []error{nil, sentinel}}
	withDrive(t, drive)

	err := SendText("alice@example.com", "", "bcc@example.com", "s", "t", "b")
	assert.ErrorIs(t, err, sentinel)
	assert.Len(t, drive.calls, 2)
}

// -----------------------------------------------------------------------------
// SendHtmlTemplate
// -----------------------------------------------------------------------------

func TestSendHtmlTemplate_NilDriveIsNoOp(t *testing.T) {
	err := SendHtmlTemplate("to@example.com", "", "", "subject", "text",
		"verify_email_en", map[string]any{"url": "https://x"})
	assert.NoError(t, err)
}

func TestSendHtmlTemplate_InvalidTemplateNameReturnsError(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendHtmlTemplate("to@example.com", "", "", "s", "t",
		"definitely_not_allowed", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid template name")
	assert.Empty(t, drive.calls, "drive must not be invoked when the template name is invalid")
}

func TestSendHtmlTemplate_RendersTemplateAndSends(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendHtmlTemplate("bob@example.com", "", "", "Verify",
		"plain fallback", "verify_email_en",
		map[string]any{"url": "https://example.com/verify?token=abc"})

	require.NoError(t, err)
	require.Len(t, drive.calls, 1)

	c := drive.calls[0]
	assert.Equal(t, "bob@example.com", c.To)
	assert.Equal(t, "Verify", c.Subject)
	assert.Equal(t, "plain fallback", c.TextBody)
	// The rendered body must contain the interpolated URL from the template.
	assert.Contains(t, c.Body, "https://example.com/verify?token=abc")
}

func TestSendHtmlTemplate_WithBcc_SendsTwice(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendHtmlTemplate("bob@example.com", "", "bcc@example.com",
		"Verify", "plain", "verify_email_en",
		map[string]any{"url": "https://example.com/v"})

	require.NoError(t, err)
	require.Len(t, drive.calls, 2)
	assert.Equal(t, "bob@example.com", drive.calls[0].To)
	assert.Equal(t, "bcc@example.com", drive.calls[1].To)
	// Both legs carry the rendered body.
	assert.Contains(t, drive.calls[0].Body, "https://example.com/v")
	assert.Contains(t, drive.calls[1].Body, "https://example.com/v")
}

func TestSendHtmlTemplate_DriveErrorPropagates(t *testing.T) {
	sentinel := errors.New("send failed")
	drive := &recordingDrive{errQueue: []error{sentinel}}
	withDrive(t, drive)

	err := SendHtmlTemplate("bob@example.com", "", "", "Verify", "plain",
		"verify_email_en", map[string]any{"url": "u"})
	assert.ErrorIs(t, err, sentinel)
}

// -----------------------------------------------------------------------------
// SendHtmlTemplateAndAttachments
// -----------------------------------------------------------------------------

func TestSendHtmlTemplateAndAttachments_NilDriveIsNoOp(t *testing.T) {
	err := SendHtmlTemplateAndAttachments("to@example.com", "", "", "subject",
		"text", "verify_email_en", map[string]any{"url": "x"}, nil)
	assert.NoError(t, err)
}

func TestSendHtmlTemplateAndAttachments_InvalidTemplateNameReturnsError(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendHtmlTemplateAndAttachments("to@example.com", "", "", "s", "t",
		"bogus_template", nil, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid template name")
	assert.Empty(t, drive.calls)
}

func TestSendHtmlTemplateAndAttachments_RendersAndAttaches(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	extra := []*types.MailAttachment{{Name: "invoice.pdf"}, {Name: "report.csv"}}

	err := SendHtmlTemplateAndAttachments("bob@example.com", "", "",
		"Welcome", "plain", "welcome_mail_en", nil, extra)

	require.NoError(t, err)
	require.Len(t, drive.calls, 1)

	c := drive.calls[0]
	assert.Equal(t, "bob@example.com", c.To)
	assert.Equal(t, "Welcome", c.Subject)
	// welcome_mail_en.html has no cid references, so the only attachments are
	// the extras we passed in.
	require.Len(t, c.Attachments, 2)
	assert.Equal(t, "invoice.pdf", c.Attachments[0].Name)
	assert.Equal(t, "report.csv", c.Attachments[1].Name)
	// Template was still rendered.
	assert.Contains(t, c.Body, "welcome")
}

func TestSendHtmlTemplateAndAttachments_NoExtraAttachments(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendHtmlTemplateAndAttachments("bob@example.com", "", "",
		"Welcome", "plain", "welcome_mail_en", nil, nil)

	require.NoError(t, err)
	require.Len(t, drive.calls, 1)
	assert.Empty(t, drive.calls[0].Attachments)
}

func TestSendHtmlTemplateAndAttachments_WithBcc(t *testing.T) {
	drive := &recordingDrive{}
	withDrive(t, drive)

	err := SendHtmlTemplateAndAttachments("bob@example.com", "", "bcc@example.com",
		"Welcome", "plain", "welcome_mail_en", nil,
		[]*types.MailAttachment{{Name: "a.txt"}})

	require.NoError(t, err)
	require.Len(t, drive.calls, 2)
	assert.Equal(t, "bob@example.com", drive.calls[0].To)
	assert.Equal(t, "bcc@example.com", drive.calls[1].To)
	// Attachments are shared on both legs.
	for i, c := range drive.calls {
		require.Len(t, c.Attachments, 1, "leg %d should carry the attachment", i)
		assert.Equal(t, "a.txt", c.Attachments[0].Name)
	}
}

func TestSendHtmlTemplateAndAttachments_DriveErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	drive := &recordingDrive{errQueue: []error{sentinel}}
	withDrive(t, drive)

	err := SendHtmlTemplateAndAttachments("bob@example.com", "", "",
		"Welcome", "plain", "welcome_mail_en", nil,
		[]*types.MailAttachment{{Name: "a.txt"}})
	assert.ErrorIs(t, err, sentinel)
}

// -----------------------------------------------------------------------------
// Integration-style: template whitelist + rendering consistency
// -----------------------------------------------------------------------------

func TestAllEmbeddedTemplatesPassWhitelist(t *testing.T) {
	// Every template we actually ship must be accepted by the whitelist,
	// otherwise SendHtmlTemplate could never load it.
	embedded := []string{
		"verify_email_en",
		"welcome_mail_en",
		"recover_password_verify_email_en",
	}
	for _, name := range embedded {
		t.Run(name, func(t *testing.T) {
			assert.True(t, isValidTemplateName(name), "shipped template %q must pass the whitelist", name)
		})
	}
}

func TestRenderedBody_HasNoUnescapedTemplateDelimiters(t *testing.T) {
	// Sanity: rendering verify_email_en with data must not leave raw "{{ }}"
	// behind.
	drive := &recordingDrive{}
	withDrive(t, drive)

	require.NoError(t, SendHtmlTemplate("bob@example.com", "", "", "Verify", "",
		"verify_email_en", map[string]any{"url": "https://e.com/v"}))

	require.NotEmpty(t, drive.calls)
	assert.False(t, strings.Contains(drive.calls[0].Body, "{{"),
		"rendered body must not contain unprocessed template actions")
}

func TestWithDrive_RestoresPreviousDrive(t *testing.T) {
	// withDrive must restore useDrive exactly after the sub-test cleans up.
	original := useDrive

	t.Run("inner", func(t *testing.T) {
		mock := &recordingDrive{}
		withDrive(t, mock)
		assert.Same(t, mock, useDrive, "drive should be swapped inside the test")
	})

	assert.Equal(t, original, useDrive, "drive must be restored after the test")
}
