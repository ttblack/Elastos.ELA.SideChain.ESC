package msg

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSmallCroTx_Deserialize(t *testing.T) {
	msg := NewSmallCroTx("8ddc8d5e4fdc61ebd20aba2f7766f6055b108ccf74f2a26e23fae1575f65f9a723cc0835390e880dc9fb0e9adf4ee10862229fb6b377689b78fc95ac89bb3d4d", "090800012a3078386232333234666434306137343834333731314339423438424339363841354641456464344566300010270000000000000100133433353534333233393436313639303232363101a082897c761899aecd6049af5ceebb5c19ba356cb4f770f6ee6c8e959bf02b9b0000ffffffff02b037db964a231458d2d6ffd5ea18944c4f90e63d547c5d3b9874df66a4ead0a3204e000000000000000000004b59387ba35cf5d6e83cc5648f0a79d5724226dd8800b037db964a231458d2d6ffd5ea18944c4f90e63d547c5d3b9874df66a4ead0a3d3cffa08000000000000000021fe52a8b287def246bd774bb3b73373d95d99774c000000000001414067b602985d809a835807ba2ffd24708cbbaf09d48c68eab3176277d7874fe643214f0d8a26af766dab19f36ec88de363c4eadd43aa602356bd2351fedb8950d3232103db983dd7ff90332b1a8bb5f1d3726c968ed4bd019e2bc54602b5ff3f05e5c0a3ac")
	buffer := new(bytes.Buffer)
	err := msg.Serialize(buffer)
	assert.NoError(t, err)

	msg2 := &SmallCroTx{}
	err =  msg2.Deserialize(buffer)
	assert.NoError(t, err)

	assert.Equal(t, msg.rawTx, msg2.rawTx)
	assert.Equal(t, msg.signature, msg2.signature)
}
