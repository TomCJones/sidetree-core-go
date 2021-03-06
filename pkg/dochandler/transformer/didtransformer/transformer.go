/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package didtransformer

import (
	"github.com/btcsuite/btcutil/base58"

	"github.com/trustbloc/sidetree-core-go/pkg/document"
	internaljws "github.com/trustbloc/sidetree-core-go/pkg/internal/jws"
	"github.com/trustbloc/sidetree-core-go/pkg/jws"
)

const (
	didContext = "https://www.w3.org/ns/did/v1"

	didResolutionContext = "https://www.w3.org/ns/did-resolution/v1"

	// ed25519VerificationKey2018 requires special handling (convert to base58).
	ed25519VerificationKey2018 = "Ed25519VerificationKey2018"
)

// Option is a registry instance option.
type Option func(opts *Transformer)

// WithMethodContext sets optional method context(s).
func WithMethodContext(ctx []string) Option {
	return func(opts *Transformer) {
		opts.methodCtx = ctx
	}
}

// WithBase sets optional @base context.
func WithBase(enabled bool) Option {
	return func(opts *Transformer) {
		opts.includeBase = enabled
	}
}

// Transformer is responsible for transforming internal to external document.
type Transformer struct {
	methodCtx   []string // used for setting additional contexts during resolution
	includeBase bool
}

// New creates a new DID Transformer.
func New(opts ...Option) *Transformer {
	transformer := &Transformer{}

	// apply options
	for _, opt := range opts {
		opt(transformer)
	}

	return transformer
}

// TransformDocument takes internal representation of document and transforms it to required representation.
func (t *Transformer) TransformDocument(doc document.Document) (*document.ResolutionResult, error) {
	internal := document.DidDocumentFromJSONLDObject(doc.JSONLdObject())

	// start with empty document
	external := document.DidDocumentFromJSONLDObject(make(document.DIDDocument))

	// add main context
	ctx := []interface{}{didContext}

	// add optional method contexts
	for _, c := range t.methodCtx {
		ctx = append(ctx, c)
	}

	if t.includeBase {
		ctx = append(ctx, getBase(internal.ID()))
	}

	external[document.ContextProperty] = ctx
	external[document.IDProperty] = internal.ID()

	result := &document.ResolutionResult{
		Context:        didResolutionContext,
		Document:       external.JSONLdObject(),
		MethodMetadata: document.MethodMetadata{},
	}

	// add keys
	err := t.processKeys(internal, result)
	if err != nil {
		return nil, err
	}

	// add services
	t.processServices(internal, result)

	return result, nil
}

func getBase(id string) interface{} {
	return &struct {
		Base string `json:"@base"`
	}{
		Base: id,
	}
}

// processServices will process services and add them to external document.
func (t *Transformer) processServices(internal document.DIDDocument, resolutionResult *document.ResolutionResult) {
	var services []document.Service

	// add did to service id
	for _, sv := range internal.Services() {
		externalService := make(document.Service)
		externalService[document.IDProperty] = t.getObjectID(internal.ID(), sv.ID())
		externalService[document.TypeProperty] = sv.Type()
		externalService[document.ServiceEndpointProperty] = sv.ServiceEndpoint()

		for key, value := range sv {
			_, ok := externalService[key]
			if !ok {
				externalService[key] = value
			}
		}

		services = append(services, externalService)
	}

	if len(services) > 0 {
		resolutionResult.Document[document.ServiceProperty] = services
	}
}

// processKeys will process keys according to Sidetree rules bellow and add them to external document
// -- general: the key is to be included in the publicKeys section of the resolved DID Document.
// -- auth: the key is to be included in the authentication section of the resolved DID Document as follows:
// If the general purpose value IS NOT present in the purpose array,
// the key descriptor object will be included directly in the authentication section of the resolved DID Document.
// If the general purpose value IS present in the purpose array,
// the key descriptor object will be directly included in the publicKeys section of the resolved DID Document,
// and included by reference in the authentication section.
// -- assertion: the key MUST be included in the assertionMethod section of the resolved DID Document
// (same rules as for auth)
// -- agreement: the key MUST be included in the keyAgreement section of the resolved DID Document
// (same rules as for auth)
// -- delegation: the key MUST be included in the capabilityDelegation section of the resolved DID Document
// (same rules as for auth)
// -- invocation: the key MUST be included in the capabilityInvocation section of the resolved DID Document
// (same rules as for auth)
// -- ops: the key is allowed to generate DID operations for the DID and will be included in method metadata.
func (t *Transformer) processKeys(internal document.DIDDocument, resolutionResult *document.ResolutionResult) error { //nolint:gocyclo,funlen,gocognit
	var authentication []interface{}
	var assertionMethod []interface{}
	var agreementKey []interface{}
	var delegationKey []interface{}
	var invocationKey []interface{}

	var publicKeys []document.PublicKey

	// add controller to public key
	for _, pk := range internal.PublicKeys() {
		// construct relative DID URL for inclusion in authentication and assertion method
		relativeID := "#" + pk.ID()

		externalPK := make(document.PublicKey)
		externalPK[document.IDProperty] = t.getObjectID(internal.ID(), pk.ID())
		externalPK[document.TypeProperty] = pk.Type()
		externalPK[document.ControllerProperty] = internal[document.IDProperty]

		if pk.Type() == ed25519VerificationKey2018 {
			ed25519PubKey, err := getED2519PublicKey(pk.PublicKeyJwk())
			if err != nil {
				return err
			}
			externalPK[document.PublicKeyBase58Property] = base58.Encode(ed25519PubKey)
		} else {
			externalPK[document.PublicKeyJwkProperty] = pk.PublicKeyJwk()
		}

		purposes := pk.Purpose()
		if isGeneralKey(purposes) { //nolint:nestif
			publicKeys = append(publicKeys, externalPK)

			// add into authentication by reference if the key has both auth and general purpose
			if isAuthenticationKey(purposes) {
				authentication = append(authentication, relativeID)
			}
			// add into assertionMethod by reference if the key has both assertion and general purpose
			if isAssertionKey(purposes) {
				assertionMethod = append(assertionMethod, relativeID)
			}
			// add into keyAgreement by reference if the key has both agreement and general purpose
			if isAgreementKey(purposes) {
				agreementKey = append(agreementKey, relativeID)
			}
			// add into capabilityDelegation by reference if the key has both delegation and general purpose
			if isDelegationKey(purposes) {
				delegationKey = append(delegationKey, relativeID)
			}
			// add into capabilityInvocation by reference if the key has both invocation and general purpose
			if isInvocationKey(purposes) {
				invocationKey = append(invocationKey, relativeID)
			}
		} else if isAuthenticationKey(purposes) {
			authentication = append(authentication, externalPK)
		} else if isAssertionKey(purposes) {
			assertionMethod = append(assertionMethod, externalPK)
		} else if isAgreementKey(purposes) {
			agreementKey = append(agreementKey, externalPK)
		} else if isDelegationKey(purposes) {
			delegationKey = append(delegationKey, externalPK)
		} else if isInvocationKey(purposes) {
			invocationKey = append(invocationKey, externalPK)
		}
	}

	if len(publicKeys) > 0 {
		resolutionResult.Document[document.PublicKeyProperty] = publicKeys
	}

	if len(authentication) > 0 {
		resolutionResult.Document[document.AuthenticationProperty] = authentication
	}

	if len(assertionMethod) > 0 {
		resolutionResult.Document[document.AssertionMethodProperty] = assertionMethod
	}

	if len(agreementKey) > 0 {
		resolutionResult.Document[document.KeyAgreementProperty] = agreementKey
	}

	if len(delegationKey) > 0 {
		resolutionResult.Document[document.DelegationKeyProperty] = delegationKey
	}

	if len(invocationKey) > 0 {
		resolutionResult.Document[document.InvocationKeyProperty] = invocationKey
	}

	return nil
}

func (t *Transformer) getObjectID(docID string, objectID string) interface{} {
	relativeID := "#" + objectID
	if t.includeBase {
		return relativeID
	}

	return docID + relativeID
}

func getED2519PublicKey(pkJWK document.JWK) ([]byte, error) {
	jwk := &jws.JWK{
		Crv: pkJWK.Crv(),
		Kty: pkJWK.Kty(),
		X:   pkJWK.X(),
		Y:   pkJWK.Y(),
	}

	return internaljws.GetED25519PublicKey(jwk)
}

// isGeneralKey returns true if key is a general key.
func isGeneralKey(purposes []string) bool {
	return isPurposeKey(purposes, document.KeyPurposeVerificationMethod)
}

// isAuthenticationKey returns true if key is an authentication key.
func isAuthenticationKey(purposes []string) bool {
	return isPurposeKey(purposes, document.KeyPurposeAuthentication)
}

// isAssertionKey returns true if key is an assertion key.
func isAssertionKey(purposes []string) bool {
	return isPurposeKey(purposes, document.KeyPurposeAssertionMethod)
}

// isAgreementKey returns true if key is an agreement key.
func isAgreementKey(purposes []string) bool {
	return isPurposeKey(purposes, document.KeyPurposeKeyAgreement)
}

// isDelegationKey returns true if key is an delegation key.
func isDelegationKey(purposes []string) bool {
	return isPurposeKey(purposes, document.KeyPurposeCapabilityDelegation)
}

// isInvocationKey returns true if key is an invocation key.
func isInvocationKey(purposes []string) bool {
	return isPurposeKey(purposes, document.KeyPurposeCapabilityInvocation)
}

func isPurposeKey(purposes []string, mode string) bool {
	for _, purpose := range purposes {
		if purpose == mode {
			return true
		}
	}

	return false
}
