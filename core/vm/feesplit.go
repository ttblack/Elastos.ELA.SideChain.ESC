package vm

import (
	"errors"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/accounts/abi"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/common"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/core/types"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/log"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/params"
)

type feesplit struct{}

func (f *feesplit) RequiredGas(evm *EVM, input []byte) uint64 {
	return params.RegisterFeeSplitGas
}

func (f *feesplit) Run(evm *EVM, input []byte) ([]byte, error) {
	if len(input) < 32 {
		return false32Byte, errors.New("error input data")
	}
	input = getData(input, 32, uint64(len(input)-32))
	Address, _ := abi.NewType("address", "", nil)
	UINT8, _ := abi.NewType("uint8", "uint8", nil)
	method := abi.NewMethod("address", "address", abi.Function, "", false, false, []abi.Argument{{"contractAddress", Address, false}, {"deployerAddress", Address, false}, {"withdrawerAddress", Address, false}, {"percent", UINT8, false}}, nil)

	//var obj FeeSplit
	splitData, err := method.Inputs.Unpack(input)
	if err != nil {
		return false32Byte, err
	}
	var splitFee = types.NewFeeSplit(splitData[0].(common.Address), splitData[1].(common.Address), splitData[2].(common.Address), splitData[3].(uint8))
	err = splitFee.Validate(evm.Origin)
	if err != nil {
		log.Error("Register fee split Validate failed", "error", err)
		return false32Byte, err
	}
	err = evm.StateDB.AddFeeSplit(splitFee)
	if err != nil {
		log.Error("Register fee split failed", "error", err)
		return false32Byte, err
	}
	return true32Byte, nil
}
