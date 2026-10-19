package status

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"golang.org/x/net/html"
	"gorm.io/gorm"
)

const maxDirectRecipients = 10

type directAccountResolver func(context.Context, string) (*models.Account, error)
type directMention struct{ username, domain string }

// Use the renderer's Unicode username/boundary and URL-first conventions.
// Unlike display rendering, an explicit malformed remote suffix must not fall
// back to a local account with the same name.
var directMentionTokens = regexp.MustCompile("(https?://[^\\s<>\"'`]+)|(?:^|[^\\p{L}\\p{N}_/@.])(@([\\p{L}\\p{N}_]+)(?:@([A-Za-z0-9](?:[A-Za-z0-9.\\-:]*[A-Za-z0-9])?))?)")

// prepareDirectRecipients runs before any status write transaction. Network
// discovery retains the existing ActivityPub resolver's SSRF and TLS checks.
// replyRecipientID must come from a parent whose visibility was checked by the caller.
func prepareDirectRecipients(ctx context.Context, content string, senderID uint64, replyRecipientID *uint64) ([]uint64, error) {
	return prepareDirectRecipientsWithResolver(ctx, content, senderID, replyRecipientID, activitypub.ResolveSearchAccount)
}

func prepareDirectRecipientsWithResolver(ctx context.Context, content string, senderID uint64, replyRecipientID *uint64, resolve directAccountResolver) ([]uint64, error) {
	mentions, err := directMentions(content)
	if err != nil {
		return nil, err
	}
	if len(mentions) > maxDirectRecipients {
		return nil, fmt.Errorf("Direct messages support at most %d explicit recipients", maxDirectRecipients)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ids := []uint64{}
	seen := map[uint64]bool{}
	add := func(account *models.Account) error {
		if account == nil || account.Id == 0 || account.IsBan {
			return errors.New("Direct recipient is unavailable")
		}
		blocked, err := models.AccountsBlocked(db.GetDB().WithContext(ctx), &models.Account{Id: senderID}, account)
		if err != nil {
			return err
		}
		if blocked {
			return errors.New("Direct recipient is blocked")
		}
		if account.Id != senderID && !seen[account.Id] {
			ids = append(ids, account.Id)
			seen[account.Id] = true
		}
		return nil
	}
	localDomain := systemsettings.GetDomain()
	for _, mention := range mentions {
		domain := mention.domain
		if strings.EqualFold(domain, localDomain) {
			domain = ""
		}
		query := db.GetDB().WithContext(ctx).Where("username = ?", mention.username)
		if domain == "" {
			query = query.Where("domain IS NULL")
		} else {
			query = query.Where("domain = ?", domain)
		}
		var account models.Account
		err := query.First(&account).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		label := mention.username
		if mention.domain != "" {
			label += "@" + mention.domain
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if domain == "" {
				return nil, fmt.Errorf("Cannot resolve direct recipient @%s", label)
			}
			resolved, err := resolve(ctx, mention.username+"@"+domain)
			if err != nil || resolved == nil || resolved.Id == 0 {
				return nil, fmt.Errorf("Cannot resolve direct recipient @%s", label)
			}
			// The resolver may return the canonical domain of an exact WebFinger acct.
			// It must never turn remote discovery into a local account or another user.
			if resolved.IsLocal() || !strings.EqualFold(resolved.Username, mention.username) {
				return nil, fmt.Errorf("Cannot resolve direct recipient @%s", label)
			}
			account = *resolved
		}
		if err := add(&account); err != nil {
			return nil, fmt.Errorf("Direct recipient @%s is unavailable", label)
		}
	}
	if replyRecipientID != nil && *replyRecipientID != senderID {
		var account models.Account
		if err := db.GetDB().WithContext(ctx).First(&account, *replyRecipientID).Error; err != nil {
			return nil, errors.New("Reply recipient is unavailable")
		}
		if err := add(&account); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("A direct message requires at least one recipient other than yourself")
	}
	return ids, nil
}

func directMentions(content string) ([]directMention, error) {
	text := directVisibleText(maskDirectCode(content))
	result := []directMention{}
	seen := map[string]bool{}
	for _, match := range directMentionTokens.FindAllStringSubmatchIndex(text, -1) {
		if match[2] >= 0 {
			continue
		} // A URL, including its @path, is not a recipient.
		end := match[5]
		if end < len(text) && strings.ContainsRune("@:/\\", rune(text[end])) {
			return nil, errors.New("Invalid explicit direct recipient")
		}
		username := text[match[6]:match[7]]
		domain := ""
		if match[8] >= 0 {
			domain = strings.ToLower(text[match[8]:match[9]])
			parsed, err := url.Parse("https://" + domain)
			if err != nil || parsed.Hostname() == "" || parsed.Host != domain || parsed.Path != "" || parsed.User != nil {
				return nil, errors.New("Invalid explicit direct recipient")
			}
		}
		key := username + "@" + domain
		if !seen[key] {
			result = append(result, directMention{username, domain})
			seen[key] = true
		}
	}
	return result, nil
}

// Attributes, comments, and code are never authorization to disclose a DM.
func directVisibleText(content string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(content))
	var out strings.Builder
	blocked := 0
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return ""
			}
			break
		}
		switch kind {
		case html.StartTagToken:
			token := tokenizer.Token()
			if token.Data == "code" || token.Data == "pre" || token.Data == "script" || token.Data == "style" || token.Data == "textarea" {
				blocked++
			}
			out.WriteByte(' ')
		case html.EndTagToken:
			token := tokenizer.Token()
			if blocked > 0 && (token.Data == "code" || token.Data == "pre" || token.Data == "script" || token.Data == "style" || token.Data == "textarea") {
				blocked--
			}
			out.WriteByte(' ')
		case html.TextToken:
			if blocked == 0 {
				out.Write(tokenizer.Raw())
			}
		case html.SelfClosingTagToken, html.CommentToken:
			out.WriteByte(' ')
		}
	}
	return out.String()
}

func maskDirectCode(content string) string {
	data := []byte(content)
	var delimiter byte
	length := 0
	for i := 0; i < len(data); {
		if data[i] == '`' || data[i] == '~' {
			character := data[i]
			j := i
			for j < len(data) && data[j] == character {
				j++
			}
			run := j - i
			if delimiter == 0 && (character == '`' || run >= 3) {
				delimiter = character
				length = run
				for k := i; k < j; k++ {
					data[k] = ' '
				}
				i = j
				continue
			}
			if delimiter == character && run == length {
				delimiter = 0
				for k := i; k < j; k++ {
					data[k] = ' '
				}
				i = j
				continue
			}
		}
		if delimiter != 0 && data[i] != '\n' {
			data[i] = ' '
		}
		i++
	}
	return string(data)
}

// Pre-read only: discovery must finish before Replace obtains row locks.
// Replace compares this snapshot again under lock before using the recipients.
func prepareReplacementDirectRecipients(ctx context.Context, id uint64, owner *models.Account, req *replaceRequest) (*models.Status, []uint64, error) {
	var original models.Status
	if err := db.GetDB().WithContext(ctx).Preload("Content").First(&original, id).Error; err != nil {
		return nil, nil, err
	}
	if original.AccountId != owner.Id || !original.IsLocal() {
		return nil, nil, replacementError{403, "forbidden"}
	}
	visibility := original.Visibility
	if req.Visibility != nil {
		probe := models.Status{}
		if err := probe.SetVisibilityByString(*req.Visibility); err != nil {
			return nil, nil, invalidReplacement("Invalid visibility value")
		}
		visibility = probe.Visibility
	}
	if visibility != models.StatusVisibilityDirect {
		return nil, nil, nil
	}
	content := original.Content.Content
	if req.Status != nil {
		content = *req.Status
	}
	replyID := original.InReplyToId
	if req.replySet {
		replyID = nil
		if req.InReplyToId != nil && *req.InReplyToId != "" {
			parsed, err := strconv.ParseUint(*req.InReplyToId, 10, 64)
			if err != nil || parsed == 0 {
				return nil, nil, invalidReplacement("invalid in_reply_to_id")
			}
			replyID = &parsed
		}
	}
	var replyAccountID *uint64
	if replyID != nil {
		if *replyID == id {
			return nil, nil, invalidReplacement("Replacement cannot reply to the post being deleted")
		}
		var parent models.Status
		if err := db.GetDB().WithContext(ctx).Preload("ToAccounts").First(&parent, *replyID).Error; err != nil {
			return nil, nil, err
		}
		allowed, err := parent.CanViewEx(db.GetDB().WithContext(ctx), owner)
		if err != nil {
			return nil, nil, err
		}
		if !allowed {
			// Same answer as a missing reply target: do not confirm it exists.
			return nil, nil, gorm.ErrRecordNotFound
		}
		if !models.ValidateReplyVisibility(&parent, visibility) {
			return nil, nil, invalidReplacement("Reply visibility cannot be broader than its parent")
		}
		replyAccountID = &parent.AccountId
	}
	ids, err := prepareDirectRecipients(ctx, content, owner.Id, replyAccountID)
	if err != nil {
		return nil, nil, invalidReplacement(err.Error())
	}
	return &original, ids, nil
}

func sameDirectRecipientSnapshot(first, second *models.Status) bool {
	if first.Content.Content != second.Content.Content || first.Visibility != second.Visibility {
		return false
	}
	if first.InReplyToId == nil || second.InReplyToId == nil {
		return first.InReplyToId == nil && second.InReplyToId == nil
	}
	return *first.InReplyToId == *second.InReplyToId
}
