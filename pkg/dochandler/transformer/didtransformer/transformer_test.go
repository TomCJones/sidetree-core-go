/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package didtransformer

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"

	"github.com/btcsuite/btcutil/base58"
	"github.com/stretchr/testify/require"

	"github.com/trustbloc/sidetree-core-go/pkg/document"
	"github.com/trustbloc/sidetree-core-go/pkg/util/pubkey"
)

func TestNewTransformer(t *testing.T) {
	transformer := New()
	require.NotNil(t, transformer)
	require.Empty(t, transformer.methodCtx)
	require.Equal(t, false, transformer.includeBase)

	const ctx1 = "ctx-1"
	transformer = New(WithMethodContext([]string{ctx1}))
	require.Equal(t, 1, len(transformer.methodCtx))
	require.Equal(t, ctx1, transformer.methodCtx[0])

	const ctx2 = "ctx-2"
	transformer = New(WithMethodContext([]string{ctx1, ctx2}))
	require.Equal(t, 2, len(transformer.methodCtx))
	require.Equal(t, ctx2, transformer.methodCtx[1])

	transformer = New(WithBase(true))
	require.Equal(t, true, transformer.includeBase)
}

func TestTransformDocument(t *testing.T) {
	r := reader(t, "testdata/doc.json")
	docBytes, err := ioutil.ReadAll(r)
	require.NoError(t, err)
	doc, err := document.FromBytes(docBytes)
	require.NoError(t, err)

	// document to be transformed has to have 'id' field
	// this field is added by sidetree protocol for any document
	const testID = "doc:abc:123"
	doc[document.IDProperty] = testID

	transformer := getDefaultTransformer()

	result, err := transformer.TransformDocument(doc)
	require.NoError(t, err)

	jsonTransformed, err := json.Marshal(result.Document)
	require.NoError(t, err)

	didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
	require.NoError(t, err)
	require.Equal(t, 1, len(didDoc.Context()))
	require.Equal(t, didContext, didDoc.Context()[0])

	// validate services
	service := didDoc.Services()[0]
	require.Contains(t, service.ID(), testID)
	require.NotEmpty(t, service.ServiceEndpoint())
	require.Equal(t, "recipientKeysValue", service["recipientKeys"])
	require.Equal(t, "routingKeysValue", service["routingKeys"])
	require.Equal(t, "IdentityHub", service.Type())

	// validate public keys
	pk := didDoc.PublicKeys()[0]
	require.Contains(t, pk.ID(), testID)
	require.NotEmpty(t, pk.Type())
	require.NotEmpty(t, pk.PublicKeyJwk())
	require.Empty(t, pk.PublicKeyBase58())

	expectedPublicKeys := []string{"master", "general-only", "dual-auth-gen", "dual-assertion-gen", "dual-agreement-gen", "dual-delegation-gen", "dual-invocation-gen"}
	require.Equal(t, len(expectedPublicKeys), len(didDoc.PublicKeys()))

	expectedAuthenticationKeys := []string{"master", "dual-auth-gen", "auth-only"}
	require.Equal(t, len(expectedAuthenticationKeys), len(didDoc.Authentication()))

	expectedAssertionMethodKeys := []string{"master", "dual-assertion-gen", "assertion-only"}
	require.Equal(t, len(expectedAssertionMethodKeys), len(didDoc.AssertionMethod()))

	expectedAgreementKeys := []string{"master", "dual-agreement-gen", "agreement-only"}
	require.Equal(t, len(expectedAgreementKeys), len(didDoc.AgreementKey()))

	expectedDelegationKeys := []string{"master", "dual-delegation-gen", "delegation-only"}
	require.Equal(t, len(expectedDelegationKeys), len(didDoc.DelegationKey()))

	expectedInvocationKeys := []string{"master", "dual-invocation-gen", "invocation-only"}
	require.Equal(t, len(expectedInvocationKeys), len(didDoc.InvocationKey()))
}

func TestWithMethodContext(t *testing.T) {
	doc := newDocWithID("doc:abc:123")

	transformer := New(WithMethodContext([]string{"ctx-1", "ctx-2"}))

	result, err := transformer.TransformDocument(doc)
	require.NoError(t, err)

	jsonTransformed, err := json.Marshal(result.Document)
	require.NoError(t, err)

	didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
	require.NoError(t, err)
	require.Equal(t, 3, len(didDoc.Context()))
	require.Equal(t, "ctx-1", didDoc.Context()[1])
	require.Equal(t, "ctx-2", didDoc.Context()[2])
}

func TestWithBase(t *testing.T) {
	r := reader(t, "testdata/doc.json")
	docBytes, err := ioutil.ReadAll(r)
	require.NoError(t, err)
	doc, err := document.FromBytes(docBytes)
	require.NoError(t, err)

	// document to be transformed has to have 'id' field
	// this field is added by sidetree protocol for any document
	const testID = "doc:abc:123"
	doc[document.IDProperty] = testID

	transformer := New(WithBase(true))

	result, err := transformer.TransformDocument(doc)
	require.NoError(t, err)

	jsonTransformed, err := json.Marshal(result.Document)
	require.NoError(t, err)

	didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
	require.NoError(t, err)
	require.Equal(t, 2, len(didDoc.Context()))

	// second context is @base
	baseMap := didDoc.Context()[1].(map[string]interface{})
	baseMap["@base"] = testID

	// validate service id doesn't contain document id
	service := didDoc.Services()[0]
	require.NotContains(t, service.ID(), testID)

	// validate public key id doesn't contain document id
	pk := didDoc.PublicKeys()[0]
	require.NotContains(t, pk.ID(), testID)
}

func TestEd25519VerificationKey2018(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	jwk, err := pubkey.GetPublicKeyJWK(publicKey)
	require.NoError(t, err)

	publicKeyBytes, err := json.Marshal(jwk)
	require.NoError(t, err)

	data := fmt.Sprintf(ed25519DocTemplate, string(publicKeyBytes))
	doc, err := document.FromBytes([]byte(data))
	require.NoError(t, err)

	const testID = "doc:abc:123"
	doc[document.IDProperty] = testID

	v := getDefaultTransformer()

	result, err := v.TransformDocument(doc)
	require.NoError(t, err)

	jsonTransformed, err := json.Marshal(result.Document)
	require.NoError(t, err)

	didDoc, err := document.DidDocumentFromBytes(jsonTransformed)
	require.NoError(t, err)
	require.Equal(t, didDoc.PublicKeys()[0].Controller(), didDoc.ID())
	require.Equal(t, didContext, didDoc.Context()[0])

	// validate service
	service := didDoc.Services()[0]
	require.Contains(t, service.ID(), testID)
	require.NotEmpty(t, service.ServiceEndpoint())
	require.Equal(t, "OpenIdConnectVersion1.0Service", service.Type())

	// validate public key
	pk := didDoc.PublicKeys()[0]
	require.Contains(t, pk.ID(), testID)
	require.Equal(t, "Ed25519VerificationKey2018", pk.Type())
	require.Empty(t, pk.PublicKeyJwk())

	// test base58 encoding
	require.Equal(t, base58.Encode(publicKey), pk.PublicKeyBase58())

	// validate length of expected keys
	expectedPublicKeys := []string{"dual-assertion-general"}
	require.Equal(t, len(expectedPublicKeys), len(didDoc.PublicKeys()))

	expectedAssertionMethodKeys := []string{"dual-assertion-general"}
	require.Equal(t, len(expectedAssertionMethodKeys), len(didDoc.AssertionMethod()))

	require.Equal(t, 0, len(didDoc.Authentication()))
	require.Equal(t, 0, len(didDoc.AgreementKey()))
}

func TestEd25519VerificationKey2018_Error(t *testing.T) {
	doc, err := document.FromBytes([]byte(ed25519Invalid))
	require.NoError(t, err)

	const testID = "doc:abc:123"
	doc[document.IDProperty] = testID

	v := getDefaultTransformer()

	result, err := v.TransformDocument(doc)
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "unknown curve")
}

func getDefaultTransformer() *Transformer {
	return New()
}

func newDocWithID(id string) document.Document {
	doc := make(document.Document)
	doc[document.IDProperty] = id

	return doc
}

func reader(t *testing.T, filename string) io.Reader {
	f, err := os.Open(filename)
	require.Nil(t, err)

	return f
}

const ed25519DocTemplate = `{
  "publicKey": [
	{
  		"id": "dual-assertion-general",
  		"type": "Ed25519VerificationKey2018",
		"purposes": ["verificationMethod", "assertionMethod"],
  		"publicKeyJwk": %s
	}
  ],
  "service": [
	{
	   "id": "oidc",
	   "type": "OpenIdConnectVersion1.0Service",
	   "serviceEndpoint": "https://openid.example.com/"
	}
  ]
}`

const ed25519Invalid = `{
  "publicKey": [
	{
  		"id": "dual-assertion-general",
  		"type": "Ed25519VerificationKey2018",
		"purposes": ["verificationMethod", "assertionMethod"],
      	"publicKeyJwk": {
        	"kty": "OKP",
        	"crv": "curve",
        	"x": "PUymIqdtF_qxaAqPABSw-C-owT1KYYQbsMKFM-L9fJA",
        	"y": "nM84jDHCMOTGTh_ZdHq4dBBdo4Z5PkEOW9jA8z8IsGc"
      	}
	}
  ]
}`

func TestIsAuthenticationKey(t *testing.T) {
	pk := document.NewPublicKey(map[string]interface{}{})
	ok := isAuthenticationKey(pk.Purpose())
	require.False(t, ok)

	pk[document.PurposesProperty] = []interface{}{document.KeyPurposeAuthentication}
	ok = isAuthenticationKey(pk.Purpose())
	require.True(t, ok)
}

func TestIsAssertionKey(t *testing.T) {
	pk := document.NewPublicKey(map[string]interface{}{})
	ok := isAssertionKey(pk.Purpose())
	require.False(t, ok)

	pk[document.PurposesProperty] = []interface{}{document.KeyPurposeAssertionMethod}
	ok = isAssertionKey(pk.Purpose())
	require.True(t, ok)
}

func TestIsAgreementKey(t *testing.T) {
	pk := document.NewPublicKey(map[string]interface{}{})
	ok := isAgreementKey(pk.Purpose())
	require.False(t, ok)

	pk[document.PurposesProperty] = []interface{}{document.KeyPurposeKeyAgreement}
	ok = isAgreementKey(pk.Purpose())
	require.True(t, ok)
}

func TestIsDelegationKey(t *testing.T) {
	pk := document.NewPublicKey(map[string]interface{}{})
	ok := isDelegationKey(pk.Purpose())
	require.False(t, ok)

	pk[document.PurposesProperty] = []interface{}{document.KeyPurposeCapabilityDelegation}
	ok = isDelegationKey(pk.Purpose())
	require.True(t, ok)
}

func TestIsInvocationKey(t *testing.T) {
	pk := document.NewPublicKey(map[string]interface{}{})
	ok := isInvocationKey(pk.Purpose())
	require.False(t, ok)

	pk[document.PurposesProperty] = []interface{}{document.KeyPurposeCapabilityInvocation}
	ok = isInvocationKey(pk.Purpose())
	require.True(t, ok)
}

func TestIsGeneralKey(t *testing.T) {
	pk := document.NewPublicKey(map[string]interface{}{})
	ok := isGeneralKey(pk.Purpose())
	require.False(t, ok)

	pk[document.PurposesProperty] = []interface{}{document.KeyPurposeVerificationMethod}
	ok = isGeneralKey(pk.Purpose())
	require.True(t, ok)
}
