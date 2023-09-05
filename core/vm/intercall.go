package vm

import (
	"bytes"

	"github.com/elastos/Elastos.ELA.SideChain.ESC/common"
)

type InternalCall struct {
	Sender   common.Address
	Contract common.Address
	To       common.Address
	GasCost  uint64
}

func (c *Contract) OpInterCall(sender common.Address, contract common.Address, to common.Address, gasUsed uint64) {
	for _, table := range c.internalCallTable {
		if bytes.Equal(table.To.Bytes(), to.Bytes()) {
			table.GasCost += gasUsed
			return
		}
	}
	call := new(InternalCall)
	call.Sender = sender
	call.Contract = contract
	call.To = to
	call.GasCost = gasUsed
	c.internalCallTable = append(c.internalCallTable, call)
	return
}

func (c *Contract) InterCallList() []*InternalCall {
	var tempList = make([]*InternalCall, 0)
	for _, data := range c.internalCallTable {
		temp := &InternalCall{Sender: data.Sender, Contract: data.Contract, To: data.To, GasCost: data.GasCost}
		tempList = append(tempList, temp)
	}
	return tempList
}
