package core

import "github.com/sorokin-vladimir/tele/internal/domain"

// addressMentions is es with every mention of a user by name addressed the way
// the owner would address that user, just before it goes to Telegram (#208).
// Whatever address came with the entity is ignored: only the owner holds
// addresses, and a client has none to give. A mention of somebody the owner
// holds no address for is left out, so the name goes as plain text rather
// than the message being refused. So is one the owner could reach only
// through a message the person wrote, which an entity cannot carry.
func (o *Owner) addressMentions(es []domain.MessageEntity) []domain.MessageEntity {
	if len(es) == 0 {
		return es
	}
	out := make([]domain.MessageEntity, 0, len(es))
	for _, e := range es {
		if e.Type == "mention_name" {
			addr, err := o.userAddress(e.UserID)
			if err != nil || addr.AccessHash == 0 {
				continue
			}
			e.AccessHash = addr.AccessHash
		}
		out = append(out, e)
	}
	return out
}
