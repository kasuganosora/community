package models

import "context"

type structuredMentionsKey struct{}

// WithStructuredMentions marks content whose recipients come from an
// authenticated protocol envelope, not text that happens to contain @names.
func WithStructuredMentions(ctx context.Context) context.Context {
	return context.WithValue(ctx, structuredMentionsKey{}, true)
}

type structuredRecipientIDsKey struct{}

// WithStructuredRecipientIDs supplies validated protocol recipients to the
// content hook so timestamped join rows exist before conversation/event hooks.
func WithStructuredRecipientIDs(ctx context.Context, ids []uint64) context.Context {
	return context.WithValue(WithStructuredMentions(ctx), structuredRecipientIDsKey{}, ids)
}
