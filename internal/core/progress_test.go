package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishProgress_DeliversToTheClient(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	c := o.Attach()

	o.publishProgress(Progress{ChatID: 1, Ref: "r1#0", Part: 2, Parts: 5, Done: 30, Total: 100})

	got := only[Progress](queued(c))
	require.Len(t, got, 1, "nothing was published")
	assert.Equal(t, "r1#0", got[0].Ref)
	assert.Equal(t, 2, got[0].Part)
	assert.Equal(t, int64(30), got[0].Done)
}

func TestPublishProgress_DropsWhenNobodyIsDraining(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	c := o.Attach()

	// More frames than the stream holds. A dropped progress frame costs one
	// repaint; blocking the uploader would cost the upload.
	for i := 0; i < 1000; i++ {
		o.publishProgress(Progress{ChatID: 1, Ref: "r1#0", Done: int64(i)})
	}

	assert.Len(t, queued(c), streamSize, "the stream should still hold what it could")
}
