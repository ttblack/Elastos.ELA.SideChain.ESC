package types

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/elastos/Elastos.ELA.SideChain.ESC/common"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/rlp"
)

type FeeSplit struct {
	// hex address of registered contract
	ContractAddress common.Address `json:"contract_address,omitempty"`
	// bech32 address of contract deployer
	DeployerAddress common.Address `json:"deployer_address,omitempty"`
	// bech32 address of account receiving the transaction fees it defaults to
	// deployer_address
	WithdrawerAddress common.Address `json:"withdrawer_address,omitempty"`

	ShareHolding *big.Int `json:"percent,omitempty"`

	TxHash common.Hash
}

func NewFeeSplit(contract, deployer, withdrawer common.Address, share uint8) *FeeSplit {
	var emptyAddress common.Address
	if bytes.Equal(emptyAddress.Bytes(), withdrawer.Bytes()) {
		withdrawer = deployer
	}
	if share < 0 || share > 100 {
		panic(any("share is need positive"))
	}
	if share == 0 {
		share = 50
	}

	return &FeeSplit{
		ContractAddress:   contract,
		DeployerAddress:   deployer,
		WithdrawerAddress: withdrawer,
		ShareHolding:      big.NewInt(int64(share)),
	}
}

// Validate performs a stateless validation of a FeeSplit
func (fs *FeeSplit) Validate(register common.Address) error {
	if bytes.Equal(fs.ContractAddress.Bytes(), common.Address{}.Bytes()) {
		str := fmt.Sprintf("ContractAddress '%s' is not a valid ethereum hex address", fs.ContractAddress.String())
		return errors.New(str)
	}
	if !bytes.Equal(fs.DeployerAddress.Bytes(), register.Bytes()) {
		str := fmt.Sprintf("DeployerAddress '%s' is not register", fs.DeployerAddress.String())
		return errors.New(str)
	}

	if bytes.Equal(fs.WithdrawerAddress.Bytes(), common.Address{}.Bytes()) {
		str := fmt.Sprintf("WithdrawerAddress '%s' is not a valid ethereum hex address", fs.WithdrawerAddress.String())
		return errors.New(str)
	}

	return nil
}

func (fs *FeeSplit) Copy() *FeeSplit {
	var f = NewFeeSplit(fs.ContractAddress, fs.DeployerAddress, fs.WithdrawerAddress, uint8(fs.ShareHolding.Int64()))
	f.TxHash = fs.TxHash
	return f
}

func (fs *FeeSplit) Serialize() []byte {
	w := new(bytes.Buffer)
	rlp.Encode(w, *fs)
	return w.Bytes()
}

func (fs *FeeSplit) Deserialize(data []byte) error {
	return rlp.DecodeBytes(data, fs)
}
