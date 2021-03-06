/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package dochandler

import (
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/trustbloc/sidetree-core-go/pkg/api/protocol"
	"github.com/trustbloc/sidetree-core-go/pkg/document"
	"github.com/trustbloc/sidetree-core-go/pkg/restapi/common"
)

// Processor processes document operations.
type Processor interface {
	Namespace() string
	ProcessOperation(operation []byte, protocolGenesisTime uint64) (*document.ResolutionResult, error)
}

// UpdateHandler handles the creation and update of documents.
type UpdateHandler struct {
	processor Processor
	protocol  protocol.Client
}

// NewUpdateHandler returns a new document update handler.
func NewUpdateHandler(processor Processor, pc protocol.Client) *UpdateHandler {
	return &UpdateHandler{
		processor: processor,
		protocol:  pc,
	}
}

// Update creates or updates a document.
func (h *UpdateHandler) Update(rw http.ResponseWriter, req *http.Request) {
	request, err := ioutil.ReadAll(req.Body)
	if err != nil {
		common.WriteError(rw, http.StatusBadRequest, err)

		return
	}

	response, err := h.doUpdate(request)
	if err != nil {
		common.WriteError(rw, err.(*common.HTTPError).Status(), err)

		return
	}
	common.WriteResponse(rw, http.StatusOK, response)
}

func (h *UpdateHandler) doUpdate(operation []byte) (*document.ResolutionResult, error) {
	currentProtocol, err := h.protocol.Current()
	if err != nil {
		return nil, err
	}

	// operation has been validated, now process it
	result, err := h.processor.ProcessOperation(operation, currentProtocol.Protocol().GenesisTime)
	if err != nil {
		if strings.Contains(err.Error(), "bad request") {
			logger.Warnf("operation validation error: %s", err.Error())

			return nil, common.NewHTTPError(http.StatusBadRequest, err)
		}

		logger.Errorf("internal server error:  %s", err.Error())

		return nil, common.NewHTTPError(http.StatusInternalServerError, err)
	}

	return result, nil
}
