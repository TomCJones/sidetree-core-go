/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package txnprovider

import (
	"errors"
	"fmt"

	"github.com/trustbloc/sidetree-core-go/pkg/api/cas"
	"github.com/trustbloc/sidetree-core-go/pkg/api/operation"
	"github.com/trustbloc/sidetree-core-go/pkg/api/protocol"
	"github.com/trustbloc/sidetree-core-go/pkg/docutil"
	"github.com/trustbloc/sidetree-core-go/pkg/versions/0_1/model"
	"github.com/trustbloc/sidetree-core-go/pkg/versions/0_1/txnprovider/models"
)

type compressionProvider interface {
	Compress(alg string, data []byte) ([]byte, error)
}

// OperationHandler creates batch files(chunk, map, anchor) from batch operations.
type OperationHandler struct {
	cas      cas.Client
	protocol protocol.Protocol
	parser   OperationParser
	cp       compressionProvider
}

// NewOperationHandler returns new operations handler.
func NewOperationHandler(p protocol.Protocol, cas cas.Client, cp compressionProvider, parser OperationParser) *OperationHandler {
	return &OperationHandler{cas: cas, protocol: p, cp: cp, parser: parser}
}

// PrepareTxnFiles will create batch files(chunk, map, anchor) from batch operations,
// store those files in CAS and return anchor string.
func (h *OperationHandler) PrepareTxnFiles(ops []*operation.QueuedOperation) (string, error) {
	parsedOps, err := h.parseOperations(ops)
	if err != nil {
		return "", err
	}

	deactivateOps := getOperations(operation.TypeDeactivate, parsedOps)

	// special case: if all ops are deactivate don't create chunk and map files
	mapFileAddr := ""
	if len(deactivateOps) != len(ops) {
		chunkFileAddr, innerErr := h.createChunkFile(parsedOps)
		if innerErr != nil {
			return "", innerErr
		}

		mapFileAddr, innerErr = h.createMapFile([]string{chunkFileAddr}, parsedOps)
		if innerErr != nil {
			return "", innerErr
		}
	}

	anchorAddr, err := h.createAnchorFile(mapFileAddr, parsedOps)
	if err != nil {
		return "", err
	}

	ad := AnchorData{
		NumberOfOperations: len(parsedOps),
		AnchorAddress:      anchorAddr,
	}

	return ad.GetAnchorString(), nil
}

func (h *OperationHandler) parseOperations(ops []*operation.QueuedOperation) ([]*model.Operation, error) {
	if len(ops) == 0 {
		return nil, errors.New("prepare txn operations called without operations, should not happen")
	}

	batchSuffixes := make(map[string]bool)

	var operations []*model.Operation
	for _, d := range ops {
		op, e := h.parser.ParseOperation(d.Namespace, d.OperationBuffer)
		if e != nil {
			return nil, e
		}

		_, ok := batchSuffixes[op.UniqueSuffix]
		if ok {
			logger.Warnf("[%s] duplicate suffix[%s] found in batch operations: discarding operation %v", d.Namespace, op.UniqueSuffix, op)

			continue
		}

		operations = append(operations, op)
		batchSuffixes[op.UniqueSuffix] = true
	}

	return operations, nil
}

// createAnchorFile will create anchor file from operations and map file and write it to CAS
// returns anchor file address.
func (h *OperationHandler) createAnchorFile(mapAddress string, ops []*model.Operation) (string, error) {
	anchorFile := models.CreateAnchorFile(mapAddress, ops)

	return h.writeModelToCAS(anchorFile, "anchor")
}

// createChunkFile will create chunk file from operations and write it to CAS
// returns chunk file address.
func (h *OperationHandler) createChunkFile(ops []*model.Operation) (string, error) {
	chunkFile := models.CreateChunkFile(ops)

	return h.writeModelToCAS(chunkFile, "chunk")
}

// createMapFile will create map file from operations and chunk file URIs and write it to CAS
// returns map file address.
func (h *OperationHandler) createMapFile(uri []string, ops []*model.Operation) (string, error) {
	mapFile := models.CreateMapFile(uri, ops)

	return h.writeModelToCAS(mapFile, "map")
}

func (h *OperationHandler) writeModelToCAS(model interface{}, alias string) (string, error) {
	bytes, err := docutil.MarshalCanonical(model)
	if err != nil {
		return "", fmt.Errorf("failed to marshal %s file: %s", alias, err.Error())
	}

	logger.Debugf("%s file: %s", alias, string(bytes))

	compressedBytes, err := h.cp.Compress(h.protocol.CompressionAlgorithm, bytes)
	if err != nil {
		return "", err
	}

	// make file available in CAS
	address, err := h.cas.Write(compressedBytes)
	if err != nil {
		return "", fmt.Errorf("failed to store %s file: %s", alias, err.Error())
	}

	return address, nil
}
