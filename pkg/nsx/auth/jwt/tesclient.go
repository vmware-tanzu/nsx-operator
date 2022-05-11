/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package jwt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

type tokenExchange struct {
	Spec tokenExchangeSpec `json:"spec"`
}

type tokenExchangeSpec struct {
	Audience           string `json:"audience"`
	GrantType          string `json:"grant_type"`
	RequestedTokenType string `json:"requested_token_type"`
	SubjectToken       string `json:"subject_token"`
	SubjectTokenType   string `json:"subject_token_type"`
}

// TESClient requests Token Exchange Service (TES) to issue JWT in exchange
// for SAML token.
type TESClient struct {
	*VCClient
}

// NewTESClient creates a TESClient object.
func NewTESClient(hostname string, port int, ssoDomain string, username, password string, caCertPem []byte, insecureSkipVerify bool) (*TESClient, error) {
	client, err := NewVCClient(hostname, port, ssoDomain, username, password, caCertPem, insecureSkipVerify)
	if err != nil {
		log.Error(err, "new TESClient failed")
		return nil, err
	}
	return &TESClient{client}, nil
}

func newTokenExchange(samlToken string, useOldAudience bool) *tokenExchange {
	audience := "vmware-tes:vc:nsxd-v2:nsx"
	if useOldAudience {
		audience = "vmware-tes:vc:nsxd:nsx"
	}
	return &tokenExchange{
		Spec: tokenExchangeSpec{
			Audience:           audience,
			GrantType:          "urn:ietf:params:oauth:grant-type:token-exchange",
			RequestedTokenType: "urn:ietf:params:oauth:token-type:id_token",
			SubjectToken:       base64.StdEncoding.EncodeToString([]byte(samlToken)),
			SubjectTokenType:   "urn:ietf:params:oauth:token-type:saml2",
		},
	}
}

// ExchangeJWT requests TES to issue JWT in exchange for the specified SAML token.
func (client *TESClient) ExchangeJWT(samlToken string, useOldAudience bool) (string, error) {
	// assume that response should have "access_token" field
	// no retry while hit "Unknown audience value" error
	log.V(1).Info("sending saml token to TES for JWT")

	exchange := newTokenExchange(samlToken, useOldAudience)
	body, err := json.Marshal(*exchange)
	if err != nil {
		return "", err
	}

	var res struct {
		Value map[string]interface{} `json:"value"`
	}
	tesErr := client.HandleRequest("/vcenter/tokenservice/token-exchange", body, &res)
	if tesErr != nil {
		msg := fmt.Sprintf("failed to exchange JWT due to error :%v", tesErr)
		log.Error(tesErr, "failed to exchange JWT")
		return "", errors.New(msg)
	}
	log.V(1).Info("exchanged JWT")
	return res.Value["access_token"].(string), nil
}
