/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package operationapplier

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/trustbloc/sidetree-core-go/pkg/api/operation"
	"github.com/trustbloc/sidetree-core-go/pkg/api/protocol"
	"github.com/trustbloc/sidetree-core-go/pkg/canonicalizer"
	"github.com/trustbloc/sidetree-core-go/pkg/commitment"
	"github.com/trustbloc/sidetree-core-go/pkg/document"
	"github.com/trustbloc/sidetree-core-go/pkg/docutil"
	"github.com/trustbloc/sidetree-core-go/pkg/internal/signutil"
	"github.com/trustbloc/sidetree-core-go/pkg/mocks"
	"github.com/trustbloc/sidetree-core-go/pkg/patch"
	"github.com/trustbloc/sidetree-core-go/pkg/util/ecsigner"
	"github.com/trustbloc/sidetree-core-go/pkg/util/pubkey"
	"github.com/trustbloc/sidetree-core-go/pkg/versions/0_1/client"
	"github.com/trustbloc/sidetree-core-go/pkg/versions/0_1/doccomposer"
	"github.com/trustbloc/sidetree-core-go/pkg/versions/0_1/model"
	"github.com/trustbloc/sidetree-core-go/pkg/versions/0_1/operationparser"
)

const (
	sha2_256          = 18
	dummyUniqueSuffix = "dummy"

	updateKeyID = "update-key"
)

var (
	p = protocol.Protocol{
		GenesisTime:          0,
		MultihashAlgorithm:   sha2_256,
		HashAlgorithm:        5,
		MaxOperationCount:    2,
		MaxOperationSize:     1024,
		CompressionAlgorithm: "GZIP",
		MaxChunkFileSize:     1024,
		MaxMapFileSize:       1024,
		MaxAnchorFileSize:    1024,
		SignatureAlgorithms:  []string{"EdDSA", "ES256"},
		KeyAlgorithms:        []string{"Ed25519", "P-256"},
		Patches:              []string{"add-public-keys", "remove-public-keys", "add-services", "remove-services", "ietf-json-patch"},
	}

	parser = operationparser.New(p)

	dc = doccomposer.New()
)

func TestApplier_Apply(t *testing.T) {
	recoveryKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	updateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	t.Run("update is first operation error", func(t *testing.T) {
		applier := New(p, parser, dc)

		const uniqueSuffix = "uniqueSuffix"
		updateOp, _, err := getAnchoredUpdateOperation(updateKey, uniqueSuffix, 1)
		require.NoError(t, err)

		doc, err := applier.Apply(updateOp, &protocol.ResolutionModel{})
		require.Error(t, err)
		require.Nil(t, doc)
		require.Equal(t, "update cannot be first operation", err.Error())
	})

	t.Run("create is second operation error", func(t *testing.T) {
		applier := New(p, parser, &mockDocComposer{})

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		doc, err := applier.Apply(createOp, &protocol.ResolutionModel{
			Doc: make(document.Document),
		})
		require.Error(t, err)
		require.Nil(t, doc)
		require.Equal(t, "create has to be the first operation", err.Error())
	})

	t.Run("apply recover to non existing document error", func(t *testing.T) {
		applier := New(p, parser, dc)

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		recoverOp, _, err := getAnchoredRecoverOperation(recoveryKey, updateKey, createOp.UniqueSuffix, 2)
		require.NoError(t, err)

		doc, err := applier.Apply(recoverOp, &protocol.ResolutionModel{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "recover can only be applied to an existing document")
		require.Nil(t, doc)
	})

	t.Run("invalid operation type error", func(t *testing.T) {
		applier := New(p, parser, dc)

		doc, err := applier.Apply(&operation.AnchoredOperation{Type: "invalid"}, &protocol.ResolutionModel{Doc: make(document.Document)})
		require.Error(t, err)
		require.Equal(t, "operation type not supported for process operation", err.Error())
		require.Nil(t, doc)
	})

	t.Run("create delta hash doesn't match delta error", func(t *testing.T) {
		store := mocks.NewMockOperationStore(nil)

		createOp, err := getCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		delta, err := getDeltaModel(validDoc, "different")
		require.NoError(t, err)

		createOp.Delta = delta

		anchoredOp := getAnchoredOperation(createOp)
		err = store.Put(anchoredOp)
		require.Nil(t, err)

		applier := New(p, parser, dc)
		rm, err := applier.Apply(anchoredOp, &protocol.ResolutionModel{})
		require.NoError(t, err)
		require.Equal(t, make(document.Document), rm.Doc)
		require.NotEmpty(t, rm.RecoveryCommitment)
		require.Empty(t, rm.UpdateCommitment)
	})

	t.Run("error - apply patches (document composer) error", func(t *testing.T) {
		applier := New(p, parser, &mockDocComposer{Err: errors.New("document composer error")})

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)
		require.Equal(t, make(document.Document), rm.Doc)
		require.NotEmpty(t, rm.RecoveryCommitment)
		require.NotEmpty(t, rm.UpdateCommitment)
	})
}

func TestUpdateDocument(t *testing.T) {
	recoveryKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, e)

	updateKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, e)

	createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
	require.NoError(t, err)

	uniqueSuffix := createOp.UniqueSuffix

	t.Run("success", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		updateOp, nextUpdateKey, err := getAnchoredUpdateOperation(updateKey, uniqueSuffix, 1)
		require.Nil(t, err)

		result, err := applier.Apply(updateOp, rm)
		require.Nil(t, err)

		// check if service type value is updated (done via json patch)
		didDoc := document.DidDocumentFromJSONLDObject(result.Doc)
		require.Equal(t, "special1", didDoc["test"])

		// test consecutive update
		updateOp, nextUpdateKey, err = getAnchoredUpdateOperation(nextUpdateKey, uniqueSuffix, 2)
		require.Nil(t, err)

		result, err = applier.Apply(updateOp, result)
		require.Nil(t, err)

		// check if service type value is updated again (done via json patch)
		didDoc = document.DidDocumentFromJSONLDObject(result.Doc)
		require.Equal(t, "special2", didDoc["test"])
	})

	t.Run("error -  operation with reused next commitment", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		// scenario: update 1 followed by update 2 followed by update 3 with reused commitment from 1

		updateOp, nextUpdateKey, err := getUpdateOperation(updateKey, uniqueSuffix, 1)
		require.Nil(t, err)

		delta1 := updateOp.Delta

		rm, err = applier.Apply(getAnchoredOperation(updateOp), rm)
		require.Nil(t, err)

		// check if service type value is updated (done via json patch)
		didDoc := document.DidDocumentFromJSONLDObject(rm.Doc)
		require.Equal(t, "special1", didDoc["test"])

		// test consecutive update
		updateOp, nextUpdateKey, err = getUpdateOperation(nextUpdateKey, uniqueSuffix, 2)
		require.Nil(t, err)

		rm, err = applier.Apply(getAnchoredOperation(updateOp), rm)
		require.Nil(t, err)

		// service type value is updated since operation is valid
		didDoc = document.DidDocumentFromJSONLDObject(rm.Doc)
		require.Equal(t, "special2", didDoc["test"])

		// two successful update operations - next update with reused commitment from op 1
		updateOp, nextUpdateKey, err = getUpdateOperation(nextUpdateKey, uniqueSuffix, 1)
		require.Nil(t, err)

		delta3 := updateOp.Delta
		delta3.UpdateCommitment = delta1.UpdateCommitment
		updateOp.Delta = delta3

		rm, err = applier.Apply(getAnchoredOperation(updateOp), rm)
		require.EqualError(t, err, "update delta doesn't match delta hash: supplied hash doesn't match original content")
	})

	t.Run("missing signed data error", func(t *testing.T) {
		applier := New(p, parser, dc)

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		updateOp, _, err := getUpdateOperation(updateKey, uniqueSuffix, 1)
		require.NoError(t, err)

		updateOp.SignedData = ""

		rm, err = applier.Apply(getAnchoredOperation(updateOp), rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "missing signed data")
	})

	t.Run("unmarshal signed data model error", func(t *testing.T) {
		applier := New(p, parser, dc)

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		updateOp, _, err := getUpdateOperation(updateKey, uniqueSuffix, 1)
		require.NoError(t, err)

		signer := ecsigner.New(updateKey, "ES256", "update-kid")

		compactJWS, err := signutil.SignPayload([]byte("payload"), signer)
		require.NoError(t, err)

		updateOp.SignedData = compactJWS

		rm, err = applier.Apply(getAnchoredOperation(updateOp), rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "failed to parse update operation in anchor mode: failed to unmarshal signed data model for update")
	})

	t.Run("invalid signature error", func(t *testing.T) {
		applier := New(p, parser, dc)

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		// sign update operation with different  key (than one used in create)
		differentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		s := ecsigner.New(differentKey, "ES256", updateKeyID)
		updateOp, _, err := getUpdateOperationWithSigner(s, updateKey, uniqueSuffix, 1)
		require.NoError(t, err)

		anchoredOp := getAnchoredOperation(updateOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "ecdsa: invalid signature")
	})

	t.Run("delta hash doesn't match delta error", func(t *testing.T) {
		applier := New(p, parser, dc)

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		updateOp, _, err := getUpdateOperation(updateKey, uniqueSuffix, 1)
		require.NoError(t, err)

		updateOp.Delta = &model.DeltaModel{UpdateCommitment: "different"}

		rm, err = applier.Apply(getAnchoredOperation(updateOp), rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "update delta doesn't match delta hash")
	})

	t.Run("error - document composer error", func(t *testing.T) {
		applier := New(p, parser, dc)

		createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
		require.NoError(t, err)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		updateOp, _, err := getAnchoredUpdateOperation(updateKey, uniqueSuffix, 1)
		require.NoError(t, err)

		applier = New(p, parser, &mockDocComposer{Err: errors.New("document composer error")})

		rm, err = applier.Apply(updateOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "document composer error")
	})
}

func TestDeactivate(t *testing.T) {
	recoveryKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	recoveryPubKey, err := pubkey.GetPublicKeyJWK(&recoveryKey.PublicKey)
	require.NoError(t, err)

	updateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
	require.NoError(t, err)

	uniqueSuffix := createOp.UniqueSuffix

	t.Run("success", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		deactivateOp, err := getAnchoredDeactivateOperation(recoveryKey, uniqueSuffix)
		require.NoError(t, err)

		doc, err := applier.Apply(deactivateOp, rm)
		require.NoError(t, err)
		require.NotNil(t, doc)
	})

	t.Run("deactivate can only be applied to an existing document", func(t *testing.T) {
		deactivateOp, err := getAnchoredDeactivateOperation(recoveryKey, uniqueSuffix)
		require.NoError(t, err)

		applier := New(p, parser, dc)
		doc, err := applier.Apply(deactivateOp, &protocol.ResolutionModel{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "deactivate can only be applied to an existing document")
		require.Nil(t, doc)
	})

	t.Run("document not found error", func(t *testing.T) {
		store, _ := getDefaultStore(recoveryKey, updateKey)

		deactivateOp, err := getAnchoredDeactivateOperation(recoveryKey, dummyUniqueSuffix)
		require.NoError(t, err)
		err = store.Put(deactivateOp)
		require.NoError(t, err)

		applier := New(p, parser, &mockDocComposer{})
		doc, err := applier.Apply(deactivateOp, &protocol.ResolutionModel{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "deactivate can only be applied to an existing document")
		require.Nil(t, doc)
	})

	t.Run("missing signed data error", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		deactivateOp, err := getDeactivateOperation(recoveryKey, uniqueSuffix)
		require.NoError(t, err)

		deactivateOp.SignedData = ""

		anchoredOp := getAnchoredOperation(deactivateOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "missing signed data")
	})

	t.Run("unmarshal signed data model error", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		deactivateOp, err := getDeactivateOperation(recoveryKey, uniqueSuffix)
		require.NoError(t, err)

		signer := ecsigner.New(recoveryKey, "ES256", "")

		compactJWS, err := signutil.SignPayload([]byte("payload"), signer)
		require.NoError(t, err)

		deactivateOp.SignedData = compactJWS

		anchoredOp := getAnchoredOperation(deactivateOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "failed to parse deactive operation in anchor mode: failed to unmarshal signed data model for deactivate")
	})

	t.Run("invalid signature error", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		// sign recover operation with different recovery key (than one used in create)
		differentRecoveryKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		signer := ecsigner.New(differentRecoveryKey, "ES256", "")
		deactivateOp, err := getDeactivateOperationWithSigner(signer, recoveryKey, uniqueSuffix)
		require.NoError(t, err)

		anchoredOp := getAnchoredOperation(deactivateOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ecdsa: invalid signature")
		require.Nil(t, rm)
	})

	t.Run("did suffix doesn't match signed value error", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		deactivateOp, err := getDeactivateOperation(recoveryKey, uniqueSuffix)
		require.NoError(t, err)

		s := ecsigner.New(recoveryKey, "ES256", "")

		jws, err := signutil.SignModel(&model.DeactivateSignedDataModel{
			DidSuffix:   "other",
			RecoveryKey: recoveryPubKey,
		}, s)
		require.NoError(t, err)

		deactivateOp.SignedData = jws

		anchoredOp := getAnchoredOperation(deactivateOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "failed to parse deactive operation in anchor mode: signed did suffix mismatch for deactivate")
	})
}

func TestRecover(t *testing.T) {
	recoveryKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, e)

	updateKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, e)

	createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
	require.NoError(t, err)

	uniqueSuffix := createOp.UniqueSuffix

	t.Run("success", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		recoverOp, nextRecoveryKey, err := getAnchoredRecoverOperation(recoveryKey, updateKey, uniqueSuffix, 1)
		require.NoError(t, err)

		rm, err = applier.Apply(recoverOp, rm)
		require.NoError(t, err)

		// test for recovered key
		docBytes, err := rm.Doc.Bytes()
		require.NoError(t, err)
		require.Contains(t, string(docBytes), "recovered")

		// apply recover again - consecutive recoveries are valid
		recoverOp, _, err = getAnchoredRecoverOperation(nextRecoveryKey, updateKey, uniqueSuffix, 2)
		require.NoError(t, err)

		doc, err := applier.Apply(recoverOp, rm)
		require.NoError(t, err)
		require.NotNil(t, doc)
	})

	t.Run("success - operation with invalid signature rejected", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		invalidRecoverOp, _, err := getRecoverOperation(recoveryKey, updateKey, uniqueSuffix)
		require.NoError(t, err)

		invalidRecoverOp.SignedData = ""

		invalidAnchoredOp := getAnchoredOperation(invalidRecoverOp)

		result, err := applier.Apply(invalidAnchoredOp, rm)
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing signed data")
		require.Nil(t, result)

		// now generate valid recovery operation with same recoveryKey
		recoverOp, _, err := getAnchoredRecoverOperation(recoveryKey, updateKey, uniqueSuffix, 2)

		result, err = applier.Apply(recoverOp, rm)
		require.NoError(t, err)

		// test for recovered key in resolved document
		docBytes, err := result.Doc.Bytes()
		require.NoError(t, err)
		require.Contains(t, string(docBytes), "recovered")
	})

	t.Run("success - operation with valid signature and invalid delta accepted", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		invalidRecoverOp, _, err := getRecoverOperation(recoveryKey, updateKey, uniqueSuffix)
		require.NoError(t, err)

		invalidRecoverOp.Delta = nil

		invalidAnchoredOp := getAnchoredOperation(invalidRecoverOp)

		result, err := applier.Apply(invalidAnchoredOp, rm)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, make(document.Document), result.Doc)
	})

	t.Run("missing signed data error", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		recoverOp, _, err := getRecoverOperation(recoveryKey, updateKey, uniqueSuffix)
		require.NoError(t, err)

		recoverOp.SignedData = ""

		anchoredOp := getAnchoredOperation(recoverOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "missing signed data")
	})

	t.Run("unmarshal signed data model error", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		recoverOp, _, err := getRecoverOperation(recoveryKey, updateKey, uniqueSuffix)
		require.NoError(t, err)

		signer := ecsigner.New(recoveryKey, "ES256", "")

		compactJWS, err := signutil.SignPayload([]byte("payload"), signer)
		require.NoError(t, err)

		recoverOp.SignedData = compactJWS

		anchoredOp := getAnchoredOperation(recoverOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "failed to parse recover operation in anchor mode: failed to unmarshal signed data model for recover")
	})

	t.Run("invalid signature error", func(t *testing.T) {
		applier := New(p, parser, dc)

		rm, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		// sign recover operation with different recovery key (than one used in create)
		differentRecoveryKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		signer := ecsigner.New(differentRecoveryKey, "ES256", "")
		recoverOp, _, err := getRecoverOperationWithSigner(signer, recoveryKey, updateKey, uniqueSuffix)
		require.NoError(t, err)

		anchoredOp := getAnchoredOperation(recoverOp)

		rm, err = applier.Apply(anchoredOp, rm)
		require.Error(t, err)
		require.Nil(t, rm)
		require.Contains(t, err.Error(), "ecdsa: invalid signature")
	})

	t.Run("delta hash doesn't match delta error", func(t *testing.T) {
		applier := New(p, parser, dc)

		createResult, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		recoverOp, _, err := getRecoverOperation(recoveryKey, updateKey, uniqueSuffix)
		require.NoError(t, err)

		recoverOp.Delta = &model.DeltaModel{}

		anchoredOp := getAnchoredOperation(recoverOp)

		recoverResult, err := applier.Apply(anchoredOp, createResult)
		require.NoError(t, err)
		require.NotNil(t, recoverResult)
		require.Equal(t, recoverResult.Doc, make(document.Document))
		require.NotEqual(t, recoverResult.RecoveryCommitment, createResult.RecoveryCommitment)
	})

	t.Run("error - document composer error", func(t *testing.T) {
		applier := New(p, parser, &mockDocComposer{Err: errors.New("doc composer error")})

		createResult, err := applier.Apply(createOp, &protocol.ResolutionModel{})
		require.NoError(t, err)

		recoverOp, _, err := getRecoverOperation(recoveryKey, updateKey, uniqueSuffix)
		require.NoError(t, err)

		anchoredOp := getAnchoredOperation(recoverOp)

		recoverResult, err := applier.Apply(anchoredOp, createResult)
		require.NoError(t, err)
		require.NotNil(t, recoverResult)
		require.Equal(t, make(document.Document), recoverResult.Doc)
		require.NotEqual(t, recoverResult.RecoveryCommitment, createResult.RecoveryCommitment)
	})
}

func getUpdateOperation(privateKey *ecdsa.PrivateKey, uniqueSuffix string, operationNumber uint) (*model.Operation, *ecdsa.PrivateKey, error) {
	s := ecsigner.New(privateKey, "ES256", updateKeyID)

	return getUpdateOperationWithSigner(s, privateKey, uniqueSuffix, operationNumber)
}

func getAnchoredUpdateOperation(privateKey *ecdsa.PrivateKey, uniqueSuffix string, operationNumber uint) (*operation.AnchoredOperation, *ecdsa.PrivateKey, error) {
	op, nextUpdateKey, err := getUpdateOperation(privateKey, uniqueSuffix, operationNumber)
	if err != nil {
		return nil, nil, err
	}

	return getAnchoredOperationWithBlockNum(op, uint64(operationNumber)), nextUpdateKey, nil
}

func getUpdateOperationWithSigner(s client.Signer, privateKey *ecdsa.PrivateKey, uniqueSuffix string, operationNumber uint) (*model.Operation, *ecdsa.PrivateKey, error) {
	p := map[string]interface{}{
		"op":    "replace",
		"path":  "/test",
		"value": "special" + strconv.Itoa(int(operationNumber)),
	}

	patchBytes, err := canonicalizer.MarshalCanonical([]map[string]interface{}{p})
	if err != nil {
		return nil, nil, err
	}

	jsonPatch, err := patch.NewJSONPatch(string(patchBytes))
	if err != nil {
		return nil, nil, err
	}

	nextUpdateKey, updateCommitment, err := generateKeyAndCommitment()
	if err != nil {
		return nil, nil, err
	}

	delta := &model.DeltaModel{
		UpdateCommitment: updateCommitment,
		Patches:          []patch.Patch{jsonPatch},
	}

	deltaHash, err := docutil.CalculateModelMultihash(delta, sha2_256)
	if err != nil {
		return nil, nil, err
	}

	updatePubKey, err := pubkey.GetPublicKeyJWK(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}

	signedData := &model.UpdateSignedDataModel{
		DeltaHash: deltaHash,
		UpdateKey: updatePubKey,
	}

	jws, err := signutil.SignModel(signedData, s)
	if err != nil {
		return nil, nil, err
	}

	op := &model.Operation{
		Namespace:    mocks.DefaultNS,
		ID:           "did:sidetree:" + uniqueSuffix,
		UniqueSuffix: uniqueSuffix,
		Delta:        delta,
		Type:         operation.TypeUpdate,
		SignedData:   jws,
	}

	return op, nextUpdateKey, nil
}

func generateKeyAndCommitment() (*ecdsa.PrivateKey, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}

	pubKey, err := pubkey.GetPublicKeyJWK(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}

	c, err := commitment.Calculate(pubKey, sha2_256, crypto.SHA256)
	if err != nil {
		return nil, "", err
	}

	return key, c, nil
}

func getDeactivateOperation(privateKey *ecdsa.PrivateKey, uniqueSuffix string) (*model.Operation, error) {
	signer := ecsigner.New(privateKey, "ES256", "")

	return getDeactivateOperationWithSigner(signer, privateKey, uniqueSuffix)
}

func getAnchoredDeactivateOperation(privateKey *ecdsa.PrivateKey, uniqueSuffix string) (*operation.AnchoredOperation, error) {
	op, err := getDeactivateOperation(privateKey, uniqueSuffix)
	if err != nil {
		return nil, err
	}

	return getAnchoredOperation(op), nil
}

func getDeactivateOperationWithSigner(singer client.Signer, privateKey *ecdsa.PrivateKey, uniqueSuffix string) (*model.Operation, error) {
	recoverPubKey, err := pubkey.GetPublicKeyJWK(&privateKey.PublicKey)
	if err != nil {
		return nil, err
	}

	signedDataModel := model.DeactivateSignedDataModel{
		DidSuffix:   uniqueSuffix,
		RecoveryKey: recoverPubKey,
	}

	jws, err := signutil.SignModel(signedDataModel, singer)
	if err != nil {
		return nil, err
	}

	return &model.Operation{
		Namespace:    mocks.DefaultNS,
		ID:           "did:sidetree:" + uniqueSuffix,
		UniqueSuffix: uniqueSuffix,
		Type:         operation.TypeDeactivate,
		SignedData:   jws,
	}, nil
}

func getRecoverOperation(recoveryKey, updateKey *ecdsa.PrivateKey, uniqueSuffix string) (*model.Operation, *ecdsa.PrivateKey, error) {
	signer := ecsigner.New(recoveryKey, "ES256", "")

	return getRecoverOperationWithSigner(signer, recoveryKey, updateKey, uniqueSuffix)
}

func getAnchoredRecoverOperation(recoveryKey, updateKey *ecdsa.PrivateKey, uniqueSuffix string, operationNumber uint) (*operation.AnchoredOperation, *ecdsa.PrivateKey, error) {
	op, nextRecoveryKey, err := getRecoverOperation(recoveryKey, updateKey, uniqueSuffix)
	if err != nil {
		return nil, nil, err
	}

	return getAnchoredOperationWithBlockNum(op, uint64(operationNumber)), nextRecoveryKey, nil
}

func getRecoverOperationWithSigner(signer client.Signer, recoveryKey, updateKey *ecdsa.PrivateKey, uniqueSuffix string) (*model.Operation, *ecdsa.PrivateKey, error) {
	recoverRequest, nextRecoveryKey, err := getDefaultRecoverRequest(signer, recoveryKey, updateKey)
	if err != nil {
		return nil, nil, err
	}

	operationBuffer, err := json.Marshal(recoverRequest)
	if err != nil {
		return nil, nil, err
	}

	return &model.Operation{
		Namespace:       mocks.DefaultNS,
		UniqueSuffix:    uniqueSuffix,
		Type:            operation.TypeRecover,
		OperationBuffer: operationBuffer,
		Delta:           recoverRequest.Delta,
		SignedData:      recoverRequest.SignedData,
	}, nextRecoveryKey, nil
}

func getRecoverRequest(signer client.Signer, delta *model.DeltaModel, signedDataModel *model.RecoverSignedDataModel) (*model.RecoverRequest, error) {
	deltaHash, err := docutil.CalculateModelMultihash(delta, sha2_256)
	if err != nil {
		return nil, err
	}

	signedDataModel.DeltaHash = deltaHash

	jws, err := signutil.SignModel(signedDataModel, signer)
	if err != nil {
		return nil, err
	}

	return &model.RecoverRequest{
		Operation:  operation.TypeRecover,
		DidSuffix:  "suffix",
		Delta:      delta,
		SignedData: jws,
	}, nil
}

func getDefaultRecoverRequest(signer client.Signer, recoveryKey, updateKey *ecdsa.PrivateKey) (*model.RecoverRequest, *ecdsa.PrivateKey, error) {
	updateCommitment, err := getCommitment(updateKey)
	if err != nil {
		return nil, nil, err
	}

	delta, err := getDeltaModel(recoveredDoc, updateCommitment)
	if err != nil {
		return nil, nil, err
	}

	deltaHash, err := docutil.CalculateModelMultihash(delta, sha2_256)
	if err != nil {
		return nil, nil, err
	}

	recoveryPubKey, err := pubkey.GetPublicKeyJWK(&recoveryKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}

	nextRecoveryKey, recoveryCommitment, err := generateKeyAndCommitment()
	if err != nil {
		return nil, nil, err
	}

	recoverSignedData := &model.RecoverSignedDataModel{
		RecoveryKey:        recoveryPubKey,
		RecoveryCommitment: recoveryCommitment,
		DeltaHash:          deltaHash,
	}

	req, err := getRecoverRequest(signer, delta, recoverSignedData)
	if err != nil {
		return nil, nil, err
	}

	return req, nextRecoveryKey, nil
}

func getDefaultStore(recoveryKey, updateKey *ecdsa.PrivateKey) (*mocks.MockOperationStore, string) {
	store := mocks.NewMockOperationStore(nil)

	createOp, err := getAnchoredCreateOperation(recoveryKey, updateKey)
	if err != nil {
		panic(err)
	}

	// store default create operation
	err = store.Put(createOp)
	if err != nil {
		panic(err)
	}

	return store, createOp.UniqueSuffix
}

func getCreateOperationWithDoc(recoveryKey, updateKey *ecdsa.PrivateKey, doc string) (*model.Operation, error) {
	createRequest, err := getCreateRequest(recoveryKey, updateKey)
	if err != nil {
		return nil, err
	}

	operationBuffer, err := json.Marshal(createRequest)
	if err != nil {
		return nil, err
	}

	uniqueSuffix, err := docutil.CalculateModelMultihash(createRequest.SuffixData, sha2_256)
	if err != nil {
		return nil, err
	}

	updateCommitment, err := getCommitment(updateKey)
	if err != nil {
		return nil, err
	}

	delta, err := getDeltaModel(doc, updateCommitment)
	if err != nil {
		return nil, err
	}

	suffixData, err := getSuffixData(recoveryKey, delta)
	if err != nil {
		return nil, err
	}

	return &model.Operation{
		Namespace:       mocks.DefaultNS,
		ID:              "did:sidetree:" + uniqueSuffix,
		UniqueSuffix:    uniqueSuffix,
		Type:            operation.TypeCreate,
		OperationBuffer: operationBuffer,
		Delta:           delta,
		SuffixData:      suffixData,
	}, nil
}

func getCreateOperation(recoveryKey, updateKey *ecdsa.PrivateKey) (*model.Operation, error) {
	return getCreateOperationWithDoc(recoveryKey, updateKey, validDoc)
}

func getAnchoredCreateOperation(recoveryKey, updateKey *ecdsa.PrivateKey) (*operation.AnchoredOperation, error) {
	op, err := getCreateOperation(recoveryKey, updateKey)
	if err != nil {
		return nil, err
	}

	return getAnchoredOperation(op), nil
}

func getAnchoredOperation(op *model.Operation) *operation.AnchoredOperation {
	anchoredOp, err := model.GetAnchoredOperation(op)
	if err != nil {
		panic(err)
	}

	return anchoredOp
}

func getAnchoredOperationWithBlockNum(op *model.Operation, blockNum uint64) *operation.AnchoredOperation {
	anchored := getAnchoredOperation(op)
	anchored.TransactionTime = blockNum

	return anchored
}

func getCreateRequest(recoveryKey, updateKey *ecdsa.PrivateKey) (*model.CreateRequest, error) {
	updateCommitment, err := getCommitment(updateKey)
	if err != nil {
		return nil, err
	}

	delta, err := getDeltaModel(validDoc, updateCommitment)
	if err != nil {
		return nil, err
	}

	suffixData, err := getSuffixData(recoveryKey, delta)
	if err != nil {
		return nil, err
	}

	return &model.CreateRequest{
		Operation:  operation.TypeCreate,
		Delta:      delta,
		SuffixData: suffixData,
	}, nil
}

func getDeltaModel(doc string, updateCommitment string) (*model.DeltaModel, error) {
	patches, err := patch.PatchesFromDocument(doc)
	if err != nil {
		return nil, err
	}

	return &model.DeltaModel{
		Patches:          patches,
		UpdateCommitment: updateCommitment,
	}, nil
}

func getCommitment(key *ecdsa.PrivateKey) (string, error) {
	pubKey, err := pubkey.GetPublicKeyJWK(&key.PublicKey)
	if err != nil {
		return "", err
	}

	c, err := commitment.Calculate(pubKey, sha2_256, crypto.SHA256)
	if err != nil {
		return "", err
	}

	return c, nil
}

func getSuffixData(privateKey *ecdsa.PrivateKey, delta *model.DeltaModel) (*model.SuffixDataModel, error) {
	recoveryCommitment, err := getCommitment(privateKey)
	if err != nil {
		return nil, err
	}

	deltaHash, err := docutil.CalculateModelMultihash(delta, sha2_256)
	if err != nil {
		return nil, err
	}

	return &model.SuffixDataModel{
		DeltaHash:          deltaHash,
		RecoveryCommitment: recoveryCommitment,
	}, nil
}

const validDoc = `{
	"publicKey": [{
		  "id": "key1",
		  "type": "JsonWebKey2020",
		  "purposes": ["verificationMethod"],
		  "publicKeyJwk": {
			"kty": "EC",
			"crv": "P-256K",
			"x": "PUymIqdtF_qxaAqPABSw-C-owT1KYYQbsMKFM-L9fJA",
			"y": "nM84jDHCMOTGTh_ZdHq4dBBdo4Z5PkEOW9jA8z8IsGc"
		  }
	}]
}`

const recoveredDoc = `{
	"publicKey": [{
		  "id": "recovered",
		  "type": "JsonWebKey2020",
		  "purposes": ["verificationMethod"],
		  "publicKeyJwk": {
			"kty": "EC",
			"crv": "P-256K",
			"x": "PUymIqdtF_qxaAqPABSw-C-owT1KYYQbsMKFM-L9fJA",
			"y": "nM84jDHCMOTGTh_ZdHq4dBBdo4Z5PkEOW9jA8z8IsGc"
		  }
	}]
}`

type mockDocComposer struct {
	Err error
}

// ApplyPatches mocks applying patches to the document.
func (m *mockDocComposer) ApplyPatches(doc document.Document, patches []patch.Patch) (document.Document, error) {
	if m.Err != nil {
		return nil, m.Err
	}

	return make(document.Document), nil
}
