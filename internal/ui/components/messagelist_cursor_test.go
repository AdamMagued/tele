package components_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageList_CursorStartsAtNewest(t *testing.T) {
	ml := components.NewMessageList(20, 40)
	ml.SetMessages(makeMessages(5))
	assert.Equal(t, 5, ml.SelectedMessageID())
}

func TestMessageList_CursorUp_SelectsOlderMessage(t *testing.T) {
	ml := components.NewMessageList(20, 40)
	ml.SetMessages(makeMessages(5))
	ml.CursorUp()
	assert.Equal(t, 4, ml.SelectedMessageID())
	ml.CursorUp()
	assert.Equal(t, 3, ml.SelectedMessageID())
}

func TestMessageList_CursorDown_SelectsNewerMessage(t *testing.T) {
	ml := components.NewMessageList(20, 40)
	ml.SetMessages(makeMessages(5))
	ml.CursorUp()
	ml.CursorUp() // cursor on msg 3
	ml.CursorDown()
	assert.Equal(t, 4, ml.SelectedMessageID())
}

func TestMessageList_CursorUp_StopsAtOldest(t *testing.T) {
	ml := components.NewMessageList(20, 40)
	ml.SetMessages(makeMessages(3))
	ml.CursorUp()
	ml.CursorUp()
	atOldest := ml.CursorUp() // already on the oldest message
	assert.Equal(t, 1, ml.SelectedMessageID())
	assert.True(t, atOldest)
}

// #124 case 1: history shorter than the viewport — the top message must be
// reachable even though nothing scrolls.
func TestMessageList_CursorUp_SelectsTopInShortChat(t *testing.T) {
	ml := components.NewMessageList(20, 40)
	ml.SetMessages(makeMessages(3))
	ml.CursorUp() // msg 2
	ml.CursorUp() // msg 1
	assert.Equal(t, 1, ml.SelectedMessageID())
	assert.Contains(t, ml.View(), "msg 1")
}

// Cursor stepping into older history pulls the viewport so the active bubble
// stays on screen.
func TestMessageList_CursorUp_ScrollsCursorIntoView(t *testing.T) {
	ml := components.NewMessageList(9, 40) // ~3 bubbles tall
	ml.SetMessages(makeMessages(20))
	for i := 0; i < 6; i++ {
		ml.CursorUp()
	}
	ml.View()
	_, ok := ml.SelectedBubbleRect()
	assert.True(t, ok, "cursor bubble must be visible after stepping up")
}

// Line-scrolling up (j/k) past the cursor must drag the cursor along so it never
// leaves the viewport.
func TestMessageList_LineScrollUp_KeepsCursorInViewport(t *testing.T) {
	ml := components.NewMessageList(3, 40) // ~1 bubble tall
	ml.SetMessages(makeMessages(10))       // cursor starts on newest (msg 10)
	ml.ScrollUpBy(6)                       // pan toward older history
	ml.View()
	_, ok := ml.SelectedBubbleRect()
	require.True(t, ok, "cursor must stay visible while line-scrolling")
	assert.NotEqual(t, 10, ml.SelectedMessageID(), "cursor dragged off the newest bubble")
}

// Line-scrolling down past the cursor must drag it down to stay in view.
func TestMessageList_LineScrollDown_KeepsCursorInViewport(t *testing.T) {
	ml := components.NewMessageList(3, 40)
	ml.SetMessages(makeMessages(10))
	for i := 0; i < 5; i++ {
		ml.CursorUp() // move cursor up into history (msg 5)
	}
	ml.ScrollDownBy(30) // scroll all the way back to the bottom
	ml.View()
	_, ok := ml.SelectedBubbleRect()
	require.True(t, ok, "cursor must stay visible while line-scrolling")
	assert.NotEqual(t, 5, ml.SelectedMessageID(), "cursor dragged toward the bottom")
}

// The reported bug: after a line scroll the cursor sits at a viewport edge; the
// first ctrl+k must step it within the viewport, not teleport-center the chat.
func TestMessageList_CursorUp_NoJumpAfterLineScroll(t *testing.T) {
	ml := components.NewMessageList(15, 40)
	ml.SetMessages(makeMessages(30))
	ml.ScrollUpBy(20) // pan into history; cursor clamps to the bottom edge
	off0 := ml.ScrollInfo().Offset
	ml.CursorUp()
	assert.Equal(t, off0, ml.ScrollInfo().Offset,
		"first ctrl+k after a line scroll must not jump-center the chat")
}

// Once the cursor reaches the middle, further steps scroll the chat.
func TestMessageList_CursorUp_ScrollsOnceCentered(t *testing.T) {
	ml := components.NewMessageList(15, 40)
	ml.SetMessages(makeMessages(30))
	off0 := ml.ScrollInfo().Offset
	for i := 0; i < 12; i++ {
		ml.CursorUp()
	}
	assert.Less(t, ml.ScrollInfo().Offset, off0,
		"after the cursor reaches the middle the chat scrolls toward older history")
}

// Symmetric: stepping down walks the cursor toward the bottom before scrolling.
func TestMessageList_CursorDown_StepsWithoutScrollingUntilBottom(t *testing.T) {
	ml := components.NewMessageList(15, 40)
	ml.SetMessages(makeMessages(30))
	for i := 0; i < 12; i++ {
		ml.CursorUp() // cursor centered up in history; viewport scrolled
	}
	off0 := ml.ScrollInfo().Offset
	ml.CursorDown()
	assert.Equal(t, off0, ml.ScrollInfo().Offset,
		"cursor descends within the viewport; the chat must not scroll yet")
}

// In the middle region the cursor stays vertically centered while the viewport
// scrolls underneath it.
func TestMessageList_CursorUp_KeepsCursorCentered(t *testing.T) {
	ml := components.NewMessageList(15, 40)
	ml.SetMessages(makeMessages(20))
	for i := 0; i < 10; i++ {
		ml.CursorUp() // msg 20 -> msg 10
	}
	ml.View()
	rect1, ok := ml.SelectedBubbleRect()
	require.True(t, ok)
	top1 := rect1.Top

	ml.CursorUp() // msg 10 -> msg 9
	ml.View()
	rect2, ok := ml.SelectedBubbleRect()
	require.True(t, ok)

	assert.Equal(t, 9, ml.SelectedMessageID())
	assert.Equal(t, top1, rect2.Top, "cursor stays put; the viewport scrolls instead")
	assert.InDelta(t, 15/2, top1, 1, "cursor sits near the vertical middle")
}

// withTallMessage returns makeMessages(n) with message id carrying lines lines
// of text, so its bubble is taller than a small pane.
func withTallMessage(n, id, lines int) []domain.Message {
	msgs := makeMessages(n)
	text := make([]string, lines)
	for l := range text {
		text[l] = fmt.Sprintf("line-%02d", l+1)
	}
	msgs[id-1].Text = strings.Join(text, "\n")
	return msgs
}

// #306: stepping onto a message taller than the pane shows where it starts;
// the rest is read by scrolling.
func TestMessageList_CursorDown_PutsTallMessageTopAtPaneTop(t *testing.T) {
	ml := components.NewMessageList(10, 40)
	ml.SetMessages(withTallMessage(12, 6, 30))
	for ml.SelectedMessageID() != 5 {
		ml.CursorUp()
	}
	ml.CursorDown()
	require.Equal(t, 6, ml.SelectedMessageID())
	assert.Contains(t, ml.View(), "line-01")
}

// #306: stepping off a tall message scrolls as far as the next one needs, so
// the selection never lands below the pane.
func TestMessageList_CursorDown_FromTallMessageShowsTheNextOne(t *testing.T) {
	ml := components.NewMessageList(10, 40)
	ml.SetMessages(withTallMessage(12, 6, 30))
	for ml.SelectedMessageID() != 6 {
		ml.CursorUp()
	}
	ml.CursorDown()
	require.Equal(t, 7, ml.SelectedMessageID())
	assert.Contains(t, ml.View(), "msg 7")
}

// #306: stepping up onto a tall message also puts its top at the top of the
// pane, instead of the middle that suits a short one.
func TestMessageList_CursorUp_PutsTallMessageTopAtPaneTop(t *testing.T) {
	ml := components.NewMessageList(10, 40)
	ml.SetMessages(withTallMessage(12, 6, 30))
	for ml.SelectedMessageID() != 7 {
		ml.CursorUp()
	}
	ml.CursorUp()
	require.Equal(t, 6, ml.SelectedMessageID())
	view := ml.View()
	assert.Contains(t, view, "line-01")
	assert.NotContains(t, view, "msg 5", "nothing above the tall bubble is on screen")
}

// #306: a message taller than half the pane but no taller than the pane is
// shown whole when stepped up onto, rather than cut off below the middle.
func TestMessageList_CursorUp_ShowsMediumMessageWhole(t *testing.T) {
	ml := components.NewMessageList(10, 40)
	ml.SetMessages(withTallMessage(12, 6, 6))
	for ml.SelectedMessageID() != 7 {
		ml.CursorUp()
	}
	ml.CursorUp()
	require.Equal(t, 6, ml.SelectedMessageID())
	view := ml.View()
	assert.Contains(t, view, "line-01")
	assert.Contains(t, view, "line-06")
}
