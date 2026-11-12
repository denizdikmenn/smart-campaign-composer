package delivery

import (
	"context"
	"errors"

	"github.com/smart-campaign-composer/backend/internal/content"
)

// ContentSource adapts the content service's approved-campaign resolver to
// a MessageSource. Used by the API (send now) and the worker (scheduled).
func ContentSource(svc *content.Service) MessageSource {
	return func(ctx context.Context, id int64, fn func(Message) error) error {
		err := svc.ApprovedMessages(ctx, id, func(m content.Message) error {
			return fn(Message{CustomerID: m.CustomerID, Channel: m.Channel, VariantPosition: m.VariantPosition,
				GroupPosition: m.GroupPosition, Language: m.Language, Source: m.Source, Subject: m.Subject, Body: m.Body, Skip: m.Skip})
		})
		if errors.Is(err, content.ErrNotApproved) {
			return ErrNotApproved
		}
		return err
	}
}
