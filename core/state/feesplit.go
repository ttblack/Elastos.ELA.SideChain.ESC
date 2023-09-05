package state

import (
	"github.com/elastos/Elastos.ELA.SideChain.ESC/common"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/core/types"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/ethdb"
)

var FEESPLIT_PRE = "ELA_FEE_SPLIT"

type feesplit struct {
	addresses map[common.Address]*types.FeeSplit
	db        ethdb.KeyValueStore
}

func newfeesplit(db ethdb.KeyValueStore) *feesplit {
	return &feesplit{
		addresses: make(map[common.Address]*types.FeeSplit),
		db:        db,
	}
}

func (fs *feesplit) ContainsAddress(address common.Address) bool {
	_, ok := fs.addresses[address]
	if !ok {
		key := FEESPLIT_PRE + address.String()
		data, err := fs.db.Get([]byte(key))
		if err != nil {
			return false
		}
		if len(data) > 0 {
			return true
		}
	}
	return ok
}

func (fs *feesplit) GetFeeSplit(address common.Address) (*types.FeeSplit, error) {
	feesplitData, ok := fs.addresses[address]
	if !ok {
		key := FEESPLIT_PRE + address.String()
		data, err := fs.db.Get([]byte(key))
		if err != nil {
			return nil, err
		}
		if len(data) > 0 {
			feesplitData = new(types.FeeSplit)
			err = feesplitData.Deserialize(data)
			if err != nil {
				return nil, err
			}
			return feesplitData, nil
		}
	}
	return feesplitData, nil
}

func (fs *feesplit) Add(address common.Address, f *types.FeeSplit) bool {
	if _, present := fs.addresses[address]; present {
		return false
	}
	fs.addresses[address] = f
	return true
}

func (fs *feesplit) DeleteAddress(address common.Address) {
	delete(fs.addresses, address)
}

func (fs *feesplit) Copy() *feesplit {
	cp := newfeesplit(fs.db)
	for k, v := range fs.addresses {
		cp.addresses[k] = v.Copy()
	}
	return cp
}

func (fs *feesplit) Commit() {
	for k, v := range fs.addresses {
		key := FEESPLIT_PRE + k.String()
		fs.db.Put([]byte(key), v.Serialize())
	}
}

type feesplitChange struct {
	contract *common.Address
}

func (fs feesplitChange) revert(s *StateDB) {
	s.accessList.DeleteAddress(*fs.contract)
}

func (fs feesplitChange) dirtied() *common.Address {
	return nil
}
