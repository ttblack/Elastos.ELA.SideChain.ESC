package vm

import (
	"github.com/elastos/Elastos.ELA.SideChain.ESC/common"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/params"
)

type PrecompiledContractESC interface {
	RequiredGas(evm *EVM, input []byte) uint64  // RequiredPrice calculates the contract gas use
	Run(evm *EVM, input []byte) ([]byte, error) // Run runs the precompiled contract
}

var PrecompiledContractsESC = map[common.Address]PrecompiledContractESC{
	common.BytesToAddress(params.FeeSplit.Bytes()): &feesplit{},
}

func RunPrecompiledContractESC(evm *EVM, p PrecompiledContractESC, input []byte, suppliedGas uint64) (ret []byte, remainingGas uint64, err error) {
	gasCost := p.RequiredGas(evm, input)
	if suppliedGas < gasCost {
		return nil, 0, ErrOutOfGas
	}
	output, err := p.Run(evm, input)
	suppliedGas -= gasCost
	return output, suppliedGas, err
}
