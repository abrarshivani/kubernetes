/*
Copyright (c) 2018 VMware, Inc. All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package methods

import (
	"context"

	"github.com/vmware/govmomi/vim25/soap"
	"gitlab.eng.vmware.com/hatchway/common-csp/cns/types"
)

type CnsCreateVolumeBody struct {
	Req    *types.CnsCreateVolume         `xml:"urn:vsan CnsCreateVolume,omitempty"`
	Res    *types.CnsCreateVolumeResponse `xml:"urn:vsan CnsCreateVolumeResponse,omitempty"`
	Fault_ *soap.Fault                    `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault,omitempty"`
}

func (b *CnsCreateVolumeBody) Fault() *soap.Fault { return b.Fault_ }

func CnsCreateVolume(ctx context.Context, r soap.RoundTripper, req *types.CnsCreateVolume) (*types.CnsCreateVolumeResponse, error) {
	var reqBody, resBody CnsCreateVolumeBody

	reqBody.Req = req

	if err := r.RoundTrip(ctx, &reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody.Res, nil
}

type CnsUpdateVolumeBody struct {
	Req    *types.CnsUpdateVolume         `xml:"urn:vsan CnsUpdateVolume,omitempty"`
	Res    *types.CnsUpdateVolumeResponse `xml:"urn:vsan CnsUpdateVolumeResponse,omitempty"`
	Fault_ *soap.Fault                    `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault,omitempty"`
}

func (b *CnsUpdateVolumeBody) Fault() *soap.Fault { return b.Fault_ }

func CnsUpdateVolume(ctx context.Context, r soap.RoundTripper, req *types.CnsUpdateVolume) (*types.CnsUpdateVolumeResponse, error) {
	var reqBody, resBody CnsUpdateVolumeBody

	reqBody.Req = req

	if err := r.RoundTrip(ctx, &reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody.Res, nil
}

type CnsDeleteVolumeBody struct {
	Req    *types.CnsDeleteVolume         `xml:"urn:vsan CnsDeleteVolume,omitempty"`
	Res    *types.CnsDeleteVolumeResponse `xml:"urn:vsan CnsDeleteVolumeResponse,omitempty"`
	Fault_ *soap.Fault                    `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault,omitempty"`
}

func (b *CnsDeleteVolumeBody) Fault() *soap.Fault { return b.Fault_ }

func CnsDeleteVolume(ctx context.Context, r soap.RoundTripper, req *types.CnsDeleteVolume) (*types.CnsDeleteVolumeResponse, error) {
	var reqBody, resBody CnsDeleteVolumeBody

	reqBody.Req = req

	if err := r.RoundTrip(ctx, &reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody.Res, nil
}

type CnsAttachVolumeBody struct {
	Req    *types.CnsAttachVolume         `xml:"urn:vsan CnsAttachVolume,omitempty"`
	Res    *types.CnsAttachVolumeResponse `xml:"urn:vsan CnsAttachVolumeResponse,omitempty"`
	Fault_ *soap.Fault                    `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault,omitempty"`
}

func (b *CnsAttachVolumeBody) Fault() *soap.Fault { return b.Fault_ }

func CnsAttachVolume(ctx context.Context, r soap.RoundTripper, req *types.CnsAttachVolume) (*types.CnsAttachVolumeResponse, error) {
	var reqBody, resBody CnsAttachVolumeBody

	reqBody.Req = req

	if err := r.RoundTrip(ctx, &reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody.Res, nil
}

type CnsDetachVolumeBody struct {
	Req    *types.CnsDetachVolume         `xml:"urn:vsan CnsDetachVolume,omitempty"`
	Res    *types.CnsDetachVolumeResponse `xml:"urn:vsan CnsDetachVolumeResponse,omitempty"`
	Fault_ *soap.Fault                    `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault,omitempty"`
}

func (b *CnsDetachVolumeBody) Fault() *soap.Fault { return b.Fault_ }

func CnsDetachVolume(ctx context.Context, r soap.RoundTripper, req *types.CnsDetachVolume) (*types.CnsDetachVolumeResponse, error) {
	var reqBody, resBody CnsDetachVolumeBody

	reqBody.Req = req

	if err := r.RoundTrip(ctx, &reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody.Res, nil
}

type CnsQueryVolumeBody struct {
	Req    *types.CnsQueryVolume         `xml:"urn:vsan CnsQueryVolume,omitempty"`
	Res    *types.CnsQueryVolumeResponse `xml:"urn:vsan CnsQueryVolumeResponse,omitempty"`
	Fault_ *soap.Fault                   `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault,omitempty"`
}

func (b *CnsQueryVolumeBody) Fault() *soap.Fault { return b.Fault_ }

func CnsQueryVolume(ctx context.Context, r soap.RoundTripper, req *types.CnsQueryVolume) (*types.CnsQueryVolumeResponse, error) {
	var reqBody, resBody CnsQueryVolumeBody

	reqBody.Req = req

	if err := r.RoundTrip(ctx, &reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody.Res, nil
}

type CnsGetTaskResultBody struct {
	Req    *types.CnsGetTaskResult         `xml:"urn:vsan CnsGetTaskResult,omitempty"`
	Res    *types.CnsGetTaskResultResponse `xml:"urn:vsan CnsGetTaskResultResponse,omitempty"`
	Fault_ *soap.Fault                     `xml:"http://schemas.xmlsoap.org/soap/envelope/ Fault,omitempty"`
}

func (b *CnsGetTaskResultBody) Fault() *soap.Fault { return b.Fault_ }

func CnsGetTaskResult(ctx context.Context, r soap.RoundTripper, req *types.CnsGetTaskResult) (*types.CnsGetTaskResultResponse, error) {
	var reqBody, resBody CnsGetTaskResultBody

	reqBody.Req = req

	if err := r.RoundTrip(ctx, &reqBody, &resBody); err != nil {
		return nil, err
	}

	return resBody.Res, nil
}
