package ui

import (
	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/core/project"
)

// Owner is the client's view of the connection owner: the surface its
// attachment offers, whether that attachment is in this process or over a
// connection (#209).
type Owner = core.Surface

// refreshProjections is gone: every mutation the client makes now goes through
// an owner command, whose commit rebuilds the projections and pushes a delta.
// Nothing has to remember to repaint (#198).

// WithOwner attaches the owner the model subscribes to, and subscribes to the
// account at once: the login screen is drawn from it.
func (m RootModel) WithOwner(o Owner) RootModel {
	m.owner = o
	if o != nil {
		m.accountSub = o.Subscribe(project.AccountWindow{})
	}
	return m
}

// subscribeChatList opens (or re-opens) the chatlist subscription for the
// current folder and the window the list component wants.
func (m *RootModel) subscribeChatList() {
	if m.owner == nil {
		return
	}
	if m.chatListSub != 0 {
		m.owner.Unsubscribe(m.chatListSub)
	}
	offset, limit, _ := m.chatList.WindowRequest()
	m.chatListSub = m.owner.Subscribe(project.ChatListWindow{
		Folder: m.activeFolder,
		Offset: offset,
		Limit:  limit,
	})
}

// syncChatListWindow asks the owner for a wider or shifted window when the
// cursor has moved near the edge of the one the client holds. It is a no-op
// while the cursor stays inside the overscan, which is the common case.
func (m *RootModel) syncChatListWindow() {
	if m.owner == nil || m.chatListSub == 0 {
		return
	}
	offset, limit, changed := m.chatList.WindowRequest()
	if !changed {
		return
	}
	m.owner.MoveWindow(m.chatListSub, project.ChatListWindow{
		Folder: m.activeFolder,
		Offset: offset,
		Limit:  limit,
	})
}
