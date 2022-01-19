// Copyright 2021 ChainSafe Systems
// SPDX-License-Identifier: LGPL-3.0-only

package evm

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/elastos/Elastos.ELA.SideChain.ESC/accounts"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/blockstore"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/bridgelog"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/chains/evm/aribiters"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/chains/evm/voter"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/config"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/dpos_msg"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/msg_pool"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/chainbridge-core/relayer"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/common"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/crypto"
	"github.com/elastos/Elastos.ELA.SideChain.ESC/log"

	"github.com/elastos/Elastos.ELA/events"
)

var Layer1ChainID uint64
var Layer2ChainID uint64

const MaxBatchCount = 100

type EventListener interface {
	ListenToEvents(startBlock *big.Int, chainID uint64, kvrw blockstore.KeyValueWriter, stopChn <-chan struct{}, errChn chan<- error) (<-chan *relayer.Message, <-chan *relayer.ChangeSuperSigner, <-chan *relayer.Message)
	ListenStatusEvents(startBlock *big.Int, chainID uint64, stopChn <-chan struct{}, errChn chan<- error) <-chan *relayer.ProposalEvent
}

type ProposalVoter interface {
	HandleProposal(message *relayer.Message) (*voter.Proposal, error)
	GetClient() voter.ChainClient
	SignAndBroadProposal(proposal *voter.Proposal) common.Hash
	SignAndBroadProposalBatch(list []*voter.Proposal) *dpos_msg.BatchMsg
	FeedbackBatchMsg(msg *dpos_msg.BatchMsg) common.Hash
	GetPublicKey() ([]byte, error)
	GetSignerAddress() (common.Address, error)
	SetArbiterList(arbiters []common.Address, totalCount int, signature [][]byte, bridgeAddress string) error
	GetArbiterList(bridgeAddress string) ([]common.Address, error)
	GetSuperSigner(bridgeAddress string) (common.Address, error)
	SuperSignerNodePublickey(bridgeAddress string) (string, error)
	IsDeployedBridgeContract(bridgeAddress string) bool
}

// EVMChain is struct that aggregates all data required for
type EVMChain struct {
	listener              EventListener // Rename
	writer                ProposalVoter
	chainID               uint64
	kvdb                  blockstore.KeyValueReaderWriter
	bridgeContractAddress string
	config                *config.GeneralChainConfig
	msgPool               *msg_pool.MsgPool
	currentProposal       *dpos_msg.BatchMsg
	arbiterManager        *aribiters.ArbiterManager
	superVoter            []byte
}

func NewEVMChain(dr EventListener, writer ProposalVoter,
	kvdb blockstore.KeyValueReaderWriter, chainID uint64,
	config *config.GeneralChainConfig, arbiterManager *aribiters.ArbiterManager,
	supervoter string) *EVMChain {
	chain := &EVMChain{listener: dr, writer: writer, kvdb: kvdb, chainID: chainID, config: config}
	chain.bridgeContractAddress = config.Opts.Bridge
	chain.superVoter = common.Hex2Bytes(supervoter)
	chain.msgPool = msg_pool.NewMsgPool(chain.superVoter)
	chain.arbiterManager = arbiterManager
	if writer != nil {
		go chain.subscribeEvent()
	}
	return chain
}

func (c *EVMChain) subscribeEvent() {
	events.Subscribe(func(e *events.Event) {
		switch e.Type {
		case dpos_msg.ETOnProposal:
			c.onProposalEvent(e)
		case dpos_msg.ETSelfOnDuty:
			go func() {
				time.Sleep(1 * time.Second) //wait block data write to db
				c.selfOnDuty(e)
			}()
		case dpos_msg.ETUpdateLayer2SuperVoter:
			c.superVoter = e.Data.([]byte)
			c.msgPool.UpdateSuperVoter(c.superVoter)
			bridgelog.Info("update super voter", "publickey:", common.Bytes2Hex(c.superVoter))
		}
	})
}

func (c *EVMChain) selfOnDuty(e *events.Event) {
	queueList := c.msgPool.GetQueueList()
	pendingList := c.msgPool.GetPendingList()

	log.Info("selfOnDuty selfOnDuty", "chainID", c.chainID, "queueList", len(queueList), "pendingList", len(pendingList))
	if c.chainID == Layer2ChainID {
		if len(queueList) > 0 {
			for _, p := range queueList {
				c.broadProposal(p)
			}
		}
		if len(pendingList) > 0 {
			log.Info("ExecuteToLayer2Proposal", "list count", len(pendingList))
			err := c.ExecuteProposals(pendingList)
			if err != nil {
				log.Error("ExecuteProposals error", "error", err)
			}
		}
	} else if c.chainID == Layer1ChainID {
		c.generateBatchProposal()
	}
}

func (c *EVMChain) broadProposal(p *voter.Proposal) {
	if p.ProposalIsComplete(c.writer.GetClient()) {
		log.Info("Proposal is executed", "proposal", p.Hash().String())
		c.msgPool.OnProposalExecuted(p.DepositNonce)
		return
	}
	hash := c.writer.SignAndBroadProposal(p)
	log.Info("SignAndBroadProposal", "hash", hash.String())
	list := c.msgPool.GetBeforeProposal(p)
	for _, msg := range list {
		go events.Notify(dpos_msg.ETOnProposal, msg) //self is a signature
	}
}

func (c *EVMChain) ExecuteProposals(list []*voter.Proposal) error {
	for _, p := range list {
		if p.ProposalIsComplete(c.writer.GetClient()) {
			log.Info("Proposal is completed", "proposal", p.Hash().String(), "dest", p.Destination, "chainid", c.chainID)
			c.msgPool.OnProposalExecuted(p.DepositNonce)
			continue
		}
		signature := c.msgPool.GetSignatures(p.Hash())
		superSig := c.msgPool.GetSuperVoterSigner(p.Hash())
		err := p.Execute(c.writer.GetClient(), signature, superSig)
		if err != nil {
			log.Error("proposal is execute error", "error", err)
			return err
		}
	}
	return nil
}

func (c *EVMChain) ExecuteProposalBatch(msg *dpos_msg.BatchMsg) error {
	items := make([]*voter.Proposal, 0)
	for _, it := range msg.Items {
		p := c.msgPool.GetQueueProposal(it.DepositNonce)
		if p.ProposalIsComplete(c.writer.GetClient()) {
			log.Info("Proposal is completed", "proposal", p.Hash().String(), "dest", p.Destination, "chainid", c.chainID)
			c.msgPool.OnProposalExecuted(p.DepositNonce)
			continue
		}
		items = append(items, p)
	}
	if len(items) > 0 {
		hash := msg.GetHash()
		signature := c.msgPool.GetSignatures(hash)
		superSig := c.msgPool.GetSuperVoterSigner(hash)
		err := voter.ExecuteBatch(c.writer.GetClient(), items, signature, superSig)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *EVMChain) onProposalEvent(e *events.Event) {
	log.Info("on deposit proposal event")
	if msg, ok := e.Data.(*dpos_msg.DepositProposalMsg); ok {
		if c.chainID != Layer2ChainID {
			return
		}
		err := c.onDepositMsg(msg)
		if err != nil {
			log.Error("onDepositMsg error", "error", err)
		}
		return
	}

	if msg, ok := e.Data.(*dpos_msg.BatchMsg); ok {
		if c.chainID != Layer1ChainID {
			return
		}
		err := c.onBatchMsg(msg)
		if err != nil {
			log.Error("onBatchMsg error", "error", err)
		}
		return
	}

	if msg, ok := e.Data.(*dpos_msg.FeedbackBatchMsg); ok {
		if c.chainID != Layer1ChainID {
			return
		}
		err := c.onFeedbackBatchMsg(msg)
		if err != nil {
			log.Error("onFeedbackBatchMsg error", "error", err)
		}
		return
	}
}

func (c *EVMChain) onFeedbackBatchMsg(msg *dpos_msg.FeedbackBatchMsg) error {
	if msg.BatchMsgHash.String() != c.currentProposal.GetHash().String() {
		return errors.New("The feedback  is not current proposal")
	}
	if c.msgPool.ArbiterIsVerified(msg.BatchMsgHash, msg.Signer) {
		return errors.New(fmt.Sprintf("is verified arbiter:%s", common.Bytes2Hex(msg.Proposer)))
	}

	superVoterSignature := c.msgPool.GetSuperVoterSigner(msg.BatchMsgHash)
	maxsign := c.getMaxArbitersSign()

	//if c.msgPool.GetVerifiedCount(msg.BatchMsgHash) > maxsign && len(superVoterSignature) > 0 {
	//	return errors.New("is collect enough feedback")
	//}

	if err := c.verifySignature(msg); err != nil {
		return err
	}

	isSuperVoter := c.msgPool.OnProposalVerified(msg.BatchMsgHash, msg.Signer, msg.Signature)
	log.Info("onFeedbackBatchMsg", "verified count", c.msgPool.GetVerifiedCount(msg.BatchMsgHash), "superVoterSignature", len(superVoterSignature), "isSuperVoter", isSuperVoter)
	if isSuperVoter {
		superVoterSignature = c.msgPool.GetSuperVoterSigner(msg.BatchMsgHash)
	}
	if c.msgPool.GetVerifiedCount(msg.BatchMsgHash) >= maxsign && len(superVoterSignature) > 0 {
		err := c.ExecuteProposalBatch(c.currentProposal)
		if err != nil {
			log.Error("ExecuteProposalBatch error", "error", err)
		}
	}
	return nil
}

func (c *EVMChain) verifySignature(msg *dpos_msg.FeedbackBatchMsg) error {
	pk, err := crypto.SigToPub(accounts.TextHash(c.currentProposal.GetHash().Bytes()), msg.Signature)
	if err != nil {
		return err
	}
	pub := crypto.CompressPubkey(pk)
	if bytes.Compare(msg.Signer, pub) != 0 {
		return errors.New(fmt.Sprintf("verified signature error, signer:%s, publicKey:%s", common.Bytes2Hex(msg.Signer), common.Bytes2Hex(pub)))
	}
	if !c.arbiterManager.HasArbiter(pub) && bytes.Compare(c.superVoter, pub) != 0 {
		return errors.New(fmt.Sprintf("verified signature is not in arbiterList, signer:%s, publicKey:%s", common.Bytes2Hex(msg.Signer), common.Bytes2Hex(pub)))
	}
	return nil
}

func (c *EVMChain) onBatchMsg(msg *dpos_msg.BatchMsg) error {
	if len(msg.Items) <= 0 {
		return errors.New("batch msg count is 0")
	}
	for _, item := range msg.Items {
		proposal := c.msgPool.GetQueueProposal(item.DepositNonce)
		if proposal == nil {
			return errors.New(fmt.Sprintf("not have this proposal:%d", item.DepositNonce))
		}
		if proposal.Destination != c.chainID {
			return errors.New(fmt.Sprintf("proposal destination is not correct, chainID:%d, propsal destination:%d", c.chainID, proposal.Destination))
		}

		if proposal.ProposalIsComplete(c.writer.GetClient()) {
			c.msgPool.OnProposalExecuted(proposal.DepositNonce)
			return errors.New(fmt.Sprintf("proposal is completed, hash:%s, dest:%d, source:%d", proposal.Hash().String(), proposal.Destination, c.chainID))
		}
		if !compareMsg(&item, proposal) {
			return errors.New("received error deposit proposal")
		}
	}

	err := c.onBatchProposal(msg, msg.GetHash().Bytes())
	if err != nil {
		return errors.New(fmt.Sprintf("onBatchProposal error: %s", err.Error()))
	} else {
		c.writer.FeedbackBatchMsg(msg)
	}

	return nil
}

func (c *EVMChain) onDepositMsg(msg *dpos_msg.DepositProposalMsg) error {
	proposal := c.msgPool.GetQueueProposal(msg.Item.DepositNonce)
	if proposal == nil {
		c.msgPool.PutBeforeProposal(msg)
		return errors.New(fmt.Sprintf("not have this proposal, nonce:%d, proposor:%s", msg.Item.DepositNonce, common.Bytes2Hex(msg.Proposer)))
	}
	if proposal.Destination != c.chainID {
		return errors.New(fmt.Sprintf("proposal destination is not correct, chainID:%d, propsal destination:%d", c.chainID, proposal.Destination))
	}
	if proposal.Destination == Layer2ChainID {
		if c.msgPool.IsPeningProposal(proposal) {
			return errors.New("all ready in execute pool")
		}
	}
	if proposal.ProposalIsComplete(c.writer.GetClient()) {
		c.msgPool.OnProposalExecuted(proposal.DepositNonce)
		return errors.New("all ready executed proposal")
	}
	phash := proposal.Hash()
	if c.msgPool.ArbiterIsVerified(phash, msg.Proposer) {
		return errors.New(fmt.Sprintf("onDepositMsg is verified arbiter:%s", common.Bytes2Hex(msg.Proposer)))
	}

	superVoterSignature := c.msgPool.GetSuperVoterSigner(phash)
	maxsign := c.getMaxArbitersSign()
	//if c.msgPool.GetVerifiedCount(proposal.Hash()) > maxsign &&
	//	len(superVoterSignature) > 0 {
	//	return errors.New("is collect enough signature")
	//}

	if compareMsg(&msg.Item, proposal) {
		err := c.onProposal(msg, proposal.Hash().Bytes())
		if err != nil {
			return errors.New(fmt.Sprintf("OnProposal error: %s", err.Error()))
		} else {
			issuperVoter := c.msgPool.OnProposalVerified(phash, msg.Proposer, msg.Signature)
			if issuperVoter {
				superVoterSignature = c.msgPool.GetSuperVoterSigner(phash)
			}
			log.Info("proposal verify suc", "verified count", c.msgPool.GetVerifiedCount(phash), "getMaxArbitersSign", maxsign, "superVoterSignature", len(superVoterSignature))
			if c.msgPool.GetVerifiedCount(phash) >= maxsign &&
				len(superVoterSignature) > 0 {
				c.msgPool.PutExecuteProposal(proposal)
			}
		}
	} else {
		return errors.New("received error deposit proposal")
	}
	return nil
}

func compareMsg(msg1 *dpos_msg.DepositItem, msg2 *voter.Proposal) bool {
	if msg2 == nil || msg1 == nil {
		return false
	}
	if msg1.SourceChainID != msg2.Source {
		return false
	}
	if msg1.DestChainID != msg2.Destination {
		return false
	}
	if bytes.Compare(msg1.ResourceId[:], msg2.ResourceId[:]) != 0 {
		return false
	}
	if bytes.Compare(msg1.Data, msg2.Data) != 0 {
		return false
	}
	return true
}

func (c *EVMChain) getMaxArbitersSign() int {
	total := c.writer.GetClient().Engine().GetTotalArbitersCount()
	return total*2/3 + 1
}

func (c *EVMChain) onProposal(msg *dpos_msg.DepositProposalMsg, proposalHash []byte) error {
	proposalHash = accounts.TextHash(proposalHash)
	pk, err := crypto.SigToPub(proposalHash, msg.Signature)
	if err != nil {
		return err
	}
	pub := crypto.CompressPubkey(pk)
	if bytes.Compare(msg.Proposer, pub) != 0 {
		return errors.New(fmt.Sprintf("verified signature error, proposer:%s, publicKey:%s", common.Bytes2Hex(msg.Proposer), common.Bytes2Hex(pub)))
	}
	return nil
}

func (c *EVMChain) onBatchProposal(msg *dpos_msg.BatchMsg, proposalHash []byte) error {
	pbk, err := c.writer.GetPublicKey()
	if err != nil {
		return err
	}
	if bytes.Equal(msg.Proposer, pbk) {
		return errors.New("is self submit proposal")
	}

	pk, err := crypto.SigToPub(accounts.TextHash(proposalHash), msg.Signature)
	if err != nil {
		return err
	}
	pub := crypto.CompressPubkey(pk)
	if bytes.Compare(msg.Proposer, pub) != 0 {
		return errors.New(fmt.Sprintf("verified signature error, proposer:%s, publicKey:%s", common.Bytes2Hex(msg.Proposer), common.Bytes2Hex(pub)))
	}
	return nil
}

// PollEvents is the goroutine that polling blocks and searching Deposit Events in them. Event then sent to eventsChan
func (c *EVMChain) PollEvents(stop <-chan struct{}, sysErr chan<- error, eventsChan chan *relayer.Message, changeSuperChan chan *relayer.ChangeSuperSigner) {
	log.Info("Polling Blocks...")
	// Handler chain specific configs and flags
	block, err := blockstore.SetupBlockstore(c.config, c.kvdb, big.NewInt(0).SetUint64(c.config.Opts.StartBlock))
	if err != nil {
		sysErr <- fmt.Errorf("error %w on getting last stored block", err)
		return
	}
	ech, changeSuperCh, nftCh := c.listener.ListenToEvents(block, c.chainID, c.kvdb, stop, sysErr)
	for {
		select {
		case <-stop:
			return
		case newEvent := <-ech:
			// Here we can place middlewares for custom logic?
			eventsChan <- newEvent
			continue
		case change := <-changeSuperCh:
			changeSuperChan <- change
			continue
		case evt := <-nftCh:
			eventsChan <- evt
			continue
		}
	}
}

func (c *EVMChain) PollStatusEvent(stop <-chan struct{}, sysErr chan<- error) {
	block, err := blockstore.SetupBlockstore(c.config, c.kvdb, big.NewInt(0).SetUint64(c.config.Opts.StartBlock))
	if err != nil {
		sysErr <- fmt.Errorf("error %w on getting last stored block", err)
		return
	}
	pch := c.listener.ListenStatusEvents(block, c.chainID, stop, sysErr)
	for {
		select {
		case <-stop:
			return
		case p := <-pch:
			proposal := c.msgPool.GetQueueProposal(p.DepositNonce)
			if proposal != nil {
				log.Info("poll proposal status", "source", p.SourceChain, "selfChain", c.chainID, "status", p.Status)
				if p.Status == relayer.ProposalStatusExecuted {
					if p.SourceChain != c.chainID {
						c.msgPool.OnProposalExecuted(p.DepositNonce)
					}
				}
			} else {
				log.Info("all ready accessed proposal", "nonce", p.DepositNonce, "sourceChain", p.SourceChain)
			}
			continue
		}
	}
}

func (c *EVMChain) WriteArbiters(arbiters []common.Address, signatures [][]byte, totalCount int) error {
	if c.writer.IsDeployedBridgeContract(c.bridgeContractAddress) == false {
		return errors.New(fmt.Sprintf("%d is not deploy chainbridge contract", c.chainID))
	}

	return c.writer.SetArbiterList(arbiters, totalCount, signatures, c.bridgeContractAddress)
}

func (c *EVMChain) GetArbiters() []common.Address {
	list, err := c.writer.GetArbiterList(c.bridgeContractAddress)
	if err != nil {
		log.Error("GetArbiterList error", "error", err)
		return []common.Address{}
	}
	return list
}

func (c *EVMChain) GetCurrentSuperSigner() common.Address {
	addr, err := c.writer.GetSuperSigner(c.bridgeContractAddress)
	if err != nil {
		log.Error("GetArbiterList error", "error", err)
		return common.Address{}
	}
	return addr
}

func (c *EVMChain) GetSuperSignerNodePublickey() string {
	nodePublickey, err := c.writer.SuperSignerNodePublickey(c.bridgeContractAddress)
	if err != nil {
		log.Error("GetSuperSignerNodePublickey error", "error", err)
		return ""
	}
	return nodePublickey
}

func (c *EVMChain) GetBridgeContract() string {
	return c.config.Opts.Bridge
}

func (c *EVMChain) Write(msg *relayer.Message) error {
	proposal, err := c.writer.HandleProposal(msg)
	if err != nil {
		return err
	}
	log.Info("handle new relayer message", "source", proposal.Source, "target", proposal.Destination, "nonce", proposal.DepositNonce)

	if proposal.ProposalIsComplete(c.writer.GetClient()) {
		return err
	}

	err = c.msgPool.PutProposal(proposal)
	if err != nil {
		return err
	}
	if msg.Destination == Layer2ChainID {
		c.broadProposal(proposal)
	}
	return nil
}

func (c *EVMChain) generateBatchProposal() {
	list := c.msgPool.GetQueueList()
	items := make([]*voter.Proposal, 0)
	count := len(list)
	for i := 0; i < count; i++ {
		if list[i].ProposalIsComplete(c.writer.GetClient()) {
			c.msgPool.OnProposalExecuted(list[i].DepositNonce)
			continue
		}
		items = append(items, list[i])
	}
	count = len(items)
	if count > 0 {
		if count > MaxBatchCount {
			items = items[0:MaxBatchCount]
		}
		c.currentProposal = c.writer.SignAndBroadProposalBatch(items)
		log.Info("GenerateBatchProposal...", "list count", count, "proposal", c.currentProposal.GetHash().String())
		c.writer.FeedbackBatchMsg(c.currentProposal)
	}
}

func (c *EVMChain) ChainID() uint64 {
	return c.chainID
}
