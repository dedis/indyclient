package indyclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// This unit test unfortunately only works when you are online, and when the BuilderNet is responding.
// It could be converted to be self-contained by learning how to start up a local indy-node cluster.

func Test_SovrinBuilderNet(t *testing.T) {
	pool, err := NewPool(SovrinPool("BuilderNet"))
	require.NoError(t, err)
	require.Equal(t, len(pool.Validators), 4)

	reply, err := pool.GetTransaction(DomainLedger, 1)
	require.NoError(t, err)
	require.NotNil(t, reply)
	require.Equal(t, reply.Op, "REPLY")
	t.Log(string(reply.Result))
}
