// Copyright 2018 VMware, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vsphere

import (
	"context"
	"fmt"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/vmware/govmomi/pbm"
	"github.com/vmware/govmomi/pbm/methods"
	"github.com/vmware/govmomi/pbm/types"
)

// fetchComplianceResultBatchSize is the batch size for querying the PBM
// FetchComplianceResult API.
const fetchComplianceResultBatchSize = 1000

// ComplianceResult holds the compliance status for a single disk.
type ComplianceResult struct {
	// FcdUUID represents the FCD's UUID.
	FcdUUID string
	// CurrentProfileID represents the ID of the profile that's currently
	// associated with the FCD.
	CurrentProfileID string
	// Status represents the PBM compliance status.
	Status types.PbmComplianceStatus
	// IsValid specifies whether the FCD UUID is valid.
	IsValid bool
	// ProfileMatched specifies whether the given profile is associated with the
	// given FCD.
	ProfileMatched bool
}

func (cr *ComplianceResult) String() string {
	return fmt.Sprintf("ComplianceResult [FcdUUID: %v, CurrentProfileID: %v, "+
		"Status: %v, IsValid: %v, ProfileMatched: %v]", cr.FcdUUID,
		cr.CurrentProfileID, cr.Status, cr.IsValid, cr.ProfileMatched)
}

// ConnectPbm creates a PBM client for the virtual center.
func (vc *VirtualCenter) ConnectPbm(ctx context.Context) error {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	if vc.PbmClient == nil {
		var err error
		if vc.PbmClient, err = pbm.NewClient(ctx, vc.Client.Client); err != nil {
			log.WithField("err", err).Error("Failed to create pbm client")
			return err
		}
	}
	return nil
}

// DisconnectPbm destroys the PBM client for the virtual center.
func (vc *VirtualCenter) DisconnectPbm(ctx context.Context) error {
	if vc.PbmClient == nil {
		log.Info("PbmClient wasn't connected, ignoring")
	} else {
		vc.PbmClient = nil
	}
	return nil
}

// createPbmServerObjectRef returns a PbmServerObjectRef for a FCD given its UUID.
func (vc *VirtualCenter) createPbmServerObjectRef(fcdUUID string) types.PbmServerObjectRef {
	return types.PbmServerObjectRef{
		Key:        fcdUUID,
		ObjectType: string(types.PbmObjectTypeVirtualDiskUUID),
		ServerUuid: vc.Client.Client.ServiceContent.About.InstanceUuid,
	}
}

// createPbmServerObjectRefs returns a slice of PbmServerObjectRefs for FCDs
// given a slice of their UUIDs.
func (vc *VirtualCenter) createPbmServerObjectRefs(fcdUUIDs []string) []types.PbmServerObjectRef {
	var objRefs []types.PbmServerObjectRef
	for _, fcdUUID := range fcdUUIDs {
		objRefs = append(objRefs, vc.createPbmServerObjectRef(fcdUUID))
	}
	return objRefs
}

// createPbmFetchComplianceResultRequest creates a request object for fetching
// PBM compliance result for the given slice of FCD UUIDs.
func (vc *VirtualCenter) createPbmFetchComplianceResultRequest(fcdUUIDs []string) types.PbmFetchComplianceResult {
	return types.PbmFetchComplianceResult{
		This:     vc.PbmClient.ServiceContent.ComplianceManager,
		Entities: vc.createPbmServerObjectRefs(fcdUUIDs),
	}
}

// buildComplianceResults returns a slice of ComplianceResult for the given FCD
// UUIDs and PBM compliance results.
func (vc *VirtualCenter) buildComplianceResults(profileID string, fcdUUIDs []string, pbmComplianceResults []types.PbmComplianceResult) []ComplianceResult {
	complianceResults := make([]ComplianceResult, 0)
	validFCDs := make(map[string]struct{})
	for _, pbmComplianceResult := range pbmComplianceResults {
		pbmFcdUUID := pbmComplianceResult.Entity.Key
		pbmProfileID := pbmComplianceResult.Profile.UniqueId
		complianceResults = append(complianceResults, ComplianceResult{
			FcdUUID:          pbmFcdUUID,
			CurrentProfileID: pbmProfileID,
			Status:           types.PbmComplianceStatus(pbmComplianceResult.ComplianceStatus),
			IsValid:          true,
			ProfileMatched:   pbmProfileID == profileID,
		})
		validFCDs[pbmFcdUUID] = struct{}{}
	}
	for _, fcdUUID := range fcdUUIDs {
		if _, ok := validFCDs[fcdUUID]; !ok {
			log.WithField("fcdUUID", fcdUUID).Warn("Couldn't identify compliance status for FCD")
			complianceResults = append(complianceResults, ComplianceResult{
				FcdUUID: fcdUUID,
				IsValid: false,
			})
		}
	}
	return complianceResults
}

// QueryCompliance queries the compliance of multiple disks with a profile given
// the profile name and the disk UUIDs.
func (vc *VirtualCenter) QueryCompliance(ctx context.Context, profileName string, fcdUUIDs []string) ([]ComplianceResult, error) {
	// Fetch the ID for the given profile.
	profileID, err := vc.PbmClient.ProfileIDByName(ctx, profileName)
	if err != nil {
		log.WithFields(log.Fields{
			"profileName": profileName, "err": err,
		}).Error("Failed to find ID for given profile name")
		return nil, err
	}

	// Fetch compliance results in batches for the given FCD UUIDs.
	var results []types.PbmComplianceResult
	for batchStart := 0; batchStart < len(fcdUUIDs); batchStart += fetchComplianceResultBatchSize {
		batchEnd := batchStart + fetchComplianceResultBatchSize
		if batchEnd > len(fcdUUIDs) {
			batchEnd = len(fcdUUIDs)
		}
		fcdBatch := fcdUUIDs[batchStart:batchEnd]

		req := vc.createPbmFetchComplianceResultRequest(fcdBatch)
		res, err := methods.PbmFetchComplianceResult(ctx, vc.PbmClient, &req)
		if err != nil {
			log.WithFields(log.Fields{
				"profileName": profileName, "fcdBatch": fcdBatch, "err": err,
			}).Error("Failed to fetch compliance result")
			return nil, err
		}
		results = append(results, res.Returnval...)
	}
	return vc.buildComplianceResults(profileID, fcdUUIDs, results), nil
}

// BulkQueryCompliance queries the compliance of multiple disks with multiple
// storage policies given a map of profile names and the UUIDs of disks
// associated with each profile.
func (vc *VirtualCenter) BulkQueryCompliance(ctx context.Context, profileFCDsMap map[string][]string) (map[string][]ComplianceResult, error) {
	cancelableCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		mutex   sync.Mutex
		poolErr error
		results = make(map[string][]ComplianceResult)
	)

	lockAndDo := func(f func()) {
		mutex.Lock()
		defer mutex.Unlock()
		f()
	}

	for profileName, fcdUUIDs := range profileFCDsMap {
		wg.Add(1)
		go func(ctx context.Context, profileName string, fcdUUIDs []string) {
			defer wg.Done()
			fcdResults, err := vc.QueryCompliance(ctx, profileName, fcdUUIDs)
			if err != nil {
				// The go http library prefixes the context.Canceled error with
				// its own message, so we do a suffix match in addition to the
				// equals match.
				if err != context.Canceled && !strings.HasSuffix(err.Error(), context.Canceled.Error()) {
					// Cancel will prevent any compliance queries that are yet
					// to happen in other go routines.
					cancel()
					lockAndDo(func() { poolErr = err })
					log.WithFields(log.Fields{
						"profileFCDsMap": profileFCDsMap, "err": err,
					}).Error("Failed to bulk fetch compliance result")
				}
				return
			}
			lockAndDo(func() { results[profileName] = fcdResults })
		}(cancelableCtx, profileName, fcdUUIDs)
	}
	wg.Wait()

	if poolErr != nil {
		return nil, poolErr
	}
	return results, nil
}

// GetStoragePolicyIDByName gets storage policy ID by name.
func (vc *VirtualCenter) GetStoragePolicyIDByName(storagePolicyID string) (string, error) {
	// TODO: Call PBM API to get Storage Policy ID by name.
	return "", nil
}
