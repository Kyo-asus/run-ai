// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package chartdownload

import (
	"fmt"
	"net/http"
)

const NGCBasicAuthUsername = "$oauthtoken"

// StatusError keeps the upstream status available for each caller's error mapping.
type StatusError struct {
	StatusCode int
	Status     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %s", e.Status)
}

// Fetch requires HTTP 200 for chart archives and repository indexes. Callers retain
// authentication, redirect, address and timeout policies through their request and client.
// On success, the caller owns the body and its size limits; rejected responses are closed here.
func Fetch(client *http.Client, request *http.Request) (*http.Response, error) {
	// #nosec G704 -- Callers enforce URL/address policy; CP and cluster allow different destinations.
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, &StatusError{StatusCode: response.StatusCode, Status: response.Status}
	}
	return response, nil
}
