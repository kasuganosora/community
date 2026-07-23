package types

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMailStruct(t *testing.T) {
	mail := &Mail{
		To:       "test@example.com",
		Cc:       "cc@example.com",
		Subject:  "Test Subject",
		Body:     "<html><body>Test</body></html>",
		TextBody: "Test",
	}

	assert.Equal(t, "test@example.com", mail.To)
	assert.Equal(t, "cc@example.com", mail.Cc)
	assert.Equal(t, "Test Subject", mail.Subject)
	assert.Equal(t, "<html><body>Test</body></html>", mail.Body)
	assert.Equal(t, "Test", mail.TextBody)
}

func TestMailWithAttachments(t *testing.T) {
	mail := &Mail{
		To:      "test@example.com",
		Subject: "Test",
		Attachments: []*MailAttachment{
			{Name: "file1.txt"},
			{Name: "file2.txt"},
		},
	}

	assert.Len(t, mail.Attachments, 2)
	assert.Equal(t, "file1.txt", mail.Attachments[0].Name)
	assert.Equal(t, "file2.txt", mail.Attachments[1].Name)
}

func TestMailAttachmentStruct(t *testing.T) {
	attachment := &MailAttachment{
		Name:      "test.txt",
		ContentId: "cid123",
	}

	assert.Equal(t, "test.txt", attachment.Name)
	assert.Equal(t, "cid123", attachment.ContentId)
	assert.Nil(t, attachment.File)
}

func TestMailAttachmentWithFile(t *testing.T) {
	var mockFile fs.File = nil // Can't easily mock fs.File

	attachment := &MailAttachment{
		File:      mockFile,
		Name:      "test.txt",
		ContentId: "cid123",
	}

	assert.Nil(t, attachment.File)
}

func TestDriveInterface(t *testing.T) {
	// Create a mock implementation of Drive interface
	mockDrive := &MockDrive{}

	var _ Drive = mockDrive // Verify interface implementation

	assert.NotNil(t, mockDrive)
}

// MockDrive implements Drive interface for testing.
type MockDrive struct{}

func (m *MockDrive) SendMail(to string, cc string, mailObject *Mail) error {
	return nil
}

func TestMailEmptyFields(t *testing.T) {
	mail := &Mail{}

	assert.Empty(t, mail.To)
	assert.Empty(t, mail.Cc)
	assert.Empty(t, mail.Subject)
	assert.Empty(t, mail.Body)
	assert.Empty(t, mail.TextBody)
	assert.Nil(t, mail.Attachments)
}

func TestMailAttachmentEmptyFields(t *testing.T) {
	attachment := &MailAttachment{}

	assert.Empty(t, attachment.Name)
	assert.Empty(t, attachment.ContentId)
	assert.Nil(t, attachment.File)
}

func TestMailMultipleRecipients(t *testing.T) {
	mail := &Mail{
		To:      "user1@example.com,user2@example.com",
		Subject: "Multi-recipient",
	}

	assert.Contains(t, mail.To, "user1@example.com")
	assert.Contains(t, mail.To, "user2@example.com")
}

func TestMailHtmlBody(t *testing.T) {
	htmlBody := `<html>
		<head><title>Test</title></head>
		<body>
			<h1>Hello</h1>
			<p>This is a test email.</p>
		</body>
	</html>`

	mail := &Mail{
		To:      "test@example.com",
		Subject: "HTML Email",
		Body:    htmlBody,
	}

	assert.Contains(t, mail.Body, "<html>")
	assert.Contains(t, mail.Body, "</html>")
	assert.Contains(t, mail.Body, "<h1>Hello</h1>")
}

func TestMailAttachmentSlice(t *testing.T) {
	attachments := []*MailAttachment{
		{Name: "file1.pdf", ContentId: "cid1"},
		{Name: "file2.png", ContentId: "cid2"},
		{Name: "file3.jpg", ContentId: "cid3"},
	}

	mail := &Mail{
		Attachments: attachments,
	}

	assert.Len(t, mail.Attachments, 3)
}
