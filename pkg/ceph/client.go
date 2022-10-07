// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ceph

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onmetal/cephlet/pkg/rook"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	k8s "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	IOPSlLimit                  LimitType = "rbd_qos_iops_limit"
	IOPSBurstLimit              LimitType = "rbd_qos_iops_burst"
	IOPSBurstDurationLimit      LimitType = "rbd_qos_iops_burst_seconds"
	ReadIOPSLimit               LimitType = "rbd_qos_read_iops_limit"
	ReadIOPSBurstLimit          LimitType = "rbd_qos_read_iops_burst"
	ReadIOPSBurstDurationLimit  LimitType = "rbd_qos_read_iops_burst_seconds"
	WriteIOPSLimit              LimitType = "rbd_qos_write_iops_limit"
	WriteIOPSBurstLimit         LimitType = "rbd_qos_write_iops_burst"
	WriteIOPSBurstDurationLimit LimitType = "rbd_qos_write_iops_burst_seconds"
	BPSLimit                    LimitType = "rbd_qos_bps_limit"
	BPSBurstLimit               LimitType = "rbd_qos_bps_burst"
	BPSBurstDurationLimit       LimitType = "rbd_qos_bps_burst_seconds"
	ReadBPSLimit                LimitType = "rbd_qos_read_bps_limit"
	ReadBPSBurstLimit           LimitType = "rbd_qos_read_bps_burst"
	ReadBPSBurstDurationLimit   LimitType = "rbd_qos_read_bps_burst_seconds"
	WriteBPSLimit               LimitType = "rbd_qos_write_bps_limit"
	WriteBPSBurstLimit          LimitType = "rbd_qos_write_bps_burst"
	WriteBPSBurstDurationLimit  LimitType = "rbd_qos_write_bps_burst_seconds"
)

type LimitType string

// Client - an interface to ceph CLI.
type Client interface {
	// SetVolumeLimit - sets IOPS limit to ceph rbd images.
	SetVolumeLimit(ctx context.Context, poolName, volumeName, volumeNamespace string, limitType LimitType, value int64) error
}

type tokenSource struct {
	k8s.Client
	http       *http.Client
	rookConfig *rook.Config
}

func (t tokenSource) Token() (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	secret := &corev1.Secret{}
	if err := t.Get(ctx, types.NamespacedName{Namespace: t.rookConfig.Namespace, Name: t.rookConfig.DashboardSecretName}, secret); err != nil {
		return nil, fmt.Errorf("unable to get ceph dashboard password: %w", err)
	}

	if secret.Data == nil {
		return nil, fmt.Errorf("secret %s is empty", k8s.ObjectKeyFromObject(secret))
	}

	pw, ok := secret.Data["password"]
	if !ok {
		return nil, fmt.Errorf("secret %s has no key 'password'", k8s.ObjectKeyFromObject(secret))
	}

	data, err := json.Marshal(authTokenRequest{
		Username: t.rookConfig.DashboardUser,
		Password: string(pw),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to marshal authTokenRequest: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/auth", t.rookConfig.DashboardEndpoint), bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("unable to create token request: %w", err)
	}
	req.Header.Add("Accept", "application/vnd.ceph.api.v1.0+json")
	req.Header.Add("Content-Type", "application/json")

	response, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to do token request: %w", err)
	}

	if isFailed(response) {
		return nil, errors.New("token request failed with code: %s " + response.Status)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read body: %w", err)
	}

	result := authTokenResponse{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unable to unmarshall authTokenResponse: %w", err)
	}

	return &oauth2.Token{
		AccessToken: result.Token,
		TokenType:   "Bearer",
	}, nil
}

func NewClient(k8sClient k8s.Client, rookConfig *rook.Config) (Client, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: rookConfig.DashboardInsecureSkipVerify},
		},
	}

	ts := tokenSource{
		Client:     k8sClient,
		http:       httpClient,
		rookConfig: rookConfig,
	}

	return &client{
		Client:     k8sClient,
		rookConfig: rookConfig,
		httpClient: oauth2.NewClient(context.WithValue(context.Background(), oauth2.HTTPClient, httpClient), ts),
	}, nil
}

type client struct {
	k8s.Client
	rookConfig *rook.Config
	httpClient *http.Client
}

func (c *client) SetVolumeLimit(ctx context.Context, poolName, volumeName, volumeNamespace string, limitType LimitType, value int64) error {
	data, err := json.Marshal(limitRequest{
		Configuration: map[string]string{
			string(limitType): fmt.Sprintf("%d", value),
		},
	})
	if err != nil {
		return fmt.Errorf("unable to marshal limitRequest: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("%s/api/block/image/%s",
		c.rookConfig.DashboardEndpoint,
		url.PathEscape(fmt.Sprintf("%s/%s/%s", poolName, volumeNamespace, volumeName)),
	), bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("unable to create limitRequest request: %w", err)
	}
	req.Header.Add("Accept", "application/vnd.ceph.api.v1.0+json")
	req.Header.Add("Content-Type", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("unable to do limitRequest request: %w", err)
	}
	if isFailed(response) {
		return errors.New("limitRequest failed with code: %s " + response.Status)
	}

	return nil
}

func isFailed(response *http.Response) bool {
	return response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices
}

type authTokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authTokenResponse struct {
	Token string `json:"token"`
}

type limitRequest struct {
	Configuration map[string]string `json:"configuration"`
}
