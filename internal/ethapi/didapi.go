package ethapi

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/elastos/Elastos.ELA.SideChain/service"

	elacom "github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/utils/http"

	"github.com/elastos/Elastos.ELA.SideChain.ETH/common"
	"github.com/elastos/Elastos.ELA.SideChain.ETH/core/rawdb"
	"github.com/elastos/Elastos.ELA.SideChain.ETH/core/vm/did"
	"github.com/elastos/Elastos.ELA.SideChain.ETH/ethdb"
)

type DidDocState uint8

const (
	Valid = iota
	Expired
	Deactivated
	NonExist
)

func (c DidDocState) String() string {
	switch c {
	case Valid:
		return "Valid"
	case Expired:
		return "Expired"
	case Deactivated:
		return "Deactivated"
	case NonExist:
		return "NonExist"
	default:
		return "Unknown"
	}
}

const (
	CredentialValid = iota
	CredentialReserve
	CredentialRevoked
	CredentialNonExist
)

// payload of DID transaction
type RpcPayloadDIDInfo struct {
	DID        string                `json:"did"`
	Status     int                   `json:"status"`
	RpcTXDatas []RpcTranasactionData `json:"transaction,omitempty"`
}

type RpcOperation struct {
	Header  did.DIDHeaderInfo `json:"header"`
	Payload string           `json:"payload"`
	Proof   did.DIDProofInfo  `json:"proof"`
}

type RpcTranasactionData struct {
	TXID      string       `json:"txid"`
	Timestamp string       `json:"timestamp"`
	Operation RpcOperation `json:"operation"`
}

func (rpcTxData *RpcTranasactionData) FromTranasactionData(txData did.TranasactionData) bool {
	hash, err := elacom.Uint256FromHexString(txData.TXID)
	if err != nil {
		return false
	}

	rpcTxData.TXID = service.ToReversedString(*hash)
	rpcTxData.Timestamp = txData.Timestamp
	rpcTxData.Operation.Header = txData.Operation.Header
	rpcTxData.Operation.Payload = txData.Operation.Payload
	rpcTxData.Operation.Proof = txData.Operation.Proof
	return true
}

// payload of DID transaction
type RpcCredentialPayloadDIDInfo struct {
	ID         string                         `json:"id"`
	Status     int                            `json:"status"`
	RpcTXDatas []RpcCredentialTransactionData `json:"transaction,omitempty"`
}


type RpcCredentialTransactionData struct {
	TXID      string              `json:"txid"`
	Timestamp string              `json:"timestamp"`
	Operation CredentialOperation `json:"operation"`
}

type CredentialOperation struct {
	Header  did.VerifiableCredentialHeaderInfo `json:"header"`
	Payload string                            `json:"payload"`
	Proof   interface{}                       `json:"proof"`
}

func (rpcTxData *RpcCredentialTransactionData) FromCredentialTranasactionData(txData did.
VerifiableCredentialTxData) bool {
	hash, err := elacom.Uint256FromHexString(txData.TXID)
	if err != nil {
		return false
	}

	rpcTxData.TXID = service.ToReversedString(*hash)
	rpcTxData.Timestamp = txData.Timestamp
	rpcTxData.Operation.Header = txData.Operation.Header
	rpcTxData.Operation.Payload = txData.Operation.Payload
	rpcTxData.Operation.Proof = txData.Operation.Proof
	return true
}

func (s *PublicTransactionPoolAPI) ResolveCredential(ctx context.Context, idParam, issuer string) (interface{}, error) {
	var credentialID string
	//remove DID_ELASTOS_PREFIX
	if rawdb.IsURIHasPrefix(idParam) {
		credentialID = rawdb.GetDIDFromUri(idParam)
	} else {
		credentialID = idParam
	}

	buf := new(bytes.Buffer)
	buf.WriteString(credentialID)
	txsData, _ := rawdb.GetAllVerifiableCredentialTxData(s.b.ChainDb().(ethdb.KeyValueStore), buf.Bytes())

	var issuerID string
	if issuer == "" {
		if len(txsData) == 0 {
			return RpcCredentialPayloadDIDInfo{ID: credentialID, Status: CredentialNonExist}, nil
		}
	} else {
		//remove DID_ELASTOS_PREFIX
		if rawdb.IsURIHasPrefix(issuer) {
			issuerID = rawdb.GetDIDFromUri(issuer)
		} else {
			issuerID = idParam
		}
	}

	var rpcPayloadDid RpcCredentialPayloadDIDInfo
	for index, txData := range txsData {
		rpcPayloadDid.ID = txData.Operation.Doc.ID
		err, timestamp := s.getTxTime(ctx, txData.TXID)
		if err != nil {
			continue
		}
		tempTXData := new(RpcCredentialTransactionData)
		ok := tempTXData.FromCredentialTranasactionData(txData)
		if !ok {
			continue
		}

		var isRevokeTransaction bool
		if len(txsData) == 2 && index == 0 {
			isRevokeTransaction = true
		}

		signer := txData.Operation.Proof.(*did.InnerDIDProofInfo).VerificationMethod
		if isRevokeTransaction && issuerID == "" && signer == txData.Operation.Doc.Issuer {
			continue
		}

		if isRevokeTransaction && issuerID != "" && signer != issuerID {
			continue
		}

		tempTXData.Timestamp = time.Unix(int64(timestamp), 0).UTC().Format(time.RFC3339)
		rpcPayloadDid.RpcTXDatas = append(rpcPayloadDid.RpcTXDatas, *tempTXData)
	}

	if len(txsData) == 0 {
		rpcPayloadDid.Status = CredentialNonExist
	} else if len(txsData) == 1 {
		rpcPayloadDid.Status = CredentialValid
	} else if len(txsData) == 2 {
		rpcPayloadDid.Status = CredentialRevoked
	}

	return rpcPayloadDid, nil
}

func (s *PublicTransactionPoolAPI) ResolveDID(ctx context.Context, idParam string, isGetAll bool) (interface{}, error) {
	var didDocState DidDocState = NonExist

	//remove DID_ELASTOS_PREFIX
	id := idParam
	if rawdb.IsURIHasPrefix(idParam) {
		id = rawdb.GetDIDFromUri(id)
	}

	//check is valid address
	_, err := elacom.Uint168FromAddress(id)
	if err != nil {
		return nil, http.NewError(int(service.InvalidParams), "invalid did")
	}

	var rpcPayloadDid RpcPayloadDIDInfo

	buf := new(bytes.Buffer)
	buf.WriteString(id)
	txData, err := rawdb.GetLastDIDTxData(s.b.ChainDb().(ethdb.KeyValueStore), buf.Bytes())
	if err != nil {
		rpcPayloadDid.DID = idParam
		rpcPayloadDid.Status = NonExist
		return rpcPayloadDid, nil
	}

	var txsData []did.TranasactionData
	if isGetAll {
		txsData, err = rawdb.GetAllDIDTxTxData(s.b.ChainDb().(ethdb.KeyValueStore), buf.Bytes())
		if err != nil {
			return nil, http.NewError(int(service.InternalError),
				"get did transaction failed")
		}

	} else {
		if txData != nil {
			txsData = append(txsData, *txData)
		}
	}

	for index, txData := range txsData {
		rpcPayloadDid.DID = txData.Operation.PayloadInfo.ID
		err, timestamp := s.getTxTime(ctx, txData.TXID)
		if err != nil {
			continue
		}
		tempTXData := new(RpcTranasactionData)
		succe := tempTXData.FromTranasactionData(txData)
		if succe == false {
			continue
		}

		tempTXData.Timestamp = time.Unix(int64(timestamp), 0).UTC().Format(time.RFC3339)
		rpcPayloadDid.RpcTXDatas = append(rpcPayloadDid.RpcTXDatas, *tempTXData)
		if index == 0 {
			if rawdb.IsDIDDeactivated(s.b.ChainDb().(ethdb.KeyValueStore), id) {
				didDocState = Deactivated
			} else {
				didDocState = Valid
			}
			rpcPayloadDid.Status = int(didDocState)
		}
	}
	return rpcPayloadDid, nil
}

func (s *PublicTransactionPoolAPI) getTxTime(ctx context.Context, txid string) (error, uint64) {
	hash := common.HexToHash(txid)

	tx, err := s.GetTransactionByHash(ctx, hash)
	if err != nil || tx == nil {
		return errors.New("unkown tx"), 0
	}
	block, err := s.b.BlockByHash(ctx, *tx.BlockHash)
	if err != nil {
		return errors.New("unkown block header"), 0

	}
	return nil, block.Time()
}